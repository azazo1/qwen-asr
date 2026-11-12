package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/azazo1/qwen-asr/internal/asrtext"
	"github.com/hybridgroup/yzma/pkg/llama"
	"github.com/hybridgroup/yzma/pkg/mtmd"
)

// 会话参数默认值.
const (
	// defaultNCtx 是上下文长度. 单段音频最多 60 秒, 音频 token 与输出合起来远小于此值.
	defaultNCtx = 8192
	// defaultNBatch 是批大小, 与 mtmd 的音频编码批次相关.
	defaultNBatch = 512
	// maxNewTokens 是单段最多生成的 token 数, 防止异常情况下无界生成.
	maxNewTokens = 2048
)

// Config 描述一次推理会话的配置.
type Config struct {
	// ModelPath 是主模型 gguf 路径.
	ModelPath string
	// ProjectorPath 是 mmproj gguf 路径.
	ProjectorPath string
	// Threads 是 CPU 线程数, 0 表示由 llama.cpp 自行决定.
	Threads int
	// Logger 用于记录推理过程的细节.
	Logger *slog.Logger
}

// Session 是一个已加载模型的推理会话.
//
// 同一个 Session 被多个 worker 共享, 内部用互斥锁串行化推理,
// 因为 llama.cpp 的 context 不是线程安全的.
type Session struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	model    llama.Model
	llamaCtx llama.Context
	mmCtx    mtmd.Context
	vocab    llama.Vocab
	sampler  llama.Sampler
	marker   string
	closed   bool
}

// Open 加载模型并返回一个可用的会话.
func Open(cfg Config) (*Session, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	model, err := llama.ModelLoadFromFile(cfg.ModelPath, llama.ModelDefaultParams())
	if err != nil {
		return nil, fmt.Errorf("加载主模型失败 (%s): %w", cfg.ModelPath, err)
	}

	ctxParams := llama.ContextDefaultParams()
	ctxParams.NCtx = defaultNCtx
	ctxParams.NBatch = defaultNBatch
	if cfg.Threads > 0 {
		ctxParams.NThreads = int32(cfg.Threads)
		ctxParams.NThreadsBatch = int32(cfg.Threads)
	}

	llamaCtx, err := llama.InitFromModel(model, ctxParams)
	if err != nil {
		llama.ModelFree(model)
		return nil, fmt.Errorf("初始化模型上下文失败: %w", err)
	}

	mmParams := mtmd.ContextParamsDefault()
	mmCtx, err := mtmd.InitFromFile(cfg.ProjectorPath, model, mmParams)
	if err != nil {
		llama.Free(llamaCtx)
		llama.ModelFree(model)
		return nil, fmt.Errorf("加载音频投影器失败 (%s): %w", cfg.ProjectorPath, err)
	}

	if !mtmd.SupportAudio(mmCtx) {
		mtmd.Free(mmCtx)
		llama.Free(llamaCtx)
		llama.ModelFree(model)
		return nil, errors.New("该模型不支持音频输入, 请确认使用的是 Qwen3-ASR 的模型与配套 mmproj")
	}

	sampler := newGreedySampler()

	return &Session{
		cfg:      cfg,
		log:      cfg.Logger,
		model:    model,
		llamaCtx: llamaCtx,
		mmCtx:    mmCtx,
		vocab:    llama.ModelGetVocab(model),
		sampler:  sampler,
		marker:   mtmd.DefaultMarker(),
	}, nil
}

// newGreedySampler 构造一个只含贪心采样的采样链.
//
// 转录需要可复现的结果, 因此不用温度采样.
func newGreedySampler() llama.Sampler {
	chain := llama.SamplerChainInit(llama.SamplerChainDefaultParams())
	if chain == 0 {
		return 0
	}
	greedy := llama.SamplerInitGreedy()
	if greedy == 0 {
		llama.SamplerFree(chain)
		return 0
	}
	llama.SamplerChainAdd(chain, greedy)
	return chain
}

// SampleRate 返回模型要求的音频采样率.
func (s *Session) SampleRate() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mmCtx == 0 {
		return 0
	}
	return mtmd.GetAudioSampleRate(s.mmCtx)
}

// Close 释放全部资源. 重复调用是安全的.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.sampler != 0 {
		llama.SamplerFree(s.sampler)
		s.sampler = 0
	}
	if s.mmCtx != 0 {
		mtmd.Free(s.mmCtx)
		s.mmCtx = 0
	}
	if s.llamaCtx != 0 {
		llama.Free(s.llamaCtx)
		s.llamaCtx = 0
	}
	if s.model != 0 {
		llama.ModelFree(s.model)
		s.model = 0
	}
	return nil
}

