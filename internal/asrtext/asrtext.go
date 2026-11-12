// Package asrtext 处理模型输出的原始文本.
//
// 这里只放纯函数, 不依赖任何推理引擎, 因此可以独立测试,
// 也不会因为加载动态库而在受限环境里失败.
package asrtext

import "strings"

// 标记常量.
const (
	// asrTextTag 是 Qwen3-ASR 在正文前的分隔标记.
	asrTextTag = "<asr_text>"
	// languagePrefix 是原始输出的开头.
	languagePrefix = "language "
)

// StripLanguagePrefix 去掉模型输出里的 "language XXX<asr_text>" 前缀.
//
// Qwen3-ASR 的原始输出形如:
//
//	language English<asr_text>Hello world.
//
// 有些版本只给出 <asr_text> 之后的正文, 有些完全不带前缀, 因此这里做容错处理.
func StripLanguagePrefix(raw string) string {
	text := raw

	// 优先按 <asr_text> 切分, 它之后就是正文.
	if idx := strings.Index(text, asrTextTag); idx >= 0 {
		text = text[idx+len(asrTextTag):]
	} else if strings.HasPrefix(text, languagePrefix) {
		// 只给了语言名没有正文时, 去掉语言前缀, 剩下的按原样返回.
		if i := strings.IndexAny(text, "\n<>"); i > 0 {
			text = text[i:]
			text = strings.TrimPrefix(text, asrTextTag)
			text = strings.TrimPrefix(text, ">")
		}
	}

	return strings.TrimSpace(text)
}

// Language 从原始输出里取出语言名, 没有时返回空串.
//
// 输出形如 "language English<asr_text>..." 时返回 "English".
func Language(raw string) string {
	if !strings.HasPrefix(raw, languagePrefix) {
		return ""
	}

	rest := raw[len(languagePrefix):]
	if idx := strings.IndexAny(rest, "<\n"); idx >= 0 {
		rest = rest[:idx]
	}

	return strings.TrimSpace(rest)
}
