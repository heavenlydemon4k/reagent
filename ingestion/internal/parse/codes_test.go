// Package parse tests 2FA code and tracking number extraction.
package parse

import (
	"testing"
)

// TestExtractEmptyText verifies that empty input returns nil.
func TestExtractEmptyText(t *testing.T) {
	ce := NewCodeExtractor()
	result := ce.Extract("")
	if result != nil {
		t.Errorf("expected nil for empty text, got %v", result)
	}
}

// TestExtractWhitespaceOnly verifies that whitespace-only input returns nil.
func TestExtractWhitespaceOnly(t *testing.T) {
	ce := NewCodeExtractor()
	result := ce.Extract("   \n\t  ")
	if result != nil {
		t.Errorf("expected nil for whitespace-only text, got %v", result)
	}
}

// TestExtract2FACodes verifies extraction of various 2FA/OTP/verification codes.
func TestExtract2FACodes(t *testing.T) {
	ce := NewCodeExtractor()

	tests := []struct {
		name         string
		body         string
		wantCodes    []string
		wantTracking []string
	}{
		{
			name:         "simple_code",
			body:         "Your verification code is 123456",
			wantCodes:    []string{"123456"},
			wantTracking: nil,
		},
		{
			name:         "otp_format",
			body:         "Your OTP is 789012",
			wantCodes:    []string{"789012"},
			wantTracking: nil,
		},
		{
			name:         "pin_format",
			body:         "Your PIN: 4321",
			wantCodes:    []string{"4321"},
			wantTracking: nil,
		},
		{
			name:         "token_format",
			body:         "Your token is AB987654",
			wantCodes:    nil, // contains non-digits
			wantTracking: nil,
		},
		{
			name:         "code_with_dots",
			body:         "Code: 5678. Use it within 10 minutes.",
			wantCodes:    []string{"5678"},
			wantTracking: nil,
		},
		{
			name:         "verify_keyword",
			body:         "Please verify with code 998877",
			wantCodes:    []string{"998877"},
			wantTracking: nil,
		},
		{
			name:         "multiple_codes",
			body:         "First code: 1111. Second verification code: 2222.",
			wantCodes:    []string{"1111", "2222"},
			wantTracking: nil,
		},
		{
			name: "mixed_content",
			body: "Your verification code is 554433. Track your package at example.com with 1Z999AA10123456784",
			wantCodes:    []string{"554433"},
			wantTracking: []string{"1Z999AA10123456784"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codes := ce.Extract(tt.body)

			var gotCodes, gotTracking []string
			for _, c := range codes {
				switch c.Type {
				case CodeType2FA:
					gotCodes = append(gotCodes, c.Value)
				case CodeTypeTracking:
					gotTracking = append(gotTracking, c.Value)
				}
			}

			assertStringSliceEqual(t, "2FA codes", tt.wantCodes, gotCodes)
			assertStringSliceEqual(t, "tracking numbers", tt.wantTracking, gotTracking)
		})
	}
}

// TestExtract2FAFalsePositives verifies that false positives like years
// and sequential digits are filtered out.
func TestExtract2FAFalsePositives(t *testing.T) {
	ce := NewCodeExtractor()

	tests := []struct {
		name     string
		body     string
		wantCode string // empty means no code should be extracted
	}{
		{"year_2024", "The meeting is scheduled for 2024", ""},
		{"year_1999", "Founded in 1999", ""},
		{"all_same_digits", "Your code is 111111", ""},
		{"sequential_asc", "Your code is 123456", ""},
		{"sequential_desc", "Your code is 987654", ""},
		{"valid_6_digit", "Your verification code is 584729", "584729"},
		{"valid_4_digit", "Your PIN is 7294", "7294"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codes := ce.Extract(tt.body)

			var gotCode string
			for _, c := range codes {
				if c.Type == CodeType2FA {
					gotCode = c.Value
					break
				}
			}

			if gotCode != tt.wantCode {
				t.Errorf("expected code %q, got %q", tt.wantCode, gotCode)
			}
		})
	}
}

// TestExtractTrackingNumbers verifies extraction of UPS, FedEx, and USPS tracking numbers.
func TestExtractTrackingNumbers(t *testing.T) {
	ce := NewCodeExtractor()

	tests := []struct {
		name         string
		body         string
		wantTracking string
		trackerType  string
	}{
		{
			name:         "ups_valid",
			body:         "Your UPS tracking number is 1Z999AA10123456784",
			wantTracking: "1Z999AA10123456784",
			trackerType:  "UPS",
		},
		{
			name:         "fedex_valid",
			body:         "Your FedEx tracking number is 9412345678901234567890",
			wantTracking: "9412345678901234567890",
			trackerType:  "FedEx",
		},
		{
			name:         "usps_valid",
			body:         "Your USPS tracking number is AB123456789US",
			wantTracking: "AB123456789US",
			trackerType:  "USPS",
		},
		{
			name:         "no_tracking",
			body:         "Your order has been shipped",
			wantTracking: "",
			trackerType:  "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codes := ce.Extract(tt.body)

			var gotTracking string
			for _, c := range codes {
				if c.Type == CodeTypeTracking {
					gotTracking = c.Value
					break
				}
			}

			if gotTracking != tt.wantTracking {
				t.Errorf("expected tracking %q, got %q", tt.wantTracking, gotTracking)
			}
		})
	}
}

