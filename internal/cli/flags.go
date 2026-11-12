package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/azazo1/qwen-asr/internal/subtitle"
	"github.com/azazo1/qwen-asr/internal/transcribe"
)

// parseWithPositionals 解析参数并返回位置参数.
//
// Go 的 flag 包遇到第一个非选项参数就会停止解析, 于是
// "models download 0.6b --source hf" 里的 --source 会被忽略.
// 这里反复调用 Parse 继续吃掉剩余参数, 让选项出现在位置参数之后也能生效.
func parseWithPositionals(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	rest := args

	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positionals, nil
		}

		// 取出当前位置参数, 从它之后继续解析.
		positionals = append(positionals, fs.Arg(0))
		rest = fs.Args()[1:]
	}
}

// stringVar 让同一个参数可以注册多个名字.
//
// flag 包对同一变量多次 Var 是安全的, 解析时任意一个名字都会写入同一处.

// inputList 收集可重复出现的输入参数.
//
// 每个 --input 或位置参数都会追加, 因此 qwen-asr a.mp4 b.mp4 与
// qwen-asr -i a.mp4 -i b.mp4 等价, 也会把 shell 通配符展开的结果全部收下.
type inputList struct {
	dst *[]string
}

// String 实现 flag.Value.
func (l inputList) String() string {
	if l.dst == nil {
		return ""
	}
	return strings.Join(*l.dst, ",")
}

// Set 追加一个输入.
func (l inputList) Set(v string) error {
	// 允许用逗号一次给多个, 与 --format 的风格保持一致.
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		*l.dst = append(*l.dst, part)
	}
	return nil
}

func stringVar(fs *flag.FlagSet, p *string, usage string, names ...string) {
	for _, name := range names {
		fs.StringVar(p, name, *p, usage)
	}
}

func intVar(fs *flag.FlagSet, p *int, usage string, names ...string) {
	for _, name := range names {
		fs.IntVar(p, name, *p, usage)
	}
}

func floatVar(fs *flag.FlagSet, p *float64, usage string, names ...string) {
	for _, name := range names {
		fs.Float64Var(p, name, *p, usage)
	}
}

func boolVar(fs *flag.FlagSet, p *bool, usage string, names ...string) {
	for _, name := range names {
		fs.BoolVar(p, name, *p, usage)
	}
}

// writeLine 是给各子命令共用的小工具.
func writeLine(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, format+"\n", args...)
}

// writeTextOutput 把纯文本结果写到文件或 stdout.
func writeTextOutput(path string, result transcribe.Result, stdout io.Writer) error {
	if path == "-" {
		fmt.Fprintln(stdout, result.Text())
		return nil
	}
	return subtitle.WriteText(path, result.Text())
}

// writeSRTOutput 写 srt 字幕.
func writeSRTOutput(path string, result transcribe.Result) error {
	if err := subtitle.WriteSRT(path, result.SubtitleSegments()); err != nil {
		return fmt.Errorf("写出 srt 失败: %w", err)
	}
	return nil
}

// writeJSONOutput 写 json 结果.
func writeJSONOutput(path string, result transcribe.Result) error {
	doc := subtitle.Document{
		File:     result.Input,
		Duration: result.Duration,
		Segments: result.SubtitleSegments(),
	}
	if err := subtitle.WriteJSON(path, doc); err != nil {
		return fmt.Errorf("写出 json 失败: %w", err)
	}
	return nil
}
