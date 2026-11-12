// Package audio 负责把输入文件解码成模型需要的单声道浮点样本, 并按静音切成若干段.
//
// 解码全部用纯 Go 完成, 不依赖 ffmpeg: wav 自行解析, mp3 与 flac 用第三方库.
// 视频容器仍然需要 ffmpeg, 由调用方先抽出音轨再交给本包.
package audio

import (
	"errors"
	"fmt"
	"math"
)

// SampleRate 是 Qwen3-ASR 要求的采样率.
const SampleRate = 16000

// Clip 是一段连续音频, 时间戳相对原始音频起点, 单位秒.
type Clip struct {
	// Index 是段序号, 从 0 开始, 保持原始时间顺序.
	Index int
	// Start 与 End 是该段在原始音频中的起止时间, 单位秒.
	Start float64
	End   float64
	// Samples 是该段的单声道样本.
	Samples []float32
}

// Duration 返回该段时长, 单位秒.
func (c Clip) Duration() float64 { return c.End - c.Start }

// ErrEmptyAudio 表示输入音频没有有效样本.
var ErrEmptyAudio = errors.New("音频没有任何样本")

// Resample 用线性插值把样本从 from 采样率转换到 to 采样率.
//
// 采样率相同时直接返回原切片, 不做拷贝.
func Resample(samples []float32, from, to int) []float32 {
	if from == to || len(samples) == 0 {
		return samples
	}
	if from <= 0 || to <= 0 {
		return samples
	}

	// 输出长度按比例缩放, 至少保留一个样本.
	outLen := int(math.Round(float64(len(samples)) * float64(to) / float64(from)))
	if outLen <= 0 {
		outLen = 1
	}

	out := make([]float32, outLen)
	ratio := float64(from) / float64(to)
	last := len(samples) - 1

	for i := 0; i < outLen; i++ {
		pos := float64(i) * ratio
		left := int(pos)
		if left >= last {
			// 末尾不足一个采样间隔时直接沿用最后一个样本, 避免越界.
			out[i] = samples[last]
			continue
		}
		frac := float32(pos - float64(left))
		out[i] = samples[left]*(1-frac) + samples[left+1]*frac
	}

	return out
}

// MixToMono 把多声道交错样本混合成单声道.
//
// channels 为 1 时直接返回原切片.
func MixToMono(samples []float32, channels int) []float32 {
	if channels <= 1 {
		return samples
	}

	outLen := len(samples) / channels
	out := make([]float32, outLen)
	scale := 1 / float32(channels)

	for i := 0; i < outLen; i++ {
		var sum float32
		for ch := 0; ch < channels; ch++ {
			sum += samples[i*channels+ch]
		}
		out[i] = sum * scale
	}

	return out
}

// frameEnergy 把样本按帧切分, 返回每帧的平均平方值.
func frameEnergy(samples []float32, frameLen int) []float64 {
	if frameLen <= 0 || len(samples) == 0 {
		return nil
	}

	n := len(samples) / frameLen
	energies := make([]float64, n)
	for i := 0; i < n; i++ {
		var sum float64
		base := i * frameLen
		for j := 0; j < frameLen; j++ {
			v := float64(samples[base+j])
			sum += v * v
		}
		energies[i] = sum / float64(frameLen)
	}
	return energies
}

// SegmentOptions 控制分段行为.
type SegmentOptions struct {
	// MaxSeconds 是单段时长上限, 超过就必须切开.
	MaxSeconds float64
	// FrameMS 是能量计算的帧长, 单位毫秒. 0 表示使用默认值 20.
	FrameMS int
	// SilenceDB 是静音判定阈值, 单位 dBFS. 0 表示使用默认值 -35.
	SilenceDB float64
	// MinSilenceSeconds 是可作为切点的最短静音时长, 0 表示默认 0.35 秒.
	MinSilenceSeconds float64
	// MinSegmentSeconds 是最短段时长, 更短的段会与相邻段合并, 0 表示默认 1.0 秒.
	MinSegmentSeconds float64
}

// 分段默认参数.
const (
	defaultFrameMS       = 20
	defaultSilenceDB     = -35.0
	defaultMinSilenceSec = 0.35
	defaultMinSegmentSec = 1.0
)

func (o SegmentOptions) withDefaults() SegmentOptions {
	if o.FrameMS <= 0 {
		o.FrameMS = defaultFrameMS
	}
	if o.SilenceDB == 0 {
		o.SilenceDB = defaultSilenceDB
	}
	if o.MinSilenceSeconds <= 0 {
		o.MinSilenceSeconds = defaultMinSilenceSec
	}
	if o.MinSegmentSeconds <= 0 {
		o.MinSegmentSeconds = defaultMinSegmentSec
	}
	if o.MaxSeconds <= 0 {
		o.MaxSeconds = 60
	}
	return o
}

// timeSpan 是一个时间区间, 单位秒.
type timeSpan struct{ start, end float64 }