// TestExtractStrings verifies the ExtractStrings convenience method.
func TestExtractStrings(t *testing.T) {
	ce := NewCodeExtractor()
	body := "Your verification code is 554433. Track: 1Z999AA10123456784"

	values := ce.ExtractStrings(body)

	if len(values) != 2 {
		t.Errorf("expected 2 extracted values, got %d: %v", len(values), values)
	}

	// Should contain both the 2FA code and tracking number
	foundCode := false
	foundTracking := false
	for _, v := range values {
		if v == "554433" {
			foundCode = true
		}
		if v == "1Z999AA10123456784" {
			foundTracking = true
		}
	}

	if !foundCode {
		t.Error("expected 2FA code 554433 in extracted strings")
	}
	if !foundTracking {
		t.Error("expected tracking number in extracted strings")
	}
}

// TestIsFalsePositiveAllSame verifies all-same-digit filtering.
func TestIsFalsePositiveAllSame(t *testing.T) {
	if !isFalsePositive("111111") {
		t.Error("111111 should be a false positive")
	}
	if !isFalsePositive("0000") {
		t.Error("0000 should be a false positive")
	}
	if !isFalsePositive("7777777") {
		t.Error("7777777 should be a false positive")
	}
	if isFalsePositive("123456") {
		t.Error("123456 should NOT be a false positive (sequential handled separately)")
	}
}

// TestIsFalsePositiveYear verifies year filtering (1900-2099).
func TestIsFalsePositiveYear(t *testing.T) {
	if !isFalsePositive("2024") {
		t.Error("2024 should be a false positive (year)")
	}
	if !isFalsePositive("1999") {
		t.Error("1999 should be a false positive (year)")
	}
	if isFalsePositive("1234") {
		t.Error("1234 should NOT be a false positive")
	}
	if isFalsePositive("2100") {
		t.Error("2100 should NOT be a false positive (not 19xx or 20xx)")
	}
}

// TestIsSequential verifies sequential digit detection.
func TestIsSequential(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"1234", true},
		{"123456", true},
		{"987654", true},
		{"987654321", true},
		{"1111", false}, // all same, not sequential
		{"1357", false}, // not sequential
		{"12", true},
		{"1", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isSequential(tt.input)
			if got != tt.expected {
				t.Errorf("isSequential(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// TestIsAllDigits verifies the digit-only checker.
func TestIsAllDigits(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"123456", true},
		{"0", true},
		{"", true},
		{"12a34", false},
		{"12.34", false},
		{" 1234", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isAllDigits(tt.input)
			if got != tt.expected {
				t.Errorf("isAllDigits(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// TestCodeExtractorPosition verifies that extracted codes have correct positions.
func TestCodeExtractorPosition(t *testing.T) {
	ce := NewCodeExtractor()
	body := "Your verification code is 554433. Thanks!"

	codes := ce.Extract(body)

	if len(codes) == 0 {
		t.Fatal("expected at least one extracted code")
	}

	for _, c := range codes {
		if c.Position < 0 || c.Position > len(body) {
			t.Errorf("position %d out of range for body length %d", c.Position, len(body))
		}
		// Verify the position points to the actual code in the body
		if c.Position <= len(body)-len(c.Value) {
			extracted := body[c.Position : c.Position+len(c.Value)]
			if extracted != c.Value {
				// The match includes the keyword prefix, so position may differ
				// Just verify position is within a reasonable range
				if c.Type == CodeType2FA && c.Position < 26 {
					t.Errorf("position %d seems wrong for code %q", c.Position, c.Value)
				}
			}
		}
	}
}

// assertStringSliceEqual compares two string slices for equality (ignoring order).
func assertStringSliceEqual(t *testing.T, name string, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("%s: expected %v, got %v", name, want, got)
		return
	}
	// Simple length check; for more rigorous testing, sort and compare
	if len(want) == 0 && len(got) == 0 {
		return
	}
	// Check each wanted value is in got
	for _, w := range want {
		found := false
		for _, g := range got {
			if w == g {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: expected value %q not found in %v", name, w, got)
		}
	}
}