// TranscribeOptions 是单次转录的参数.
type TranscribeOptions struct {
	// Language 指定语言名 (如 Chinese). 为空时由模型自动识别.
	Language string
}

// Transcribe 把一段 PCM 样本转成文本.
//
// samples 必须是单声道浮点样本, 采样率等于 SampleRate().
// 返回的文本已经剥掉了模型自带的 "language X<asr_text>" 前缀.
func (s *Session) Transcribe(ctx context.Context, samples []float32, opt TranscribeOptions) (string, error) {
	if len(samples) == 0 {
		return "", errors.New("空音频")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return "", errors.New("会话已关闭")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	bitmap := mtmd.BitmapInitFromAudio(uint64(len(samples)), &samples[0])
	if bitmap == 0 {
		return "", errors.New("无法从样本构造音频输入")
	}
	defer mtmd.BitmapFree(bitmap)

	// 使用与模型卡一致的 chat 模板: 音频占位符由 marker 表示, 由 mtmd 替换成真实音频 token.
	prompt := s.buildPrompt(opt.Language)

	chunks := mtmd.InputChunksInit()
	defer mtmd.InputChunksFree(chunks)

	input := mtmd.NewInputText(prompt, false, true)
	if rc := mtmd.Tokenize(s.mmCtx, chunks, input, []mtmd.Bitmap{bitmap}); rc != 0 {
		return "", fmt.Errorf("音频编码失败, 返回码 %d", rc)
	}

	var nPos llama.Pos
	nBatch := llama.NBatch(s.llamaCtx)
	if rc := mtmd.HelperEvalChunks(s.mmCtx, s.llamaCtx, chunks, 0, 0, int32(nBatch), true, &nPos); rc != 0 {
		return "", fmt.Errorf("音频推理失败, 返回码 %d", rc)
	}

	raw, err := s.generate(ctx, nPos)
	if err != nil {
		return "", err
	}

	// 每段结束都清空 KV cache, 让各段互不影响, 也避免长音频累积占满上下文.
	s.clearMemory()

	return asrtext.StripLanguagePrefix(raw), nil
}

// buildPrompt 按 Qwen3-ASR 的 chat 模板拼出带音频占位符的 prompt.
func (s *Session) buildPrompt(language string) string {
	var b strings.Builder

	b.WriteString("<|im_start|>system\n")
	if language != "" {
		// 模型卡的约定: system 段放语言名可以固定识别语言.
		b.WriteString(language)
	}
	b.WriteString("<|im_end|>\n")

	b.WriteString("<|im_start|>user\n")
	b.WriteString(s.marker)
	b.WriteString("<|im_end|>\n")

	b.WriteString("<|im_start|>assistant\n")

	return b.String()
}

// generate 从当前位置贪心采样直到结束符或达到上限.
func (s *Session) generate(ctx context.Context, nPos llama.Pos) (string, error) {
	var out strings.Builder
	buf := make([]byte, 256)
	pos := nPos

	for i := 0; i < maxNewTokens; i++ {
		// 每若干个 token 检查一次取消, 让长时间推理可以被中断.
		if i%16 == 0 {
			if err := ctx.Err(); err != nil {
				return out.String(), err
			}
		}

		token := llama.SamplerSample(s.sampler, s.llamaCtx, -1)
		if llama.VocabIsEOG(s.vocab, token) {
			break
		}

		piece, nextBuf := tokenPiece(s.vocab, token, buf)
		buf = nextBuf
		out.WriteString(piece)

		batch := llama.BatchGetOne([]llama.Token{token})
		batch.Pos = &pos
		if _, err := llama.Decode(s.llamaCtx, batch); err != nil {
			return out.String(), fmt.Errorf("解码失败: %w", err)
		}
		pos++
	}

	return out.String(), nil
}

// tokenPiece 取 token 对应的文本片段, 缓冲区不够时自动扩容.
func tokenPiece(vocab llama.Vocab, token llama.Token, buf []byte) (string, []byte) {
	n := llama.TokenToPiece(vocab, token, buf, 0, true)
	if n < 0 {
		// 负值表示需要的长度.
		buf = make([]byte, -n)
		n = llama.TokenToPiece(vocab, token, buf, 0, true)
	}
	if n <= 0 || int(n) > len(buf) {
		return "", buf
	}
	return string(buf[:n]), buf
}

// clearMemory 清空 KV cache.
func (s *Session) clearMemory() {
	llama.Synchronize(s.llamaCtx)
	mem, err := llama.GetMemory(s.llamaCtx)
	if err != nil {
		s.log.Debug("无法获取 KV cache, 跳过清理", "error", err)
		return
	}
	llama.MemoryClear(mem, true)
}
