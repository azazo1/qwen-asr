package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTranscribeFlagsPositionalInput(t *testing.T) {
	// 允许把输入文件直接当位置参数传, 方便拖拽使用.
	f, err := parseTranscribeFlags([]string{"talk.wav"}, os.Stderr)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if f.input != "talk.wav" {
		t.Errorf("input 为 %q, 预期 talk.wav", f.input)
	}
}

func TestParseTranscribeFlagsBothAliases(t *testing.T) {
	// 长短选项都应生效.
	f, err := parseTranscribeFlags([]string{"-i", "a.wav", "-j", "8"}, os.Stderr)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if f.input != "a.wav" || f.jobs != 8 {
		t.Errorf("短选项解析错误: input=%q jobs=%d", f.input, f.jobs)
	}

	f, err = parseTranscribeFlags([]string{"--input", "b.wav", "--jobs", "2"}, os.Stderr)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if f.input != "b.wav" || f.jobs != 2 {
		t.Errorf("长选项解析错误: input=%q jobs=%d", f.input, f.jobs)
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
