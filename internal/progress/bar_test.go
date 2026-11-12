package progress

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// report 构造一个按音频时长推进的进度.
func report(segments, totalSegments int, seconds, totalSeconds float64) Report {
	return Report{
		Segments:      segments,
		TotalSegments: totalSegments,
		Seconds:       seconds,
		TotalSeconds:  totalSeconds,
	}
}

// age 把进度条的开始时间往前挪, 用来模拟已运行一段时间.
//
// 速度估算要求先积累一点时长, 测试里不必真的等.
// 同时清掉上一次重绘时间, 否则紧接着的 Set 会被节流拦下.
func age(b *Bar, d time.Duration) {
	b.start = time.Now().Add(-d)
	b.lastRender = time.Time{}
}

func TestInteractiveRendersCoreFields(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)
	bar.Set(report(2, 6, 10, 30))
	out := buf.String()

	if !strings.HasPrefix(out, "\r") {
		t.Errorf("应回到行首渲染, 实际 %q", out)
	}
	if !strings.Contains(out, "33.3%") {
		t.Errorf("应显示百分比, 实际 %q", out)
	}
	if !strings.Contains(out, "2/6 段") {
		t.Errorf("应显示分段数, 实际 %q", out)
	}
	if !strings.Contains(out, "音频 10.0s/30.0s") {
		t.Errorf("应显示音频进度, 实际 %q", out)
	}
}

// 百分比按音频时长算: 6 段里跑完 2 段, 但这两段占了一半音频, 就该显示 50%.
func TestPercentUsesAudioDuration(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)
	bar.Set(report(2, 6, 15, 30))

	if !strings.Contains(buf.String(), "50.0%") {
		t.Errorf("应按音频时长算百分比, 实际 %q", buf.String())
	}
}

// 时长未知时退回按分段数算, 不能显示 NaN 或 0%.
func TestPercentFallsBackToSegments(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)
	bar.Set(report(3, 4, 0, 0))

	if !strings.Contains(buf.String(), "75.0%") {
		t.Errorf("时长未知时应按分段数算, 实际 %q", buf.String())
	}
	if strings.Contains(buf.String(), "NaN") {
		t.Errorf("不应出现 NaN, 实际 %q", buf.String())
	}
}

// 数据不足时给出占位, 不要给出误导性的速度.
func TestSpeedHiddenUntilEnoughData(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)
	bar.Set(report(1, 4, 5, 20))

	out := buf.String()
	if strings.Contains(out, "x") && strings.Contains(out, "剩余 0s") {
		t.Errorf("数据不足时不应给出速度和剩余时间, 实际 %q", out)
	}
	if !strings.Contains(out, "剩余 --") {
		t.Errorf("数据不足时剩余时间应显示占位, 实际 %q", out)
	}
}

// 速度与剩余时间: 10 秒音频用了 2 秒, 速度 5x, 剩 20 秒音频约 4 秒.
func TestSpeedAndRemainingTime(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)
	bar.Set(report(1, 3, 10, 30))
	age(bar, 2*time.Second)

	buf.Reset()
	bar.Set(report(1, 3, 10, 30))
	out := buf.String()

	if !strings.Contains(out, "5.0x") {
		t.Errorf("速度应为 5.0x, 实际 %q", out)
	}
	if !strings.Contains(out, "剩余 4s") {
		t.Errorf("剩余时间应约为 4s, 实际 %q", out)
	}
}

// 全部完成时剩余时间应为 0, 不能显示负数或一直不清零.
func TestRemainingIsZeroWhenFinished(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)
	bar.Set(report(3, 3, 30, 30))
	age(bar, 6*time.Second)

	buf.Reset()
	bar.Set(report(3, 3, 30, 30))

	if !strings.Contains(buf.String(), "剩余 0s") {
		t.Errorf("完成时剩余时间应为 0s, 实际 %q", buf.String())
	}
	if strings.Contains(buf.String(), "剩余 -") {
		t.Errorf("剩余时间不应为负数, 实际 %q", buf.String())
	}
}

// 最后一段必须刷新, 否则进度条会停在未满状态.
func TestFinalSegmentAlwaysRenders(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(report(1, 2, 5, 10))
	buf.Reset()
	bar.Set(report(2, 2, 10, 10))

	if !strings.Contains(buf.String(), "100.0%") {
		t.Errorf("最后一段应显示 100%%, 实际 %q", buf.String())
	}
}

