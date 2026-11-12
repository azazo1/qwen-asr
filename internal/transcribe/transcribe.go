// Package transcribe 负责把"输入文件"到"转录结果"的整个流程串起来.
package transcribe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/azazo1/qwen-asr/internal/audio"
	"github.com/azazo1/qwen-asr/internal/engine"
	"github.com/azazo1/qwen-asr/internal/subtitle"
)

// Options 控制一次转录的编排行为.
type Options struct {
	// Jobs 是同时进行的推理段数.
	Jobs int
	// ChunkSeconds 是单段时长上限.
	ChunkSeconds float64
	// Language 固定识别语言, 空表示自动.
	Language string
	// KeepTemp 为 true 时保留中间产生的 wav 文件.
	KeepTemp bool
	// TempDir 是中间文件目录.
	TempDir string
	// Lock 为 true 时只处理单个文件, 不做并发分段.
	Lock bool
	// Logger 是日志入口.
	Logger *slog.Logger
	// Toolchain 是本机可用的外部音频工具.
	Toolchain audio.Toolchain
	// Speech 是可选的语音检测器. 为 nil 时按能量阈值分段.
	Speech SpeechDetector
}

// SpeechDetector 检测音频中的语音区间, 时间相对传入样本的起点, 单位秒.
//
// 传入的样本固定为 audio.SampleRate, 实现方不必再做重采样.
type SpeechDetector interface {
	Detect(ctx context.Context, samples []float32) ([]audio.SpeechSpan, error)
}

// Segment 是一段转录结果.
type Segment struct {
	// Index 是段序号.
	Index int
	// Start 与 End 是相对原音频的起止秒数.
	Start float64
	End   float64
	// Text 是该段的文本.
	Text string
	// Failed 表示该段识别失败.
	Failed bool
}

// Result 是一个输入文件的完整转录结果.
type Result struct {
	// Input 是原始输入路径.
	Input string
	// Duration 是音频总时长, 单位秒.
	Duration float64
	// Segments 是按顺序排列的各段结果.
	Segments []Segment
	// Failed 是识别失败的段数.
	Failed int
}

// Text 把各段文本拼成一段完整文本.
func (r Result) Text() string { return subtitle.JoinSegments(r.toSubtitle()) }

// toSubtitle 把结果段转成输出层使用的结构.
func (r Result) toSubtitle() []subtitle.Segment {
	out := make([]subtitle.Segment, 0, len(r.Segments))
	for _, s := range r.Segments {
		out = append(out, subtitle.Segment{
			Index: s.Index, Start: s.Start, End: s.End, Text: s.Text, Failed: s.Failed,
		})
	}
	return out
}

// SubtitleSegments 返回可直接用于输出层的段列表.
func (r Result) SubtitleSegments() []subtitle.Segment { return r.toSubtitle() }

// HasFailure 表示是否有个别段识别失败.
func (r Result) HasFailure() bool { return r.Failed > 0 }

// Engine 是推理引擎需要满足的接口, 便于替换与测试.
type Engine interface {
	// SampleRate 返回模型要求的采样率.
	SampleRate() int
	// Transcribe 转录一段样本.
	Transcribe(ctx context.Context, samples []float32, opt engine.TranscribeOptions) (string, error)
}

// Transcriber 编排解码, 分段, 推理与结果收集.
type Transcriber struct {
	eng  Engine
	opts Options
	log  *slog.Logger
}

// New 构造一个编排器.
func New(eng Engine, opts Options) *Transcriber {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Jobs <= 0 {
		opts.Jobs = 4
	}
	if opts.ChunkSeconds <= 0 {
		opts.ChunkSeconds = 60
	}
	return &Transcriber{eng: eng, opts: opts, log: opts.Logger}
}

// Run 处理单个输入文件.
func (t *Transcriber) Run(ctx context.Context, input string) (Result, error) {
	start := time.Now()
	log := t.log.With("input", filepath.Base(input))

	result := Result{Input: input}

	log.Info("开始读取音频")
	samples, err := t.loadSamples(ctx, input, log)
	if err != nil {
		return result, err
	}

	rate := t.eng.SampleRate()
	if rate <= 0 {
		rate = audio.SampleRate
	}

	result.Duration = float64(len(samples)) / float64(rate)
	log.Debug("音频已载入", "samples", len(samples), "seconds", result.Duration, "rate", rate)

	clips, err := t.planClips(ctx, samples, rate, log)
	if err != nil {
		return result, err
	}
	log.Info("开始转录", "segments", len(clips), "duration", formatSeconds(result.Duration))

	result.Segments = t.transcribeClips(ctx, clips, log)
	for _, seg := range result.Segments {
		if seg.Failed {
			result.Failed++
		}
	}

	log.Info("转录完成",
		"segments", len(result.Segments),
		"failed", result.Failed,
		"elapsed", time.Since(start).Round(time.Millisecond))

	return result, nil
}

// planClips 决定音频如何切段: 启用语音检测时按检测到的区间切, 否则按能量阈值切.
func (t *Transcriber) planClips(ctx context.Context, samples []float32, rate int, log *slog.Logger) ([]audio.Clip, error) {
	if t.opts.Speech == nil {
		return audio.Segment(samples, rate, audio.SegmentOptions{MaxSeconds: t.opts.ChunkSeconds})
	}

	// VAD 模型固定要求 16 kHz, 模型采样率不是时就临时重采样一份.
	detectSamples := samples
	if rate != audio.SampleRate {
		detectSamples = audio.Resample(samples, rate, audio.SampleRate)
	}

	start := time.Now()
	spans, err := t.opts.Speech.Detect(ctx, detectSamples)
	if err != nil {
		return nil, fmt.Errorf("语音检测失败: %w", err)
	}
	log.Info("语音检测完成",
		"spans", len(spans),
		"elapsed", time.Since(start).Round(time.Millisecond))

	return audio.SegmentBySpeech(samples, rate, spans, t.opts.ChunkSeconds)
}

