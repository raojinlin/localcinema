package main

import (
	"embed"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type IndexData struct {
	Videos     []VideoFile
	Page       int
	PageSize   int
	Total      int
	TotalPages int
}

type APIVideoResponse struct {
	Videos     []APIVideo `json:"videos"`
	Page       int        `json:"page"`
	PageSize   int        `json:"pageSize"`
	Total      int        `json:"total"`
	TotalPages int        `json:"totalPages"`
}

type APIVideo struct {
	Name        string       `json:"name"`
	RelPath     string       `json:"relPath"`
	Size        int64        `json:"size"`
	SizeStr     string       `json:"sizeStr"`
	Duration    string       `json:"duration"`
	DurationSec float64      `json:"durationSec"`
	DirectPlay  bool         `json:"directPlay"`
	Meta        *DirMetadata `json:"meta,omitempty"`
}

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var templates = template.Must(
	template.New("").Funcs(template.FuncMap{
		"add":      func(a, b int) int { return a + b },
		"subtract": func(a, b int) int { return a - b },
	}).ParseFS(templateFS, "templates/*.html"),
)

type Server struct {
	videoDir string
}

func NewServer(videoDir string) *Server {
	return &Server{videoDir: videoDir}
}

func (s *Server) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/play", s.handlePlay)
	mux.HandleFunc("/video", s.handleVideo)
	mux.HandleFunc("/hls/", s.handleHLS)
	mux.HandleFunc("/thumb", s.handleThumb)
	mux.HandleFunc("/shorts", s.handleShorts)
	mux.HandleFunc("/api/videos", s.handleAPIVideos)
	mux.Handle("/static/", http.FileServer(http.FS(staticFS)))
	return http.ListenAndServe(addr, logMiddleware(mux))
}

// responseWriter 包装，用于捕获状态码和响应大小
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
	bytes      int64
}

func (w *loggingResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *loggingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(lw, r)
		elapsed := time.Since(start)

		// 跳过高频请求的日志
		path := r.URL.Path
		if strings.HasSuffix(path, ".ts") || path == "/thumb" {
			return
		}

		clientIP := r.RemoteAddr
		if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
			clientIP = ip
		}

		query := r.URL.RawQuery
		if query != "" {
			path = path + "?" + query
		}

		var sizeStr string
		if lw.bytes > 0 {
			sizeStr = " " + formatSize(lw.bytes)
		}

		log.Printf("[HTTP] %s %s %d %s%s <- %s",
			r.Method, path, lw.statusCode, elapsed.Round(time.Millisecond), sizeStr, clientIP)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	videos, err := ScanVideos(s.videoDir)
	if err != nil {
		http.Error(w, "扫描视频目录失败", http.StatusInternalServerError)
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size <= 0 {
		size = 20
	}
	total := len(videos)
	totalPages := (total + size - 1) / size
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * size
	end := start + size
	if end > total {
		end = total
	}

	data := IndexData{
		Videos:     videos[start:end],
		Page:       page,
		PageSize:   size,
		Total:      total,
		TotalPages: totalPages,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("模板渲染错误: %v", err)
	}
}

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
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
	useHLS := needsTranscode(fullPath) || needsStreamingMp4(fullPath)

	// 获取所有视频用于"相关视频"展示
	allVideos, _ := ScanVideos(s.videoDir)
	var related []VideoFile
	for _, v := range allVideos {
		if v.RelPath != file {
			related = append(related, v)
		}
	}

	name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	if meta := readDirMetadata(filepath.Dir(fullPath)); meta != nil && meta.Title != "" {
		name = meta.Title
	}

	data := struct {
		Name    string
		File    string
		UseHLS  bool
		HLSKey  string
		Related []VideoFile
	}{
		Name:    name,
		File:    file,
		UseHLS:  useHLS,
		Related: related,
	}

	if useHLS {
		data.HLSKey = hlsJobKey(fullPath)
		// 预启动 HLS 转码
		if _, err := getOrStartHLS(fullPath); err != nil {
			log.Printf("[HLS] 启动失败: %v", err)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "player.html", data); err != nil {
		log.Printf("模板渲染错误: %v", err)
	}
}

func (s *Server) handleVideo(w http.ResponseWriter, r *http.Request) {
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
	// 只有原生 MP4（且 moov 在前面）才走直接提供
	http.ServeFile(w, r, fullPath)
}

