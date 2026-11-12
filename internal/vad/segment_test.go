package vad

import (
	"math"
	"testing"
)

func TestFrameCount(t *testing.T) {
	cases := map[int]int{0: 0, 1: 1, WindowSamples: 1, WindowSamples + 1: 2, 2 * WindowSamples: 2}
	for samples, want := range cases {
		if got := FrameCount(samples); got != want {
			t.Errorf("FrameCount(%d) = %d, 预期 %d", samples, got, want)
		}
	}
}

// speechThenSilence 造出前 speechFrames 帧为语音, 其余为静音的帧序列.
func speechThenSilence(speechFrames, silenceFrames int) []float32 {
	probs := make([]float32, 0, speechFrames+silenceFrames)
	for i := 0; i < speechFrames; i++ {
		probs = append(probs, 0.9)
	}
	for i := 0; i < silenceFrames; i++ {
		probs = append(probs, 0.01)
	}
	return probs
}

func TestRangesFromProbsNoSpeech(t *testing.T) {
	if got := RangesFromProbs(nil, 16000, SampleRate, Options{}); got != nil {
		t.Errorf("空概率应返回 nil, 得到 %v", got)
	}
	if got := RangesFromProbs(speechThenSilence(0, 20), 20*WindowSamples, SampleRate, Options{}); got != nil {
		t.Errorf("全静音应返回 nil, 得到 %v", got)
	}
}

func TestRangesFromProbsWholeClip(t *testing.T) {
	frames := 10
	probs := make([]float32, frames)
	for i := range probs {
		probs[i] = 0.9
	}
	total := frames * WindowSamples

	got := RangesFromProbs(probs, total, SampleRate, Options{})
	if len(got) != 1 {
		t.Fatalf("应得到一整段, 实际 %v", got)
	}
	if got[0].Start != 0 {
		t.Errorf("起点应为 0, 实际 %v", got[0].Start)
	}
	// 末尾补齐后不超过原音频长度.
	if want := float64(total) / SampleRate; math.Abs(got[0].End-want) > 1e-6 {
		t.Errorf("终点应为 %v, 实际 %v", want, got[0].End)
	}
}

// 两段语音之间的静音足够长时, 应切成两段并各自补齐边距.
func TestRangesFromProbsTwoSegments(t *testing.T) {
	probs := append(speechThenSilence(8, 10), speechThenSilence(8, 0)...)
	total := len(probs) * WindowSamples

	got := RangesFromProbs(probs, total, SampleRate, Options{})
	if len(got) != 2 {
		t.Fatalf("应得到两段, 实际 %v", got)
	}
	if got[0].Start != 0 {
		t.Errorf("第一段起点应为 0, 实际 %v", got[0].Start)
	}
	// 两段之间的间隔必须保留, 说明确实切开了.
	if got[1].Start <= got[0].End {
		t.Errorf("第二段起点 %v 应晚于第一段终点 %v", got[1].Start, got[0].End)
	}
	if want := float64(total) / SampleRate; math.Abs(got[1].End-want) > 1e-6 {
		t.Errorf("第二段应延伸到结尾 %v, 实际 %v", want, got[1].End)
	}
}

// 短于最短语音时长的片段直接丢弃, 避免把噪声当成语音.
func TestRangesFromProbsDropsShortSpeech(t *testing.T) {
	// 4 帧约 128 毫秒, 低于默认 250 毫秒.
	probs := speechThenSilence(4, 20)
	got := RangesFromProbs(probs, len(probs)*WindowSamples, SampleRate, Options{})
	if len(got) != 0 {
		t.Errorf("过短的语音应被丢弃, 得到 %v", got)
	}
}

func TestRangesFromProbsRespectsThreshold(t *testing.T) {
	// 概率 0.4 在默认阈值下不算语音, 调低阈值后应被识别.
	probs := make([]float32, 10)
	for i := range probs {
		probs[i] = 0.4
	}
	total := len(probs) * WindowSamples

	if got := RangesFromProbs(probs, total, SampleRate, Options{}); len(got) != 0 {
		t.Errorf("默认阈值下不应识别为语音, 得到 %v", got)
	}
	if got := RangesFromProbs(probs, total, SampleRate, Options{Threshold: 0.3}); len(got) != 1 {
		t.Errorf("降低阈值后应识别为语音, 得到 %v", got)
	}
}
