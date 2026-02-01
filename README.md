# LocalCinema

轻量级家庭影院服务器。一个命令启动，手机、平板、电脑打开浏览器即可观看本地视频。

## 功能

- **全格式支持** — MP4 直接播放，MKV / AVI / MOV / WebM / M4V / WMV / FLV 自动 HLS 转码
- **自动下载 ffmpeg** — 首次运行时自动下载 ffmpeg/ffprobe，无需手动安装
- **硬件加速转码** — macOS 使用 VideoToolbox，转码快速且 CPU 占用低
- **智能缓存** — 转码结果、视频封面、时长信息持久缓存，二次播放秒开
- **播放进度记忆** — 自动保存播放位置，下次打开提示从上次位置继续
- **深色/浅色主题** — 自动跟随系统，也可手动切换
- **多设备访问** — 局域网内任何设备浏览器可用，移动端和桌面端自适应布局
- **视频封面与时长** — 自动生成缩略图和时长显示
- **搜索与视图切换** — 首页搜索、列表/平铺视图切换
- **隐私优先** — 纯本地运行，不依赖任何第三方服务

## 安装

**Homebrew (macOS/Linux)**
```bash
brew install raojinlin/tap/localcinema
```

**一键安装脚本**
```bash
curl -fsSL https://raw.githubusercontent.com/raojinlin/localcinema/main/install.sh | sh
```

**Go Install**
```bash
go install github.com/raojinlin/localcinema@latest
```

**Docker**
```bash
docker run -v ~/Movies:/videos -p 8080:8080 ghcr.io/raojinlin/localcinema
```

**二进制下载**

前往 [GitHub Releases](https://github.com/raojinlin/localcinema/releases) 下载对应平台的压缩包。

## 快速开始

```bash
# 启动（默认扫描 ~/Movies，监听 8080 端口）
localcinema

# 指定目录和端口
localcinema -dir /path/to/videos -port 9090

# 清空 HLS 转码缓存
localcinema -clear-cache
```

手机连接同一 WiFi，浏览器访问终端输出的地址即可。

## 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-dir` | `~/Movies` | 视频文件目录 |
| `-port` | `8080` | 服务器监听端口 |
| `-clear-cache` | — | 清空 HLS 转码缓存后退出 |

## ffmpeg

程序启动时会按以下顺序查找 ffmpeg/ffprobe：

1. `~/.cache/localcinema/bin/` — 本地缓存
2. 系统 `PATH`

如果都找不到，会自动从网络下载静态编译版本到 `~/.cache/localcinema/bin/`，支持 macOS 和 Linux（amd64/arm64）。

也可以手动安装：

```bash
# macOS
brew install ffmpeg

# Linux (Ubuntu/Debian)
sudo apt install ffmpeg
```

macOS 下使用 VideoToolbox 硬件加速转码（`h264_videotoolbox`），需要 ffmpeg 编译时启用 `--enable-videotoolbox`。

## 缓存

所有缓存存储在 `~/.cache/localcinema/`：

| 目录 | 内容 |
|------|------|
| `bin/` | 自动下载的 ffmpeg/ffprobe |
| `hls/` | HLS 转码分片（m3u8 + ts），视频文件修改后自动失效 |
| `thumbs/` | 视频封面（jpg）和时长信息（dur） |

## 支持的格式

| 格式 | 播放方式 |
|------|----------|
| `.mp4` `.m4v` | 直接播放（H.264）/ HLS 转码（HEVC 等） |
| `.mkv` `.avi` `.mov` `.webm` `.wmv` `.flv` | 自动 HLS 转码 |

## 技术栈

- Go（单二进制，内嵌模板和静态资源）
- ffmpeg / ffprobe（转码与探测）
- HLS.js（浏览器端 HLS 播放）
- 纯 CSS 深色/浅色主题，无 JS 框架依赖

## 依赖

- Go 1.22+
- ffmpeg / ffprobe（自动下载或手动安装）
