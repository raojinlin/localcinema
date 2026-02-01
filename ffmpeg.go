package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

var (
	ffmpegBin  string
	ffprobeBin string
)

func binCacheDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "localcinema", "bin")
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func ffmpegPath() string {
	if ffmpegBin != "" {
		return ffmpegBin
	}
	return "ffmpeg" + exeSuffix()
}

func ffprobePath() string {
	if ffprobeBin != "" {
		return ffprobeBin
	}
	return "ffprobe" + exeSuffix()
}

// EnsureFFmpeg locates or downloads ffmpeg and ffprobe.
func EnsureFFmpeg() error {
	dir := binCacheDir()

	// Check local cache first, then system PATH
	for _, tool := range []struct {
		name string
		ptr  *string
	}{
		{"ffmpeg", &ffmpegBin},
		{"ffprobe", &ffprobeBin},
	} {
		local := filepath.Join(dir, tool.name+exeSuffix())
		if _, err := os.Stat(local); err == nil {
			*tool.ptr = local
			continue
		}
		if p, err := exec.LookPath(tool.name); err == nil {
			*tool.ptr = p
		}
	}

	// If both resolved, done
	if ffmpegBin != "" && ffprobeBin != "" {
		return nil
	}

	// Need to download
	osName, arch, err := platformInfo()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	if runtime.GOOS == "windows" {
		// Windows: gyan.dev 提供单个 zip 包含 ffmpeg.exe 和 ffprobe.exe
		ffmpegDest := filepath.Join(dir, "ffmpeg.exe")
		ffprobeDest := filepath.Join(dir, "ffprobe.exe")
		_, errF := os.Stat(ffmpegDest)
		_, errP := os.Stat(ffprobeDest)
		if errF != nil || errP != nil {
			url := "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"
			fmt.Println("正在下载 ffmpeg (Windows) ...")
			if err := downloadAndExtractMultiple(url, dir, []string{"ffmpeg.exe", "ffprobe.exe"}); err != nil {
				return fmt.Errorf("下载 ffmpeg 失败: %w", err)
			}
			fmt.Println("ffmpeg 下载完成")
		}
	} else {
		for _, tool := range []string{"ffmpeg", "ffprobe"} {
			dest := filepath.Join(dir, tool)
			if _, err := os.Stat(dest); err == nil {
				continue // already exists
			}

			url := fmt.Sprintf("https://ffmpeg.martin-riedl.de/redirect/latest/%s/%s/release/%s.zip", osName, arch, tool)
			fmt.Printf("正在下载 %s ...\n", tool)

			if err := downloadAndExtract(url, tool, dest); err != nil {
				return fmt.Errorf("下载 %s 失败: %w", tool, err)
			}
			fmt.Printf("%s 下载完成\n", tool)
		}
	}

	ffmpegBin = filepath.Join(dir, "ffmpeg"+exeSuffix())
	ffprobeBin = filepath.Join(dir, "ffprobe"+exeSuffix())
	return nil
}

func platformInfo() (osName, arch string, err error) {
	switch runtime.GOOS {
	case "darwin":
		osName = "macos"
	case "linux":
		osName = "linux"
	case "windows":
		osName = "windows"
	default:
		return "", "", fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}

	switch runtime.GOARCH {
	case "amd64":
		arch = "amd64"
	case "arm64":
		arch = "arm64"
	default:
		return "", "", fmt.Errorf("不支持的架构: %s", runtime.GOARCH)
	}
	return
}

// downloadAndExtractMultiple 下载 zip 并提取多个二进制到 dir（用于 Windows gyan.dev 包）
func downloadAndExtractMultiple(url, dir string, binaries []string) error {
	tmp, err := downloadToTemp(url, "ffmpeg")
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	zr, err := zip.OpenReader(tmp)
	if err != nil {
		return err
	}
	defer zr.Close()

	need := make(map[string]bool)
	for _, b := range binaries {
		need[b] = true
	}

	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if !need[name] {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, name)
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
		delete(need, name)
	}

	if len(need) > 0 {
		var missing []string
		for b := range need {
			missing = append(missing, b)
		}
		return fmt.Errorf("zip 中未找到: %v", missing)
	}
	return nil
}

// downloadToTemp 下载 URL 到临时文件，返回路径
func downloadToTemp(url, prefix string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", prefix+"-*.zip")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()

	var downloaded int64
	buf := make([]byte, 256*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := tmp.Write(buf[:n]); err != nil {
				tmp.Close()
				os.Remove(tmpPath)
				return "", err
			}
			downloaded += int64(n)
			fmt.Printf("\r  已下载: %.1f MB", float64(downloaded)/(1024*1024))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return "", readErr
		}
	}
	fmt.Println()
	tmp.Close()
	return tmpPath, nil
}

func downloadAndExtract(url, binaryName, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Write to temp file (needed for zip random access)
	tmp, err := os.CreateTemp("", binaryName+"-*.zip")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	var downloaded int64
	buf := make([]byte, 256*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := tmp.Write(buf[:n]); err != nil {
				tmp.Close()
				return err
			}
			downloaded += int64(n)
			fmt.Printf("\r  已下载: %.1f MB", float64(downloaded)/(1024*1024))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			tmp.Close()
			return readErr
		}
	}
	fmt.Println()
	tmp.Close()

	// Extract binary from zip
	zr, err := zip.OpenReader(tmpPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	matchName := binaryName + exeSuffix()
	for _, f := range zr.File {
		if filepath.Base(f.Name) == matchName {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, rc)
			out.Close()
			return err
		}
	}

	return fmt.Errorf("zip 中未找到 %s", binaryName)
}
