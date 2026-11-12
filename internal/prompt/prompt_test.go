package prompt

import (
	"strings"
	"testing"
)

func TestPrompterChoose(t *testing.T) {
	p := New(strings.NewReader("2\n"), &strings.Builder{}, true)

	idx, err := p.Choose("请选择", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("提问失败: %v", err)
	}
	if idx != 1 {
		t.Errorf("下标为 %d, 预期 1", idx)
	}
}

func TestPrompterChooseDefaultsToFirst(t *testing.T) {
	// 直接回车应当选第一项, 让最常见的路径最省事.
	p := New(strings.NewReader("\n"), &strings.Builder{}, true)

	idx, err := p.Choose("请选择", []string{"a", "b"})
	if err != nil {
		t.Fatalf("提问失败: %v", err)
	}
	if idx != 0 {
		t.Errorf("下标为 %d, 预期 0", idx)
	}
}

func TestPrompterChooseRetriesOnInvalid(t *testing.T) {
	p := New(strings.NewReader("9\nabc\n3\n"), &strings.Builder{}, true)

	idx, err := p.Choose("请选择", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("提问失败: %v", err)
	}
	if idx != 2 {
		t.Errorf("下标为 %d, 预期 2", idx)
	}
}

func TestPrompterNonInteractive(t *testing.T) {
	// 非交互环境下提问必须立刻失败, 不能挂住等待输入.
	p := New(strings.NewReader(""), &strings.Builder{}, false)

	if _, err := p.Choose("请选择", []string{"a"}); err == nil {
		t.Error("非交互环境下 Choose 应当报错")
	}
	if _, err := p.Confirm("确认?", true); err == nil {
		t.Error("非交互环境下 Confirm 应当报错")
	}
}

func TestPrompterConfirm(t *testing.T) {
	cases := []struct {
		input      string
		defaultYes bool
		want       bool
	}{
		{"y\n", false, true},
		{"n\n", true, false},
		{"\n", true, true},   // 回车取默认值
		{"\n", false, false}, // 同上
		{"yes\n", false, true},
		{"NO\n", true, false},
	}

	for _, c := range cases {
		p := New(strings.NewReader(c.input), &strings.Builder{}, true)
		got, err := p.Confirm("确认?", c.defaultYes)
		if err != nil {
			t.Fatalf("提问失败: %v", err)
		}
		if got != c.want {
			t.Errorf("输入 %q (默认 %v) 得到 %v, 预期 %v", c.input, c.defaultYes, got, c.want)
		}
	}
}
