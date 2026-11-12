// Package logging 提供统一的日志初始化.
//
// 所有日志输出到 stderr, stdout 留给转录结果, 便于管道使用.
package logging

import (
	"io"
	"log/slog"
)

// New 构造文本格式的 logger.
//
// verbose 为 true 时输出 debug 级别, 否则只输出 info 及以上.
func New(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
	}))
}
