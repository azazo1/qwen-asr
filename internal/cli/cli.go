package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/azazo1/qwen-asr/internal/audio"
	"github.com/azazo1/qwen-asr/internal/buildinfo"
	"github.com/azazo1/qwen-asr/internal/config"
	"github.com/azazo1/qwen-asr/internal/engine"
	"github.com/azazo1/qwen-asr/internal/fileout"
	"github.com/azazo1/qwen-asr/internal/logging"
	"github.com/azazo1/qwen-asr/internal/model"
	"github.com/azazo1/qwen-asr/internal/prompt"
	"github.com/azazo1/qwen-asr/internal/transcribe"
)

// 退出码.
const (
	// ExitOK 表示成功.
	ExitOK = 0
	// ExitFailure 表示一般性失败.
	ExitFailure = 1
	// ExitUsage 表示命令行用法有误.
	ExitUsage = 2
	// ExitPartial 表示部分分段识别失败, 结果仍可查看.
	ExitPartial = 3
)

// Run 是命令行入口, 返回进程退出码.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printMainUsage(stderr)
		return ExitUsage
	}

	// 先处理没有子命令的简单请求.
	switch args[0] {
	case "version", "--version", "-V":
		fmt.Fprintln(stdout, buildinfo.Version())
		return ExitOK
	case "help", "--help", "-h":
		printMainUsage(stdout)
		return ExitOK
	case "models":
		return runModels(ctx, args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr)
	}

	// 其余情况都按 transcribe 处理, 兼容直接把 -i 放在最前面的用法.
	return runTranscribe(ctx, args, stdin, stdout, stderr)
}

func printMainUsage(w io.Writer) {
	fmt.Fprintf(w, `%s - 本地 Qwen3-ASR 语音转录

用法:
  %s -i <输入> [选项]        转录音频或视频文件
  %s models <子命令>          管理本地模型
  %s doctor [--engine-only]   检查运行环境
  %s version                  显示版本

常用选项:
  -i, --input     输入文件或目录 (支持 %s)
  -o, --output    输出路径, 默认与输入同目录
      --format    输出格式, 逗号分隔: txt,srt,json (默认 srt)
  -j, --jobs      并发转录的分段数 (默认 4)
      --model     0.6b / 1.7b / 本地 gguf 路径 (默认 %s)
      --language  固定识别语言, 例如 Chinese, 默认自动识别

运行 %s models -h 或 %s doctor -h 查看子命令细节.
`, config.AppName, config.AppName, config.AppName, config.AppName, config.AppName,
		strings.Join(audio.SupportedExts(), " "), model.DefaultKey, config.AppName, config.AppName)
}

// transcribeFlags 收集转录相关参数.
type transcribeFlags struct {
	input        string
	output       string
	format       string
	jobs         int
	chunkSeconds float64
	model        string
	language     string
	dataDir      string
	llamaLib     string
	threads      int
	keepTemp     bool
	verbose      bool
}

// parseTranscribeFlags 解析转录参数.
//
// 用 flag 的 Var 风格为同一参数注册多个名字, 便于同时支持长短选项.
func parseTranscribeFlags(args []string, stderr io.Writer) (*transcribeFlags, error) {
	fs := flag.NewFlagSet("transcribe", flag.ContinueOnError)
	fs.SetOutput(stderr)

	f := &transcribeFlags{}

	stringVar(fs, &f.input, "输入文件或目录", "i", "input")
	stringVar(fs, &f.output, "输出路径", "o", "output")
	stringVar(fs, &f.format, "输出格式, 逗号分隔: txt,srt,json (默认 srt)", "format")
	intVar(fs, &f.jobs, "并发转录的分段数", "j", "jobs")
	floatVar(fs, &f.chunkSeconds, "单段时长上限 (秒)", "chunk-seconds")
	stringVar(fs, &f.model, "模型档位或 gguf 路径", "m", "model")
	stringVar(fs, &f.language, "固定识别语言", "language")
	stringVar(fs, &f.dataDir, "数据目录", "data-dir")
	stringVar(fs, &f.llamaLib, "llama.cpp 库目录", "llama-lib")
	intVar(fs, &f.threads, "CPU 线程数, 0 表示自动", "threads")
	boolVar(fs, &f.keepTemp, "保留中间文件", "keep-temp")
	boolVar(fs, &f.verbose, "输出调试日志", "v", "verbose")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "用法: %s -i <输入> [选项]\n\n选项:\n", config.AppName)
		fs.PrintDefaults()
	}

	positionals, err := parseWithPositionals(fs, args)
	if err != nil {
		return nil, err
	}

	// 允许把输入文件作为位置参数, 更方便直接把文件拖进来.
	// 选项写在位置参数之后也能被正确解析, 见 parseWithPositionals.
	if f.input == "" && len(positionals) > 0 {
		f.input = positionals[0]
	}

	return f, nil
}

