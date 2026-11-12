// Package progress 在终端里用原地刷新的进度条展示任务进度.
//
// 非终端 (管道, 重定向到文件) 下不画进度条, 改为每隔几秒输出一行文本,
// 避免把控制字符写进日志文件.
//
// 进度条与日志共用同一个输出流, 因此这里提供一个包装后的 writer:
// 日志写入前先擦掉进度条所在的行, 写完之后下一次更新再重画,
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
)

// Bar 是一个可原地刷新的进度条.
//
// 零值不可用, 需通过 New 创建. 所有方法都是并发安全的.
type Bar struct {
	mu  sync.Mutex
	out io.Writer

	interactive bool

	total  int
	done   int
	failed int
	start  time.Time

	lastRender time.Time
	lastLine   time.Time

	// active 表示终端里当前有一行未换行的进度条, 需要先擦除再写日志.
	active bool
	// lineLen 是上一次渲染的可见字符数, 用于用空格覆盖旧内容.
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
func (b *Bar) Set(done, total, failed int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	b.done, b.total, b.failed = done, total, failed
	if b.total <= 0 {
		return
	}
	if b.start.IsZero() {
		b.start = time.Now()
	}

	if !b.interactive {
		b.maybeLine()
		return
	}

	// 最后一段必须刷新, 否则进度条会停在未满的状态.
	if done < b.total && time.Since(b.lastRender) < renderInterval {
		return
	}
	b.lastRender = time.Now()
	b.draw()
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

// render 生成进度条文本.
func (b *Bar) render() string {
	ratio := float64(b.done) / float64(b.total)
	if ratio > 1 {
		ratio = 1
	}

	filled := int(ratio * barWidth)
	filled = min(max(filled, 0), barWidth)

	var sb strings.Builder
	sb.WriteString("  [")
	sb.WriteString(strings.Repeat("#", filled))
	sb.WriteString(strings.Repeat("-", barWidth-filled))
	fmt.Fprintf(&sb, "] %5.1f%%  %d/%d", ratio*100, b.done, b.total)
	if b.failed > 0 {
		fmt.Fprintf(&sb, "  失败 %d", b.failed)
	}
	fmt.Fprintf(&sb, "  %s", formatElapsed(time.Since(b.start)))
	return sb.String()
}

// maybeLine 是退化模式: 每隔一段时间输出一行, 不含控制字符.
func (b *Bar) maybeLine() {
	if time.Since(b.lastLine) < lineInterval && b.done < b.total {
		return
	}
	b.lastLine = time.Now()
	fmt.Fprintf(b.out, "  进度 %d/%d (%.1f%%), 失败 %d, 已用 %s\n",
		b.done, b.total, 100*float64(b.done)/float64(b.total), b.failed,
		formatElapsed(time.Since(b.start)))
}

// formatElapsed 把时长格式化成便于阅读的形式.
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	m := int(d / time.Minute)
	s := int(d/time.Second) % 60
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
