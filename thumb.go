package main

import (
	"crypto/md5"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

func servePlaceholder(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/placeholder.svg")
	if err != nil {
		http.Error(w, "封面生成失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}

var (
	thumbCacheDir string
	thumbOnce     sync.Once
)

// InitThumbCache 初始化封面缓存目录
func InitThumbCache() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	thumbCacheDir = filepath.Join(home, ".cache", "localcinema", "thumbs")
	return os.MkdirAll(thumbCacheDir, 0755)
}

// thumbPath 封面缓存路径（基于视频路径+修改时间）
func thumbPath(videoPath string) string {
	info, _ := os.Stat(videoPath)
	var mtime int64
	if info != nil {
		mtime = info.ModTime().UnixNano()
	}
	h := md5.Sum([]byte(fmt.Sprintf("%s|%d", videoPath, mtime)))
	return filepath.Join(thumbCacheDir, fmt.Sprintf("%x.jpg", h[:8]))
}

// generateThumb 使用 ffmpeg 截取视频封面
func generateThumb(videoPath, outPath string) error {
	// 多种策略依次尝试
	attempts := [][]string{
		// 1. 跳到第 5 秒截取
		{"-ss", "5", "-i", videoPath,
			"-vframes", "1", "-vf", "scale=320:-2", "-q:v", "6", "-y", outPath},
		// 2. 从头截取（视频可能不足 5 秒）
		{"-i", videoPath,
			"-vframes", "1", "-vf", "scale=320:-2", "-q:v", "6", "-y", outPath},
		// 3. 增大探测量（应对头部信息不完整的文件）
		{"-analyzeduration", "20000000", "-probesize", "50000000",
			"-ss", "5", "-i", videoPath,
			"-vframes", "1", "-vf", "scale=320:-2", "-q:v", "6", "-y", outPath},
		// 4. 增大探测量 + 从头
		{"-analyzeduration", "20000000", "-probesize", "50000000",
			"-i", videoPath,
			"-vframes", "1", "-vf", "scale=320:-2", "-q:v", "6", "-y", outPath},
	}

	var lastOutput []byte
	var lastErr error
	for _, args := range attempts {
		cmd := exec.Command(ffmpegPath(), args...)
		lastOutput, lastErr = cmd.CombinedOutput()
		if lastErr == nil {
			if info, err := os.Stat(outPath); err == nil && info.Size() > 0 {
				return nil
			}
		}
	}

	log.Printf("[封面] 生成失败 %s: %v\n%s", filepath.Base(videoPath), lastErr, string(lastOutput))
	return lastErr
}

// handleThumb 提供视频封面
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Query().Get("file")
	if file == "" {
		http.Error(w, "缺少 file 参数", http.StatusBadRequest)
		return
	}

	if !s.isValidPath(file) {
		http.Error(w, "无效的文件路径", http.StatusForbidden)
		return
	}

	fullPath := filepath.Join(s.videoDir, file)
	cached := thumbPath(fullPath)

	// 检查缓存
	if _, err := os.Stat(cached); err != nil {
		// 缓存不存在，生成
		if err := generateThumb(fullPath, cached); err != nil {
			servePlaceholder(w, r)
			return
		}
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, cached)
}
