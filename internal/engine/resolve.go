// Package engine 通过 yzma 的 purego 绑定调用本机已有的 llama.cpp 动态库.
// 本包不下载或内置 llama.cpp. 导入本包会初始化 yzma 依赖的 libffi.
package engine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/azazo1/qwen-asr/internal/enginelib"
	"github.com/hybridgroup/yzma/pkg/llama"
	"github.com/hybridgroup/yzma/pkg/mtmd"
)

// Origin 说明引擎库从哪里来.
type Origin string

const (
	OriginExplicit Origin = "explicit"
	OriginSystem   Origin = "system"
)

// Resolved 是一次成功的引擎解析结果.
type Resolved struct {
	Dir    string
	Origin Origin
}

func (r Resolved) String() string {
	return fmt.Sprintf("%s (%s)", r.Dir, r.Origin)
}

// ResolveOptions 控制引擎解析过程.
type ResolveOptions struct {
	// ExplicitDir 是用户显式指定的库目录, 空表示搜索系统安装.
	ExplicitDir string
}

// Resolve 优先使用显式路径, 否则搜索系统安装, 并验证库的符号可加载.
func Resolve(opts ResolveOptions) (Resolved, error) {
	var systemDirs []string
	if opts.ExplicitDir == "" {
		systemDirs = append(enginelib.CandidatesFromServer(findLlamaServer()), enginelib.SystemCandidates()...)
	}
	return resolveWith(opts, systemDirs, LoadLibraries)
}

// resolveWith 分离路径选择和动态加载, 便于独立验证候选选择与失败处理.
func resolveWith(opts ResolveOptions, systemDirs []string, load func(string) error) (Resolved, error) {
	if opts.ExplicitDir != "" {
		if !enginelib.HasRequiredLibs(opts.ExplicitDir, runtime.GOOS) {
			return Resolved{}, fmt.Errorf("显式指定的引擎目录 %s 缺少必需库 (%s). %s",
				opts.ExplicitDir, strings.Join(enginelib.RequiredLibs, ", "), installationHint)
		}
		if err := load(opts.ExplicitDir); err != nil {
			return Resolved{}, fmt.Errorf("显式指定的引擎目录 %s 无法加载: %w. %s", opts.ExplicitDir, err, installationHint)
		}
		return Resolved{Dir: opts.ExplicitDir, Origin: OriginExplicit}, nil
	}

	seen := map[string]bool{}
	var failures []string
	for _, dir := range systemDirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		if !enginelib.HasRequiredLibs(dir, runtime.GOOS) {
			continue
		}
		if err := load(dir); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		return Resolved{Dir: dir, Origin: OriginSystem}, nil
	}
	if len(failures) > 0 {
		return Resolved{}, fmt.Errorf("%w\n加载失败详情:\n  %s", ErrEngineNotFound, strings.Join(failures, "\n  "))
	}
	return Resolved{}, ErrEngineNotFound
}

const installationHint = "请安装支持 Qwen3-ASR 且包含 llama/mtmd/ggml 动态库的 llama.cpp " +
	"(macOS 可运行 brew install llama.cpp; 其他平台见 https://github.com/ggml-org/llama.cpp). " +
	"已安装时可用 --llama-lib 或 QWEN_ASR_LLAMA_LIB 指定库目录"

// ErrEngineNotFound 表示本机没有可用引擎, 调用方应报错退出.
var ErrEngineNotFound = errors.New("未找到可用的 llama.cpp 推理引擎. " + installationHint)

// LoadLibraries 只加载库与符号, 后端由 InitBackends 统一初始化.
func LoadLibraries(dir string) error {
	if err := llama.Load(dir); err != nil {
		return fmt.Errorf("加载 llama 库失败: %w", err)
	}
	if err := mtmd.Load(dir); err != nil {
		return fmt.Errorf("加载 mtmd 库失败: %w", err)
	}
	if os.Getenv("QWEN_ASR_LLAMA_LOG") == "" {
		llama.LogSet(llama.LogSilent())
		mtmd.LogSet(llama.LogSilent())
	}
	return nil
}

// InitBackends 必须在加载库之后, 创建模型之前调用一次.
func InitBackends() error {
	if err := llama.Init(); err != nil {
		return fmt.Errorf("初始化 llama 后端失败: %w. %s", err, installationHint)
	}
	if llama.GGMLBackendDeviceCount() == 0 {
		return errors.New("推理引擎没有可用的计算设备, 请检查 CPU/GPU 后端库是否完整. " + installationHint)
	}
	return nil
}

func findLlamaServer() string {
	for _, name := range []string{"llama-server", "llama-server.exe", "llama-cli", "llama-cli.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}
