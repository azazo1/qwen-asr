// Package subtitle 负责文本后处理与输出格式生成.
package subtitle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Segment 是输出时需要的一段带时间的文本.
//
// 与 transcribe.Segment 保持字段一致, 但这里独立定义, 避免两个包互相依赖.
type Segment struct {
	Index  int     `json:"index"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Text   string  `json:"text"`
	Failed bool    `json:"failed,omitempty"`
}

// JoinSegments 把各段文本拼成连贯的一段.
//
// 中文等无空格文字直接相接, 拉丁文字之间补一个空格,
// 否则英文句子之间会粘在一起.
func JoinSegments(segments []Segment) string {
	var b strings.Builder

	for _, seg := range segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}

		if b.Len() > 0 && needsSpace(b.String(), text) {
			b.WriteString(" ")
		}
		b.WriteString(text)
	}

	return b.String()
}

// needsSpace 判断两段文本拼接处是否需要空格.
//
// 只有两侧都是拉丁文字时才补空格: 中文与日文之间加空格反而别扭,
// 而英文句子之间不加空格会粘成一串.
func needsSpace(prev, next string) bool {
	last := firstRuneFromEnd(prev)
	first := firstRune(next)
	if last == 0 || first == 0 {
		return false
	}

	return isLatin(last) && isLatin(first)
}

// firstRune 返回字符串的第一个字符.
func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

// firstRuneFromEnd 返回字符串的最后一个字符.
func firstRuneFromEnd(s string) rune {
	var last rune
	for _, r := range s {
		last = r
	}
	return last
}

// isLatin 判断字符是否属于拉丁文字体系.
func isLatin(r rune) bool {
	if r < unicode.MaxASCII {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsPunct(r)
	}
	// 带变音符号的拉丁字母.
	return unicode.In(r, unicode.Latin)
}

// WriteText 写出纯文本结果.
func WriteText(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("无法创建输出目录: %w", err)
	}
	if err := os.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
		return fmt.Errorf("无法写入 %s: %w", path, err)
	}
	return nil
}

// WriteSRT 写出 SRT 字幕.
//
// 时间轴使用分段边界: Qwen3-ASR 本身不输出词级时间戳,
// 段内文本被视为整段同时出现.
func WriteSRT(path string, segments []Segment) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("无法创建输出目录: %w", err)
	}

	var b strings.Builder
	seq := 1

	for _, seg := range segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}

		fmt.Fprintf(&b, "%d\n", seq)
		fmt.Fprintf(&b, "%s --> %s\n", formatSRTTime(seg.Start), formatSRTTime(seg.End))
		b.WriteString(text)
		b.WriteString("\n\n")
		seq++
	}

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("无法写入 %s: %w", path, err)
	}
	return nil
}

// formatSRTTime 把秒数格式化成 SRT 的 HH:MM:SS,mmm.
func formatSRTTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}

	total := int(seconds)
	ms := int((seconds - float64(total)) * 1000)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60

	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// Document 是 JSON 输出的结构.
type Document struct {
	File     string    `json:"file"`
	Duration float64   `json:"duration"`
	Language string    `json:"language,omitempty"`
	Segments []Segment `json:"segments"`
}

// WriteJSON 写出 JSON 结果.
func WriteJSON(path string, doc Document) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("无法创建输出目录: %w", err)
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("无法序列化 JSON: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("无法写入 %s: %w", path, err)
	}
	return nil
}
