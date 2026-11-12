// Package progress 在终端里用原地刷新的进度条展示任务进度.
//
// 非终端 (管道, 重定向到文件) 下不画进度条, 改为每隔几秒输出一行文本,
// 避免把控制字符写进日志文件.
//
// 进度条与日志共用同一个输出流, 因此这里提供一个包装后的 writer:
// 日志写入前先擦掉进度条所在的行, 等下一次更新再重画,
// 这样日志不会被进度条截断成半行.
package progress

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	// renderInterval 限制进度条重绘频率, 避免每个分段都刷一次终端.
	renderInterval = 100 * time.Millisecond
	// lineInterval 是退化模式下输出一行的间隔.
	lineInterval = 5 * time.Second
	// barWidth 是进度条本体的字符宽度.
	barWidth = 24
	// minSpeedSeconds 是估算速度前必须积累的时长.
	//
	// 太早算出来的速度会被模型加载后的第一次推理拉偏, 显示成夸张的倍速.
	minSpeedSeconds = 500 * time.Millisecond
)

// Report 是一次进度快照.
//
// 百分比按音频时长计算, 而不是按分段个数: 分段长短不一,
// 用个数算会让进度与实际处理量明显脱节, 剩余时间也跟着失真.
type Report struct {
	// Segments 是已完成分段数.
	Segments int
	// TotalSegments 是分段总数.
	TotalSegments int
	// Failed 是失败分段数.
	Failed int
	// Seconds 是已处理的音频时长.
	Seconds float64
	// TotalSeconds 是本次要处理的音频总时长, 0 表示未知.
	TotalSeconds float64
	// File 是当前处理到第几个文件, 从 1 开始. 0 表示不显示文件进度.
	File int
	// TotalFiles 是文件总数.
	TotalFiles int
}

// Bar 是一个可原地刷新的进度条.
//
// 零值不可用, 需通过 New 创建. 所有方法都是并发安全的.
type Bar struct {
	mu  sync.Mutex
	out io.Writer

	interactive bool

	report Report
	start  time.Time

	lastRender time.Time
	lastLine   time.Time

	// active 表示终端里当前有一行未换行的进度条, 需要先擦除再写日志.
	active bool
	// lineLen 是上一次渲染内容的字节数, 用于用空格覆盖旧内容.
	lineLen int
	closed  bool
}

// New 创建一个进度条. interactive 为 false 时退化为定期输出文本行.
//
// 计时从第一次 Set 开始, 这样显示的是任务本身的耗时, 不包含加载模型等准备时间.
func New(out io.Writer, interactive bool) *Bar {
	return &Bar{out: out, interactive: interactive}
}

// Writer 返回可供日志使用的 writer, 写入时会正确处理进度条所在行.
func (b *Bar) Writer() io.Writer { return barWriter{b} }

type barWriter struct{ b *Bar }

func (w barWriter) Write(p []byte) (int, error) { return w.b.writeLog(p) }

// writeLog 在写日志前擦除进度条, 保证日志从行首开始且完整.
func (b *Bar) writeLog(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.interactive && b.active {
		b.erase()
	}
	return b.out.Write(p)
}

// Set 更新进度.
func (b *Bar) Set(r Report) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	b.report = r
	if r.TotalSegments <= 0 && r.TotalSeconds <= 0 {
		return
	}
	if b.start.IsZero() {
		b.start = time.Now()
	}

	if !b.interactive {
		b.maybeLine()
		return
	}

	// 最后一次更新必须刷新, 否则进度条会停在未满的状态.
	finished := r.Segments >= r.TotalSegments && r.TotalSegments > 0
	if !finished && time.Since(b.lastRender) < renderInterval {
		return
	}
	b.lastRender = time.Now()
	b.draw()
}

// Reset 清掉当前进度与其计时, 用于开始处理下一个任务.
//
// 不重置的话, 第二个文件会继承第一个文件的耗时, 倍速和剩余时间都会失真.
func (b *Bar) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.report = Report{}
	b.start = time.Time{}
	b.lastRender = time.Time{}
	b.lastLine = time.Time{}
}

// Close 擦掉进度条, 不再输出任何内容. 重复调用是安全的.
func (b *Bar) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true
	if b.interactive && b.active {
		b.erase()
	}
}

// draw 重画进度条, 不换行.
func (b *Bar) draw() {
	text := b.render()
	// 用 \r 回到行首, 再用空格补齐旧内容, 不依赖 ANSI 清行序列.
	padding := ""
	if n := b.lineLen - len(text); n > 0 {
		padding = strings.Repeat(" ", n)
	}
	fmt.Fprintf(b.out, "\r%s%s", text, padding)
	b.lineLen = len(text)
	b.active = true
}

// erase 擦除当前进度条行并回到行首.
func (b *Bar) erase() {
	if b.lineLen == 0 {
		return
	}
	fmt.Fprintf(b.out, "\r%s\r", strings.Repeat(" ", b.lineLen))
	b.lineLen = 0
	b.active = false
}

