// Package parse transforms raw MIME email into structured ParsedEmail.
// This file extracts 2FA/OTP codes and tracking numbers from email body text.
// Extracted codes are returned as sidecar data — they are NEVER logged
// to protect user privacy.
package parse

import (
	"regexp"
	"strings"
)

// CodeType indicates the kind of extracted code.
type CodeType string

const (
	CodeType2FA      CodeType = "2fa"
	CodeTypeTracking CodeType = "tracking"
)

// ExtractedCode represents a single code or tracking number found in
// an email body, with its position for audit purposes.
type ExtractedCode struct {
	Type     CodeType `json:"type"`
	Value    string   `json:"value"`
	Position int      `json:"position"` // byte offset in the body text
}

// CodeExtractor finds time-sensitive codes and tracking numbers in
// email plain-text bodies. It is stateless and safe for concurrent use.
type CodeExtractor struct{}

// NewCodeExtractor creates a new CodeExtractor.
func NewCodeExtractor() *CodeExtractor {
	return &CodeExtractor{}
}

// compiled regex patterns (compiled once, reused across calls).
// These are package-level to avoid recompilation overhead.
var (
	// 2FA / OTP / verification codes: 4-8 digit numbers preceded by keywords.
	// The keyword-to-code gap allows up to 20 non-digit characters.
	// Examples: "Your code is 123456", "Verification: 1234", "OTP: 789012"
	twoFAPattern = regexp.MustCompile(`(?i)(?:code|verify|verification|otp|token|pin)[^\d]{0,20}(\d{4,8})`)

	// UPS tracking numbers: 1Z + 16 alphanumeric + 2 digits
	trackingUPSPattern = regexp.MustCompile(`\b(1Z[0-9A-Z]{16}\d{2})\b`)

	// FedEx tracking numbers: 94 + 20 digits
	trackingFedExPattern = regexp.MustCompile(`\b(94\d{20})\b`)

	// USPS tracking numbers: 2 uppercase letters + 9 digits + "US"
	trackingUSPSPattern = regexp.MustCompile(`\b([A-Z]{2}\d{9}US)\b`)
)

// Extract scans the body text for 2FA/OTP codes and tracking numbers.
// It returns all matches with their positions. No code values are ever logged.
func (ce *CodeExtractor) Extract(bodyText string) []ExtractedCode {
	if strings.TrimSpace(bodyText) == "" {
		return nil
	}

	var results []ExtractedCode

	// Extract 2FA/OTP codes.
	results = append(results, ce.extract2FA(bodyText)...)

	// Extract tracking numbers.
	results = append(results, ce.extractTracking(bodyText)...)

	return results
}

// ExtractStrings returns only the string values of extracted codes.
// This is a convenience method for the parser orchestrator.
func (ce *CodeExtractor) ExtractStrings(bodyText string) []string {
	codes := ce.Extract(bodyText)
	values := make([]string, 0, len(codes))
	for _, c := range codes {
		values = append(values, c.Value)
	}
	return values
}

// extract2FA finds 2FA/OTP/verification codes in the text.
// Pattern: keyword (code|verify|verification|otp|token|pin) within 20 chars of a 4-8 digit number.
func (ce *CodeExtractor) extract2FA(bodyText string) []ExtractedCode {
	var results []ExtractedCode

	matches := twoFAPattern.FindAllStringIndex(bodyText, -1)
	for _, m := range matches {
		if len(m) != 2 {
			continue
		}
		// Extract just the digit capture group.
		submatch := twoFAPattern.FindStringSubmatch(bodyText[m[0]:m[1]])
		if len(submatch) < 2 {
			continue
		}
		code := submatch[1]
		// Validate: must be 4-8 digits.
		if len(code) < 4 || len(code) > 8 {
			continue
		}
		// Exclude common false positives: years, repeated digits patterns.
		if isFalsePositive(code) {
			continue
		}
		results = append(results, ExtractedCode{
			Type:     CodeType2FA,
			Value:    code,
			Position: m[0],
		})
	}

	return results
}

// extractTracking finds shipping tracking numbers.
// Supports UPS (1Z...), FedEx (94...), and USPS (...US) formats.
func (ce *CodeExtractor) extractTracking(bodyText string) []ExtractedCode {
	var results []ExtractedCode

	// UPS: 1Z + 16 alphanum + 2 digits.
	upsMatches := trackingUPSPattern.FindAllStringIndex(bodyText, -1)
	for _, m := range upsMatches {
		if len(m) == 2 {
			results = append(results, ExtractedCode{
				Type:     CodeTypeTracking,
				Value:    bodyText[m[0]:m[1]],
				Position: m[0],
			})
		}
	}

	// FedEx: 94 + 20 digits.
	fedexMatches := trackingFedExPattern.FindAllStringIndex(bodyText, -1)
	for _, m := range fedexMatches {
		if len(m) == 2 {
			results = append(results, ExtractedCode{
				Type:     CodeTypeTracking,
				Value:    bodyText[m[0]:m[1]],
				Position: m[0],
			})
		}
	}

	// USPS: 2 uppercase + 9 digits + "US".
	uspsMatches := trackingUSPSPattern.FindAllStringIndex(bodyText, -1)
	for _, m := range uspsMatches {
		if len(m) == 2 {
			results = append(results, ExtractedCode{
				Type:     CodeTypeTracking,
				Value:    bodyText[m[0]:m[1]],
				Position: m[0],
			})
		}
	}

	return results
}

// isFalsePositive filters out digit sequences that are unlikely to be
// 2FA codes (e.g., years like 2024, phone number fragments).
func isFalsePositive(code string) bool {
	// All same digit (e.g., "111111", "0000").
	allSame := true
	first := code[0]
	for i := 1; i < len(code); i++ {
		if code[i] != first {
			allSame = false
			break
		}
	}
	if allSame {
		return true
	}

	// Looks like a year (1900-2099).
	if len(code) == 4 {
		// Simple year check: first two digits are 19 or 20.
		if (code[:2] == "19" || code[:2] == "20") && isAllDigits(code) {
			return true
		}
	}

	// Sequential digits (e.g., "123456", "987654").
	if isSequential(code) {
		return true
	}

	return false
}

// isAllDigits reports whether s contains only digit characters.
func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isSequential reports whether digits are in ascending or descending sequence.
func isSequential(s string) bool {
	if len(s) < 2 {
		return false
	}
	ascending := true
	descending := true

	for i := 1; i < len(s); i++ {
		prev := s[i-1]
		curr := s[i]
		if curr != prev+1 {
			ascending = false
		}
		if curr != prev-1 {
			descending = false
		}
	}

	return ascending || descending
}
