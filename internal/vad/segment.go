package vad

import "math"

// 与 Silero 官方实现一致的默认判定参数.
const (
	// WindowSamples 是模型每帧的采样点数 (16000 Hz 下).
	WindowSamples = 512
	// ContextSamples 是每帧额外拼接的历史采样点数.
	ContextSamples = 64
	// StateSize 是 LSTM 隐藏状态的长度.
	StateSize = 128

	defaultThreshold       = 0.5
	defaultMinSilenceMS    = 100
	defaultMinSpeechMS     = 250
	defaultSpeechPadMS     = 30
	defaultNegThresholdGap = 0.15
)

// Options 控制语音区间判定.
type Options struct {
	// Threshold 是判定为语音的概率阈值.
	Threshold float64
	// NegThreshold 是判定语音结束的负阈值, 0 表示 threshold - 0.15.
	NegThreshold float64
	// MinSilenceMS 是可结束一段语音的最短静音时长, 0 表示 100 毫秒.
	MinSilenceMS float64
	// MinSpeechMS 是最短语音时长, 更短的片段会被丢弃, 0 表示 250 毫秒.
	MinSpeechMS float64
	// SpeechPadMS 是语音两端额外保留的时长, 0 表示 30 毫秒.
	SpeechPadMS float64
}

func (o Options) withDefaults() Options {
	if o.Threshold <= 0 || o.Threshold >= 1 {
		o.Threshold = defaultThreshold
	}
	if o.NegThreshold <= 0 {
		o.NegThreshold = math.Max(o.Threshold-defaultNegThresholdGap, 0.01)
	}
	if o.MinSilenceMS <= 0 {
		o.MinSilenceMS = defaultMinSilenceMS
	}
	if o.MinSpeechMS <= 0 {
		o.MinSpeechMS = defaultMinSpeechMS
	}
	if o.SpeechPadMS < 0 {
		o.SpeechPadMS = defaultSpeechPadMS
	}
	return o
}

// RangesFromProbs 把逐帧语音概率转成语音区间.
//
// probs 的第 i 项对应原音频 [i*WindowSamples, (i+1)*WindowSamples) 这一段.
// totalSamples 是原音频的采样点总数, 用来裁剪最后一帧与填充边距.
// 返回的区间按时间顺序排列, 互不重叠, 且都落在 [0, totalSamples/sampleRate] 内.
func RangesFromProbs(probs []float32, totalSamples, sampleRate int, opt Options) []Range {
	if len(probs) == 0 || totalSamples <= 0 || sampleRate <= 0 {
		return nil
	}
	opt = opt.withDefaults()

	msToSamples := func(ms float64) float64 { return float64(sampleRate) * ms / 1000 }
	minSilence := msToSamples(opt.MinSilenceMS)
	minSpeech := msToSamples(opt.MinSpeechMS)
	pad := msToSamples(opt.SpeechPadMS)

	type span struct{ start, end int }
	var found []span

	triggered := false
	start := 0
	tempEnd := 0

	for i, p := range probs {
		cur := i * WindowSamples
		prob := float64(p)

		// 语音重新出现时撤销刚才待定的结束点.
		if prob >= opt.Threshold && tempEnd != 0 {
			tempEnd = 0
		}

		if prob >= opt.Threshold && !triggered {
			triggered = true
			start = cur
			continue
		}

		if prob < opt.NegThreshold && triggered {
			if tempEnd == 0 {
				tempEnd = cur
			}
			if float64(cur-tempEnd) < minSilence {
				continue
			}
			if float64(tempEnd-start) > minSpeech {
				found = append(found, span{start: start, end: tempEnd})
			}
			triggered = false
			tempEnd = 0
		}
	}

	// 音频在语音中途结束时, 把最后一段补到末尾.
	if triggered && float64(totalSamples-start) > minSpeech {
		found = append(found, span{start: start, end: totalSamples})
	}

	if len(found) == 0 {
		return nil
	}

	// 两端补齐, 并避免补齐后相邻区间重叠.
	for i := range found {
		if i == 0 {
			found[i].start = max(0, found[i].start-int(pad))
		}
		if i != len(found)-1 {
			silence := float64(found[i+1].start - found[i].end)
			if silence < 2*pad {
				half := int(silence / 2)
				found[i].end += half
				found[i+1].start = max(0, found[i+1].start-half)
			} else {
				found[i].end = min(totalSamples, found[i].end+int(pad))
				found[i+1].start = max(0, found[i+1].start-int(pad))
			}
		} else {
			found[i].end = min(totalSamples, found[i].end+int(pad))
		}
	}

	out := make([]Range, 0, len(found))
	for _, s := range found {
		end := min(s.end, totalSamples)
		if end <= s.start {
			continue
		}
		out = append(out, Range{
			Start: float64(s.start) / float64(sampleRate),
			End:   float64(end) / float64(sampleRate),
		})
	}
	return out
}

// FrameCount 返回给定样本数需要推理的帧数.
func FrameCount(samples int) int {
	if samples <= 0 {
		return 0
	}
	return (samples + WindowSamples - 1) / WindowSamples
}
