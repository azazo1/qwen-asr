// Command qwen-asr 是本地 Qwen3-ASR 语音转录工具.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/azazo1/qwen-asr/internal/cli"
)

func main() {
	// 收到中断信号时取消整个流程, 让下载与推理都能干净收尾.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
