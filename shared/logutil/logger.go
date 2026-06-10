// Package logutil provides a structured, leveled logger built on log/slog.
//
// Every service in the Reagent stack should construct its root logger through
// New or NewWithSanitizer so that:
//   - JSON output is used in production/staging.
//   - Text output is used in development/local (more readable).
//   - PII fields are automatically redacted in non-development environments.
//
// Usage:
//
//	log := logutil.New("service", "ingestion")
//	log.Info("email fetched", "message_id", msgID)
//	log.Error("publish failed", "error", err)
package logutil

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// Level helpers
// ---------------------------------------------------------------------------

// levelFromEnv reads LOG_LEVEL from the environment and returns the
// corresponding slog.Level. Defaults to Info when unset or unrecognised.
func levelFromEnv() slog.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ---------------------------------------------------------------------------
// Logger construction
// ---------------------------------------------------------------------------

// New returns a *slog.Logger pre-configured for the current environment.
//
// In development/local the handler emits human-readable text.
// In production/staging the handler emits newline-delimited JSON, suitable
// for ingestion by CloudWatch, Datadog, or similar log aggregators.
//
// attrs is an optional list of key-value pairs that will be added as
// permanent attributes to every log line (e.g. "service", "ingestion").
// The list must contain an even number of elements.
func New(attrs ...any) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     levelFromEnv(),
		AddSource: IsDevelopment(), // source file:line only in dev
	}

	var h slog.Handler
	if IsDevelopment() {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}

	log := slog.New(h)
	if len(attrs) > 0 {
		log = log.With(attrs...)
	}
	return log
}

// NewWithSanitizer returns a *slog.Logger that wraps each log record through
// the provided Sanitizer before emission. Use this when you need explicit
// control over a shared Sanitizer instance (e.g. for testing or custom
// redaction rules).
//
// In most cases New is sufficient — the Sanitizer helpers (RedactEmail,
// RedactSubject, etc.) should be called at the call site.
func NewWithSanitizer(s *Sanitizer, attrs ...any) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     levelFromEnv(),
		AddSource: IsDevelopment(),
	}

	var inner slog.Handler
	if IsDevelopment() {
		inner = slog.NewTextHandler(os.Stdout, opts)
	} else {
		inner = slog.NewJSONHandler(os.Stdout, opts)
	}

	h := &sanitizingHandler{inner: inner, s: s}
	log := slog.New(h)
	if len(attrs) > 0 {
		log = log.With(attrs...)
	}
	return log
}

// ---------------------------------------------------------------------------
// sanitizingHandler — slog.Handler adapter
// ---------------------------------------------------------------------------

// sanitizingHandler wraps an inner slog.Handler and redacts PII from each
// record's attributes before passing the record downstream.
type sanitizingHandler struct {
	inner slog.Handler
	s     *Sanitizer
	// pre-With attributes accumulated via WithAttrs
	preAttrs []slog.Attr
}

// Enabled delegates to the inner handler.
func (h *sanitizingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle redacts known PII keys in r's attributes, then delegates.
func (h *sanitizingHandler) Handle(ctx context.Context, r slog.Record) error {
	// Collect all attrs from the record.
	attrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, h.redactAttr(a))
		return true
	})

	// Rebuild a clean record with redacted attrs.
	clean := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	clean.AddAttrs(attrs...)
	return h.inner.Handle(ctx, clean)
}

// WithAttrs returns a new handler with the given attrs appended (and
// redacted).
func (h *sanitizingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = h.redactAttr(a)
	}
	return &sanitizingHandler{
		inner: h.inner.WithAttrs(redacted),
		s:     h.s,
	}
}

// WithGroup delegates to the inner handler.
func (h *sanitizingHandler) WithGroup(name string) slog.Handler {
	return &sanitizingHandler{
		inner: h.inner.WithGroup(name),
		s:     h.s,
	}
}

// redactAttr applies PII redaction to a single slog.Attr.
func (h *sanitizingHandler) redactAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() != slog.KindString {
		return a
	}
	val := a.Value.String()
	switch strings.ToLower(a.Key) {
	case "body_text", "body_html", "body", "content", "text":
		return slog.String(a.Key, h.s.RedactBody(val))
	case "subject":
		return slog.String(a.Key, h.s.RedactSubject(val))
	case "sender_email", "from", "sender", "to", "recipient_emails":
		return slog.String(a.Key, h.s.RedactEmail(val))
	case "attachment_s3_uris":
		return slog.String(a.Key, "[REDACTED:s3_paths]")
	case "instruction", "user_input", "transcription", "message":
		return slog.String(a.Key, h.s.RedactGeneric(val, 20))
	}
	return a
}

// ---------------------------------------------------------------------------
// Convenience: service-level constructors
// ---------------------------------------------------------------------------

// ForService is a shorthand for New("service", name). It mirrors the common
// pattern used across Reagent services.
//
//	log := logutil.ForService("ingestion")
func ForService(name string) *slog.Logger {
	return New("service", name)
}

// ForServiceWithSanitizer combines ForService and NewWithSanitizer.
func ForServiceWithSanitizer(name string, s *Sanitizer) *slog.Logger {
	return NewWithSanitizer(s, "service", name)
}
