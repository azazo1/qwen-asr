package progress

import (
	"bytes"
	"strings"
	"testing"
)

func TestInteractiveRendersBar(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(1, 4, 0)
	out := buf.String()

	if !strings.HasPrefix(out, "\r") {
		t.Errorf("应回到行首渲染, 实际 %q", out)
	}
	if !strings.Contains(out, "25.0%") {
		t.Errorf("应显示百分比, 实际 %q", out)
	}
	if !strings.Contains(out, "1/4") {
		t.Errorf("应显示段数, 实际 %q", out)
	}
	// 进度条本体用 ASCII, 避免终端编码问题.
	if !strings.Contains(out, "#") || !strings.Contains(out, "-") {
		t.Errorf("应包含进度条本体, 实际 %q", out)
	}
}

// 最后一段必须刷新, 否则进度条会停在未满状态.
func TestFinalSegmentAlwaysRenders(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(1, 2, 0)
	buf.Reset()
	// 紧接着完成, 即使没到重绘间隔也要画出来.
	bar.Set(2, 2, 0)

	if !strings.Contains(buf.String(), "100.0%") {
		t.Errorf("最后一段应显示 100%%, 实际 %q", buf.String())
	}
	if !strings.Contains(buf.String(), "2/2") {
		t.Errorf("最后一段应显示 2/2, 实际 %q", buf.String())
	}
}

// 同一次进度内的高频更新应被节流, 避免每段都刷一次终端.
func TestRendersAreThrottled(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(1, 10, 0)
	buf.Reset()
	for i := 2; i < 10; i++ {
		bar.Set(i, 10, 0)
	}

	if buf.Len() != 0 {
		t.Errorf("重绘间隔内的更新不应输出, 实际 %q", buf.String())
	}
}

// 写日志前必须先擦掉进度条, 保证日志从行首开始.
func TestLogWriteErasesBarFirst(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(1, 4, 0)
	buf.Reset()

	if _, err := bar.Writer().Write([]byte("一行日志\n")); err != nil {
		t.Fatalf("写日志失败: %v", err)
	}
	out := buf.String()

	// 擦除动作应当是: 回车 + 空格 + 回车, 然后才是日志正文.
	if !strings.HasSuffix(out, "一行日志\n") {
		t.Errorf("日志应完整输出在末尾, 实际 %q", out)
	}
	if !strings.Contains(out, "\r ") {
		t.Errorf("应先擦除进度条行, 实际 %q", out)
	}
	if strings.Contains(out, "25.0%") {
		t.Errorf("日志前不应残留进度条正文, 实际 %q", out)
	}
}

// 关闭之后不应再画进度条.
func TestCloseStopsRendering(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(1, 4, 0)
	bar.Close()
	buf.Reset()

	bar.Set(2, 4, 0)
	if buf.Len() != 0 {
		t.Errorf("关闭后不应继续渲染, 实际 %q", buf.String())
	}
}

// 非终端下不能写控制字符, 否则会污染日志文件.
func TestNonInteractiveHasNoControlChars(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, false)

	// 第一次 Set 会输出一行, 之后受间隔限制.
	bar.Set(1, 4, 0)
	out := buf.String()

	if strings.ContainsAny(out, "\r\x1b") {
		t.Errorf("非终端输出不应含控制字符, 实际 %q", out)
	}
	if !strings.Contains(out, "1/4") {
		t.Errorf("应输出进度文本, 实际 %q", out)
	}
}

// 失败段数要体现在进度里, 便于用户判断结果是否完整.
func TestFailedCountIsShown(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(2, 4, 1)
	if !strings.Contains(buf.String(), "失败 1") {
		t.Errorf("应显示失败段数, 实际 %q", buf.String())
	}
}

// 并发更新进度与写日志时, 每行日志都必须完整, 不能被进度条或彼此截断.
func TestConcurrentProgressAndLogsStayIntact(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	const segments = 20
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= segments; i++ {
			bar.Set(i, segments, 0)
		}
	}()

	for i := 0; i < segments; i++ {
		if _, err := bar.Writer().Write([]byte("日志行\n")); err != nil {
			t.Fatalf("写日志失败: %v", err)
		}
	}
	<-done

	// 把渲染序列切开, 只保留日志正文, 检查每行都完整.
	lines := strings.Split(buf.String(), "\n")
	count := 0
	for _, line := range lines {
		if !strings.Contains(line, "日志行") {
			continue
		}
		count++
		// 日志行里出现进度条正文说明擦除不彻底, 会和日志混在一起.
		if strings.Contains(line, "[") && strings.Contains(line, "100.0%") {
			t.Errorf("日志行混入了进度条: %q", line)
		}
	}
	if count != segments {
		t.Errorf("应有 %d 行日志, 实际 %d 行", segments, count)
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := map[string]string{
		"0s":    formatElapsed(0),
		"9s":    formatElapsed(9_400_000_000),
		"1m05s": formatElapsed(65_000_000_000),
	}
	for want, got := range cases {
		if got != want {
			t.Errorf("formatElapsed 得到 %q, 预期 %q", got, want)
		}
	}
}
