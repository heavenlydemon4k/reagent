// Package logger provides structured logging using slog with configurable output.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	instance *slog.Logger
	once     sync.Once
)

// Init initializes the global structured logger.
// Call once at application startup.
func Init(level string, format string) {
	once.Do(func() {
		instance = newLogger(level, format)
	})
}

// L returns the global logger instance.
func L() *slog.Logger {
	if instance == nil {
		Init("info", "json")
	}
	return instance
}

// newLogger creates a configured slog.Logger.
func newLogger(level string, format string) *slog.Logger {
	var opts slog.HandlerOptions

	lvl := parseLevel(level)
	opts.Level = lvl

	if !isProduction() {
		opts.AddSource = true
	}

	var handler slog.Handler
	if strings.ToLower(format) == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, &opts)
	}

	return slog.New(handler)
}

// parseLevel converts a string level to slog.Level.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// isProduction checks if running in production mode.
func isProduction() bool {
	return strings.ToLower(os.Getenv("ENVIRONMENT")) == "production"
}

// Helper functions for common logging patterns.

// Debug logs a debug message.
func Debug(msg string, args ...any) {
	L().Debug(msg, args...)
}

// Info logs an info message.
func Info(msg string, args ...any) {
	L().Info(msg, args...)
}

// Warn logs a warning message.
func Warn(msg string, args ...any) {
	L().Warn(msg, args...)
}

// Error logs an error message.
func Error(msg string, args ...any) {
	L().Error(msg, args...)
}

// WithContext adds context values to the logger and returns a new context.
func WithContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// FromContext extracts a logger from context, falling back to the global logger.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return L()
}

type loggerKey struct{}

// RequestAttrs holds common request-scoped fields for structured logging.
type RequestAttrs struct {
	RequestID  string
	UserID     string
	DeviceID   string
	Method     string
	Path       string
	StatusCode int
	DurationMs int64
}

// WithRequest creates a logger with request-scoped fields.
func WithRequest(ctx context.Context, attrs RequestAttrs) *slog.Logger {
	logger := FromContext(ctx).With(
		"request_id", attrs.RequestID,
		"user_id", attrs.UserID,
		"device_id", attrs.DeviceID,
		"method", attrs.Method,
		"path", attrs.Path,
		"status_code", attrs.StatusCode,
		"duration_ms", attrs.DurationMs,
	)
	return logger
}
