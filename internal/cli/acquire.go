// Package cli 实现命令行界面.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azazo1/qwen-asr/internal/config"
	"github.com/azazo1/qwen-asr/internal/download"
	"github.com/azazo1/qwen-asr/internal/model"
	"github.com/azazo1/qwen-asr/internal/prompt"
)

// Acquirer 负责在模型缺失时把模型弄到本地.
type Acquirer struct {
	// Dirs 是数据目录.
	Dirs config.Dirs
	// Prompter 用于交互.
	Prompter *prompt.Prompter
	// Out 是面向用户的输出.
	Out io.Writer
	// Source 是下载源偏好.
	Source config.DownloadSource
	// Jobs 是内置下载器的分块并发数.
	Jobs int
}

// Ensure 保证指定档位的模型已在本地, 必要时引导用户下载.
//
// 返回最终可用的 Spec. 若用户拒绝或环境不允许交互, 返回错误.
func (a *Acquirer) Ensure(ctx context.Context, requested string) (model.Spec, error) {
	modelsDir := a.Dirs.ModelsDir()

	// 已经指定了具体档位且就绪, 直接用.
	if requested != "" {
		if spec, ok := model.Lookup(requested); ok {
			if status := model.Check(spec, modelsDir); status.Ready() {
				return spec, nil
			}
		}
	}

	// 没有指定档位时, 优先复用已经下好的模型.
	if requested == "" {
		if ready := model.ReadySpecs(modelsDir); len(ready) > 0 {
			// 多个就绪时按目录顺序取第一个, 保持行为稳定.
			return ready[0], nil
		}
	}

	spec, err := a.chooseSpec(requested)
	if err != nil {
		return model.Spec{}, err
	}

	status := model.Check(spec, modelsDir)
	if status.Ready() {
		return spec, nil
	}

	if err := a.acquire(ctx, spec, status); err != nil {
		return model.Spec{}, err
	}

	// 下载完再检查一次, 避免 CLI 静默失败后被当成成功.
	final := model.Check(spec, modelsDir)
	if !final.Ready() {
		return model.Spec{}, fmt.Errorf("模型下载后仍不完整: %s", final.Describe())
	}
	return spec, nil
}

// chooseSpec 决定要下载哪一档.
func (a *Acquirer) chooseSpec(requested string) (model.Spec, error) {
	if requested != "" {
		if spec, ok := model.Lookup(requested); ok {
			return spec, nil
		}
		return model.Spec{}, fmt.Errorf("未知的模型档位 %q, 可选: %s",
			requested, strings.Join(model.Keys(), ", "))
	}

	specs := model.Catalog()
	options := make([]string, 0, len(specs))
	for _, s := range specs {
		options = append(options, fmt.Sprintf("%s  %s  下载约 %s",
			s.Key, s.Repo, model.HumanSize(s.TotalSize())))
	}

	idx, err := a.Prompter.Choose("还没有可用的本地模型, 请选择要下载的档位 (0.6B 更小更快, 1.7B 更准):", options)
	if err != nil {
		// 非交互环境下没法提问, 直接给出可复制的命令, 让用户自己决定怎么下.
		return model.Spec{}, fmt.Errorf("%w\n\n请先下载模型, 任选其一:\n%s",
			err, a.manualInstructions())
	}
	return specs[idx], nil
}

// manualInstructions 生成可直接复制执行的模型下载命令.
//
// 两个 CLI 的参数不同, 这里都列出来, 用户装了哪个就用哪个.
func (a *Acquirer) manualInstructions() string {
	dir := a.Dirs.ModelsDir()

	var b strings.Builder
	for _, spec := range model.Catalog() {
		fmt.Fprintf(&b, "\n  # %s, 约 %s\n", spec.Key, model.HumanSize(spec.TotalSize()))
		fmt.Fprintf(&b, "  hf download %s --local-dir %q\n", spec.Repo, dir)
		fmt.Fprintf(&b, "  modelscope download --model %s --local-dir %q\n", spec.Repo, dir)
	}

	fmt.Fprintf(&b, "\n或者用本工具自带的能力下载 (会跳过交互):\n")
	fmt.Fprintf(&b, "  %s models download 0.6b --data-dir %q\n", config.AppName, a.Dirs.Root)
	fmt.Fprintf(&b, "\n模型目录: %s\n", dir)

	return b.String()
}

