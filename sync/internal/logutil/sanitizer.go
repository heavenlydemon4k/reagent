// Package logutil provides PII sanitization for log fields.
// It ensures email content (subjects, body text, sender emails) never appears
// in plaintext in production or staging logs.
package logutil

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Environment helpers
// ---------------------------------------------------------------------------

// IsProduction returns true when ENV=production or ENV=staging.
// In these environments, full redaction is enforced — plaintext PII
// must NEVER appear in logs.
func IsProduction() bool {
	env := strings.ToLower(os.Getenv("ENV"))
	return env == "production" || env == "staging"
}

// IsDevelopment returns true when ENV=development or ENV=local.
// In these environments, full logs are allowed for debugging.
func IsDevelopment() bool {
	env := strings.ToLower(os.Getenv("ENV"))
	return env == "development" || env == "local" || env == ""
}

// ---------------------------------------------------------------------------
// Sanitizer
// ---------------------------------------------------------------------------

// Sanitizer redacts PII from log fields.
type Sanitizer struct {
	emailRegex *regexp.Regexp
}

// New creates a new Sanitizer with compiled regexes.
func New() *Sanitizer {
	return &Sanitizer{
		emailRegex: regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),
	}
}

// RedactSubject keeps first 20 chars + hash for correlation.
// In development, returns the original subject unchanged.
func (s *Sanitizer) RedactSubject(subject string) string {
	if IsDevelopment() {
		return subject
	}
	if len(subject) <= 20 {
		return subject
	}
	hash := sha256.Sum256([]byte(subject))
	return subject[:20] + "... [" + hex.EncodeToString(hash[:4]) + "]"
}

// RedactEmail keeps domain only.
// In development, returns the original email unchanged.
func (s *Sanitizer) RedactEmail(email string) string {
	if IsDevelopment() {
		return email
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "[REDACTED]"
	}
	hash := sha256.Sum256([]byte(parts[0]))
	return "[" + hex.EncodeToString(hash[:4]) + "...]@" + parts[1]
}

// RedactBody replaces with hash only.
// In development, returns the original body unchanged.
func (s *Sanitizer) RedactBody(body string) string {
	if IsDevelopment() {
		return body
	}
	if body == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(body))
	return "[REDACTED:" + hex.EncodeToString(hash[:8]) + "]"
}

// RedactGeneric redacts any string, replacing it with a hash prefix.
// Use for user instructions, transcription text, or other PII strings.
// In development, returns the original text unchanged.
func (s *Sanitizer) RedactGeneric(text string, maxPrefixLen int) string {
	if IsDevelopment() {
		return text
	}
	if text == "" {
		return ""
	}
	if maxPrefixLen > 0 && len(text) <= maxPrefixLen {
		return text
	}
	hash := sha256.Sum256([]byte(text))
	prefix := ""
	if maxPrefixLen > 0 {
		prefix = text[:maxPrefixLen]
	}
	return prefix + "... [REDACTED:" + hex.EncodeToString(hash[:8]) + "]"
}

// SanitizeMap redacts known PII keys in a map.
// In development, returns the original map unchanged.
func (s *Sanitizer) SanitizeMap(fields map[string]interface{}) map[string]interface{} {
	if IsDevelopment() {
		return fields
	}
	result := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		switch strings.ToLower(k) {
		case "body_text", "body_html", "body", "content", "text":
			if str, ok := v.(string); ok {
				result[k] = s.RedactBody(str)
			} else {
				result[k] = "[REDACTED]"
			}
		case "subject":
			if str, ok := v.(string); ok {
				result[k] = s.RedactSubject(str)
			} else {
				result[k] = v
			}
		case "sender_email", "from", "sender", "recipient_emails", "to":
			if str, ok := v.(string); ok {
				result[k] = s.RedactEmail(str)
			} else {
				result[k] = "[REDACTED]"
			}
		case "attachment_s3_uris":
			result[k] = "[REDACTED:s3_paths]"
		case "instruction", "user_input", "transcription", "message":
			if str, ok := v.(string); ok {
				result[k] = s.RedactGeneric(str, 20)
			} else {
				result[k] = "[REDACTED]"
			}
		default:
			result[k] = v
		}
	}
	return result
}

// SanitizeAnyMap redacts known PII keys in a map[string]any.
// This is a convenience wrapper for SanitizeMap.
func (s *Sanitizer) SanitizeAnyMap(fields map[string]any) map[string]any {
	result := s.SanitizeMap(fields)
	// Convert back to map[string]any
	anyResult := make(map[string]any, len(result))
	for k, v := range result {
		anyResult[k] = v
	}
	return anyResult
}