// loadSamples 把任意支持的输入读成目标采样率的单声道样本.
func (t *Transcriber) loadSamples(ctx context.Context, input string, log *slog.Logger) ([]float32, error) {
	rate := t.eng.SampleRate()
	if rate <= 0 {
		rate = audio.SampleRate
	}

	switch audio.ClassifyExt(input) {
	case audio.KindNative:
		start := time.Now()
		samples, err := audio.DecodeFileAs(input, rate)
		if err != nil {
			return nil, err
		}
		log.Debug("解码完成", "elapsed", time.Since(start).Round(time.Millisecond))
		return samples, nil

	case audio.KindNeedsFFmpeg:
		if !t.opts.Toolchain.HasFFmpeg() {
			return nil, fmt.Errorf(
				"处理 %s 需要 ffmpeg, 但当前环境未找到. 可以安装 ffmpeg, 或先用它把音轨提取成 wav/mp3/flac 再交给本工具",
				filepath.Ext(input))
		}

		wavPath, cleanup, err := t.extractToWav(ctx, input)
		if err != nil {
			return nil, err
		}
		if !t.opts.KeepTemp {
			defer cleanup()
		} else {
			log.Info("保留中间文件", "path", wavPath)
		}

		start := time.Now()
		samples, err := audio.DecodeFileAs(wavPath, rate)
		if err != nil {
			return nil, err
		}
		log.Debug("解码完成", "elapsed", time.Since(start).Round(time.Millisecond))
		return samples, nil

	default:
		return nil, fmt.Errorf("不支持的文件类型 %q", filepath.Ext(input))
	}
}

// extractToWav 用 ffmpeg 把输入转成临时 wav.
func (t *Transcriber) extractToWav(ctx context.Context, input string) (string, func(), error) {
	if err := os.MkdirAll(t.opts.TempDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("无法创建临时目录 %s: %w", t.opts.TempDir, err)
	}

	dir, err := os.MkdirTemp(t.opts.TempDir, "extract-*")
	if err != nil {
		return "", nil, fmt.Errorf("无法创建临时目录: %w", err)
	}
	cleanup := func() { os.RemoveAll(dir) }

	out := filepath.Join(dir, "audio.wav")
	if err := t.opts.Toolchain.ExtractAudio(ctx, input, out); err != nil {
		// 抽取失败时不必保留空的临时目录.
		cleanup()
		return "", nil, err
	}
	return out, cleanup, nil
}

// transcribeClips 并发转录各段, 结果按段序号归位.
func (t *Transcriber) transcribeClips(ctx context.Context, clips []audio.Clip, log *slog.Logger) []Segment {
	segments := make([]Segment, len(clips))
	for i, c := range clips {
		segments[i] = Segment{Index: c.Index, Start: c.Start, End: c.End}
	}

	var (
		wg   sync.WaitGroup
		sem  = make(chan struct{}, t.opts.Jobs)
		mu   sync.Mutex
		errs []error
	)

	start := time.Now()
	completed := 0
	// 在 mu 保护下更新, 并发任务按实际完成数报告进度.
	reportProgress := func() {
		completed++
		log.Info("转录进度",
			"completed", completed,
			"total", len(clips),
			"percent", fmt.Sprintf("%.1f%%", 100*float64(completed)/float64(len(clips))),
			"failed", len(errs),
			"elapsed", time.Since(start).Round(time.Millisecond))
	}

	for i, clip := range clips {
		wg.Add(1)
		go func(idx int, c audio.Clip) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				segments[idx] = Segment{Index: c.Index, Start: c.Start, End: c.End, Failed: true}
				mu.Unlock()
				return
			}

			text, err := t.transcribeWithRetry(ctx, c)
			if err != nil {
				log.Error("分段识别失败",
					"segment", c.Index,
					"start", formatSeconds(c.Start),
					"end", formatSeconds(c.End),
					"error", err)

				mu.Lock()
				segments[idx] = Segment{Index: c.Index, Start: c.Start, End: c.End, Failed: true}
				errs = append(errs, err)
				reportProgress()
				mu.Unlock()
				return
			}

			mu.Lock()
			segments[idx] = Segment{Index: c.Index, Start: c.Start, End: c.End, Text: text}
			reportProgress()
			mu.Unlock()
		}(i, clip)
	}

	wg.Wait()

	if len(errs) > 0 {
		log.Warn("部分分段未能识别", "count", len(errs))
	}
	return segments
}

// transcribeWithRetry 对单段做有限次重试.
func (t *Transcriber) transcribeWithRetry(ctx context.Context, clip audio.Clip) (string, error) {
	const attempts = 3

	var lastErr error
	for i := 0; i < attempts; i++ {
		text, err := t.eng.Transcribe(ctx, clip.Samples, engine.TranscribeOptions{Language: t.opts.Language})
		if err == nil {
			return text, nil
		}
		lastErr = err

		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return "", err
		}
		t.log.Debug("分段识别重试", "segment", clip.Index, "attempt", i+1, "error", err)
	}
	return "", lastErr
}

// formatSeconds 把秒数格式化成便于阅读的字符串.
func formatSeconds(v float64) string {
	if v < 60 {
		return fmt.Sprintf("%.1fs", v)
	}
	m := int(v) / 60
	s := v - float64(m*60)
	return fmt.Sprintf("%dm%.0fs", m, s)
}
