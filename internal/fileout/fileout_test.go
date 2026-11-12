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

func TestCollectInputsAllMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"a.wav", "b.mp3"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("无法创建 %s: %v", name, err)
		}
		paths = append(paths, p)
	}

	got, errs := CollectInputsAll(paths)
	if len(errs) != 0 {
		t.Fatalf("不应有错误: %v", errs)
	}
	if len(got) != 2 {
		t.Fatalf("应收集 2 个文件, 实际 %d: %v", len(got), got)
	}
	// 保持用户给出的顺序, 便于预期处理次序.
	if got[0] != paths[0] || got[1] != paths[1] {
		t.Errorf("顺序应与输入一致, 实际 %v", got)
	}
}

// 同一个文件用不同写法重复给出时只处理一次, 免得重复转录并覆盖输出.
func TestCollectInputsAllDedupes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.wav")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("无法创建: %v", err)
	}

	got, errs := CollectInputsAll([]string{p, p, filepath.Join(dir, ".", "a.wav")})
	if len(errs) != 0 {
		t.Fatalf("不应有错误: %v", errs)
	}
	if len(got) != 1 {
		t.Errorf("重复输入应去重为一个, 实际 %d: %v", len(got), got)
	}
}

// 一个输入出错不应该影响其余输入, 否则通配符里有个坏文件就全废了.
func TestCollectInputsAllContinuesAfterError(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "a.wav")
	if err := os.WriteFile(good, []byte("x"), 0o644); err != nil {
		t.Fatalf("无法创建: %v", err)
	}

	got, errs := CollectInputsAll([]string{
		filepath.Join(dir, "missing.wav"), good, filepath.Join(dir, "bad.txt"),
	})
	if len(errs) != 2 {
		t.Errorf("应报告 2 个错误, 实际 %d: %v", len(errs), errs)
	}
	if len(got) != 1 || got[0] != good {
		t.Errorf("应保留可处理的文件, 实际 %v", got)
	}
}

func TestCollectInputsAllDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.wav", "b.flac", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("无法创建 %s: %v", name, err)
		}
	}

	got, errs := CollectInputsAll([]string{dir})
	if len(errs) != 0 {
		t.Fatalf("不应有错误: %v", errs)
	}
	if len(got) != 2 {
		t.Errorf("目录里应收集 2 个音频文件, 实际 %d: %v", len(got), got)
	}
}

func TestFormattedExt(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"out.srt", "srt", true},
		{"out.SRT", "srt", true},
		{"字幕.json", "json", true},
		{"out.txt", "txt", true},
		{"out.mp4", "", false},
		{"out", "", false},
	}
	for _, c := range cases {
		got, ok := FormattedExt(c.path)
		if ok != c.ok || got != c.want {
			t.Errorf("FormattedExt(%q) = %q,%v, 预期 %q,%v", c.path, got, ok, c.want, c.ok)
		}
	}
}

func TestIsOutputDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "out.srt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatalf("无法创建: %v", err)
	}

	cases := []struct {
		output string
		want   bool
	}{
		{"", true},
		{"-", true},
		{dir, true},
		{dir + string(os.PathSeparator), true},
		{dir + "/", true},
		{file, false},
		{filepath.Join(dir, "new.json"), false},
	}
	for _, c := range cases {
		if got := IsOutputDir(c.output); got != c.want {
			t.Errorf("IsOutputDir(%q) = %v, 预期 %v", c.output, got, c.want)
		}
	}
}

// -o 的后缀决定格式, 这样不必再写 --format.
func TestFormatsForOutputInfersFromExtension(t *testing.T) {
	for _, path := range []string{"out.srt", "out.json", "out.txt", "字幕.SRT"} {
		got, err := FormatsForOutput("", path)
		if err != nil {
			t.Fatalf("FormatsForOutput(%q) 出错: %v", path, err)
		}
		want, _ := FormattedExt(path)
		if len(got) != 1 || got[0] != want {
			t.Errorf("FormatsForOutput(%q) = %v, 预期 [%s]", path, got, want)
		}
	}
}

// 显式的 --format 优先于后缀.
func TestFormatsForOutputExplicitWins(t *testing.T) {
	got, err := FormatsForOutput("txt,json", "out.srt")
	if err != nil {
		t.Fatalf("出错: %v", err)
	}
	if len(got) != 2 || got[0] != "txt" || got[1] != "json" {
		t.Errorf("显式格式应优先, 实际 %v", got)
	}
}

// 目录或未指定 -o 时保持默认 srt.
func TestFormatsForOutputDefaultsForDir(t *testing.T) {
	dir := t.TempDir()
	for _, output := range []string{"", "-", dir, dir + string(os.PathSeparator)} {
		got, err := FormatsForOutput("", output)
		if err != nil {
			t.Fatalf("FormatsForOutput(%q) 出错: %v", output, err)
		}
		if len(got) != 1 || got[0] != FormatSRT {
			t.Errorf("FormatsForOutput(%q) = %v, 预期 [srt]", output, got)
		}
	}
}

// 后缀无法识别时要报错, 不能悄悄把用户写的后缀换成 srt.
func TestFormatsForOutputRejectsUnknownExtension(t *testing.T) {
	if _, err := FormatsForOutput("", "out.xyz"); err == nil {
		t.Error("无法识别的后缀应当报错")
	}
}
