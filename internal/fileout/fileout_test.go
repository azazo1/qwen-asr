package fileout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFormats(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"", []string{"srt"}, false},
		{"txt", []string{"txt"}, false},
		{"txt,srt", []string{"txt", "srt"}, false},
		{"TXT, SRT , json", []string{"txt", "srt", "json"}, false},
		{"txt,txt", []string{"txt"}, false}, // 去重
		{"docx", nil, true},
		{",,,", nil, true},
	}

	for _, c := range cases {
		got, err := ParseFormats(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseFormats(%q) 的错误为 %v, 预期出错 %v", c.in, err, c.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("ParseFormats(%q) = %v, 预期 %v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("ParseFormats(%q) = %v, 预期 %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestResolvePathDefault(t *testing.T) {
	// 没给输出参数时, 输出到输入文件旁边, 只换扩展名.
	got := ResolvePath("", "/audio/talk.wav", "srt")
	want := filepath.Join("/audio", "talk.srt")

	if got != want {
		t.Errorf("得到 %q, 预期 %q", got, want)
	}
}

func TestResolvePathToDirectory(t *testing.T) {
	dir := t.TempDir()

	got := ResolvePath(dir, "/audio/talk.mp4", "json")
	want := filepath.Join(dir, "talk.json")

	if got != want {
		t.Errorf("得到 %q, 预期 %q", got, want)
	}
}

func TestResolvePathTrailingSeparator(t *testing.T) {
	// 目录还不存在时, 以分隔符结尾也要被当成目录.
	dir := filepath.Join(t.TempDir(), "newdir") + string(os.PathSeparator)

	got := ResolvePath(dir, "/audio/talk.wav", "txt")
	want := filepath.Join(dir, "talk.txt")

	if got != want {
		t.Errorf("得到 %q, 预期 %q", got, want)
	}
}

func TestResolvePathExplicitFile(t *testing.T) {
	// 明确的文件名要尊重用户的选择.
	got := ResolvePath("/out/result.srt", "/audio/talk.wav", "srt")

	if got != "/out/result.srt" {
		t.Errorf("得到 %q, 预期 /out/result.srt", got)
	}
}

func TestCollectInputsSingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.wav")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("无法创建文件: %v", err)
	}

	got, err := CollectInputs(path)
	if err != nil {
		t.Fatalf("收集失败: %v", err)
	}
	if len(got) != 1 || got[0] != path {
		t.Errorf("得到 %v, 预期 [%s]", got, path)
	}
}

func TestCollectInputsRejectsUnsupported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("无法创建文件: %v", err)
	}

	if _, err := CollectInputs(path); err == nil {
		t.Fatal("不支持的类型应当报错")
	}
}

func TestCollectInputsDirectoryFiltersByExt(t *testing.T) {
	dir := t.TempDir()

	// 混合放入可处理与不可处理的文件.
	for _, name := range []string{"a.wav", "b.mp3", "c.mp4", "d.txt", "e.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("无法创建 %s: %v", name, err)
		}
	}
	// 子目录里的音频也要被找到.
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("无法创建子目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f.flac"), []byte("x"), 0o644); err != nil {
		t.Fatalf("无法创建子文件: %v", err)
	}

	got, err := CollectInputs(dir)
	if err != nil {
		t.Fatalf("收集失败: %v", err)
	}

	if len(got) != 4 {
		t.Fatalf("应收集 4 个文件, 实际 %d: %v", len(got), got)
	}
	for _, path := range got {
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".wav", ".mp3", ".mp4", ".flac":
		default:
			t.Errorf("收集到了不该处理的文件: %s", path)
		}
	}
}

func TestCollectInputsMissing(t *testing.T) {
	if _, err := CollectInputs(filepath.Join(t.TempDir(), "nope.wav")); err == nil {
		t.Fatal("不存在的输入应当报错")
	}
}