// acquire 按"外部 CLI 优先, 内置下载器兜底"的顺序获取模型.
func (a *Acquirer) acquire(ctx context.Context, spec model.Spec, status model.Status) error {
	modelsDir := a.Dirs.ModelsDir()
	if err := a.Dirs.Ensure(); err != nil {
		return err
	}

	fmt.Fprintf(a.Out, "\n模型 %s 尚未就绪: %s\n", spec.Key, status.Describe())
	fmt.Fprintf(a.Out, "将下载 %d 个文件到 %s, 共约 %s\n",
		len(spec.Files()), modelsDir, model.HumanSize(spec.TotalSize()))

	clis := download.DetectExternalCLI()
	if len(clis) > 0 {
		return a.acquireViaCLI(ctx, spec, clis)
	}
	return a.acquireBuiltin(ctx, spec)
}

// acquireViaCLI 用本机已装的 modelscope 或 hf 下载.
func (a *Acquirer) acquireViaCLI(ctx context.Context, spec model.Spec, clis []download.ExternalCLI) error {
	chosen := clis[0]

	if len(clis) > 1 {
		options := make([]string, 0, len(clis))
		for _, c := range clis {
			options = append(options, c.Name)
		}
		idx, err := a.Prompter.Choose("检测到多个可用的下载命令行, 请选择:", options)
		if err != nil {
			// 非交互环境下按探测顺序取第一个, 不因为没法提问而失败.
			fmt.Fprintf(a.Out, "无法交互选择下载命令行 (%v), 使用 %s\n", err, chosen.Name)
		} else {
			chosen = clis[idx]
		}
	}

	args := chosen.Command(spec.Repo, a.Dirs.ModelsDir())
	fmt.Fprintf(a.Out, "\n将执行:\n  %s\n", download.DisplayCommand(args))

	ok, err := a.Prompter.Confirm("确认执行该命令?", true)
	if err != nil {
		return fmt.Errorf("需要确认才能调用 %s 下载: %w", chosen.Name, err)
	}
	if !ok {
		fmt.Fprintln(a.Out, "已取消下载.")
		return errors.New("用户取消了模型下载")
	}

	start := time.Now()
	if err := chosen.Run(ctx, spec.Repo, a.Dirs.ModelsDir()); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "%s 下载完成, 用时 %s\n", chosen.Name, time.Since(start).Round(time.Second))
	return nil
}

// acquireBuiltin 用内置并发下载器下载.
func (a *Acquirer) acquireBuiltin(ctx context.Context, spec model.Spec) error {
	fmt.Fprintf(a.Out, "未检测到 modelscope 或 hf 命令行, 改用内置下载器 (并发 %d).\n", a.Jobs)
	fmt.Fprintf(a.Out, "也可以手动下载 %s 里的 gguf 文件到 %s\n", spec.Repo, a.Dirs.ModelsDir())

	// 终端里用回车原地刷新; 管道或文件里退化成定时换行, 避免刷屏.
	interactive := IsTerminal(os.Stdin)
	var lastReport time.Time

	dl := download.New(download.Options{
		Jobs: a.Jobs,
		Progress: func(p download.Progress) {
			if interactive {
				fmt.Fprintf(a.Out, "\r  %-40s %5.1f%%  %s / %s",
					p.File, p.Percent(), model.HumanSize(p.Done), model.HumanSize(p.Total))
				return
			}
			if time.Since(lastReport) < 5*time.Second {
				return
			}
			lastReport = time.Now()
			fmt.Fprintf(a.Out, "  %-40s %5.1f%%  %s / %s\n",
				p.File, p.Percent(), model.HumanSize(p.Done), model.HumanSize(p.Total))
		},
	})

	if err := dl.FetchSpec(ctx, spec, a.Dirs.ModelsDir(), a.Source); err != nil {
		fmt.Fprintln(a.Out)
		return err
	}
	fmt.Fprintln(a.Out)
	return nil
}

// IsTerminal 判断给定文件是否是字符设备 (终端).
func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// resolveModelPaths 把 --model 取值解析成实际文件路径.
func resolveModelPaths(value string, dirs config.Dirs, acquire *Acquirer, ctx context.Context) (model.Paths, error) {
	// 显式给了本地文件路径时直接使用, 不触发下载.
	if value != "" {
		if _, ok := model.Lookup(value); !ok {
			if info, err := os.Stat(value); err == nil && !info.IsDir() {
				paths := model.Paths{Model: value, Projector: model.ProjectorFor(value)}
				if paths.Projector == "" {
					return model.Paths{}, fmt.Errorf(
						"找不到与 %s 配套的音频投影器; 请把 mmproj-<模型名>.gguf 放在同一目录", filepath.Base(value))
				}
				return paths, nil
			}
		}
	}

	spec, err := acquire.Ensure(ctx, value)
	if err != nil {
		return model.Paths{}, err
	}
	return spec.Paths(dirs.ModelsDir()), nil
}
