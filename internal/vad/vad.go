// Package vad 用 Silero VAD 模型检测语音区间.
//
// 推理走 onnxruntime 的纯 Go 绑定 (purego + 运行时 dlopen), 因此本仓库不引入 cgo.
// onnxruntime 与 Silero 模型都由用户提供: 这两者一个体积大, 一个属于模型权重,
// 都不适合随二进制分发, 缺失时直接报错并提示获取方式.
//
// 本包只负责"哪几段是语音"; 超长语音如何切给模型, 交给 audio 包.
package vad

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Range 是一段语音在原音频中的位置, 单位秒.
type Range struct {
	Start float64
	End   float64
}

// Duration 返回区间时长, 单位秒.
func (r Range) Duration() float64 { return r.End - r.Start }

// SampleRate 是 Silero VAD 要求的采样率.
const SampleRate = 16000

// 环境变量名.
const (
	// EnvModel 指定 Silero VAD 模型文件.
	EnvModel = "QWEN_ASR_VAD_MODEL"
	// EnvOnnxLib 指定 onnxruntime 动态库.
	EnvOnnxLib = "QWEN_ASR_ONNX_LIB"
)

// ErrNotConfigured 表示没有启用 VAD, 调用方应回退到能量分段.
var ErrNotConfigured = errors.New("未配置 VAD 模型")

const libraryHint = "请安装 ONNX Runtime 1.28 的动态库 " +
	"(https://github.com/microsoft/onnxruntime/releases), 并用 --onnx-lib 或 " +
	EnvOnnxLib + " 指定库文件或库目录"

const modelHint = "请下载 Silero VAD 的 ONNX 模型 (https://github.com/snakers4/silero-vad), " +
	"并用 --vad 或 " + EnvModel + " 指定模型文件"

// ONNXRuntimeCandidates 返回 onnxruntime 动态库的常见位置, 顺序即优先级.
func ONNXRuntimeCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/opt/homebrew/lib/libonnxruntime.dylib",
			"/usr/local/lib/libonnxruntime.dylib",
			"/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib",
		}
	case "windows":
		var out []string
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if base := os.Getenv(env); base != "" {
				out = append(out, filepath.Join(base, "onnxruntime", "lib", "onnxruntime.dll"))
				out = append(out, filepath.Join(base, "onnxruntime", "onnxruntime.dll"))
			}
		}
		return out
	default:
		return []string{
			"/usr/local/lib/libonnxruntime.so",
			"/usr/lib/libonnxruntime.so",
			"/usr/lib64/libonnxruntime.so",
		}
	}
}

// ResolveLibrary 找出 onnxruntime 动态库文件.
//
// 显式指定的路径可以是库文件本身, 也可以是包含库的目录.
func ResolveLibrary(explicit string) (string, error) {
	if explicit != "" {
		path, err := locateLibrary(explicit)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	for _, candidate := range ONNXRuntimeCandidates() {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("未找到 ONNX Runtime 动态库. %s", libraryHint)
}

// locateLibrary 把显式路径解析成实际的库文件.
func locateLibrary(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("无法访问 onnxruntime 库 %s: %w. %s", path, err, libraryHint)
	}
	if !info.IsDir() {
		return path, nil
	}

	// 目录形式: 按当前平台的文件名在目录里找.
	for _, name := range libraryFileNames() {
		candidate := filepath.Join(path, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("目录 %s 里没有 onnxruntime 动态库 (期望 %s). %s",
		path, strings.Join(libraryFileNames(), " 或 "), libraryHint)
}

// libraryFileNames 返回当前平台可能的 onnxruntime 库文件名.
func libraryFileNames() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"onnxruntime.dll"}
	case "darwin":
		return []string{
			"libonnxruntime.dylib",
			"libonnxruntime_" + runtime.GOARCH + ".dylib",
		}
	default:
		return []string{
			"libonnxruntime.so",
			"libonnxruntime_" + runtime.GOARCH + ".so",
		}
	}
}

// ResolveModel 找出 Silero VAD 模型文件.
//
// explicit 为空时依次尝试环境变量与数据目录下的约定位置, 找不到就返回 ErrNotConfigured,
// 让调用方安静地回退到能量分段, 而不是把缺少可选模型当成错误.
func ResolveModel(explicit, dataDir string) (string, error) {
	if explicit == "" {
		explicit = os.Getenv(EnvModel)
	}
	if explicit != "" {
		info, err := os.Stat(explicit)
		if err != nil {
			return "", fmt.Errorf("无法访问 VAD 模型 %s: %w. %s", explicit, err, modelHint)
		}
		if info.IsDir() {
			return "", fmt.Errorf("VAD 模型路径 %s 是目录, 需要指向 .onnx 文件. %s", explicit, modelHint)
		}
		return explicit, nil
	}

	for _, candidate := range DefaultModelPaths(dataDir) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", ErrNotConfigured
}

// DefaultModelPaths 返回自动查找 VAD 模型的位置, 顺序即优先级.
func DefaultModelPaths(dataDir string) []string {
	var out []string
	if dataDir != "" {
		out = append(out, filepath.Join(dataDir, "models", "silero_vad.onnx"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".local", "share", "qwen-asr", "models", "silero_vad.onnx"))
	}
	return out
}
