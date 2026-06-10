package logutil

import (
	"os"
	"strings"
	"testing"
)

func TestIsProduction(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		expected bool
	}{
		{"production", "production", true},
		{"staging", "staging", true},
		{"development", "development", false},
		{"local", "local", false},
		{"empty", "", false},
		{"Production uppercase", "Production", true},
		{"STAGING uppercase", "STAGING", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("ENV", tt.env)
			defer os.Unsetenv("ENV")
			if got := IsProduction(); got != tt.expected {
				t.Errorf("IsProduction() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRedactSubject(t *testing.T) {
	// Force production mode
	os.Setenv("ENV", "production")
	defer os.Unsetenv("ENV")

	s := NewSanitizer()

	t.Run("short subject unchanged", func(t *testing.T) {
		subject := "Short subj"
		got := s.RedactSubject(subject)
		if got != subject {
			t.Errorf("RedactSubject(%q) = %q, want %q", subject, got, subject)
		}
	})

	t.Run("long subject redacted", func(t *testing.T) {
		subject := "This is a very long email subject line that exceeds twenty characters"
		got := s.RedactSubject(subject)
		if !strings.HasPrefix(got, "This is a very long ") {
			t.Errorf("RedactSubject(%q) = %q, expected 20-char prefix", subject, got)
		}
		if !strings.Contains(got, "... [") {
			t.Errorf("RedactSubject(%q) = %q, expected '... [' suffix", subject, got)
		}
	})

	t.Run("exactly 20 chars", func(t *testing.T) {
		subject := "Exactly twenty chars"
		got := s.RedactSubject(subject)
		if got != subject {
			t.Errorf("RedactSubject(%q) = %q, want %q (20 chars should pass)", subject, got, subject)
		}
	})

	t.Run("development passes through", func(t *testing.T) {
		os.Setenv("ENV", "development")
		defer os.Setenv("ENV", "production")
		subject := "This is a very long email subject line that exceeds twenty characters"
		got := s.RedactSubject(subject)
		if got != subject {
			t.Errorf("development mode: RedactSubject(%q) = %q, want %q", subject, got, subject)
		}
	})
}

func TestRedactEmail(t *testing.T) {
	// Force production mode
	os.Setenv("ENV", "production")
	defer os.Unsetenv("ENV")

	s := NewSanitizer()

	t.Run("valid email redacted", func(t *testing.T) {
		email := "john.doe@example.com"
		got := s.RedactEmail(email)
		if strings.Contains(got, "john.doe") {
			t.Errorf("RedactEmail(%q) = %q, should not contain local part", email, got)
		}
		if !strings.HasSuffix(got, "@example.com") {
			t.Errorf("RedactEmail(%q) = %q, should preserve domain", email, got)
		}
		if !strings.HasPrefix(got, "[") {
			t.Errorf("RedactEmail(%q) = %q, should have hash prefix", email, got)
		}
	})

	t.Run("invalid email", func(t *testing.T) {
		email := "not-an-email"
		got := s.RedactEmail(email)
		if got != "[REDACTED]" {
			t.Errorf("RedactEmail(%q) = %q, want [REDACTED]", email, got)
		}
	})

	t.Run("development passes through", func(t *testing.T) {
		os.Setenv("ENV", "development")
		defer os.Setenv("ENV", "production")
		email := "john.doe@example.com"
		got := s.RedactEmail(email)
		if got != email {
			t.Errorf("development mode: RedactEmail(%q) = %q, want %q", email, got, email)
		}
	})
}

func TestRedactBody(t *testing.T) {
	// Force production mode
	os.Setenv("ENV", "production")
	defer os.Unsetenv("ENV")

	s := NewSanitizer()

	t.Run("non-empty body redacted", func(t *testing.T) {
		body := "This is the body of an email with sensitive content."
		got := s.RedactBody(body)
		if !strings.HasPrefix(got, "[REDACTED:") {
			t.Errorf("RedactBody(%q) = %q, expected [REDACTED:...] prefix", body, got)
		}
		if strings.Contains(got, "sensitive content") {
			t.Errorf("RedactBody(%q) = %q, should not contain original text", body, got)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		got := s.RedactBody("")
		if got != "" {
			t.Errorf("RedactBody(\"\") = %q, want empty string", got)
		}
	})

	t.Run("development passes through", func(t *testing.T) {
		os.Setenv("ENV", "development")
		defer os.Setenv("ENV", "production")
		body := "This is the body of an email with sensitive content."
		got := s.RedactBody(body)
		if got != body {
			t.Errorf("development mode: RedactBody(%q) = %q, want %q", body, got, body)
		}
	})
}

func TestSanitizeMap(t *testing.T) {
	// Force production mode
	os.Setenv("ENV", "production")
	defer os.Unsetenv("ENV")

	s := NewSanitizer()

	t.Run("body_text redacted", func(t *testing.T) {
		fields := map[string]interface{}{
			"body_text": "sensitive email body content here",
			"other_key": "safe value",
		}
		got := s.SanitizeMap(fields)
		bodyText := got["body_text"].(string)
		if !strings.HasPrefix(bodyText, "[REDACTED:") {
			t.Errorf("SanitizeMap body_text = %q, expected [REDACTED:...]", bodyText)
		}
		if got["other_key"] != "safe value" {
			t.Errorf("SanitizeMap other_key was modified")
		}
	})

	t.Run("subject redacted", func(t *testing.T) {
		fields := map[string]interface{}{
			"subject": "This is a very long email subject that should be truncated",
		}
		got := s.SanitizeMap(fields)
		subject := got["subject"].(string)
		if !strings.Contains(subject, "... [") {
			t.Errorf("SanitizeMap subject = %q, expected truncation with hash", subject)
		}
	})

	t.Run("sender_email redacted", func(t *testing.T) {
		fields := map[string]interface{}{
			"sender_email": "alice@company.com",
		}
		got := s.SanitizeMap(fields)
		email := got["sender_email"].(string)
		if strings.Contains(email, "alice") {
			t.Errorf("SanitizeMap sender_email = %q, local part should be redacted", email)
		}
		if !strings.HasSuffix(email, "@company.com") {
			t.Errorf("SanitizeMap sender_email = %q, domain should be preserved", email)
		}
	})

	t.Run("instruction redacted", func(t *testing.T) {
		fields := map[string]interface{}{
			"instruction": "Please reply saying I accept the offer of $100k salary",
		}
		got := s.SanitizeMap(fields)
		inst := got["instruction"].(string)
		if !strings.Contains(inst, "REDACTED") {
			t.Errorf("SanitizeMap instruction = %q, expected REDACTED", inst)
		}
	})

	t.Run("development passes through", func(t *testing.T) {
		os.Setenv("ENV", "development")
		defer os.Setenv("ENV", "production")
		fields := map[string]interface{}{
			"body_text":    "sensitive content",
			"subject":      "long subject that would be truncated",
			"sender_email": "alice@company.com",
		}
		got := s.SanitizeMap(fields)
		if got["body_text"] != "sensitive content" {
			t.Errorf("development mode: body_text was modified")
		}
		if got["subject"] != "long subject that would be truncated" {
			t.Errorf("development mode: subject was modified")
		}
		if got["sender_email"] != "alice@company.com" {
			t.Errorf("development mode: sender_email was modified")
		}
	})
}

func TestRedactGeneric(t *testing.T) {
	os.Setenv("ENV", "production")
	defer os.Unsetenv("ENV")

	s := NewSanitizer()

	t.Run("generic text redacted", func(t *testing.T) {
		text := "This is a user instruction that should be redacted for privacy"
		got := s.RedactGeneric(text, 10)
		if !strings.HasPrefix(got, "This is a ") {
			t.Errorf("RedactGeneric(%q) = %q, expected 10-char prefix", text, got)
		}
		if !strings.Contains(got, "[REDACTED:") {
			t.Errorf("RedactGeneric(%q) = %q, expected [REDACTED:...]", text, got)
		}
	})

	t.Run("short text unchanged", func(t *testing.T) {
		text := "short"
		got := s.RedactGeneric(text, 10)
		if got != text {
			t.Errorf("RedactGeneric(%q) = %q, want %q", text, got, text)
		}
	})

	t.Run("empty text", func(t *testing.T) {
		got := s.RedactGeneric("", 10)
		if got != "" {
			t.Errorf("RedactGeneric(\"\") = %q, want empty", got)
		}
	})
}
