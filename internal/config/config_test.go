package config

import (
	"path/filepath"
	"testing"
)

func TestResolveDataDirExplicit(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "data")

	got, err := ResolveDataDir(want)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got.Root != want {
		t.Errorf("数据目录为 %q, 预期 %q", got.Root, want)
	}
}

func TestResolveDataDirFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDataDir, dir)

	got, err := ResolveDataDir("")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got.Root != dir {
		t.Errorf("数据目录为 %q, 预期 %q", got.Root, dir)
	}
}

func TestExplicitBeatsEnv(t *testing.T) {
	envDir := t.TempDir()
	explicitDir := t.TempDir()
	t.Setenv(EnvDataDir, envDir)

	got, err := ResolveDataDir(explicitDir)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 命令行参数优先级最高.
	if got.Root != explicitDir {
		t.Errorf("数据目录为 %q, 预期 %q", got.Root, explicitDir)
	}
}

func TestSubdirs(t *testing.T) {
	d := Dirs{Root: "/data"}

	if got := d.ModelsDir(); got != filepath.Join("/data", "models") {
		t.Errorf("模型目录为 %q", got)
	}
	if got := d.TempDir(); got != filepath.Join("/data", "tmp") {
		t.Errorf("临时目录为 %q", got)
	}
}

func TestParseDownloadSource(t *testing.T) {
	cases := []struct {
		in      string
		want    DownloadSource
		wantErr bool
	}{
		{"", "", false},
		{"modelscope", SourceModelScope, false},
		{"hf", SourceHuggingFace, false},
		{"huggingface", SourceHuggingFace, false},
		{"ftp", "", true},
	}

	for _, c := range cases {
		got, err := ParseDownloadSource(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseDownloadSource(%q) 的错误为 %v, 预期出错 %v", c.in, err, c.wantErr)
		}
		if err == nil && got != c.want {
			t.Errorf("ParseDownloadSource(%q) = %q, 预期 %q", c.in, got, c.want)
		}
	}
}
