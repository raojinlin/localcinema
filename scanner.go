package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var videoExts = map[string]bool{
	".mp4":  true,
	".mkv":  true,
	".avi":  true,
	".mov":  true,
	".webm": true,
	".m4v":  true,
	".wmv":  true,
	".flv":  true,
}

type DirMetadata struct {
	Title       string   `json:"title"`
	Source      string   `json:"source"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
}

type VideoFile struct {
	Name        string
	RelPath     string
	Size        int64
	SizeStr     string
	Duration    string // "1:23:45" 格式
	DurationSec float64
	DirectPlay  bool
	Meta        *DirMetadata
}

// directPlayExts are formats that browsers can play natively without HLS transcoding.
var directPlayExts = map[string]bool{
	".mp4": true,
	".m4v": true,
}

func ScanVideos(root string) ([]VideoFile, error) {
	var videos []VideoFile
	metaCache := map[string]*DirMetadata{}
	metaChecked := map[string]bool{}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if videoExts[ext] {
			rel, _ := filepath.Rel(root, path)
			name := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))

			dir := filepath.Dir(path)
			if !metaChecked[dir] {
				metaChecked[dir] = true
				metaCache[dir] = readDirMetadata(dir)
			}
			meta := metaCache[dir]

			if meta != nil && meta.Title != "" {
				name = meta.Title
			}

			dur := getDuration(path)
			videos = append(videos, VideoFile{
				Name:        name,
				RelPath:     rel,
				Size:        info.Size(),
				SizeStr:     formatSize(info.Size()),
				Duration:    dur,
				DurationSec: parseDurationStr(dur),
				DirectPlay:  directPlayExts[ext],
				Meta:        meta,
			})
		}
		return nil
	})

	sort.Slice(videos, func(i, j int) bool {
		return videos[i].Name < videos[j].Name
	})

	return videos, err
}

// readDirMetadata reads metadata.json from the given directory.
// Returns nil if the file doesn't exist or can't be parsed.
func readDirMetadata(dir string) *DirMetadata {
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil
	}
	var meta DirMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return &meta
}

// parseDurationStr converts "H:MM:SS" or "M:SS" format to seconds.
func parseDurationStr(s string) float64 {
	if s == "" {
		return 0
	}
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 2:
		m, _ := strconv.ParseFloat(parts[0], 64)
		sec, _ := strconv.ParseFloat(parts[1], 64)
		return m*60 + sec
	case 3:
		h, _ := strconv.ParseFloat(parts[0], 64)
		m, _ := strconv.ParseFloat(parts[1], 64)
		sec, _ := strconv.ParseFloat(parts[2], 64)
		return h*3600 + m*60 + sec
	}
	return 0
}

// getDuration 获取视频时长，优先读缓存
func getDuration(videoPath string) string {
	// 读缓存
	cached := durationCachePath(videoPath)
	if data, err := os.ReadFile(cached); err == nil {
		return strings.TrimSpace(string(data))
	}

	// 多种策略依次尝试
	attempts := [][]string{
		{"-v", "quiet", "-show_entries", "format=duration", "-print_format", "flat", videoPath},
		{"-v", "quiet", "-analyzeduration", "20000000", "-probesize", "50000000",
			"-show_entries", "format=duration", "-print_format", "flat", videoPath},
	}

	for _, args := range attempts {
		cmd := exec.Command(ffprobePath(), args...)
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if dur := parseDuration(string(out)); dur != "" {
			os.MkdirAll(filepath.Dir(cached), 0755)
			os.WriteFile(cached, []byte(dur), 0644)
			return dur
		}
	}
	return ""
}

// parseDuration 解析 ffprobe 输出中的 format.duration="6325.292000"
func parseDuration(s string) string {
	if idx := strings.Index(s, "=\""); idx >= 0 {
		s = s[idx+2:]
		if end := strings.Index(s, "\""); end >= 0 {
			secs, err := strconv.ParseFloat(s[:end], 64)
			if err == nil {
				return formatDuration(secs)
			}
		}
	}
	return ""
}

func durationCachePath(videoPath string) string {
	info, _ := os.Stat(videoPath)
	var mtime int64
	if info != nil {
		mtime = info.ModTime().UnixNano()
	}
	h := md5.Sum([]byte(fmt.Sprintf("%s|%d", videoPath, mtime)))
	return filepath.Join(thumbCacheDir, fmt.Sprintf("%x.dur", h[:8]))
}

func formatDuration(secs float64) string {
	total := int(math.Round(secs))
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func formatSize(bytes int64) string {
	const (
		MB = 1024 * 1024
		GB = 1024 * MB
	)
	if bytes >= GB {
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	}
	return fmt.Sprintf("%.0f MB", float64(bytes)/float64(MB))
}