// handleHLS 提供 HLS 分片文件（m3u8 和 ts）
func (s *Server) handleHLS(w http.ResponseWriter, r *http.Request) {
	// URL: /hls/{key}/{filename}
	path := strings.TrimPrefix(r.URL.Path, "/hls/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}

	key := parts[0]
	fileName := parts[1]

	// 安全校验：文件名不能包含路径分隔符
	if strings.Contains(fileName, "/") || strings.Contains(fileName, "..") {
		http.NotFound(w, r)
		return
	}

	// 查找对应的 HLS 任务并更新访问时间
	TouchHLS(key)

	hlsJobsMu.Lock()
	job, ok := hlsJobs[key]
	hlsJobsMu.Unlock()

	// 任务不在内存中，但磁盘缓存可能存在
	var hlsDir string
	if ok {
		hlsDir = job.Dir
	} else {
		cacheDir := filepath.Join(hlsCacheDir, key)
		if isCacheComplete(cacheDir) {
			hlsDir = cacheDir
		} else {
			http.Error(w, "转码任务不存在或已结束", http.StatusNotFound)
			return
		}
	}

	filePath := filepath.Join(hlsDir, fileName)

	// m3u8 可能还在生成中，等待文件出现且包含至少一个 .ts 引用
	if strings.HasSuffix(fileName, ".m3u8") {
		ready := false
		for i := 0; i < 150; i++ { // 最多等 15 秒
			data, err := os.ReadFile(filePath)
			if err == nil && strings.Contains(string(data), ".ts") {
				ready = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ready {
			http.Error(w, "m3u8 not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasSuffix(fileName, ".ts") {
		// ts 分片可能还在写入，等待文件出现
		ready := false
		for i := 0; i < 300; i++ { // 最多等 30 秒
			if _, err := os.Stat(filePath); err == nil {
				ready = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ready {
			http.Error(w, "ts segment not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
	}

	http.ServeFile(w, r, filePath)
}

// isValidPath 校验路径安全性，防止目录穿越
func (s *Server) isValidPath(relPath string) bool {
	if relPath == "" {
		return false
	}

	cleaned := filepath.Clean(relPath)

	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return false
	}

	full := filepath.Join(s.videoDir, cleaned)
	if !strings.HasPrefix(full, s.videoDir+string(os.PathSeparator)) {
		return false
	}

	ext := strings.ToLower(filepath.Ext(cleaned))
	return videoExts[ext]
}

func (s *Server) handleShorts(w http.ResponseWriter, r *http.Request) {
	videos, err := ScanVideos(s.videoDir)
	if err != nil {
		http.Error(w, "扫描视频目录失败", http.StatusInternalServerError)
		return
	}

	var direct []VideoFile
	for _, v := range videos {
		if v.DirectPlay {
			direct = append(direct, v)
		}
	}

	// Initial page: first 5 direct-play videos
	limit := 5
	if len(direct) < limit {
		limit = len(direct)
	}

	data := struct {
		Videos []VideoFile
		Total  int
	}{
		Videos: direct[:limit],
		Total:  len(direct),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "shorts.html", data); err != nil {
		log.Printf("模板渲染错误: %v", err)
	}
}

func (s *Server) handleAPIVideos(w http.ResponseWriter, r *http.Request) {
	videos, err := ScanVideos(s.videoDir)
	if err != nil {
		http.Error(w, "扫描视频目录失败", http.StatusInternalServerError)
		return
	}

	// Filter direct-play only if requested
	if r.URL.Query().Get("direct") == "1" {
		var filtered []VideoFile
		for _, v := range videos {
			if v.DirectPlay {
				filtered = append(filtered, v)
			}
		}
		videos = filtered
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size <= 0 {
		size = 20
	}
	total := len(videos)
	totalPages := (total + size - 1) / size
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * size
	end := start + size
	if end > total {
		end = total
	}

	apiVideos := make([]APIVideo, 0, end-start)
	for _, v := range videos[start:end] {
		apiVideos = append(apiVideos, APIVideo{
			Name:        v.Name,
			RelPath:     v.RelPath,
			Size:        v.Size,
			SizeStr:     v.SizeStr,
			Duration:    v.Duration,
			DurationSec: v.DurationSec,
			DirectPlay:  v.DirectPlay,
			Meta:        v.Meta,
		})
	}

	resp := APIVideoResponse{
		Videos:     apiVideos,
		Page:       page,
		PageSize:   size,
		Total:      total,
		TotalPages: totalPages,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}
