// Package thread tests fuzzy subject matching and Levenshtein distance.
package thread

import (
	"testing"
)

// TestLevenshteinDistanceEmptyStrings verifies behavior with empty strings.
func TestLevenshteinDistanceEmptyStrings(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected int
	}{
		{"both_empty", "", "", 0},
		{"first_empty", "", "hello", 5},
		{"second_empty", "hello", "", 5},
		{"first_empty_unicode", "", "héllo", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LevenshteinDistance(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("LevenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

// TestLevenshteinDistanceIdentical verifies that identical strings have distance 0.
func TestLevenshteinDistanceIdentical(t *testing.T) {
	tests := []string{
		"hello",
		"Héllo Wörld",
		"你好世界",
		"",
		"The quick brown fox jumps over the lazy dog",
	}

	for _, s := range tests {
		t.Run(s, func(t *testing.T) {
			got := LevenshteinDistance(s, s)
			if got != 0 {
				t.Errorf("LevenshteinDistance(%q, %q) = %d, want 0", s, s, got)
			}
		})
	}
}

// TestLevenshteinDistanceKnown verifies known edit distances.
func TestLevenshteinDistanceKnown(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected int
	}{
		{"kitten_to_sitting", "kitten", "sitting", 3},
		{"saturday_to_sunday", "saturday", "sunday", 3},
		{"book_to_back", "book", "back", 2},
		{"one_char_insert", "cat", "cats", 1},
		{"one_char_delete", "cats", "cat", 1},
		{"one_char_substitute", "cat", "cut", 1},
		{"completely_different", "abc", "xyz", 3},
		{"prefix", "hello", "helo", 1},
		{"unicode", "café", "cafe", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LevenshteinDistance(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("LevenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

// TestLevenshteinDistanceSymmetric verifies that distance is symmetric.
func TestLevenshteinDistanceSymmetric(t *testing.T) {
	pairs := []struct{ a, b string }{
		{"kitten", "sitting"},
		{"hello world", "hElLo WoRlD"},
		{"你好", "你们好"},
		{"abcdef", "azced"},
	}

	for _, p := range pairs {
		d1 := LevenshteinDistance(p.a, p.b)
		d2 := LevenshteinDistance(p.b, p.a)
		if d1 != d2 {
			t.Errorf("distance not symmetric: d(%q,%q)=%d, d(%q,%q)=%d",
				p.a, p.b, d1, p.b, p.a, d2)
		}
	}
}

// TestLevenshteinDistanceUnicode verifies correct handling of Unicode strings.
func TestLevenshteinDistanceUnicode(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected int
	}{
		{"add_accent", "cafe", "café", 1},
		{"chinese_one_char", "你好", "你们好", 1},
		{"emoji", "😀", "😁", 1},
		{"mixed", "hello 世界", "hallo 世界", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LevenshteinDistance(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("LevenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

// TestNormalizeSubjectStripsPrefixes verifies that re:/fwd:/fw: prefixes
// are stripped from subject lines.
func TestNormalizeSubjectStripsPrefixes(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Re: Hello", "hello"},
		{"RE: Hello", "hello"},
		{"re: Hello", "hello"},
		{"Fwd: Hello", "hello"},
		{"FWD: Hello", "hello"},
		{"fwd: Hello", "hello"},
		{"Fw: Hello", "hello"},
		{"FW: Hello", "hello"},
		{"Aw: Hello", "hello"},
		{"WG: Hello", "hello"},
		{"Re: Re: Hello", "hello"},
		{"Fwd: Re: Hello", "hello"},
		{"Re[2]: Hello", "hello"},
		{"Hello", "hello"},
		{"  Hello  ", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeSubject(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeSubject(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestNormalizeSubjectStripsExternal verifies that [external] tags are stripped.
func TestNormalizeSubjectStripsExternal(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"[External] Hello", "hello"},
		{"[external] Hello", "hello"},
		{"[EXTERNAL] Hello", "hello"},
		{"Re: [External] Hello", "hello"},
		{"[External] Re: Hello", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeSubject(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeSubject(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestNormalizeSubjectCollapsesWhitespace verifies that consecutive
// whitespace is collapsed to a single space.
func TestNormalizeSubjectCollapsesWhitespace(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello   World", "hello world"},
		{"Hello\tWorld", "hello world"},
		{"Hello\nWorld", "hello world"},
		{"  Hello  World  ", "hello world"},
		{"Hello\t\t\tWorld", "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeSubject(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeSubject(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestNormalizeSubjectLowercases verifies that output is lowercased.
func TestNormalizeSubjectLowercases(t *testing.T) {
	input := "Hello World"
	got := NormalizeSubject(input)
	if got != "hello world" {
		t.Errorf("NormalizeSubject(%q) = %q, want lowercase", input, got)
	}
}

// TestNormalizeSubjectForKey strips non-alphanumeric characters.
func TestNormalizeSubjectForKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Invoice #123", "invoice 123"},
		{"Re: Project Q3 (urgent)!", "project q3 urgent"},
		{"Meeting @ 3pm", "meeting 3pm"},
		{"Budget $$$ 2024", "budget 2024"},
		{"[External] Re: Hello!", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeSubjectForKey(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeSubjectForKey(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestFuzzySubjectMatchExact verifies that identical normalized subjects match.
func TestFuzzySubjectMatchExact(t *testing.T) {
	match, score := FuzzySubjectMatch("Hello World", "Hello World")
	if !match {
		t.Error("identical subjects should match")
	}
	if score != 0 {
		t.Errorf("exact match should have score 0, got %f", score)
	}
}

// TestFuzzySubjectMatchPrefixVariants verifies that prefix-stripped subjects match.
func TestFuzzySubjectMatchPrefixVariants(t *testing.T) {
	tests := []struct {
		a        string
		b        string
		expected bool
	}{
		{"Re: Hello", "Hello", true},
		{"Fwd: Hello", "Hello", true},
		{"Re: Meeting Notes", "Meeting Notes", true},
		{"[External] Hello", "Hello", true},
		{"Different Subject", "Hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			match, _ := FuzzySubjectMatch(tt.a, tt.b)
			if match != tt.expected {
				t.Errorf("FuzzySubjectMatch(%q, %q) match=%v, want %v", tt.a, tt.b, match, tt.expected)
			}
		})
	}
}

// TestFuzzySubjectMatchThreshold verifies the distance threshold of 3.
func TestFuzzySubjectMatchThreshold(t *testing.T) {
	tests := []struct {
		a        string
		b        string
		expected bool
		maxScore float64
	}{
		{"hello", "helo", true, 1},
		{"hello", "hallo", true, 1},
		{"hello", "hell", true, 1},
		{"hello", "hi", false, 2},
		{"abcdef", "abcxyz", false, 3},
		{"meeting notes", "meeting note", true, 1},
		{"project plan", "projct plan", true, 1},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			match, score := FuzzySubjectMatch(tt.a, tt.b)
			if match != tt.expected {
				t.Errorf("FuzzySubjectMatch(%q, %q) match=%v, want %v (score=%f)",
					tt.a, tt.b, match, tt.expected, score)
			}
			if score > tt.maxScore {
				t.Errorf("FuzzySubjectMatch(%q, %q) score=%f, want <= %f",
					tt.a, tt.b, score, tt.maxScore)
			}
		})
	}
}

// TestFuzzySubjectMatchUnicode verifies fuzzy matching with Unicode subjects.
func TestFuzzySubjectMatchUnicode(t *testing.T) {
	match, score := FuzzySubjectMatch("Rendez-vous demain", "Re: Rendez-vous demain!")
	if !match {
		t.Errorf("should match after normalization, got score %f", score)
	}
}
