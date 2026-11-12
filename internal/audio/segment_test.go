package audio

import (
	"math"
	"testing"
)

// 生成一段正弦波, 便于构造可预测的样本.
func tone(seconds float64, rate int, amp float32) []float32 {
	n := int(seconds * float64(rate))
	out := make([]float32, n)
	for i := range out {
		out[i] = amp * float32(math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
	}
	return out
}

// 生成静音.
func silence(seconds float64, rate int) []float32 {
	return make([]float32, int(seconds*float64(rate)))
}

func concat(parts ...[]float32) []float32 {
	var out []float32
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestSegmentNoSilenceHardCut(t *testing.T) {
	// 30 秒连续声音, 上限 10 秒, 应切成 3 段.
	rate := 16000
	samples := tone(30, rate, 0.5)

	clips, err := Segment(samples, rate, SegmentOptions{MaxSeconds: 10})
	if err != nil {
		t.Fatalf("Segment 失败: %v", err)
	}

	if len(clips) != 3 {
		t.Fatalf("预期 3 段, 实际 %d 段", len(clips))
	}

	// 各段必须首尾相接并且覆盖完整时长.
	var covered float64
	for i, c := range clips {
		if c.Index != i {
			t.Errorf("第 %d 段的 Index 为 %d", i, c.Index)
		}
		if len(c.Samples) == 0 {
			t.Errorf("第 %d 段没有样本", i)
		}
		covered += c.Duration()
	}

	if math.Abs(covered-30) > 0.05 {
		t.Errorf("总时长 %.3f, 预期接近 30", covered)
	}
}

func TestSegmentCutsAtSilence(t *testing.T) {
	// 8 秒声音 + 2 秒静音 + 8 秒声音, 上限 12 秒:
	// 应优先在静音处切开, 而不是硬切在 12 秒.
	rate := 16000
	samples := concat(tone(8, rate, 0.5), silence(2, rate), tone(8, rate, 0.5))

	clips, err := Segment(samples, rate, SegmentOptions{MaxSeconds: 12})
	if err != nil {
		t.Fatalf("Segment 失败: %v", err)
	}

	if len(clips) != 2 {
		t.Fatalf("预期 2 段, 实际 %d 段", len(clips))
	}

	// 第一段应结束在静音区间内 (8 到 10 秒之间).
	if clips[0].End < 8 || clips[0].End > 10 {
		t.Errorf("第一段结束于 %.2f, 预期落在 8-10 秒的静音里", clips[0].End)
	}
}

func TestSegmentSingleClip(t *testing.T) {
	// 短音频不需要切开.
	rate := 16000
	samples := tone(3, rate, 0.5)

	clips, err := Segment(samples, rate, SegmentOptions{MaxSeconds: 60})
	if err != nil {
		t.Fatalf("Segment 失败: %v", err)
	}

	if len(clips) != 1 {
		t.Fatalf("预期 1 段, 实际 %d 段", len(clips))
	}
	if math.Abs(clips[0].End-3) > 0.01 {
		t.Errorf("该段结束于 %.3f, 预期 3", clips[0].End)
	}
}

func TestSegmentEmptyAudio(t *testing.T) {
	if _, err := Segment(nil, 16000, SegmentOptions{}); err == nil {
		t.Fatal("空音频应当报错")
	}
}

func TestSegmentInvalidRate(t *testing.T) {
	if _, err := Segment([]float32{0.1}, 0, SegmentOptions{}); err == nil {
		t.Fatal("采样率为 0 时应当报错")
	}
}

func TestSegmentClipsAreWithinLimit(t *testing.T) {
	// 每段都不能超过上限, 这是喂给模型的前提.
	rate := 16000
	samples := concat(tone(5, rate, 0.5), silence(1, rate), tone(20, rate, 0.5))

	const limit = 7.0
	clips, err := Segment(samples, rate, SegmentOptions{MaxSeconds: limit})
	if err != nil {
		t.Fatalf("Segment 失败: %v", err)
	}

	for _, c := range clips {
		if c.Duration() > limit+0.01 {
			t.Errorf("第 %d 段时长 %.2f 超过上限 %.1f", c.Index, c.Duration(), limit)
		}
	}
}

func TestResampleChangesLength(t *testing.T) {
	// 从 16k 降到 8k, 样本数应减半.
	samples := tone(1, 16000, 0.5)
	out := Resample(samples, 16000, 8000)

	if len(out) != len(samples)/2 {
		t.Errorf("重采样后长度 %d, 预期 %d", len(out), len(samples)/2)
	}
}

func TestResampleSameRateReturnsInput(t *testing.T) {
	samples := []float32{0.1, 0.2, 0.3}
	out := Resample(samples, 16000, 16000)

	// 同一采样率不应复制, 直接复用底层数组.
	if &out[0] != &samples[0] {
		t.Error("相同采样率时应直接返回原切片")
	}
}

func TestResamplePreservesEndpoints(t *testing.T) {
	samples := []float32{1, 1, 1, 1}
	out := Resample(samples, 4, 8)

	if math.Abs(float64(out[0])-1) > 1e-6 {
		t.Errorf("首样本为 %v, 预期 1", out[0])
	}
	// 末样本必须落在原数据范围内, 不能出现零值或越界读到的垃圾.
	if math.Abs(float64(out[len(out)-1])-1) > 1e-6 {
		t.Errorf("末样本为 %v, 预期 1", out[len(out)-1])
	}
}

func TestMixToMono(t *testing.T) {
	// 立体声交错: (1,0), (0,1) 混成 0.5, 0.5.
	got := MixToMono([]float32{1, 0, 0, 1}, 2)

	want := []float32{0.5, 0.5}
	if len(got) != len(want) {
		t.Fatalf("长度 %d, 预期 %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Errorf("第 %d 个样本为 %v, 预期 %v", i, got[i], want[i])
		}
	}
}

func TestMixToMonoPassthrough(t *testing.T) {
	samples := []float32{0.1, 0.2}
	got := MixToMono(samples, 1)

	if &got[0] != &samples[0] {
		t.Error("单声道时应直接返回原切片")
	}
}

func TestClassifyExt(t *testing.T) {
	cases := []struct {
		path string
		want Kind
	}{
		{"a.wav", KindNative},
		{"a.MP3", KindNative},
		{"a.flac", KindNative},
		{"a.mp4", KindNeedsFFmpeg},
		{"a.mkv", KindNeedsFFmpeg},
		{"a.m4a", KindNeedsFFmpeg},
		{"a.txt", KindUnsupported},
		{"noext", KindUnsupported},
	}

	for _, c := range cases {
		if got := ClassifyExt(c.path); got != c.want {
			t.Errorf("ClassifyExt(%q) = %v, 预期 %v", c.path, got, c.want)
		}
	}
}
