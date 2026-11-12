// Package model 维护内置的模型清单, 并判断本地模型是否完整.
package model

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File 描述模型仓库里的一个文件.
type File struct {
	// Name 是仓库内的文件名, 也是本地保存的文件名.
	Name string
	// Size 是文件的准确字节数, 用于判断完整性.
	Size int64
	// SHA256 是文件内容的哈希. 只有上游提供时才填, 空表示无法校验.
	SHA256 string
}

// Spec 描述一档模型.
type Spec struct {
	// Key 是命令行使用的短名, 例如 0.6b.
	Key string
	// Repo 是 HuggingFace 与 ModelScope 共用的仓库 id.
	Repo string
	// Model 是主模型文件.
	Model File
	// Projector 是音频投影器文件, 与主模型配套.
	Projector File
}

// TotalSize 返回该档模型需要下载的总字节数.
func (s Spec) TotalSize() int64 { return s.Model.Size + s.Projector.Size }

// Files 返回需要下载的全部文件.
func (s Spec) Files() []File { return []File{s.Model, s.Projector} }

// HumanSize 把字节数格式化成便于阅读的形式.
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(n)
	for _, u := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, u)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}

// 内置的两档模型. 都使用 Q8_0 量化: 相对 bf16 体积减半, 精度损失很小.
var (
	spec06b = Spec{
		Key:  "0.6b",
		Repo: "ggml-org/Qwen3-ASR-0.6B-GGUF",
		Model: File{
			Name: "Qwen3-ASR-0.6B-Q8_0.gguf",
			Size: 804749248,
		},
		Projector: File{
			Name: "mmproj-Qwen3-ASR-0.6B-Q8_0.gguf",
			Size: 214392480,
		},
	}

	spec17b = Spec{
		Key:  "1.7b",
		Repo: "ggml-org/Qwen3-ASR-1.7B-GGUF",
		Model: File{
			Name: "Qwen3-ASR-1.7B-Q8_0.gguf",
			Size: 2165034944,
		},
		Projector: File{
			Name: "mmproj-Qwen3-ASR-1.7B-Q8_0.gguf",
			Size: 355709344,
		},
	}
)

// DefaultKey 是没有指定模型时使用的档位.
const DefaultKey = "0.6b"

// Catalog 返回全部可选档位, 顺序固定便于展示.
func Catalog() []Spec { return []Spec{spec06b, spec17b} }

// Lookup 按短名查找模型. 名字大小写不敏感.
func Lookup(key string) (Spec, bool) {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, s := range Catalog() {
		if s.Key == normalized {
			return s, true
		}
	}
	return Spec{}, false
}

// Keys 返回全部档位短名.
func Keys() []string {
	specs := Catalog()
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Key)
	}
	sort.Strings(out)
	return out
}

// Paths 是一档模型在本地磁盘上的两个文件路径.
type Paths struct {
	Model     string
	Projector string
}

// Paths 计算该档模型在给定目录下的路径.
func (s Spec) Paths(modelsDir string) Paths {
	return Paths{
		Model:     filepath.Join(modelsDir, s.Model.Name),
		Projector: filepath.Join(modelsDir, s.Projector.Name),
	}
}

// Status 描述一档模型在本地的就绪情况.
type Status struct {
	Spec Spec
	// Missing 是缺失或大小不符的文件.
	Missing []File
	// Paths 是本地路径.
	Paths Paths
}

// Ready 表示该档模型可以立即使用.
func (s Status) Ready() bool { return len(s.Missing) == 0 }

// Describe 返回缺失情况的简短描述.
func (s Status) Describe() string {
	if s.Ready() {
		return "已就绪"
	}
	names := make([]string, 0, len(s.Missing))
	for _, f := range s.Missing {
		names = append(names, f.Name)
	}
	return "缺少 " + strings.Join(names, ", ")
}

// Check 判断一档模型在本地是否完整.
//
// 只比对文件大小, 不做哈希: 下载中断留下的文件几乎总会改变大小,
// 而完整哈希在 2 GB 量级上代价较高, 由 --verify-sha256 单独触发.
func Check(spec Spec, modelsDir string) Status {
	status := Status{Spec: spec, Paths: spec.Paths(modelsDir)}

	for _, f := range spec.Files() {
		path := filepath.Join(modelsDir, f.Name)
		info, err := os.Stat(path)
		if err != nil {
			status.Missing = append(status.Missing, f)
			continue
		}
		if info.IsDir() || info.Size() != f.Size {
			status.Missing = append(status.Missing, f)
		}
	}

	return status
}

// CheckAll 返回全部档位的就绪情况.
func CheckAll(modelsDir string) []Status {
	specs := Catalog()
	out := make([]Status, 0, len(specs))
	for _, s := range specs {
		out = append(out, Check(s, modelsDir))
	}
	return out
}

// ReadySpecs 返回本地已就绪的档位.
func ReadySpecs(modelsDir string) []Spec {
	var out []Spec
	for _, status := range CheckAll(modelsDir) {
		if status.Ready() {
			out = append(out, status.Spec)
		}
	}
	return out
}

// ResolvePath 把一个 --model 取值解析成具体文件路径.
//
// 取值可以是档位短名 (0.6b / 1.7b), 也可以是本地 gguf 路径.
// 返回主模型路径与投影器路径; 传路径时投影器需要靠同名约定推断.
func ResolvePath(value, modelsDir string) (Paths, error) {
	if value == "" {
		value = DefaultKey
	}

	if spec, ok := Lookup(value); ok {
		status := Check(spec, modelsDir)
		if !status.Ready() {
			return Paths{}, fmt.Errorf("模型 %s %s", spec.Key, status.Describe())
		}
		return status.Paths, nil
	}

	// 当作本地路径处理.
	info, err := os.Stat(value)
	if err != nil {
		return Paths{}, fmt.Errorf("无法识别的 --model 取值 %q: 既不是 %s, 也不是可读的文件",
			value, strings.Join(Keys(), " / "))
	}
	if info.IsDir() {
		return Paths{}, fmt.Errorf("--model 指向目录 %q, 请给出具体的 .gguf 文件", value)
	}

	return Paths{Model: value}, nil
}

// ProjectorFor 按模型文件名推断配套投影器路径.
//
// 约定与上游仓库一致: 投影器名是 mmproj-<主模型名>.
// 找不到同目录同名文件时返回空串.
func ProjectorFor(modelPath string) string {
	dir := filepath.Dir(modelPath)
	base := filepath.Base(modelPath)
	candidate := filepath.Join(dir, "mmproj-"+base)

	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return ""
}
