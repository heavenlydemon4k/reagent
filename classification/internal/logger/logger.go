// Package logger provides a thin slog wrapper with level configuration.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"
)

// Logger wraps slog.Logger with typed helper methods.
type Logger struct {
	*slog.Logger
}

// New creates a configured Logger.
func New(levelStr, format string) *Logger {
	var level slog.Level
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}

	if format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return &Logger{Logger: slog.New(handler)}
}

// With returns a Logger with the given key-value pairs attached.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{Logger: l.Logger.With(args...)}
}

// WithError returns a Logger with error attached.
func (l *Logger) WithError(err error) *Logger {
	return l.With("error", err.Error())
}

// WithComponent returns a Logger with a component name attached.
func (l *Logger) WithComponent(name string) *Logger {
	return l.With("component", name)
}

// WithRequestID returns a Logger with a request ID attached.
func (l *Logger) WithRequestID(id string) *Logger {
	return l.With("request_id", id)
}

// Fatal logs at error level and exits.
func (l *Logger) Fatal(msg string, args ...any) {
	l.Error(msg, args...)
	os.Exit(1)
}

// Nop returns a no-op Logger for testing.
func Nop() *Logger {
	return &Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// ctxKey is an unexported type for context keys.
type ctxKey struct{}

var loggerKey ctxKey

// WithContext stores a Logger in context.
func WithContext(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext retrieves a Logger from context. Falls back to a default INFO logger.
func FromContext(ctx context.Context) *Logger {
	if l, ok := ctx.Value(loggerKey).(*Logger); ok {
		return l
	}
	return New("info", "json")
}

// Structured helpers for consistent log shape.

func (l *Logger) Infow(msg string, keyvals ...any) {
	l.Info(msg, keyvals...)
}

func (l *Logger) Warnw(msg string, keyvals ...any) {
	l.Warn(msg, keyvals...)
}

func (l *Logger) Errorw(msg string, keyvals ...any) {
	l.Error(msg, keyvals...)
}

func (l *Logger) Debugw(msg string, keyvals ...any) {
	l.Debug(msg, keyvals...)
}

// Timer returns a function that logs duration when called.
func (l *Logger) Timer(operation string) func() {
	start := time.Now()
	return func() {
		l.Debug("operation completed", "operation", operation, "duration_ms", time.Since(start).Milliseconds())
	}
}
