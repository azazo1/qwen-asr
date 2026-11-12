package vad

import (
	"context"
	"fmt"
	"slices"
	"sync"

	ort "github.com/getcharzp/onnxruntime_purego"
)

// Silero 模型约定的输入输出名.
const (
	inputName       = "input"
	stateInputName  = "state"
	sampleRateName  = "sr"
	outputName      = "output"
	stateOutputName = "stateN"
)

// Silero 是加载好的 Silero VAD 模型, 可重复用于多段音频.
//
// 内部保存 LSTM 状态与 64 点上下文, 因此状态是实例级的, 必须串行复用:
// 同一实例同时跑两个输入会互相污染状态. 这里用互斥锁兜住并发调用,
// 调用方不必自己保证串行.
//
// 另外注意 onnxruntime 的纯 Go 绑定内部维护一个全局引擎, 张量必须由该引擎创建.
// 本工具只在进程内创建一个 Silero 实例, 因此不会触发全局引擎被覆盖的问题;
// 若将来需要多实例, 应改为共享同一个引擎.
type Silero struct {
	engine  *ort.Engine
	session *ort.Session
	opts    Options

	mu sync.Mutex

	// state 是 LSTM 隐藏状态, 形状 [2, 1, 128].
	state []float32
	// context 是上一帧末尾的采样点, 与当前帧拼接后送进模型.
	context []float32
}

// OpenSilero 加载模型, 并检查模型输入输出是否符合预期.
//
// libraryPath 为空时自动探测系统 onnxruntime.
func OpenSilero(modelPath, libraryPath string, opts Options) (*Silero, error) {
	lib, err := ResolveLibrary(libraryPath)
	if err != nil {
		return nil, err
	}

	engine, err := ort.NewEngine(lib)
	if err != nil {
		return nil, fmt.Errorf("加载 onnxruntime 失败 (%s): %w. %s", lib, err, libraryHint)
	}

	sessionOpts, err := engine.NewSessionOptions()
	if err != nil {
		engine.Close()
		return nil, fmt.Errorf("创建 onnxruntime 会话选项失败: %w", err)
	}
	defer sessionOpts.Close()
	// 单线程足够: VAD 模型很小, 多线程只会为每帧增加调度开销.
	_ = sessionOpts.SetIntraOpNumThreads(1)

	session, err := engine.NewSession(modelPath, sessionOpts)
	if err != nil {
		engine.Close()
		return nil, fmt.Errorf("加载 VAD 模型失败 (%s): %w. %s", modelPath, err, modelHint)
	}

	silero := &Silero{
		engine:  engine,
		session: session,
		opts:    opts.withDefaults(),
	}
	if err := silero.validate(); err != nil {
		silero.Close()
		return nil, err
	}
	silero.reset()
	return silero, nil
}

// validate 检查模型是否是可用的 Silero VAD ONNX.
func (s *Silero) validate() error {
	inputs := make([]string, 0, len(s.session.Inputs))
	for _, in := range s.session.Inputs {
		inputs = append(inputs, in.Name)
	}
	for _, name := range []string{inputName, stateInputName, sampleRateName} {
		if !slices.Contains(inputs, name) {
			return fmt.Errorf("VAD 模型缺少输入 %q, 实际输入为 %v; 请使用 Silero VAD 的 ONNX 模型 (v5 或更新). %s",
				name, inputs, modelHint)
		}
	}

	outputs := make([]string, 0, len(s.session.Outputs))
	for _, out := range s.session.Outputs {
		outputs = append(outputs, out.Name)
	}
	for _, name := range []string{outputName, stateOutputName} {
		if !slices.Contains(outputs, name) {
			return fmt.Errorf("VAD 模型缺少输出 %q, 实际输出为 %v; 请使用 Silero VAD 的 ONNX 模型 (v5 或更新). %s",
				name, outputs, modelHint)
		}
	}
	return nil
}

// reset 清空跨帧状态, 让下一次 Detect 从头开始.
func (s *Silero) reset() {
	s.state = make([]float32, 2*StateSize)
	s.context = make([]float32, ContextSamples)
}

// Close 释放模型与运行时资源. 重复调用是安全的.
func (s *Silero) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.session != nil {
		s.session.Close()
		s.session = nil
	}
	if s.engine != nil {
		s.engine.Close()
		s.engine = nil
	}
}

// Detect 返回 samples 中的语音区间, 时间相对 samples 起点.
//
// samples 必须是 16 kHz 单声道. 长音频会逐帧推理, 可用 ctx 中途取消.
func (s *Silero) Detect(ctx context.Context, samples []float32) ([]Range, error) {
	if len(samples) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 每次检测都从头开始, 避免上一段音频的状态影响这一段.
	s.reset()

	frames := FrameCount(len(samples))
	probs := make([]float32, 0, frames)

	for i := 0; i < frames; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		chunk := make([]float32, WindowSamples)
		start := i * WindowSamples
		if start < len(samples) {
			copy(chunk, samples[start:min(start+WindowSamples, len(samples))])
		}

		prob, err := s.runFrame(chunk)
		if err != nil {
			return nil, fmt.Errorf("VAD 第 %d 帧推理失败: %w", i, err)
		}
		probs = append(probs, prob)
	}

	return RangesFromProbs(probs, len(samples), SampleRate, s.opts), nil
}

// runFrame 推理一帧并更新跨帧状态, 返回该帧的语音概率.
func (s *Silero) runFrame(chunk []float32) (float32, error) {
	// 模型输入是 64 点历史上下文加当前 512 点.
	input := make([]float32, ContextSamples+WindowSamples)
	copy(input, s.context)
	copy(input[ContextSamples:], chunk)

	inputValue, err := ort.NewTensor([]int64{1, int64(len(input))}, input)
	if err != nil {
		return 0, fmt.Errorf("创建输入张量失败: %w", err)
	}
	defer inputValue.Close()

	stateValue, err := ort.NewTensor([]int64{2, 1, StateSize}, s.state)
	if err != nil {
		return 0, fmt.Errorf("创建状态张量失败: %w", err)
	}
	defer stateValue.Close()

	rateValue, err := ort.NewTensor([]int64{}, []int64{SampleRate})
	if err != nil {
		return 0, fmt.Errorf("创建采样率张量失败: %w", err)
	}
	defer rateValue.Close()

	outputs, err := s.session.Run(map[string]*ort.Value{
		inputName:      inputValue,
		stateInputName: stateValue,
		sampleRateName: rateValue,
	})
	if err != nil {
		return 0, err
	}
	defer func() {
		for _, value := range outputs {
			value.Close()
		}
	}()

	probData, err := ort.GetTensorData[float32](outputs[outputName])
	if err != nil {
		return 0, fmt.Errorf("读取 %s 输出失败: %w", outputName, err)
	}
	if len(probData) == 0 {
		return 0, fmt.Errorf("模型输出 %s 为空", outputName)
	}
	prob := probData[0]

	newState, err := ort.GetTensorData[float32](outputs[stateOutputName])
	if err != nil {
		return 0, fmt.Errorf("读取 %s 输出失败: %w", stateOutputName, err)
	}
	if len(newState) != len(s.state) {
		return 0, fmt.Errorf("模型输出 %s 长度为 %d, 预期 %d", stateOutputName, len(newState), len(s.state))
	}

	// 必须在输出 Value 关闭之前拷贝走数据.
	copy(s.state, newState)
	copy(s.context, input[len(input)-ContextSamples:])

	return prob, nil
}
