package logger

import (
	"context"
	"log/slog"
	"os"
)

type AppLogger struct {
	handler slog.Logger
}

func NewLogger(env string) *AppLogger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}
	if env == "production" {
		opts.Level = slog.LevelInfo
	}
	stdoutHandler := slog.NewJSONHandler(os.Stdout, opts)

	logger := slog.New(stdoutHandler)

	return &AppLogger{handler: *logger}
}

func (l *AppLogger) Info(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelInfo, msg, fields...)
}

func (l *AppLogger) Debug(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelDebug, msg, fields...)
}

func (l *AppLogger) Warn(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelWarn, msg, fields...)
}

func (l *AppLogger) Error(ctx context.Context, msg string, fields ...Field) {
	l.log(ctx, slog.LevelError, msg, fields...)
}

func (l *AppLogger) log(ctx context.Context, level slog.Level, msg string, fields ...Field) {
	attrs := make([]any, len(fields))
	for i, f := range fields {
		attrs[i] = slog.Any(f.Key, f.Value)
	}
	l.handler.Log(ctx, level, msg, attrs...)
}

// Logger returns the underlying slog.Logger for use with libraries that require it.
func (l *AppLogger) Logger() *slog.Logger {
	return &l.handler
}
