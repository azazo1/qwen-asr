package vad

import (
	"context"
	"math"
	"os"
	"testing"
)

// 真实模型验证: 需要外部提供 onnxruntime 与 Silero 模型, 未设置时跳过.
//
// 这些文件体积大且属于第三方权重, 不适合提交到仓库, 所以只在本地手工验证.
// 用环境变量显式开启, 避免默认测试因为缺少动态库而失败.
func TestSileroRealModel(t *testing.T) {
	model := os.Getenv("QWEN_ASR_VAD_TEST_MODEL")
	lib := os.Getenv("QWEN_ASR_VAD_TEST_LIB")
	if model == "" || lib == "" {
		t.Skip("未设置 QWEN_ASR_VAD_TEST_MODEL / QWEN_ASR_VAD_TEST_LIB, 跳过真实模型验证")
	}

	detector, err := OpenSilero(model, lib, Options{})
	if err != nil {
		t.Fatalf("加载真实模型失败: %v", err)
	}
	defer detector.Close()

	// 造 3 秒音频: 0.2-0.8 秒和 1.6-2.4 秒是"语音"(正弦波), 其余静音.
	// 正弦波不是真实人声, 模型可能不判为语音, 所以这里只验证推理链路可用,
	// 即模型能跑完、输出合法概率、区间落在音频范围内.
	total := 3 * SampleRate
	samples := make([]float32, total)
	for i := 0; i < total; i++ {
		sec := float64(i) / float64(SampleRate)
		inSpeech := (sec >= 0.2 && sec < 0.8) || (sec >= 1.6 && sec < 2.4)
		if inSpeech {
			samples[i] = float32(0.3 * math.Sin(2*math.Pi*220*sec))
		}
	}

	ranges, err := detector.Detect(context.Background(), samples)
	if err != nil {
		t.Fatalf("推理失败: %v", err)
	}

	totalSec := float64(total) / float64(SampleRate)
	for _, r := range ranges {
		if r.Start < 0 || r.End > totalSec+1e-6 || r.End <= r.Start {
			t.Errorf("区间越界或非法: %+v (总时长 %v)", r, totalSec)
		}
	}
	t.Logf("检测到 %d 段语音: %+v", len(ranges), ranges)

	// 静音音频应当得到空结果, 说明模型不是恒定输出语音.
	silence := make([]float32, SampleRate)
	silentRanges, err := detector.Detect(context.Background(), silence)
	if err != nil {
		t.Fatalf("静音推理失败: %v", err)
	}
	if len(silentRanges) != 0 {
		t.Errorf("纯静音不应检测出语音, 得到 %+v", silentRanges)
	}
}

// 同一实例连续检测不同音频时, 状态必须被重置, 结果不能受上一段影响.
func TestSileroResetsStateBetweenCalls(t *testing.T) {
	model := os.Getenv("QWEN_ASR_VAD_TEST_MODEL")
	lib := os.Getenv("QWEN_ASR_VAD_TEST_LIB")
	if model == "" || lib == "" {
		t.Skip("未设置真实模型路径, 跳过")
	}

	detector, err := OpenSilero(model, lib, Options{})
	if err != nil {
		t.Fatalf("加载真实模型失败: %v", err)
	}
	defer detector.Close()

	ctx := context.Background()
	loud := make([]float32, SampleRate)
	for i := range loud {
		loud[i] = float32(0.4 * math.Sin(2*math.Pi*300*float64(i)/float64(SampleRate)))
	}
	silence := make([]float32, SampleRate)

	// 先跑一段有声音的, 再跑静音; 若状态没重置, 静音可能被误判为语音延续.
	if _, err := detector.Detect(ctx, loud); err != nil {
		t.Fatalf("第一次推理失败: %v", err)
	}
	got, err := detector.Detect(ctx, silence)
	if err != nil {
		t.Fatalf("第二次推理失败: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("状态未重置: 静音得到 %+v", got)
	}
}
