package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/hajimehoshi/go-mp3"
	"github.com/mewkiz/flac"
)

// DecodeFile 读取音频文件, 返回单声道样本与原始采样率.
//
// 支持 wav / mp3 / flac. 多声道会在返回前混成单声道,
// 但不会重采样, 由调用方决定目标采样率.
func DecodeFile(path string) ([]float32, int, error) {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".wav":
		return decodeWav(path)
	case ".mp3":
		return decodeMP3(path)
	case ".flac":
		return decodeFLAC(path)
	default:
		return nil, 0, fmt.Errorf("不支持的音频格式 %q, 只支持 wav / mp3 / flac", ext)
	}
}

// DecodeFileAs 读取音频并重采样到目标采样率.
func DecodeFileAs(path string, targetRate int) ([]float32, error) {
	samples, rate, err := DecodeFile(path)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, ErrEmptyAudio
	}
	return Resample(samples, rate, targetRate), nil
}

// wavFormat 描述 wav 头里的关键字段.
type wavFormat struct {
	format     uint16
	channels   uint16
	sampleRate uint32
	bitsPer    uint16
}

// decodeWav 自行解析 RIFF/WAVE, 支持 16 位 PCM 与 32 位浮点.
func decodeWav(path string) ([]float32, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("无法打开 wav 文件: %w", err)
	}
	defer f.Close()

	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, 0, fmt.Errorf("无法读取 wav 文件: %w", err)
	}
	if len(raw) < 12 {
		return nil, 0, errors.New("wav 文件过短, 缺少 RIFF 头")
	}
	if string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, 0, errors.New("不是合法的 wav 文件")
	}

	var (
		fmtChunk wavFormat
		data     []byte
		gotFmt   bool
	)

	off := 12
	for off+8 <= len(raw) {
		id := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		body := off + 8
		if size < 0 || body+size > len(raw) {
			// 截断的块直接忽略, 已经读到的 data 仍然可用.
			break
		}

		switch id {
		case "fmt ":
			if size < 16 {
				return nil, 0, errors.New("wav 的 fmt 块过短")
			}
			fmtChunk = wavFormat{
				format:     binary.LittleEndian.Uint16(raw[body : body+2]),
				channels:   binary.LittleEndian.Uint16(raw[body+2 : body+4]),
				sampleRate: binary.LittleEndian.Uint32(raw[body+4 : body+8]),
				bitsPer:    binary.LittleEndian.Uint16(raw[body+14 : body+16]),
			}
			gotFmt = true
		case "data":
			data = raw[body : body+size]
		}

		off = body + size
		// RIFF 块按偶数字节对齐.
		if size%2 == 1 {
			off++
		}
	}

	if !gotFmt {
		return nil, 0, errors.New("wav 文件缺少 fmt 块")
	}
	if data == nil {
		return nil, 0, errors.New("wav 文件缺少 data 块")
	}
	if fmtChunk.channels == 0 {
		return nil, 0, errors.New("wav 的声道数为 0")
	}

	samples, err := wavSamples(data, fmtChunk)
	if err != nil {
		return nil, 0, err
	}

	return MixToMono(samples, int(fmtChunk.channels)), int(fmtChunk.sampleRate), nil
}

// wavSamples 把 data 块按位深解释成浮点样本.
func wavSamples(data []byte, f wavFormat) ([]float32, error) {
	switch {
	case f.format == 1 && f.bitsPer == 16:
		n := len(data) / 2
		out := make([]float32, n)
		for i := 0; i < n; i++ {
			out[i] = float32(int16(binary.LittleEndian.Uint16(data[i*2:i*2+2]))) / 32768
		}
		return out, nil
	case f.format == 1 && f.bitsPer == 8:
		// 8 位 wav 是无符号的, 以 128 为中心.
		out := make([]float32, len(data))
		for i, b := range data {
			out[i] = (float32(b) - 128) / 128
		}
		return out, nil
	case f.format == 3 && f.bitsPer == 32:
		n := len(data) / 4
		out := make([]float32, n)
		for i := 0; i < n; i++ {
			bits := binary.LittleEndian.Uint32(data[i*4 : i*4+4])
			out[i] = math.Float32frombits(bits)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("不支持的 wav 编码: format=%d bits=%d", f.format, f.bitsPer)
	}
}

// decodeMP3 用纯 Go 库解码 mp3.
func decodeMP3(path string) ([]float32, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("无法打开 mp3 文件: %w", err)
	}
	defer f.Close()

	dec, err := mp3.NewDecoder(f)
	if err != nil {
		return nil, 0, fmt.Errorf("无法解析 mp3: %w", err)
	}

	raw, err := io.ReadAll(dec)
	if err != nil {
		return nil, 0, fmt.Errorf("无法解码 mp3: %w", err)
	}

	// go-mp3 固定把 mp3 解码成 16 位小端立体声交错样本.
	const mp3Channels = 2
	n := len(raw) / 2
	stereo := make([]float32, n)
	for i := 0; i < n; i++ {
		stereo[i] = float32(int16(binary.LittleEndian.Uint16(raw[i*2:i*2+2]))) / 32768
	}

	return MixToMono(stereo, mp3Channels), dec.SampleRate(), nil
}

// decodeFLAC 用纯 Go 库解码 flac.
func decodeFLAC(path string) ([]float32, int, error) {
	stream, err := flac.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("无法打开 flac 文件: %w", err)
	}
	defer stream.Close()

	info := stream.Info
	if info == nil {
		return nil, 0, errors.New("flac 文件缺少流信息")
	}
	channels := int(info.NChannels)
	if channels <= 0 {
		return nil, 0, errors.New("flac 的声道数为 0")
	}

	var samples []float32

	for {
		frame, err := stream.ParseNext()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("无法解码 flac: %w", err)
		}

		// flac 的样本是任意位深的整数, 按位深归一化.
		scale := float32(1)
		if info.BitsPerSample > 0 {
			scale = 1 / float32(int64(1)<<(info.BitsPerSample-1))
		}

		block := int(frame.BlockSize)
		for i := 0; i < block; i++ {
			for ch := 0; ch < channels; ch++ {
				if i >= len(frame.Subframes[ch].Samples) {
					break
				}
				samples = append(samples, float32(frame.Subframes[ch].Samples[i])*scale)
			}
		}
	}

	return MixToMono(samples, channels), int(info.SampleRate), nil
}
