// Package enginelib 提供引擎库文件的名字约定与目录检测.
//
// 这里刻意不导入 yzma, 因为 yzma 底层的 ffi 包会在 init 阶段加载动态库,
// 在受限环境里会直接 panic. 把纯路径逻辑分离出来, 既方便测试,
// 也让调用方在真正需要推理时才付出加载代价.
package enginelib

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RequiredLibs 返回 llama.cpp 必须提供的库名 (不含平台前后缀).
//
// ggml 的后端库是运行期按名字加载的, 不在这里强制要求.
var RequiredLibs = []string{"llama", "mtmd", "ggml", "ggml-base"}

// Filename 给出某个库在指定平台上的文件名.
func Filename(goos, lib string) string {
	switch goos {
	case "windows":
		return lib + ".dll"
	case "darwin":
		return "lib" + lib + ".dylib"
	default:
		return "lib" + lib + ".so"
	}
}

// HasRequiredLibs 判断目录里是否齐备必需库文件.
//
// 同时接受带版本号的文件名 (如 libllama.0.9.0.dylib), 因为发行版常这样命名.
func HasRequiredLibs(dir, goos string) bool {
	for _, lib := range RequiredLibs {
		if !hasLibFile(dir, lib, goos) {
			return false
		}
	}
	return true
}

func hasLibFile(dir, lib, goos string) bool {
	exact := Filename(goos, lib)
	if info, err := os.Stat(filepath.Join(dir, exact)); err == nil && !info.IsDir() {
		return true
	}

	// 退一步按前缀匹配带版本号的文件名.
	ext := filepath.Ext(exact)
	prefix := strings.TrimSuffix(exact, ext)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix+".") && strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// SystemCandidates 返回系统 llama.cpp 库的常见位置, 顺序即优先级.
func SystemCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/opt/homebrew/lib",
			"/usr/local/lib",
			"/opt/homebrew/opt/llama.cpp/lib",
		}
	case "windows":
		var out []string
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if base := os.Getenv(env); base != "" {
				out = append(out, filepath.Join(base, "llama.cpp", "lib"))
				out = append(out, filepath.Join(base, "llama.cpp"))
			}
		}
		return out
	default:
		return []string{
			"/usr/local/lib",
			"/usr/lib",
			"/usr/lib64",
			"/lib",
		}
	}
}

// CandidatesFromServer 从 llama-server 可执行文件位置推断库目录.
//
// 很多发行方式把可执行文件放 bin/, 库放同级的 lib/.
func CandidatesFromServer(serverPath string) []string {
	if serverPath == "" {
		return nil
	}
	binDir := filepath.Dir(serverPath)
	return []string{
		filepath.Join(filepath.Dir(binDir), "lib"),
		binDir,
	}
}
