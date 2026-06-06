// Package logger provides structured logging for the Ingestion Mesh.
// It wraps slog with context support and environment-aware formatting.
package logger

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/decisionstack/ingestion/internal/config"
)

// contextKey is used for storing logger in context.
type contextKey struct{}

var (
	globalLogger *Logger
	once         sync.Once
)

// Logger wraps Go's slog with context support and helper methods.
type Logger struct {
	handler Handler
	level   Level
	format  string // "json" or "text"
}

// Handler is the logging interface.
type Handler interface {
	Handle(level Level, msg string, args []any)
}

// Level represents the logging level.
type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

func levelFromString(s string) Level {
	switch s {
	case "debug":
		return DebugLevel
	case "warn":
		return WarnLevel
	case "error":
		return ErrorLevel
	default:
		return InfoLevel
	}
}

func (l Level) String() string {
	switch l {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// New creates a new Logger from configuration.
func New(cfg *config.Config) *Logger {
	level := levelFromString(cfg.LogLevel)
	format := cfg.LogFormat
	if format != "json" {
		format = "text"
	}

	var handler Handler
	switch format {
	case "json":
		handler = &jsonHandler{w: os.Stdout, level: level}
	default:
		handler = &textHandler{w: os.Stdout, level: level}
	}

	return &Logger{
		handler: handler,
		level:   level,
		format:  format,
	}
}

// Init initializes the global logger from config.
func Init(cfg *config.Config) {
	once.Do(func() {
		globalLogger = New(cfg)
	})
}

// L returns the global logger.
func L() *Logger {
	if globalLogger == nil {
		once.Do(func() {
			globalLogger = New(&config.Config{LogLevel: "info", LogFormat: "text", AppVersion: "dev"})
		})
	}
	return globalLogger
}

// WithContext returns the logger from context, or the global logger.
func WithContext(ctx context.Context) *Logger {
	if l, ok := ctx.Value(contextKey{}).(*Logger); ok {
		return l
	}
	return L()
}

// WithContext injects the logger into context.
func (l *Logger) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, l)
}

// With returns a new logger with additional key-value pairs.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{
		handler: &prefixHandler{base: l.handler, prefix: args},
		level:   l.level,
		format:  l.format,
	}
}

// Debug logs a debug message.
func (l *Logger) Debug(ctx context.Context, msg string, args ...any) {
	l.log(ctx, DebugLevel, msg, args)
}

// Info logs an info message.
func (l *Logger) Info(ctx context.Context, msg string, args ...any) {
	l.log(ctx, InfoLevel, msg, args)
}

// Warn logs a warning message.
func (l *Logger) Warn(ctx context.Context, msg string, args ...any) {
	l.log(ctx, WarnLevel, msg, args)
}

// Error logs an error message.
func (l *Logger) Error(ctx context.Context, msg string, args ...any) {
	l.log(ctx, ErrorLevel, msg, args)
}

func (l *Logger) log(ctx context.Context, level Level, msg string, args []any) {
	if level < l.level {
		return
	}
	if ctx != nil {
		if rid, ok := ctx.Value("request_id").(string); ok && rid != "" {
			args = append([]any{"request_id", rid}, args...)
		}
	}
	l.handler.Handle(level, msg, args)
}

// Debug is a package-level helper.
func Debug(ctx context.Context, msg string, args ...any) { L().Debug(ctx, msg, args...) }

// Info is a package-level helper.
func Info(ctx context.Context, msg string, args ...any) { L().Info(ctx, msg, args...) }

// Warn is a package-level helper.
func Warn(ctx context.Context, msg string, args ...any) { L().Warn(ctx, msg, args...) }

// Error is a package-level helper.
func Error(ctx context.Context, msg string, args ...any) { L().Error(ctx, msg, args...) }

// ============================================================================
// JSON Handler
// ============================================================================

type jsonHandler struct {
	w     io.Writer
	level Level
	mu    sync.Mutex
}

func (h *jsonHandler) Handle(level Level, msg string, args []any) {
	if level < h.level {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	pairs := make(map[string]interface{})
	pairs["time"] = now
	pairs["level"] = level.String()
	pairs["msg"] = msg

	for i := 0; i < len(args)-1; i += 2 {
		key := fmt.Sprint(args[i])
		val := args[i+1]
		pairs[key] = val
	}

	// Build JSON manually to avoid importing encoding/json here
	h.mu.Lock()
	defer h.mu.Unlock()

	fmt.Fprintf(h.w, "{")
	first := true
	for k, v := range pairs {
		if !first {
			fmt.Fprintf(h.w, ",")
		}
		first = false
		fmt.Fprintf(h.w, "\"%s\":", k)
		switch val := v.(type) {
		case string:
			fmt.Fprintf(h.w, "\"%s\"", escapeJSON(val))
		case int, int8, int16, int32, int64:
			fmt.Fprintf(h.w, "%d", val)
		case uint, uint8, uint16, uint32, uint64:
			fmt.Fprintf(h.w, "%d", val)
		case float32:
			fmt.Fprintf(h.w, "%g", val)
		case float64:
			fmt.Fprintf(h.w, "%g", val)
		case bool:
			fmt.Fprintf(h.w, "%t", val)
		default:
			fmt.Fprintf(h.w, "\"%s\"", escapeJSON(fmt.Sprint(val)))
		}
	}
	fmt.Fprintf(h.w, "}\n")
}

func escapeJSON(s string) string {
	var result string
	for _, r := range s {
		switch r {
		case '"':
			result += "\\\""
		case '\\':
			result += "\\\\"
		case '\n':
			result += "\\n"
		case '\r':
			result += "\\r"
		case '\t':
			result += "\\t"
		default:
			result += string(r)
		}
	}
	return result
}

// ============================================================================
// Text Handler
// ============================================================================

type textHandler struct {
	w     io.Writer
	level Level
	mu    sync.Mutex
}

func (h *textHandler) Handle(level Level, msg string, args []any) {
	if level < h.level {
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	h.mu.Lock()
	defer h.mu.Unlock()

	fmt.Fprintf(h.w, "%s %s %s", now, level.String(), msg)
	for i := 0; i < len(args)-1; i += 2 {
		fmt.Fprintf(h.w, " %s=%v", args[i], args[i+1])
	}
	fmt.Fprintf(h.w, "\n")
}

// ============================================================================
// Prefix Handler (for With)
// ============================================================================

type prefixHandler struct {
	base   Handler
	prefix []any
}

func (h *prefixHandler) Handle(level Level, msg string, args []any) {
	h.base.Handle(level, msg, append(h.prefix, args...))
}
