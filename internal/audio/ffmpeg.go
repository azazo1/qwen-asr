package audio

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// 支持的音频扩展名, 这些格式由纯 Go 解码器处理, 不需要 ffmpeg.
var nativeAudioExts = map[string]bool{
	".wav":  true,
	".mp3":  true,
	".flac": true,
}

// 支持的视频容器扩展名, 需要 ffmpeg 抽取音轨.
var videoExts = map[string]bool{
	".mp4":  true,
	".mkv":  true,
	".mov":  true,
	".webm": true,
	".avi":  true,
	".flv":  true,
	".ts":   true,
	".m4v":  true,
	".wmv":  true,
}

// 其他常见音频容器, 也需要 ffmpeg 转换.
var containerAudioExts = map[string]bool{
	".m4a":  true,
	".aac":  true,
	".ogg":  true,
	".opus": true,
	".wma":  true,
	".aiff": true,
	".aif":  true,
	".amr":  true,
}

// Kind 描述一个输入文件的处理方式.
type Kind int

// 输入类型.
const (
	// KindUnsupported 表示扩展名不在支持范围内.
	KindUnsupported Kind = iota
	// KindNative 表示可以由纯 Go 解码器直接处理.
	KindNative
	// KindNeedsFFmpeg 表示需要 ffmpeg 先抽出音轨.
	KindNeedsFFmpeg
)

// ClassifyExt 根据扩展名判断处理方式.
func ClassifyExt(path string) Kind {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case nativeAudioExts[ext]:
		return KindNative
	case videoExts[ext], containerAudioExts[ext]:
		return KindNeedsFFmpeg
	default:
		return KindUnsupported
	}
}

// SupportedExts 返回全部可接受的扩展名, 供目录扫描与帮助信息使用.
func SupportedExts() []string {
	var out []string
	for ext := range nativeAudioExts {
		out = append(out, ext)
	}
	for ext := range videoExts {
		out = append(out, ext)
	}
	for ext := range containerAudioExts {
		out = append(out, ext)
	}
	return out
}

// Toolchain 描述本机可用的外部音频工具.
type Toolchain struct {
	// FFmpeg 是 ffmpeg 可执行文件路径, 空表示不可用.
	FFmpeg string
	// FFprobe 是 ffprobe 可执行文件路径, 空表示不可用.
	FFprobe string
}

// DetectToolchain 探测 ffmpeg 与 ffprobe.
//
// 两者都缺失不是错误, 此时仍然可以处理 wav / mp3 / flac.
func DetectToolchain() Toolchain {
	var tc Toolchain
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		tc.FFmpeg = p
	}
	if p, err := exec.LookPath("ffprobe"); err == nil {
		tc.FFprobe = p
	}
	return tc
}

// HasFFmpeg 表示能否处理视频与其他容器格式.
func (t Toolchain) HasFFmpeg() bool { return t.FFmpeg != "" }

// ExtractAudio 用 ffmpeg 把任意容器转成 16 kHz 单声道 wav.
//
// 这是视频与 m4a 等格式进入纯 Go 解码管线之前的预处理步骤.
func (t Toolchain) ExtractAudio(ctx context.Context, input, output string) error {
	if !t.HasFFmpeg() {
		return fmt.Errorf("处理 %s 需要 ffmpeg, 但当前环境未找到 ffmpeg", filepath.Ext(input))
	}

	cmd := exec.CommandContext(ctx, t.FFmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", input,
		"-vn",      // 丢掉视频流
		"-ac", "1", // 单声道
		"-ar", fmt.Sprint(SampleRate),
		"-c:a", "pcm_s16le",
		output,
	)

	// 整个转换过程不产生交互, 也不读 stdin.
	cmd.Stdin = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("ffmpeg 转换失败: %w: %s", err, msg)
		}
		return fmt.Errorf("ffmpeg 转换失败: %w", err)
	}
	return nil
}

// ProbeDuration 用 ffprobe 读取媒体时长, 单位秒.
func (t Toolchain) ProbeDuration(ctx context.Context, input string) (float64, error) {
	if t.FFprobe == "" {
		return 0, fmt.Errorf("读取 %s 时长需要 ffprobe", filepath.Base(input))
	}

	cmd := exec.CommandContext(ctx, t.FFprobe,
		"-hide_banner", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe 读取时长失败: %w", err)
	}

	var d float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &d); err != nil {
		return 0, fmt.Errorf("无法解析 ffprobe 输出的时长: %w", err)
	}
	return d, nil
}
