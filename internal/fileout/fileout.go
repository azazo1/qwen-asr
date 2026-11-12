// Package fileout 负责把输入与输出参数解析成具体文件路径, 以及筛选待处理文件.
//
// 这些都是纯路径逻辑, 不依赖任何推理引擎, 因此可以独立测试.
package fileout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azazo1/qwen-asr/internal/audio"
)

// 支持的文件大小与扩展名.
const (
	// FormatText 是纯文本格式.
	FormatText = "txt"
	// FormatSRT 是字幕格式.
	FormatSRT = "srt"
	// FormatJSON 是结构化格式.
	FormatJSON = "json"
)

// ParseFormats 解析输出格式列表.
//
// 空字符串返回默认的 srt; 重复项会被去重; 未知格式报错.
func ParseFormats(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{FormatSRT}, nil
	}

	allowed := map[string]bool{
		FormatText: true,
		FormatSRT:  true,
		FormatJSON: true,
	}

	var out []string
	seen := map[string]bool{}

	for _, part := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if !allowed[name] {
			return nil, fmt.Errorf("未知的输出格式 %q, 可选: txt, srt, json", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}

	if len(out) == 0 {
		return nil, errors.New("没有指定任何输出格式")
	}
	return out, nil
}

// ResolvePath 计算某个格式的输出文件路径.
//
// outputArg 为空时输出到输入文件旁边; 指向已有目录或以分隔符结尾时视为目录;
// 否则视为文件名并替换成对应扩展名.
func ResolvePath(outputArg, input, format string) string {
	ext := "." + format

	if outputArg == "" {
		dir := filepath.Dir(input)
		name := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
		return filepath.Join(dir, name+ext)
	}

	// 已有的目录, 或以分隔符结尾, 都当成目录.
	if info, err := os.Stat(outputArg); err == nil && info.IsDir() {
		name := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
		return filepath.Join(outputArg, name+ext)
	}
	if strings.HasSuffix(outputArg, string(os.PathSeparator)) || strings.HasSuffix(outputArg, "/") {
		name := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
		return filepath.Join(outputArg, name+ext)
	}

	// 明确的文件名: 扩展名一致时原样使用, 否则替换扩展名.
	if strings.EqualFold(filepath.Ext(outputArg), ext) {
		return outputArg
	}
	return strings.TrimSuffix(outputArg, filepath.Ext(outputArg)) + ext
}

// CollectInputs 把输入展开成待处理文件列表.
//
// 输入是目录时递归收集, 并按扩展名过滤掉不支持的文件.
func CollectInputs(input string) ([]string, error) {
	info, err := os.Stat(input)
	if err != nil {
		return nil, fmt.Errorf("无法访问输入 %s: %w", input, err)
	}

	if !info.IsDir() {
		if audio.ClassifyExt(input) == audio.KindUnsupported {
			return nil, fmt.Errorf("不支持的文件类型 %q (支持: %s)",
				filepath.Ext(input), strings.Join(audio.SupportedExts(), " "))
		}
		return []string{input}, nil
	}

	var out []string
	err = filepath.WalkDir(input, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if audio.ClassifyExt(path) == audio.KindUnsupported {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("遍历目录 %s 失败: %w", input, err)
	}

	return out, nil
}
