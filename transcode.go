package main

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const largeMp4Threshold = 500 * 1024 * 1024 // 500MB

var (
	hlsCacheDir string // HLS 缓存根目录

	// hlsJobs 跟踪正在进行的 HLS 转码任务
	hlsJobs   = make(map[string]*HLSJob)
	hlsJobsMu sync.Mutex
)

type HLSJob struct {
	Dir        string       // HLS 分片输出目录
	Cmd        *exec.Cmd    // ffmpeg 进程（缓存命中时为 nil）
	Done       chan struct{} // 转码完成信号
	Cached     bool         // 是否来自缓存
	lastAccess int64        // 最后访问时间（unix 秒）
}

// InitHLSCache 初始化 HLS 缓存目录
func InitHLSCache() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	hlsCacheDir = filepath.Join(home, ".cache", "localcinema", "hls")
	if err := os.MkdirAll(hlsCacheDir, 0755); err != nil {
		return err
	}
	log.Printf("[缓存] 目录: %s", hlsCacheDir)
	return nil
}

// ClearHLSCache 清空所有缓存
func ClearHLSCache() error {
	if hlsCacheDir == "" {
		return nil
	}
	log.Printf("[缓存] 清空: %s", hlsCacheDir)
	return os.RemoveAll(hlsCacheDir)
}

// needsTranscode 判断非 MP4 格式
func needsTranscode(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext != ".mp4" && ext != ".m4v"
}

// needsStreamingMp4 判断大 MP4 是否需要流式处理（moov 不在前面）
func needsStreamingMp4(filePath string) bool {
	info, err := os.Stat(filePath)
	if err != nil || info.Size() < largeMp4Threshold {
		return false
	}
	return !hasMoovAtFront(filePath)
}

func hasMoovAtFront(filePath string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	var offset int64
	buf := make([]byte, 8)
	for {
		if _, err := f.ReadAt(buf, offset); err != nil {
			break
		}

		size := int64(binary.BigEndian.Uint32(buf[0:4]))
		boxType := string(buf[4:8])

		if size == 1 {
			buf64 := make([]byte, 8)
			if _, err := f.ReadAt(buf64, offset+8); err != nil {
				break
			}
			size = int64(binary.BigEndian.Uint64(buf64))
		}
		if size == 0 {
			info, _ := f.Stat()
			size = info.Size() - offset
		}
		if size < 8 {
			break
		}

		switch boxType {
		case "moov":
			return true
		case "mdat":
			return false
		}

		offset += size
	}
	return true
}

func probeVideoCodec(filePath string) string {
	cmd := exec.Command(ffprobePath(),
		"-v", "quiet",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name",
		"-print_format", "flat",
		filePath,
	)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	s := string(out)
	if idx := strings.Index(s, "=\""); idx >= 0 {
		s = s[idx+2:]
		if end := strings.Index(s, "\""); end >= 0 {
			return s[:end]
		}
	}
	return ""
}

func canBrowserPlayCodec(codec string) bool {
	switch codec {
	case "h264", "avc1", "avc":
		return true
	default:
		return false
	}
}

// hlsJobKey 基于文件路径+修改时间生成 key，文件变化后缓存自动失效
func hlsJobKey(filePath string) string {
	info, err := os.Stat(filePath)
	var mtime int64
	if err == nil {
		mtime = info.ModTime().UnixNano()
	}
	data := fmt.Sprintf("%s|%d", filePath, mtime)
	h := md5.Sum([]byte(data))
	return fmt.Sprintf("%x", h[:8])
}

// isCacheComplete 检查缓存目录中是否有完整的 m3u8（包含 #EXT-X-ENDLIST）
func isCacheComplete(dir string) bool {
	m3u8Path := filepath.Join(dir, "stream.m3u8")
	data, err := os.ReadFile(m3u8Path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "#EXT-X-ENDLIST")
}

