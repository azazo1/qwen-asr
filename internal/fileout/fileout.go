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

// FormattedExt 判断后缀是否对应一种输出格式, 是则返回该格式名.
//
// 用于让 -o 的后缀直接决定输出格式: 写 -o 字幕.json 就产出 json,
// 不必再额外写 --format, 也不会把用户写的后缀悄悄替换成别的格式.
func FormattedExt(path string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case FormatText, FormatSRT, FormatJSON:
		return ext, true
	default:
		return "", false
	}
}

// IsOutputDir 判断 -o 的取值是否表示目录而不是单个文件.
//
// 空串与 "-" 表示不指定输出文件, 按目录处理;
// 已存在的目录, 以及以路径分隔符结尾的写法, 也都视为目录.
func IsOutputDir(outputArg string) bool {
	if outputArg == "" || outputArg == "-" {
		return true
	}
	if info, err := os.Stat(outputArg); err == nil && info.IsDir() {
		return true
	}
	return strings.HasSuffix(outputArg, string(os.PathSeparator)) || strings.HasSuffix(outputArg, "/")
}

// FormatsForOutput 决定本次要写哪些格式.
//
// 显式的 formatArg 优先. 未指定时, 从 -o 的后缀推断:
// 目录或未给出时不带格式信息, 交由默认格式处理.
func FormatsForOutput(formatArg, outputArg string) ([]string, error) {
	if strings.TrimSpace(formatArg) != "" {
		return ParseFormats(formatArg)
	}

	if IsOutputDir(outputArg) {
		return ParseFormats("")
	}

	if format, ok := FormattedExt(outputArg); ok {
		return []string{format}, nil
	}
	return nil, fmt.Errorf(
		"无法从输出路径 %s 推断格式; 请使用 .txt/.srt/.json 后缀, 或用 --format 指定", outputArg)
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

// CollectInputsAll 展开多个输入, 按给定顺序去重后返回.
//
// 单个输入出错不会中断其余输入: 例如同时给了一个不存在的文件和一个目录,
// 目录里的文件仍应处理. 出错信息汇总后与结果一起返回, 由调用方决定如何呈现.
// 同一个文件通过不同写法重复给出时只处理一次, 避免重复转录和互相覆盖输出.
func CollectInputsAll(inputs []string) ([]string, []error) {
	var (
		out  []string
		errs []error
		seen = map[string]bool{}
	)

	for _, input := range inputs {
		paths, err := CollectInputs(input)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, path := range paths {
			// 用绝对路径判重, 这样 ./a.wav 与 a.wav 视为同一个文件.
			key, keyErr := filepath.Abs(path)
			if keyErr != nil {
				key = path
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, path)
		}
	}

	return out, errs
}
