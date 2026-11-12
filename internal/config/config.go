// Package config 负责解析运行期用到的路径与默认值.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// 环境变量名. 使用 QWEN_ASR_ 前缀, 避免与其他工具冲突.
const (
	EnvDataDir        = "QWEN_ASR_DATA_DIR"
	EnvDownloadSource = "QWEN_ASR_DOWNLOAD_SOURCE"
	EnvLlamaLib       = "QWEN_ASR_LLAMA_LIB"
)

// AppName 是数据目录与日志中使用的程序名.
const AppName = "qwen-asr"

// Dirs 描述数据目录下的各个子目录.
type Dirs struct {
	// Root 是数据目录根.
	Root string
}

// ResolveDataDir 按优先级解析数据目录.
//
// 优先级: explicit (命令行) > 环境变量 > 平台默认目录.
func ResolveDataDir(explicit string) (Dirs, error) {
	if explicit != "" {
		root, err := filepath.Abs(explicit)
		if err != nil {
			return Dirs{}, fmt.Errorf("无法解析数据目录 %q: %w", explicit, err)
		}
		return Dirs{Root: root}, nil
	}

	if fromEnv := os.Getenv(EnvDataDir); fromEnv != "" {
		root, err := filepath.Abs(fromEnv)
		if err != nil {
			return Dirs{}, fmt.Errorf("无法解析 %s 指向的数据目录 %q: %w", EnvDataDir, fromEnv, err)
		}
		return Dirs{Root: root}, nil
	}

	root, err := defaultDataDir()
	if err != nil {
		return Dirs{}, err
	}
	return Dirs{Root: root}, nil
}

// ModelsDir 返回模型存放目录.
func (d Dirs) ModelsDir() string { return filepath.Join(d.Root, "models") }

// TempDir 返回转录过程中的临时文件目录.
func (d Dirs) TempDir() string { return filepath.Join(d.Root, "tmp") }

// Ensure 创建数据目录下的必要子目录.
func (d Dirs) Ensure() error {
	for _, dir := range []string{d.Root, d.ModelsDir(), d.TempDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("无法创建目录 %s: %w", dir, err)
		}
	}
	return nil
}

// defaultDataDir 按平台惯例给出默认数据目录.
func defaultDataDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法获取用户主目录: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", AppName), nil
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, AppName), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法获取用户主目录: %w", err)
		}
		return filepath.Join(home, "AppData", "Local", AppName), nil
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, AppName), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法获取用户主目录: %w", err)
		}
		return filepath.Join(home, ".local", "share", AppName), nil
	}
}

// DownloadSource 表示模型下载源.
type DownloadSource string

// 支持的下载源.
const (
	SourceModelScope  DownloadSource = "modelscope"
	SourceHuggingFace DownloadSource = "hf"
)

// ParseDownloadSource 解析下载源标识.
//
// 空字符串返回空值, 表示使用默认策略 (优先 ModelScope, 失败回落 HF).
func ParseDownloadSource(raw string) (DownloadSource, error) {
	switch DownloadSource(raw) {
	case "":
		return "", nil
	case SourceModelScope:
		return SourceModelScope, nil
	case SourceHuggingFace, "huggingface":
		return SourceHuggingFace, nil
	default:
		return "", errors.New("下载源只能是 modelscope 或 hf")
	}
}