// getOrStartHLS 获取已有任务、命中缓存、或启动新的 HLS 转码
func getOrStartHLS(filePath string) (*HLSJob, error) {
	key := hlsJobKey(filePath)
	fileName := filepath.Base(filePath)

	hlsJobsMu.Lock()
	if job, ok := hlsJobs[key]; ok {
		hlsJobsMu.Unlock()
		return job, nil
	}

	// 检查磁盘缓存
	cacheDir := filepath.Join(hlsCacheDir, key)
	if isCacheComplete(cacheDir) {
		log.Printf("[HLS] %s: 命中缓存 (%s)", fileName, key)
		job := &HLSJob{
			Dir:        cacheDir,
			Cached:     true,
			Done:       make(chan struct{}),
			lastAccess: time.Now().Unix(),
		}
		close(job.Done) // 已完成
		hlsJobs[key] = job
		hlsJobsMu.Unlock()
		return job, nil
	}

	// 创建缓存目录
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		hlsJobsMu.Unlock()
		return nil, fmt.Errorf("创建缓存目录失败: %w", err)
	}

	codec := probeVideoCodec(filePath)
	log.Printf("[HLS] %s: 视频编码=%s", fileName, codec)

	m3u8Path := filepath.Join(cacheDir, "stream.m3u8")
	segPattern := filepath.Join(cacheDir, "seg%05d.ts")

	// 公共参数：显式选第一条视频+第一条音频轨，音频统一转 AAC 立体声
	commonArgs := []string{
		"-map", "0:v:0",
		"-map", "0:a:0?", // ? 表示没有音轨也不报错
		"-c:a", "aac",
		"-ac", "2",
		"-b:a", "128k",
		"-f", "hls",
		"-hls_time", "6",
		"-hls_list_size", "0",
		"-hls_segment_filename", segPattern,
		"-hls_flags", "independent_segments",
	}

	var args []string
	if canBrowserPlayCodec(codec) {
		log.Printf("[HLS] %s: H.264 copy 模式", fileName)
		args = append([]string{"-loglevel", "error", "-i", filePath,
			"-c:v", "copy",
			"-bsf:v", "h264_mp4toannexb", // H.264 -> Annex B 格式，ts 容器必须
		}, commonArgs...)
	} else {
		var videoArgs []string
		if runtime.GOOS == "darwin" {
			log.Printf("[HLS] %s: %s -> H.264 转码 (硬件加速)", fileName, codec)
			videoArgs = []string{"-c:v", "h264_videotoolbox", "-b:v", "4M"}
		} else {
			log.Printf("[HLS] %s: %s -> H.264 转码 (软编码)", fileName, codec)
			videoArgs = []string{"-c:v", "libx264", "-preset", "fast", "-b:v", "4M"}
		}
		args = append([]string{"-loglevel", "error", "-i", filePath}, videoArgs...)
		args = append(args, "-force_key_frames", "expr:gte(t,n_forced*2)")
		args = append(args, commonArgs...)
	}
	args = append(args, m3u8Path)

	log.Printf("[HLS] %s: ffmpeg %s", fileName, strings.Join(args, " "))

	cmd := exec.Command(ffmpegPath(), args...)

	job := &HLSJob{
		Dir:        cacheDir,
		Cmd:        cmd,
		Done:       make(chan struct{}),
		lastAccess: time.Now().Unix(),
	}
	hlsJobs[key] = job
	hlsJobsMu.Unlock()

	go func() {
		defer close(job.Done)
		// 丢弃 stdout/stderr，避免内存堆积（已通过 -loglevel error 限制输出）
		cmd.Stdout = nil
		cmd.Stderr = nil
		err := cmd.Run()
		if err != nil {
			log.Printf("[HLS] %s: ffmpeg 退出: %v", fileName, err)
			// 转码失败，清理不完整的缓存
			os.RemoveAll(cacheDir)
		} else {
			log.Printf("[HLS] %s: 转码完成，已缓存 (%s)", fileName, key)
			job.Cached = true
		}

		// 转码完成后不从 hlsJobs 删除（保留以便继续提供分片服务）
		// 由 reaper 在空闲后清理内存记录（缓存文件保留在磁盘）
	}()

	return job, nil
}

// TouchHLS 更新任务的最后访问时间
func TouchHLS(key string) {
	hlsJobsMu.Lock()
	if job, ok := hlsJobs[key]; ok {
		atomic.StoreInt64(&job.lastAccess, time.Now().Unix())
	}
	hlsJobsMu.Unlock()
}

// StopHLS 停止指定的 HLS 任务（不删除缓存文件）
func StopHLS(key string) {
	hlsJobsMu.Lock()
	job, ok := hlsJobs[key]
	if ok {
		delete(hlsJobs, key)
	}
	hlsJobsMu.Unlock()

	if ok && job.Cmd != nil && job.Cmd.Process != nil && !job.Cached {
		log.Printf("[HLS] 停止空闲转码任务: %s", key)
		job.Cmd.Process.Kill()
		// 转码中断，删除不完整的缓存
		os.RemoveAll(job.Dir)
	}
	// 已完成的缓存保留在磁盘
}

const hlsIdleTimeout = 60 // 秒，无请求后清理内存记录

// StartHLSReaper 定期清理空闲任务的内存记录
func StartHLSReaper() {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now().Unix()
			hlsJobsMu.Lock()
			var idleKeys []string
			for key, job := range hlsJobs {
				last := atomic.LoadInt64(&job.lastAccess)
				if last > 0 && now-last > hlsIdleTimeout {
					idleKeys = append(idleKeys, key)
				}
			}
			hlsJobsMu.Unlock()

			for _, key := range idleKeys {
				StopHLS(key)
			}
		}
	}()
}