// 同一次进度内的高频更新应被节流, 避免每段都刷一次终端.
func TestRendersAreThrottled(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(report(1, 10, 1, 10))
	buf.Reset()
	for i := 2; i < 10; i++ {
		bar.Set(report(i, 10, float64(i), 10))
	}

	if buf.Len() != 0 {
		t.Errorf("重绘间隔内的更新不应输出, 实际 %q", buf.String())
	}
}

// 写日志前必须先擦掉进度条, 保证日志从行首开始.
func TestLogWriteErasesBarFirst(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(report(1, 4, 5, 20))
	buf.Reset()

	if _, err := bar.Writer().Write([]byte("一行日志\n")); err != nil {
		t.Fatalf("写日志失败: %v", err)
	}
	out := buf.String()

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

	bar.Set(report(1, 4, 5, 20))
	bar.Close()
	buf.Reset()

	bar.Set(report(2, 4, 10, 20))
	if buf.Len() != 0 {
		t.Errorf("关闭后不应继续渲染, 实际 %q", buf.String())
	}
}

// 非终端下不能写控制字符, 否则会污染日志文件.
func TestNonInteractiveHasNoControlChars(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, false)

	bar.Set(report(1, 4, 5, 20))
	out := buf.String()

	if strings.ContainsAny(out, "\r\x1b") {
		t.Errorf("非终端输出不应含控制字符, 实际 %q", out)
	}
	if !strings.Contains(out, "1/4 段") {
		t.Errorf("应输出进度文本, 实际 %q", out)
	}
}

// 失败段数要体现在进度里, 便于用户判断结果是否完整.
func TestFailedCountIsShown(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(Report{Segments: 2, TotalSegments: 4, Failed: 1, Seconds: 10, TotalSeconds: 20})
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
			bar.Set(report(i, segments, float64(i), segments))
		}
	}()

	for i := 0; i < segments; i++ {
		if _, err := bar.Writer().Write([]byte("日志行\n")); err != nil {
			t.Fatalf("写日志失败: %v", err)
		}
	}
	<-done

	lines := strings.Split(buf.String(), "\n")
	count := 0
	for _, line := range lines {
		if !strings.Contains(line, "日志行") {
			continue
		}
		count++
		if strings.Contains(line, "100.0%") {
			t.Errorf("日志行混入了进度条: %q", line)
		}
	}
	if count != segments {
		t.Errorf("应有 %d 行日志, 实际 %d 行", segments, count)
	}
}

func TestFormatClock(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{9 * time.Second, "9s"},
		{65 * time.Second, "1m05s"},
		{3720 * time.Second, "1h02m"},
		{-5 * time.Second, "0s"},
	}
	for _, c := range cases {
		if got := formatClock(c.in); got != c.want {
			t.Errorf("formatClock(%v) = %q, 预期 %q", c.in, got, c.want)
		}
	}
}

// 批量处理时要显示文件进度, 否则看不出整体还剩几个文件.
func TestFileProgressShown(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)
	r := report(1, 4, 5, 20)
	r.File, r.TotalFiles = 2, 5
	bar.Set(r)

	if !strings.Contains(buf.String(), "文件 2/5") {
		t.Errorf("应显示文件进度, 实际 %q", buf.String())
	}
}

// 只处理一个文件时不显示文件进度, 免得出现无意义的 1/1.
func TestFileProgressHiddenForSingleFile(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)
	r := report(1, 4, 5, 20)
	r.File, r.TotalFiles = 1, 1
	bar.Set(r)

	if strings.Contains(buf.String(), "文件") {
		t.Errorf("单文件不应显示文件进度, 实际 %q", buf.String())
	}
}

// 开始下一个文件时重置计时与进度, 否则倍速会被上一个文件的耗时拉偏.
func TestResetClearsTimingAndProgress(t *testing.T) {
	var buf bytes.Buffer
	bar := New(&buf, true)

	bar.Set(report(4, 4, 20, 20))
	age(bar, 10*time.Second)
	bar.Reset()

	if !bar.start.IsZero() {
		t.Errorf("Reset 后开始时间应清零")
	}

	buf.Reset()
	bar.Set(report(1, 4, 5, 20))
	out := buf.String()
	if strings.Contains(out, "x ") {
		t.Errorf("重置后不应沿用旧速度, 实际 %q", out)
	}
	if !strings.Contains(out, "剩余 --") {
		t.Errorf("重置后数据不足, 剩余时间应显示占位, 实际 %q", out)
	}
}
