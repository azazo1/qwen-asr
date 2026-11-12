package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookup(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"0.6b", true},
		{"1.7b", true},
		{"0.6B", true},   // 大小写不敏感
		{" 1.7b ", true}, // 去空白
		{"3b", false},
		{"", false},
	}

	for _, c := range cases {
		_, ok := Lookup(c.key)
		if ok != c.want {
			t.Errorf("Lookup(%q) 的 ok 为 %v, 预期 %v", c.key, ok, c.want)
		}
	}
}

func TestCatalogSizes(t *testing.T) {
	// 清单里的字节数必须为正, 否则 completeness 检测永远失败.
	for _, spec := range Catalog() {
		if spec.Model.Size <= 0 || spec.Projector.Size <= 0 {
			t.Errorf("%s 的文件大小未填写: %+v", spec.Key, spec)
		}
		if spec.TotalSize() != spec.Model.Size+spec.Projector.Size {
			t.Errorf("%s 的总大小计算不一致", spec.Key)
		}
	}
}

// touchFile 创建指定大小的文件.
func touchFile(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("无法创建目录: %v", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("无法创建文件: %v", err)
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatalf("无法设置大小: %v", err)
	}
}

func TestCheckReportsMissing(t *testing.T) {
	dir := t.TempDir()
	spec, _ := Lookup("0.6b")

	status := Check(spec, dir)
	if status.Ready() {
		t.Fatal("空目录下不应就绪")
	}
	if len(status.Missing) != 2 {
		t.Errorf("应有 2 个缺失文件, 实际 %d", len(status.Missing))
	}
}

func TestCheckDetectsTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	spec, _ := Lookup("0.6b")
	paths := spec.Paths(dir)

	// 主模型大小不对, 投影器正确.
	touchFile(t, paths.Model, spec.Model.Size-1)
	touchFile(t, paths.Projector, spec.Projector.Size)

	status := Check(spec, dir)
	if status.Ready() {
		t.Fatal("主模型大小不符时不应就绪")
	}
	if len(status.Missing) != 1 || status.Missing[0].Name != spec.Model.Name {
		t.Errorf("缺失清单不正确: %+v", status.Missing)
	}
}

func TestCheckReady(t *testing.T) {
	dir := t.TempDir()
	spec, _ := Lookup("0.6b")
	paths := spec.Paths(dir)

	touchFile(t, paths.Model, spec.Model.Size)
	touchFile(t, paths.Projector, spec.Projector.Size)

	if status := Check(spec, dir); !status.Ready() {
		t.Fatalf("大小完全一致时应就绪: %s", status.Describe())
	}
}

func TestResolvePathRejectsIncompleteModel(t *testing.T) {
	dir := t.TempDir()

	// 模型没下载时, 解析短名必须报错而不是返回不存在的路径.
	if _, err := ResolvePath("0.6b", dir); err == nil {
		t.Fatal("模型未就绪时应报错")
	}
}

func TestResolvePathUnknownValue(t *testing.T) {
	dir := t.TempDir()

	if _, err := ResolvePath("不存在的路径.gguf", dir); err == nil {
		t.Fatal("既不是档位也不是文件时应报错")
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{804749248, "767.5 MB"},
	}

	for _, c := range cases {
		if got := HumanSize(c.in); got != c.want {
			t.Errorf("HumanSize(%d) = %q, 预期 %q", c.in, got, c.want)
		}
	}
}

func TestProjectorFor(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "Qwen3-ASR-0.6B-Q8_0.gguf")
	projectorPath := filepath.Join(dir, "mmproj-Qwen3-ASR-0.6B-Q8_0.gguf")

	// 没有配套投影器时应返回空串.
	if got := ProjectorFor(modelPath); got != "" {
		t.Errorf("没有投影器时返回 %q, 预期空串", got)
	}

	touchFile(t, projectorPath, 10)
	if got := ProjectorFor(modelPath); got != projectorPath {
		t.Errorf("得到 %q, 预期 %q", got, projectorPath)
	}
}