func runTranscribe(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	f, err := parseTranscribeFlags(args, stderr)
	if err != nil {
		return ExitUsage
	}

	if f.input == "" {
		fmt.Fprintf(stderr, "缺少输入: 请用 -i 指定要转录的文件或目录\n\n")
		printMainUsage(stderr)
		return ExitUsage
	}

	// 先检查引擎, 缺失时立即退出, 不进入模型下载或创建数据目录.
	logger := logging.New(stderr, f.verbose)
	explicitLib := f.llamaLib
	if explicitLib == "" {
		explicitLib = os.Getenv(config.EnvLlamaLib)
	}
	resolved, err := engine.Resolve(engine.ResolveOptions{ExplicitDir: explicitLib})
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}
	if err := engine.InitBackends(); err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}
	logger.Info("推理引擎已就绪", "dir", resolved.Dir, "origin", string(resolved.Origin))

	dirs, err := config.ResolveDataDir(f.dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}
	if err := dirs.Ensure(); err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}

	// 交互能力取决于 stdin 与 stderr 是否都是终端: 提问写 stderr, 读 stdin.
	interactive := IsTerminal(os.Stdin) && IsTerminal(os.Stderr)
	prompter := prompt.New(stdin, stderr, interactive)

	source, err := config.ParseDownloadSource(os.Getenv(config.EnvDownloadSource))
	if err != nil {
		fmt.Fprintf(stderr, "错误: %s 取值无效: %v\n", config.EnvDownloadSource, err)
		return ExitFailure
	}

	acquirer := &Acquirer{
		Dirs:     dirs,
		Prompter: prompter,
		Out:      stderr,
		Source:   source,
		Jobs:     8,
	}

	paths, err := resolveModelPaths(f.model, dirs, acquirer, ctx)
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}

	session, err := engine.Open(engine.Config{
		ModelPath:     paths.Model,
		ProjectorPath: paths.Projector,
		Threads:       f.threads,
		Logger:        logger,
	})
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}
	defer session.Close()

	toolchain := audio.DetectToolchain()
	if !toolchain.HasFFmpeg() {
		logger.Debug("未找到 ffmpeg, 视频与其他容器格式将不可用")
	}

	tr := transcribe.New(session, transcribe.Options{
		Jobs:         f.jobs,
		ChunkSeconds: f.chunkSeconds,
		Language:     f.language,
		KeepTemp:     f.keepTemp,
		TempDir:      dirs.TempDir(),
		Logger:       logger,
		Toolchain:    toolchain,
	})

	formats, err := fileout.ParseFormats(f.format)
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitUsage
	}

	inputs, err := fileout.CollectInputs(f.input)
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}
	if len(inputs) == 0 {
		fmt.Fprintf(stderr, "错误: 在 %s 里没有找到可处理的文件\n", f.input)
		return ExitFailure
	}

	exitCode := ExitOK
	for _, input := range inputs {
		result, err := tr.Run(ctx, input)
		if err != nil {
			// 取消是用户行为, 不再逐个报错.
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(stderr, "已取消.")
				return ExitFailure
			}
			fmt.Fprintf(stderr, "错误: %s: %v\n", input, err)
			exitCode = ExitFailure
			continue
		}

		if err := writeOutputs(f.output, input, result, formats, stdout); err != nil {
			fmt.Fprintf(stderr, "错误: %v\n", err)
			exitCode = ExitFailure
			continue
		}

		if result.HasFailure() && exitCode == ExitOK {
			exitCode = ExitPartial
		}
	}

	return exitCode
}

// writeOutputs 按要求的格式写出结果.
func writeOutputs(outputArg, input string, result transcribe.Result, formats []string, stdout io.Writer) error {
	// 没给输出路径时, txt 直接打到 stdout, 便于管道使用;
	// 其他格式仍然落盘到输入文件旁边.
	if outputArg == "" && len(formats) == 1 && formats[0] == "txt" {
		fmt.Fprintln(stdout, result.Text())
		return nil
	}

	for _, format := range formats {
		path := fileout.ResolvePath(outputArg, input, format)

		switch format {
		case "txt":
			if err := writeTextOutput(path, result, stdout); err != nil {
				return err
			}
		case "srt":
			if err := writeSRTOutput(path, result); err != nil {
				return err
			}
		case "json":
			if err := writeJSONOutput(path, result); err != nil {
				return err
			}
		}
	}

	return nil
}

// 输出参数是目录或以分隔符结尾时, 视为目录;
// 否则视为文件名, 并替换成对应扩展名.
func resolveOutputPath(outputArg, input, format string) string {
	ext := "." + format

	if outputArg == "" {
		base := input
		dir := filepath.Dir(base)
		name := strings.TrimSuffix(filepath.Base(base), filepath.Ext(base))
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

	// 明确的文件名: 只有单格式时尊重用户给的名字, 多格式时各自换扩展名.
	if filepath.Ext(outputArg) == ext {
		return outputArg
	}
	return strings.TrimSuffix(outputArg, filepath.Ext(outputArg)) + ext
}
