package audio

import (
	"errors"
	"math"
	"testing"
)

func ones(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = 0.1
	}
	return out
}

// 跳过静音后, 每段的时间戳仍应指向原音频中的真实位置.
func TestSegmentBySpeechKeepsTimeline(t *testing.T) {
	samples := ones(SampleRate) // 1 秒
	spans := []SpeechSpan{{Start: 0.2, End: 0.4}, {Start: 0.6, End: 0.8}}

	got, err := SegmentBySpeech(samples, SampleRate, spans, 60)
	if err != nil {
		t.Fatalf("分段失败: %v", err)
	}
	// 0.2 秒的间隔小于合并阈值, 两段会合成一段, 但位置必须覆盖原区间.
	if len(got) != 1 {
		t.Fatalf("应合并为一段, 实际 %d 段", len(got))
	}
	if math.Abs(got[0].Start-0.2) > 1e-6 || math.Abs(got[0].End-0.8) > 1e-6 {
		t.Errorf("区间应为 [0.2, 0.8], 实际 [%v, %v]", got[0].Start, got[0].End)
	}
	if len(got[0].Samples) != int(0.6*SampleRate) {
		t.Errorf("样本数应为 %d, 实际 %d", int(0.6*SampleRate), len(got[0].Samples))
	}
}

// 间隔超过合并阈值时保持独立, 静音部分不应进入任何一段.
func TestSegmentBySpeechSeparatesDistantSpans(t *testing.T) {
	samples := ones(SampleRate)
	spans := []SpeechSpan{{Start: 0.1, End: 0.3}, {Start: 0.9, End: 1.0}}

	got, err := SegmentBySpeech(samples, SampleRate, spans, 60)
	if err != nil {
		t.Fatalf("分段失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应得到两段, 实际 %d 段", len(got))
	}
	if got[1].Start < 0.85 {
		t.Errorf("第二段起点应接近 0.9, 实际 %v", got[1].Start)
	}
}

// 超过单段上限的语音必须在区间内切开, 且切片之间不留空隙.
func TestSegmentBySpeechSplitsLongSpan(t *testing.T) {
	samples := ones(3 * SampleRate)
	spans := []SpeechSpan{{Start: 0, End: 3}}

	got, err := SegmentBySpeech(samples, SampleRate, spans, 1.0)
	if err != nil {
		t.Fatalf("分段失败: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("3 秒按 1 秒上限应切成 3 段, 实际 %d 段", len(got))
	}
	for i, clip := range got {
		if clip.Duration() > 1.0+1e-6 {
			t.Errorf("第 %d 段时长 %v 超过上限", i, clip.Duration())
		}
		if clip.Index != i {
			t.Errorf("第 %d 段序号应为 %d, 实际 %d", i, i, clip.Index)
		}
		if len(clip.Samples) == 0 {
			t.Errorf("第 %d 段没有样本", i)
		}
	}
}

// 越界区间要裁剪到音频范围内.
func TestSegmentBySpeechClampsSpans(t *testing.T) {
	samples := ones(SampleRate)
	spans := []SpeechSpan{{Start: -1, End: 0.3}, {Start: 0.9, End: 99}}

	got, err := SegmentBySpeech(samples, SampleRate, spans, 60)
	if err != nil {
		t.Fatalf("分段失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应得到两段, 实际 %d 段", len(got))
	}
	if got[0].Start != 0 {
		t.Errorf("第一段起点应被裁剪到 0, 实际 %v", got[0].Start)
	}
	if math.Abs(got[1].End-1.0) > 1e-6 {
		t.Errorf("第二段终点应被裁剪到 1.0, 实际 %v", got[1].End)
	}
}

// 没有任何语音时必须报出可理解的错误, 而不是产出空结果.
func TestSegmentBySpeechNoSpeech(t *testing.T) {
	_, err := SegmentBySpeech(ones(SampleRate), SampleRate, nil, 60)
	if !errors.Is(err, ErrNoSpeech) {
		t.Errorf("应返回 ErrNoSpeech, 实际 %v", err)
	}
}

func TestSegmentBySpeechEmptyAudio(t *testing.T) {
	if _, err := SegmentBySpeech(nil, SampleRate, []SpeechSpan{{Start: 0, End: 1}}, 60); !errors.Is(err, ErrEmptyAudio) {
		t.Errorf("空音频应返回 ErrEmptyAudio, 实际 %v", err)
	}
}
