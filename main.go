package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
)

func main() {
	home, _ := os.UserHomeDir()
	defaultDir := filepath.Join(home, "Movies")

	dir := flag.String("dir", defaultDir, "视频文件目录")
	port := flag.Int("port", 8080, "服务器端口")
	clearCache := flag.Bool("clear-cache", false, "清空 HLS 转码缓存后退出")
	flag.Parse()

	// 初始化缓存
	if err := InitHLSCache(); err != nil {
		log.Fatalf("初始化 HLS 缓存失败: %v", err)
	}
	if err := InitThumbCache(); err != nil {
		log.Fatalf("初始化封面缓存失败: %v", err)
	}

	if *clearCache {
		if err := ClearHLSCache(); err != nil {
			log.Fatalf("清空缓存失败: %v", err)
		}
		fmt.Println("缓存已清空")
		return
	}

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		log.Fatalf("无效的目录路径: %v", err)
	}

	info, err := os.Stat(absDir)
	if err != nil || !info.IsDir() {
		log.Fatalf("目录不存在: %s", absDir)
	}

	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("LocalCinema 服务器启动中...\n")
	fmt.Printf("视频目录: %s\n", absDir)
	fmt.Printf("监听端口: %d\n", *port)

	if ips := getLocalIPs(); len(ips) > 0 {
		for _, ip := range ips {
			fmt.Printf("手机访问: http://%s:%d\n", ip, *port)
		}
	}

	if err := EnsureFFmpeg(); err != nil {
		fmt.Printf("警告: ffmpeg 未就绪: %v\n", err)
		fmt.Println("非 MP4 格式视频将无法播放")
	} else {
		fmt.Printf("ffmpeg: %s\n", ffmpegPath())
		fmt.Printf("ffprobe: %s\n", ffprobePath())
	}

	StartHLSReaper()

	srv := NewServer(absDir)
	log.Fatal(srv.ListenAndServe(addr))
}

func getLocalIPs() []string {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			ips = append(ips, ipnet.IP.String())
		}
	}
	return ips
}
