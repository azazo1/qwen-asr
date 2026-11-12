package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/azazo1/qwen-asr/internal/audio"
	"github.com/azazo1/qwen-asr/internal/buildinfo"
	"github.com/azazo1/qwen-asr/internal/config"
	"github.com/azazo1/qwen-asr/internal/download"
	"github.com/azazo1/qwen-asr/internal/engine"
	"github.com/azazo1/qwen-asr/internal/model"
)

// runModels 处理 models 子命令.
func runModels(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printModelsUsage(stderr)
		return ExitUsage
	}

	switch args[0] {
	case "list", "ls":
		return modelsList(args[1:], stdout, stderr)
	case "path":
		return modelsPath(args[1:], stdout, stderr)
	case "download":
		return modelsDownload(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printModelsUsage(stdout)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "未知的 models 子命令 %q\n\n", args[0])
		printModelsUsage(stderr)
		return ExitUsage
	}
}

func printModelsUsage(w io.Writer) {
	fmt.Fprintf(w, `用法: %s models <子命令>

子命令:
  list                 列出可用档位与本地的就绪情况
  path                 打印模型目录
  download [档位]      下载指定档位 (默认 %s)

档位: %s
`, config.AppName, model.DefaultKey, strings.Join(model.Keys(), ", "))
}

// modelsList 列出各档位状态.
func modelsList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("models list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := ""
	stringVar(fs, &dataDir, "数据目录", "data-dir")

	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	dirs, err := config.ResolveDataDir(dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}

	fmt.Fprintf(stdout, "模型目录: %s\n\n", dirs.ModelsDir())
	fmt.Fprintf(stdout, "%-8s %-14s %-10s %s\n", "档位", "大小", "状态", "仓库")

	for _, status := range model.CheckAll(dirs.ModelsDir()) {
		state := "缺失"
		if status.Ready() {
			state = "就绪"
		}
		fmt.Fprintf(stdout, "%-8s %-14s %-10s %s\n",
			status.Spec.Key,
			model.HumanSize(status.Spec.TotalSize()),
			state,
			status.Spec.Repo)
	}

	return ExitOK
}

// modelsPath 打印模型目录.
func modelsPath(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("models path", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := ""
	stringVar(fs, &dataDir, "数据目录", "data-dir")

	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	dirs, err := config.ResolveDataDir(dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}

	fmt.Fprintln(stdout, dirs.ModelsDir())
	return ExitOK
}

// modelsDownload 下载指定档位.
func modelsDownload(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("models download", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := ""
	jobs := 8
	source := ""
	stringVar(fs, &dataDir, "数据目录", "data-dir")
	intVar(fs, &jobs, "下载并发数", "j", "jobs")
	stringVar(fs, &source, "下载源: modelscope 或 hf", "source")

	positionals, err := parseWithPositionals(fs, args)
	if err != nil {
		return ExitUsage
	}

	key := model.DefaultKey
	if len(positionals) > 0 {
		key = positionals[0]
	}

	spec, ok := model.Lookup(key)
	if !ok {
		fmt.Fprintf(stderr, "错误: 未知档位 %q, 可选: %s\n", key, strings.Join(model.Keys(), ", "))
		return ExitUsage
	}

	dirs, err := config.ResolveDataDir(dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}
	if err := dirs.Ensure(); err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}

	if status := model.Check(spec, dirs.ModelsDir()); status.Ready() {
		fmt.Fprintf(stdout, "模型 %s 已就绪, 无需下载: %s\n", spec.Key, dirs.ModelsDir())
		return ExitOK
	}

	preferred, err := config.ParseDownloadSource(sourceWithEnv(source))
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitUsage
	}

	// 终端里用回车原地刷新; 管道或文件里退化成定时换行输出, 避免刷出成百上千行.
	interactive := IsTerminal(os.Stdout)
	var lastReport time.Time

	dl := download.New(download.Options{
		Jobs: jobs,
		Progress: func(p download.Progress) {
			if interactive {
				fmt.Fprintf(stdout, "\r  %-40s %5.1f%%  %s / %s",
					p.File, p.Percent(), model.HumanSize(p.Done), model.HumanSize(p.Total))
				return
			}
			if time.Since(lastReport) < 5*time.Second {
				return
			}
			lastReport = time.Now()
			fmt.Fprintf(stdout, "  %-40s %5.1f%%  %s / %s\n",
				p.File, p.Percent(), model.HumanSize(p.Done), model.HumanSize(p.Total))
		},
	})

	if err := dl.FetchSpec(ctx, spec, dirs.ModelsDir(), preferred); err != nil {
		fmt.Fprintln(stdout)
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "模型 %s 下载完成: %s\n", spec.Key, dirs.ModelsDir())
	return ExitOK
}

