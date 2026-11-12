package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseTranscribeFlagsPositionalInput(t *testing.T) {
	// 允许把输入文件直接当位置参数传, 方便拖拽使用.
	f, err := parseTranscribeFlags([]string{"talk.wav"}, os.Stderr)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(f.inputs) != 1 || f.inputs[0] != "talk.wav" {
		t.Errorf("inputs 为 %v, 预期 [talk.wav]", f.inputs)
	}
}

func TestParseTranscribeFlagsBothAliases(t *testing.T) {
	// 长短选项都应生效.
	f, err := parseTranscribeFlags([]string{"-i", "a.wav", "-j", "8"}, os.Stderr)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(f.inputs) != 1 || f.inputs[0] != "a.wav" || f.jobs != 8 {
		t.Errorf("短选项解析错误: inputs=%v jobs=%d", f.inputs, f.jobs)
	}

	f, err = parseTranscribeFlags([]string{"--input", "b.wav", "--jobs", "2"}, os.Stderr)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(f.inputs) != 1 || f.inputs[0] != "b.wav" || f.jobs != 2 {
		t.Errorf("长选项解析错误: inputs=%v jobs=%d", f.inputs, f.jobs)
	}
}

func TestRunVersion(t *testing.T) {
	var out, errOut strings.Builder

	code := Run(t.Context(), []string{"version"}, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Errorf("退出码 %d, 预期 %d", code, ExitOK)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Error("version 应当输出非空版本号")
	}
}

func TestRunNoArgsShowsUsage(t *testing.T) {
	var out, errOut strings.Builder

	code := Run(t.Context(), nil, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Errorf("退出码 %d, 预期 %d", code, ExitUsage)
	}
	if !strings.Contains(errOut.String(), "用法") {
		t.Error("应当打印用法说明")
	}
}

func TestMissingEngineStopsBeforeModelAcquisition(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	var out, errOut strings.Builder
	code := Run(t.Context(), []string{
		"-i", "audio.wav", "--llama-lib", filepath.Join(root, "missing-engine"),
		"--data-dir", dataDir,
	}, strings.NewReader(""), &out, &errOut)
	if code != ExitFailure {
		t.Fatalf("退出码为 %d, 预期 %d", code, ExitFailure)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("引擎不可用时不应创建数据目录: %v", err)
	}
}

func TestRunMissingInput(t *testing.T) {
	var out, errOut strings.Builder

	code := Run(t.Context(), []string{"-i", ""}, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Errorf("退出码 %d, 预期 %d", code, ExitUsage)
	}
}

// 多个位置参数都要成为输入, 这是通配符展开后最常见的用法.
func TestParseTranscribeFlagsMultiplePositionals(t *testing.T) {
	f, err := parseTranscribeFlags([]string{"a.mp4", "b.mp4", "c.mp4"}, os.Stderr)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	want := []string{"a.mp4", "b.mp4", "c.mp4"}
	if !reflect.DeepEqual(f.inputs, want) {
		t.Errorf("inputs = %v, 预期 %v", f.inputs, want)
	}
}

// --input 可以重复, 也可以和位置参数混用.
func TestParseTranscribeFlagsRepeatedInput(t *testing.T) {
	f, err := parseTranscribeFlags([]string{"-i", "a.wav", "--input", "b.wav", "c.wav"}, os.Stderr)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	want := []string{"a.wav", "b.wav", "c.wav"}
	if !reflect.DeepEqual(f.inputs, want) {
		t.Errorf("inputs = %v, 预期 %v", f.inputs, want)
	}
}

// 单个 -i 仍按原样工作, 保持既有用法兼容.
func TestParseTranscribeFlagsSingleInput(t *testing.T) {
	f, err := parseTranscribeFlags([]string{"-i", "only.wav"}, os.Stderr)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(f.inputs) != 1 || f.inputs[0] != "only.wav" {
		t.Errorf("inputs = %v", f.inputs)
	}
}

// 多个输入时 -o 指向单个文件会互相覆盖, 必须拒绝.
func TestOutputIsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "out.srt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatalf("无法创建: %v", err)
	}

	cases := []struct {
		output string
		want   bool
	}{
		{"", false},
		{"-", false},
		{file, true},
		{filepath.Join(dir, "new.srt"), true},
		{dir, false},
		{dir + string(os.PathSeparator), false},
	}
	for _, c := range cases {
		if got := outputIsFile(c.output); got != c.want {
			t.Errorf("outputIsFile(%q) = %v, 预期 %v", c.output, got, c.want)
		}
	}
}
