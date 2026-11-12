package cli

import (
	"context"
	"errors"
	"log/slog"

	"github.com/azazo1/qwen-asr/internal/audio"
	"github.com/azazo1/qwen-asr/internal/config"
	"github.com/azazo1/qwen-asr/internal/transcribe"
	"github.com/azazo1/qwen-asr/internal/vad"
)

// sileroDetector 把 vad.Silero 适配成转录层需要的接口.
//
// 转录层只依赖自己的接口, 不直接引用 onnxruntime 相关代码.
type sileroDetector struct {
	model *vad.Silero
}

// Detect 返回语音区间.
func (d sileroDetector) Detect(ctx context.Context, samples []float32) ([]audio.SpeechSpan, error) {
	ranges, err := d.model.Detect(ctx, samples)
	if err != nil {
		return nil, err
	}
	out := make([]audio.SpeechSpan, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, audio.SpeechSpan{Start: r.Start, End: r.End})
	}
	return out, nil
}

// newSpeechDetector 按需创建 VAD 检测器.
//
// 显式指定 --vad 时, 任何加载失败都会中断运行: 用户既然点名要用 VAD,
// 就不该悄悄退回能量分段. 自动探测到的模型加载失败则只警告并回退,
// 免得因为缺少可选的 onnxruntime 让原本能用的转录跑不起来.
//
// 返回的清理函数可能为 nil, 表示没有需要释放的资源.
func newSpeechDetector(f *transcribeFlags, dirs config.Dirs, log *slog.Logger) (transcribe.SpeechDetector, func(), error) {
	explicit := f.vadModel != ""

	modelPath, err := vad.ResolveModel(f.vadModel, dirs.Root)
	if err != nil {
		if errors.Is(err, vad.ErrNotConfigured) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	model, err := vad.OpenSilero(modelPath, f.onnxLib, vad.Options{Threshold: f.vadThreshold})
	if err != nil {
		if explicit {
			return nil, nil, err
		}
		log.Warn("找到 VAD 模型但无法启用, 改用能量分段", "model", modelPath, "error", err)
		return nil, nil, nil
	}

	log.Info("语音检测已启用", "model", modelPath)
	return sileroDetector{model: model}, model.Close, nil
}
