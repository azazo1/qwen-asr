package audio

import (
	"errors"
	"fmt"
	"math"
)

// SpeechSpan 是一段语音在原音频中的位置, 单位秒.
type SpeechSpan struct {
	Start float64
	End   float64
}

// mergeGapSeconds 是合并相邻语音区间时容忍的最大静音间隔.
//
// 太小的间隔不值得单独推理一次: 合并后仍有足够上下文, 又少一次模型调用.
const mergeGapSeconds = 0.5

// SegmentBySpeech 按语音区间切分, 跳过区间之外的静音.
//
// 与 Segment 不同, 这里不使用能量阈值: 调用方已经用 VAD 模型给出了语音位置.
// 每段的 Start 与 End 仍是原音频中的真实时间, 因此字幕时间戳不会因为跳过静音而偏移.
// 超过 maxSeconds 的语音会硬切, 保证每段都在模型可处理长度内.
func SegmentBySpeech(samples []float32, sampleRate int, spans []SpeechSpan, maxSeconds float64) ([]Clip, error) {
	if len(samples) == 0 {
		return nil, ErrEmptyAudio
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("采样率必须为正数, 收到 %d", sampleRate)
	}
	if maxSeconds <= 0 {
		maxSeconds = 60
	}

	totalSec := float64(len(samples)) / float64(sampleRate)
	normalized := normalizeSpans(spans, totalSec)
	if len(normalized) == 0 {
		return nil, ErrNoSpeech
	}

	merged := mergeSpans(normalized, mergeGapSeconds, maxSeconds)

	var clips []Clip
	for _, span := range merged {
		cur := span.Start
		for cur < span.End-1e-9 {
			end := math.Min(cur+maxSeconds, span.End)
			clips = append(clips, Clip{Start: cur, End: end})
			cur = end
		}
	}

	out := make([]Clip, 0, len(clips))
	for _, c := range clips {
		startSample := int(math.Round(c.Start * float64(sampleRate)))
		endSample := int(math.Round(c.End * float64(sampleRate)))
		startSample = max(startSample, 0)
		endSample = min(endSample, len(samples))
		if endSample <= startSample {
			continue
		}
		c.Index = len(out)
		c.Samples = samples[startSample:endSample]
		out = append(out, c)
	}

	if len(out) == 0 {
		return nil, ErrEmptyAudio
	}
	return out, nil
}

// ErrNoSpeech 表示 VAD 没有在音频里找到语音.
var ErrNoSpeech = errors.New("没有检测到语音; 可以尝试调低 --vad-threshold, 或不启用 VAD 改回能量分段")

// normalizeSpans 裁剪到有效范围并丢弃非法区间, 保持时间顺序.
func normalizeSpans(spans []SpeechSpan, totalSec float64) []SpeechSpan {
	out := make([]SpeechSpan, 0, len(spans))
	for _, s := range spans {
		start := math.Max(s.Start, 0)
		end := math.Min(s.End, totalSec)
		if end-start <= 0 {
			continue
		}
		if n := len(out); n > 0 && start < out[n-1].Start {
			// 乱序区间会让后续合并与切分失去意义, 直接跳过.
			continue
		}
		out = append(out, SpeechSpan{Start: start, End: end})
	}
	return out
}

// mergeSpans 合并间隔很小的相邻区间, 合并后不能超过 maxSeconds.
func mergeSpans(spans []SpeechSpan, gap, maxSeconds float64) []SpeechSpan {
	if len(spans) <= 1 {
		return spans
	}

	out := make([]SpeechSpan, 0, len(spans))
	for _, s := range spans {
		if len(out) > 0 {
			last := &out[len(out)-1]
			if s.Start-last.End <= gap && s.End-last.Start <= maxSeconds {
				last.End = s.End
				continue
			}
		}
		out = append(out, s)
	}
	return out
}