// ratio 返回完成比例, 优先按音频时长计算.
func (b *Bar) ratio() float64 {
	var ratio float64
	switch {
	case b.report.TotalSeconds > 0:
		ratio = b.report.Seconds / b.report.TotalSeconds
	case b.report.TotalSegments > 0:
		ratio = float64(b.report.Segments) / float64(b.report.TotalSegments)
	}
	return min(max(ratio, 0), 1)
}

// speed 返回相对实时的处理倍速, 例如 3.6 表示每秒处理 3.6 秒音频.
//
// 用累计时长与累计音频算平均速度, 比逐段瞬时速度稳定, 不会跳来跳去.
func (b *Bar) speed(elapsed time.Duration) float64 {
	if b.report.Seconds <= 0 || elapsed < minSpeedSeconds {
		return 0
	}
	return b.report.Seconds / elapsed.Seconds()
}

// render 生成进度条文本.
func (b *Bar) render() string {
	ratio := b.ratio()
	filled := min(max(int(ratio*barWidth), 0), barWidth)

	var sb strings.Builder
	sb.WriteString("  [")
	sb.WriteString(strings.Repeat("#", filled))
	sb.WriteString(strings.Repeat("-", barWidth-filled))
	fmt.Fprintf(&sb, "] %5.1f%%", ratio*100)

	// 分段个数单独列出: 时长百分比看不出还剩多少段要跑.
	fmt.Fprintf(&sb, "  %d/%d 段", b.report.Segments, b.report.TotalSegments)

	// 批量处理时补上文件进度, 否则看不出整体还要跑几个文件.
	if b.report.TotalFiles > 1 && b.report.File > 0 {
		fmt.Fprintf(&sb, "  文件 %d/%d", b.report.File, b.report.TotalFiles)
	}

	elapsed := time.Since(b.start)
	sb.WriteString("  ")
	sb.WriteString(formatElapsed(elapsed))

	// 速度与剩余时间: 数据不足时显示占位, 避免给出误导性的数字.
	if speed := b.speed(elapsed); speed > 0 {
		fmt.Fprintf(&sb, "  剩余 %s", formatETA(b.remaining(speed)))
		fmt.Fprintf(&sb, "  %.1fx", speed)
	} else {
		sb.WriteString("  剩余 --")
	}

	// 音频时长进度, 让"处理了多少内容"有直观参照.
	if b.report.TotalSeconds > 0 {
		fmt.Fprintf(&sb, "  音频 %s/%s",
			formatSeconds(b.report.Seconds), formatSeconds(b.report.TotalSeconds))
	}

	if b.report.Failed > 0 {
		fmt.Fprintf(&sb, "  失败 %d", b.report.Failed)
	}
	return sb.String()
}

// remaining 返回按当前速度估算的剩余时间.
func (b *Bar) remaining(speed float64) time.Duration {
	rest := b.report.TotalSeconds - b.report.Seconds
	if b.report.TotalSeconds <= 0 || rest <= 0 {
		return 0
	}
	return time.Duration(rest / speed * float64(time.Second))
}

// maybeLine 是退化模式: 每隔一段时间输出一行, 不含控制字符.
func (b *Bar) maybeLine() {
	finished := b.report.Segments >= b.report.TotalSegments && b.report.TotalSegments > 0
	if time.Since(b.lastLine) < lineInterval && !finished {
		return
	}
	b.lastLine = time.Now()

	elapsed := time.Since(b.start)
	var sb strings.Builder
	fmt.Fprintf(&sb, "  进度 %.1f%%  %d/%d 段  %s",
		b.ratio()*100, b.report.Segments, b.report.TotalSegments, formatElapsed(elapsed))
	if b.report.TotalFiles > 1 && b.report.File > 0 {
		fmt.Fprintf(&sb, "  文件 %d/%d", b.report.File, b.report.TotalFiles)
	}
	if speed := b.speed(elapsed); speed > 0 {
		fmt.Fprintf(&sb, "  剩余 %s  %.1fx", formatETA(b.remaining(speed)), speed)
	}
	if b.report.TotalSeconds > 0 {
		fmt.Fprintf(&sb, "  音频 %s/%s",
			formatSeconds(b.report.Seconds), formatSeconds(b.report.TotalSeconds))
	}
	if b.report.Failed > 0 {
		fmt.Fprintf(&sb, "  失败 %d", b.report.Failed)
	}
	fmt.Fprintln(b.out, sb.String())
}

// formatElapsed 把时长格式化成便于阅读的形式, 至少精确到秒.
func formatElapsed(d time.Duration) string {
	return formatClock(d.Round(time.Second))
}

// formatETA 把剩余时间格式化成便于阅读的形式.
func formatETA(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return formatClock(d.Round(time.Second))
}

// formatClock 把时长写成 3s / 1m05s / 1h02m 形式.
func formatClock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d / time.Second)
	h, m, s := total/3600, (total/60)%60, total%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// formatSeconds 是音频时长, 保留一位小数.
func formatSeconds(v float64) string {
	return fmt.Sprintf("%.1fs", v)
}