// sourceWithEnv 在未显式指定下载源时读取环境变量.
func sourceWithEnv(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv(config.EnvDownloadSource)
}

// runDoctor 检查运行环境.
func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := ""
	llamaLib := ""
	engineOnly := false
	modelKey := ""
	stringVar(fs, &dataDir, "数据目录", "data-dir")
	stringVar(fs, &llamaLib, "llama.cpp 库目录", "llama-lib")
	stringVar(fs, &modelKey, "要检查的模型档位", "model")
	boolVar(fs, &engineOnly, "只检查推理引擎", "engine-only")

	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	dirs, err := config.ResolveDataDir(dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "错误: %v\n", err)
		return ExitFailure
	}

	ok := true
	fmt.Fprintf(stdout, "%s %s\n\n", config.AppName, buildinfo.Version())

	// 推理引擎.
	explicit := llamaLib
	if explicit == "" {
		explicit = os.Getenv(config.EnvLlamaLib)
	}

	resolved, err := engine.Resolve(engine.ResolveOptions{ExplicitDir: explicit})
	if err == nil {
		err = engine.InitBackends()
	}
	if err != nil {
		fmt.Fprintf(stdout, "推理引擎:  不可用\n  %v\n", err)
		ok = false
	} else {
		fmt.Fprintf(stdout, "推理引擎:  %s (%s)\n", resolved.Dir, resolved.Origin)
	}

	if engineOnly {
		if ok {
			return ExitOK
		}
		return ExitFailure
	}

	// 外部音频工具.
	tc := audio.DetectToolchain()
	if tc.HasFFmpeg() {
		fmt.Fprintf(stdout, "ffmpeg:    %s\n", tc.FFmpeg)
	} else {
		fmt.Fprintf(stdout, "ffmpeg:    未找到 (仅影响视频与 m4a 等容器格式)\n")
	}

	// 下载命令行.
	if clis := download.DetectExternalCLI(); len(clis) > 0 {
		names := make([]string, 0, len(clis))
		for _, c := range clis {
			names = append(names, c.Name)
		}
		fmt.Fprintf(stdout, "下载命令行: %s\n", strings.Join(names, ", "))
	} else {
		fmt.Fprintf(stdout, "下载命令行: 未找到 (将使用内置下载器)\n")
	}

	// 模型.
	fmt.Fprintf(stdout, "数据目录:  %s\n\n", dirs.Root)
	fmt.Fprintf(stdout, "%-8s %-10s %s\n", "档位", "状态", "说明")
	for _, status := range model.CheckAll(dirs.ModelsDir()) {
		state := "缺失"
		if status.Ready() {
			state = "就绪"
		}
		fmt.Fprintf(stdout, "%-8s %-10s %s\n", status.Spec.Key, state, status.Spec.Repo)
	}

	if modelKey != "" {
		spec, found := model.Lookup(modelKey)
		if !found {
			fmt.Fprintf(stdout, "\n错误: 未知档位 %q\n", modelKey)
			return ExitFailure
		}
		if status := model.Check(spec, dirs.ModelsDir()); !status.Ready() {
			fmt.Fprintf(stdout, "\n%s %s\n", spec.Key, status.Describe())
			ok = false
		}
	}

	if !ok {
		return ExitFailure
	}
	return ExitOK
}
