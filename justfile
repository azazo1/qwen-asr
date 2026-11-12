[private]
default:
    @just --list

# 构建二进制, 运行时使用本机已安装的 llama.cpp.
build:
    go build -buildvcs=false -o dist/qwen-asr ./cmd/qwen-asr

# 直接运行, 参数透传, 例如 just run -i a.mp4 --format srt.
run *args:
    go run ./cmd/qwen-asr {{args}}

# 运行全部测试.
test:
    go test ./... -count=1

# 使用 gopls 检查全部 Go 源文件, 包含 unusedfunc 等 hint 级别诊断.
check:
    find cmd internal -type f -name '*.go' -exec gopls check -severity=hint {} +

# 静态检查与格式检查 (只报告不修改).
lint:
    go vet ./...
    @echo "以下文件未格式化:"
    @gofmt -l .

# 格式化代码.
fmt:
    gofmt -w .

# 检查运行环境, 包括推理引擎, ffmpeg, 本地模型.
doctor:
    go run ./cmd/qwen-asr doctor

# 生成当前平台的 CLI 发布产物, 不附带 llama.cpp.
dist:
    uv run --no-project python scripts/dist.py