// Segment 把样本切成语义上尽量完整的若干段.
//
// 策略: 先按能量找出静音区间, 再在"不超过 MaxSeconds"的前提下贪心地
// 把切点落在最靠近上限的静音中点; 找不到合适静音时按上限硬切.
// 这一步是纯函数, 便于单测.
func Segment(samples []float32, sampleRate int, opt SegmentOptions) ([]Clip, error) {
	if len(samples) == 0 {
		return nil, ErrEmptyAudio
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("采样率必须为正数, 收到 %d", sampleRate)
	}

	opt = opt.withDefaults()

	frameLen := sampleRate * opt.FrameMS / 1000
	if frameLen <= 0 {
		frameLen = 1
	}
	energies := frameEnergy(samples, frameLen)

	// 把静音区间换算成秒, 供后续贪心选点.
	threshold := math.Pow(10, opt.SilenceDB/10)
	frameSec := float64(opt.FrameMS) / 1000

	var silences []timeSpan
	runStart := -1
	for i, e := range energies {
		if e < threshold {
			if runStart < 0 {
				runStart = i
			}
			continue
		}
		if runStart >= 0 {
			appendSilence(&silences, runStart, i, frameSec, opt.MinSilenceSeconds)
			runStart = -1
		}
	}
	if runStart >= 0 {
		appendSilence(&silences, runStart, len(energies), frameSec, opt.MinSilenceSeconds)
	}

	totalSec := float64(len(samples)) / float64(sampleRate)
	clips := planClips(totalSec, silences, opt)

	// 把时间区间对应的样本切出来.
	for i := range clips {
		startSample := int(math.Round(clips[i].Start * float64(sampleRate)))
		endSample := int(math.Round(clips[i].End * float64(sampleRate)))
		if startSample < 0 {
			startSample = 0
		}
		if endSample > len(samples) {
			endSample = len(samples)
		}
		if endSample <= startSample {
			continue
		}
		clips[i].Index = i
		clips[i].Samples = samples[startSample:endSample]
	}

	// 丢掉空段, 并重新编号.
	out := clips[:0]
	for _, c := range clips {
		if len(c.Samples) > 0 {
			c.Index = len(out)
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil, ErrEmptyAudio
	}
	return out, nil
}

func appendSilence(dst *[]timeSpan, runStart, runEnd int, frameSec, minSilenceSec float64) {
	start := float64(runStart) * frameSec
	end := float64(runEnd) * frameSec
	if end-start >= minSilenceSec {
		*dst = append(*dst, timeSpan{start: start, end: end})
	}
}

// planClips 只根据总时长与静音区间规划分段边界, 不接触样本数据.
func planClips(totalSec float64, silences []timeSpan, opt SegmentOptions) []Clip {
	maxSec := opt.MaxSeconds
	var clips []Clip

	cur := 0.0
	for cur < totalSec-1e-9 {
		limit := cur + maxSec
		if limit >= totalSec {
			clips = append(clips, Clip{Start: cur, End: totalSec})
			break
		}

		// 在 (cur, limit] 里找中点最靠后的静音作为切点.
		cut := -1.0
		for _, s := range silences {
			mid := (s.start + s.end) / 2
			if mid <= cur {
				continue
			}
			if mid > limit {
				break
			}
			// 切点必须在静音区间内部, 否则会切到语音.
			if s.end <= mid || s.start >= mid {
				continue
			}
			cut = mid
		}

		if cut < 0 {
			// 没有可用静音, 按上限硬切.
			cut = limit
		}

		clips = append(clips, Clip{Start: cur, End: cut})
		cur = cut
	}

	// 合并过短的段, 避免产生大量碎片.
	if opt.MinSegmentSeconds > 0 {
		clips = mergeShortClips(clips, opt.MinSegmentSeconds, maxSec)
	}
	return clips
}

// mergeShortClips 把短于 minSec 的段并到相邻段里.
//
// 合并只在两段时长之和不超过 maxSec 时进行, 否则保留原样:
// 宁可留下一个短段, 也不产出超过模型上限的段.
func mergeShortClips(clips []Clip, minSec, maxSec float64) []Clip {
	if len(clips) <= 1 {
		return clips
	}

	out := make([]Clip, 0, len(clips))
	for _, c := range clips {
		if len(out) > 0 {
			last := &out[len(out)-1]
			if last.Duration() < minSec && last.Duration()+c.Duration() <= maxSec {
				last.End = c.End
				continue
			}
		}
		out = append(out, c)
	}

	// 末尾仍过短时, 尝试并回前一段.
	if len(out) >= 2 {
		last := out[len(out)-1]
		prev := &out[len(out)-2]
		if last.Duration() < minSec && prev.Duration()+last.Duration() <= maxSec {
			prev.End = last.End
			out = out[:len(out)-1]
		}
	}

	return out
}
