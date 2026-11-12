package enginelib

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFilename(t *testing.T) {
	cases := []struct {
		goos string
		want string
	}{
		{"darwin", "libllama.dylib"},
		{"linux", "libllama.so"},
		{"windows", "llama.dll"},
	}

	for _, c := range cases {
		if got := Filename(c.goos, "llama"); got != c.want {
			t.Errorf("Filename(%q) = %q, 预期 %q", c.goos, got, c.want)
		}
	}
}

// writeFakeLibs 在目录里造出必需库文件, 用来验证检测逻辑.
func writeFakeLibs(t *testing.T, dir string, goos string, names []string) {
	t.Helper()
	for _, lib := range names {
		path := filepath.Join(dir, Filename(goos, lib))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("无法写入 %s: %v", path, err)
		}
	}
}

func TestHasRequiredLibs(t *testing.T) {
	dir := t.TempDir()

	if HasRequiredLibs(dir, runtime.GOOS) {
		t.Error("空目录不应被判定为齐备")
	}

	// 缺一个库时仍然不算齐备.
	writeFakeLibs(t, dir, runtime.GOOS, RequiredLibs[:len(RequiredLibs)-1])
	if HasRequiredLibs(dir, runtime.GOOS) {
		t.Error("缺少一个库时不应被判定为齐备")
	}

	writeFakeLibs(t, dir, runtime.GOOS, RequiredLibs[len(RequiredLibs)-1:])
	if !HasRequiredLibs(dir, runtime.GOOS) {
		t.Error("库齐备时应判定为可用")
	}
}

func TestHasRequiredLibsAcceptsVersionedNames(t *testing.T) {
	// 发行版常把库命名成 libllama.0.9.0.dylib, 这种也要认.
	dir := t.TempDir()
	goos := runtime.GOOS

	for _, lib := range RequiredLibs {
		ext := filepath.Ext(Filename(goos, lib))
		base := Filename(goos, lib)
		name := base[:len(base)-len(ext)] + ".0.9.0" + ext
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("无法写入: %v", err)
		}
	}

	if !HasRequiredLibs(dir, goos) {
		t.Error("带版本号的库文件名也应被接受")
	}
}

func TestCandidatesFromServer(t *testing.T) {
	got := CandidatesFromServer("/opt/llama/bin/llama-server")
	want := []string{"/opt/llama/lib", "/opt/llama/bin"}

	if len(got) != len(want) {
		t.Fatalf("得到 %v, 预期 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 项为 %q, 预期 %q", i, got[i], want[i])
		}
	}
}

func TestCandidatesFromServerEmpty(t *testing.T) {
	if got := CandidatesFromServer(""); got != nil {
		t.Errorf("空路径应返回 nil, 得到 %v", got)
	}
}

func TestSystemCandidatesNotEmpty(t *testing.T) {
	if len(SystemCandidates()) == 0 {
		t.Error("系统候选路径不应为空")
	}
}
