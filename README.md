# qwen-asr

本地 Qwen3-ASR 语音转录命令行工具. 全程离线推理, 不上传任何音频.

## 特点

- **完全本地**: 推理走 llama.cpp 的 GGUF 权重, 音频不离开本机.
- **复用本机引擎**: 直接使用已安装的 llama.cpp, 不内置或自动下载引擎, 缺失时提示安装.
- **自己管模型**: 没有本地模型时交互引导下载; 装了 `modelscope` 或 `hf` 就用它们, 都没有则用内置的并发下载器, 支持断点续传.
- **格式覆盖**: wav / mp3 / flac 由内置解码器处理; 装了 ffmpeg 时视频与 m4a 等容器也能直接转录.
- **字幕输出**: 默认在输入文件旁生成同名 `.srt`, 也可同时产出 txt / srt / json.
- **转录进度**: 终端里显示原地刷新的进度条, 含完成段数, 百分比与耗时; 输出被重定向时退化为定期文本行.

## 安装

从 [Releases](https://github.com/azazo1/qwen-asr/releases) 下载对应平台的归档, 解压后把所在目录加入 `PATH`. Windows ARM64 的随附 DLL 需要与 exe 保持在同一目录.

Go 通过 yzma/libffi 调用本地引擎, 不使用 cgo. macOS 和 Windows x64 的 libffi 由依赖内置并自动释放到用户缓存, Windows ARM64 随包提供, Linux 需要系统提供 `libffi.so.8`. 用户缓存和数据目录需要可写.

也可以从源码构建:

```shell
just build
```

运行前需要安装支持 Qwen3-ASR 的 llama.cpp, 且包含 `llama`, `mtmd`, `ggml`, `ggml-base` 及计算后端动态库. macOS 可使用 `brew install llama.cpp`; Linux 和 Windows 请按 [llama.cpp 官方说明](https://github.com/ggml-org/llama.cpp) 安装或构建共享库. 仅有 `llama-cli` 可执行文件不足以运行本工具.

安装到非标准目录时, 使用 `--llama-lib <库目录>` 或 `QWEN_ASR_LLAMA_LIB` 指定路径. 可运行 `qwen-asr doctor --engine-only` 检查是否可用.

开发构建和发布构建均使用本机引擎. `just dist` 只生成 CLI 发布产物, 不构建或打包 llama.cpp.

## 快速开始

```shell
# 默认生成同目录的 录音.srt, 首次运行会提示选择模型档位并下载
qwen-asr -i 录音.mp3

# 指定模型与输出格式
qwen-asr -i 视频.mp4 --model 1.7b --format txt,srt -o ./字幕

# 批量处理目录
qwen-asr -i ./音频目录 -j 8

# 检查环境
qwen-asr doctor
```

## 模型

只提供 Q8_0 量化的两档, 兼顾体积与精度:

| 档位 | 体积 | 说明 |
| --- | --- | --- |
| `0.6b` | 约 1.0 GB | 默认档位, 体积小, 速度更快 |
| `1.7b` | 约 2.5 GB | 精度更高, 中文场景提升明显 |

模型来自 `ggml-org/Qwen3-ASR-*-GGUF`, HuggingFace 与 ModelScope 都有镜像. 默认优先 ModelScope,
可用 `QWEN_ASR_DOWNLOAD_SOURCE=hf` 切换.

## 常用选项

| 选项 | 说明 |
| --- | --- |
| `-i, --input` | 输入文件或目录 |
| `-o, --output` | 输出路径, 默认与输入同目录 |
| `--format` | `txt,srt,json` 逗号分隔, 默认 `srt`; 显式选择 `txt` 且不指定 `-o` 时打印到 stdout |
| `-j, --jobs` | 并发转录段数, 默认 4 |
| `--chunk-seconds` | 单段时长上限, 默认 60 秒 |
| `--model` | `0.6b` / `1.7b` / 本地 gguf 路径 |
| `--language` | 固定识别语言, 例如 `Chinese`, 默认自动识别 |
| `--data-dir` | 数据目录, 存放模型和临时文件 |
| `--llama-lib` | 指定 llama.cpp 库目录, 优先于系统自动探测 |
| `--vad` | Silero VAD 模型 `.onnx` 路径, 指定后按语音区间切分 |
| `--onnx-lib` | onnxruntime 动态库文件或目录 |
| `--vad-threshold` | VAD 语音概率阈值, 默认 0.5 |

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `QWEN_ASR_DATA_DIR` | 数据目录 |
| `QWEN_ASR_LLAMA_LIB` | llama.cpp 库目录 |
| `QWEN_ASR_DOWNLOAD_SOURCE` | `modelscope` 或 `hf` |
| `QWEN_ASR_LLAMA_LOG` | 设为非空时打开 llama.cpp 自身的日志, 便于排查推理问题 |
| `QWEN_ASR_VAD_MODEL` | Silero VAD 模型路径 |
| `QWEN_ASR_ONNX_LIB` | onnxruntime 动态库文件或目录 |

## 数据目录

默认位置:

- macOS: `~/Library/Application Support/qwen-asr`
- Linux: `$XDG_DATA_HOME/qwen-asr` 或 `~/.local/share/qwen-asr`
- Windows: `%LOCALAPPDATA%\qwen-asr`

其下 `models/` 存权重, `tmp/` 存转录中间文件.

## 推理引擎

显式指定的库目录优先, 加载失败时直接报错. 未指定时, 检查 `llama-server` 或 `llama-cli` 附近的库目录和各平台常见安装目录, 并实际加载符号进行检查.

没有可用引擎时立即退出并提示安装, 不会下载或释放 llama.cpp, 也不会进入模型下载流程.

## 语音检测 (VAD)

默认按音量阈值切分. 指定 [Silero VAD](https://github.com/snakers4/silero-vad) 模型后, 改用模型判断哪些区间有人声, 跳过静音部分, 减少无意义推理, 语音边界也更准确:

```shell
qwen-asr -i 录音.mp3 --vad ~/models/silero_vad.onnx --onnx-lib /opt/homebrew/lib
```

需要两样外部资源, 都不随本工具分发:

- **Silero VAD 的 ONNX 模型** (v5 或更新), 放哪里都可以, 用 `--vad` 指定; 也可以放在数据目录的 `models/silero_vad.onnx` 自动识别.
- **ONNX Runtime 1.28 动态库**, 通过 `--onnx-lib` 指定文件或目录; 未指定时探测系统常见目录.

推理走纯 Go 的 purego 绑定, 不需要 cgo. 显式给出 `--vad` 时任何加载失败都会直接报错, 不会悄悄退回能量分段. 自动探测到模型但缺 onnxruntime 时只警告并回退.

VAD 只决定"哪几段是语音", 字幕时间戳仍取自原音频, 不会因为跳过静音而偏移. 它不提供词级对齐.

实测例: 5 秒音频首尾各含 1 秒静音时, 不启用 VAD 得到 `00:00:00,000 --> 00:00:05,033`, 启用后得到 `00:00:01,056 --> 00:00:04,064`, 文本一致.

## 已知限制

- srt 的时间戳是分段边界, 不是词级对齐. Qwen3-ASR 本身不输出时间戳, 需要额外的对齐模型.
- 不装 ffmpeg 时无法处理 mp4 / mkv 等视频容器, 请先自行提取音轨.
- CPU/GPU 加速能力取决于本机安装的 llama.cpp 后端.

## 开发

```shell
just build      # 构建开发二进制
just test       # 运行测试
just lint       # 静态检查与格式检查
just doctor     # 检查本机环境
just dist       # 生成当前平台的 CLI 发布产物
```

## 许可

llama.cpp 遵循 MIT, 模型权重遵循 Apache-2.0. 本工具不分发 llama.cpp 库.
