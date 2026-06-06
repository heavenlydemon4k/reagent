// Package thread tests deterministic thread key generation.
package thread

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"testing"
)

// TestGenerateThreadKeyDeterministic verifies that the same inputs always
// produce the same SHA-256 hash output.
func TestGenerateThreadKeyDeterministic(t *testing.T) {
	tests := []struct {
		name    string
		emails  []string
		subject string
	}{
		{
			name:    "simple",
			emails:  []string{"alice@example.com", "bob@example.com"},
			subject: "Meeting tomorrow",
		},
		{
			name:    "unicode_subject",
			emails:  []string{"alice@example.com"},
			subject: "Rendez-vous demain à 14h",
		},
		{
			name:    "many_participants",
			emails:  []string{"a@x.com", "b@x.com", "c@x.com", "d@x.com", "e@x.com"},
			subject: "Project update Q3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key1 := GenerateThreadKey(tt.emails, tt.subject)
			key2 := GenerateThreadKey(tt.emails, tt.subject)

			if key1 != key2 {
				t.Errorf("GenerateThreadKey not deterministic: %q vs %q", key1, key2)
			}

			// Must be a valid hex string of SHA-256 length (64 chars)
			if len(key1) != 64 {
				t.Errorf("expected key length 64, got %d", len(key1))
			}
			if _, err := hex.DecodeString(key1); err != nil {
				t.Errorf("key is not valid hex: %v", err)
			}
		})
	}
}

// TestGenerateThreadKeyDifferentSubjects verifies that different subjects
// produce different hashes even with the same participants.
func TestGenerateThreadKeyDifferentSubjects(t *testing.T) {
	emails := []string{"alice@example.com", "bob@example.com"}

	key1 := GenerateThreadKey(emails, "Meeting tomorrow")
	key2 := GenerateThreadKey(emails, "Meeting next week")
	key3 := GenerateThreadKey(emails, "meeting tomorrow") // different case

	if key1 == key2 {
		t.Error("different subjects should produce different keys")
	}
	if key1 == key3 {
		t.Error("case-sensitive subjects should produce different keys")
	}
	if key2 == key3 {
		t.Error("different subjects should produce different keys")
	}
}

// TestGenerateThreadKeyReFwdStripped verifies that re:/fwd:/fw: prefixes
// are stripped from the subject before hashing, so "re: subject" and
// "subject" produce the same key.
func TestGenerateThreadKeyReFwdStripped(t *testing.T) {
	emails := []string{"alice@example.com", "bob@example.com"}

	tests := []struct {
		name      string
		subject1  string
		subject2  string
		shouldMatch bool
	}{
		{"re_prefix", "Meeting tomorrow", "Re: Meeting tomorrow", true},
		{"fwd_prefix", "Meeting tomorrow", "Fwd: Meeting tomorrow", true},
		{"fw_prefix", "Meeting tomorrow", "FW: Meeting tomorrow", true},
		{"nested_prefix", "Re: Meeting tomorrow", "Re: Re: Meeting tomorrow", true},
		{"re_fwd_combo", "Re: Meeting tomorrow", "Fwd: Re: Meeting tomorrow", true},
		{"external_tag", "Meeting tomorrow", "[External] Meeting tomorrow", true},
		{"actual_diff", "Meeting tomorrow", "Different subject", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key1 := GenerateThreadKey(emails, tt.subject1)
			key2 := GenerateThreadKey(emails, tt.subject2)

			if tt.shouldMatch && key1 != key2 {
				t.Errorf("expected same key for %q and %q, got %q vs %q",
					tt.subject1, tt.subject2, key1, key2)
			}
			if !tt.shouldMatch && key1 == key2 {
				t.Errorf("expected different keys for %q and %q, both got %q",
					tt.subject1, tt.subject2, key1)
			}
		})
	}
}

// TestGenerateThreadKeyParticipantsSorted verifies that participant emails
// are sorted (case-insensitive) before hashing, so different orderings
// of the same emails produce the same key.
func TestGenerateThreadKeyParticipantsSorted(t *testing.T) {
	subject := "Project planning"

	tests := []struct {
		name   string
		order1 []string
		order2 []string
	}{
		{
			name:   "two_swapped",
			order1: []string{"alice@example.com", "bob@example.com"},
			order2: []string{"bob@example.com", "alice@example.com"},
		},
		{
			name:   "three_reversed",
			order1: []string{"a@x.com", "b@x.com", "c@x.com"},
			order2: []string{"c@x.com", "b@x.com", "a@x.com"},
		},
		{
			name:   "case_insensitive",
			order1: []string{"Alice@Example.com", "BOB@EXAMPLE.COM"},
			order2: []string{"bob@example.com", "alice@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key1 := GenerateThreadKey(tt.order1, subject)
			key2 := GenerateThreadKey(tt.order2, subject)

			if key1 != key2 {
				t.Errorf("same participants in different order should produce same key: %q vs %q",
					key1, key2)
			}
		})
	}
}

// TestGenerateThreadKeyDeduplicatesParticipants verifies that duplicate
// participant emails are deduplicated before hashing.
func TestGenerateThreadKeyDeduplicatesParticipants(t *testing.T) {
	subject := "Team sync"
	emailsWithDups := []string{
		"alice@example.com",
		"bob@example.com",
		"alice@example.com", // duplicate
	}
	emailsUnique := []string{
		"alice@example.com",
		"bob@example.com",
	}

	key1 := GenerateThreadKey(emailsWithDups, subject)
	key2 := GenerateThreadKey(emailsUnique, subject)

	if key1 != key2 {
		t.Error("duplicate participants should be deduplicated")
	}
}

// TestGenerateThreadKeyEmptyParticipantSkipped verifies that empty
// participant strings are skipped.
func TestGenerateThreadKeyEmptyParticipantSkipped(t *testing.T) {
	emailsWithEmpty := []string{"alice@example.com", "", "bob@example.com"}
	emailsClean := []string{"alice@example.com", "bob@example.com"}
	subject := "Hello"

	key1 := GenerateThreadKey(emailsWithEmpty, subject)
	key2 := GenerateThreadKey(emailsClean, subject)

	if key1 != key2 {
		t.Error("empty participant emails should be skipped")
	}
}

// TestGenerateThreadKeyDifferentParticipants verifies that different
// sets of participants produce different hashes.
func TestGenerateThreadKeyDifferentParticipants(t *testing.T) {
	subject := "Same subject"

	key1 := GenerateThreadKey([]string{"alice@example.com"}, subject)
	key2 := GenerateThreadKey([]string{"bob@example.com"}, subject)
	key3 := GenerateThreadKey([]string{"alice@example.com", "bob@example.com"}, subject)

	if key1 == key2 {
		t.Error("different single participants should produce different keys")
	}
	if key1 == key3 {
		t.Error("different participant counts should produce different keys")
	}
	if key2 == key3 {
		t.Error("different participant counts should produce different keys")
	}
}

// TestGenerateThreadKeyAlgorithm manually verifies the hashing algorithm
// by reproducing the expected computation.
func TestGenerateThreadKeyAlgorithm(t *testing.T) {
	emails := []string{"bob@example.com", "alice@example.com"} // out of order
	subject := "Re: Hello World"

	// Expected: sort emails case-insensitively, deduplicate
	sorted := make([]string, len(emails))
	copy(sorted, emails)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i]) < strings.ToLower(sorted[j])
	})

	// Deduplicate and lowercase
	seen := make(map[string]struct{})
	deduped := make([]string, 0, len(sorted))
	for _, e := range sorted {
		le := strings.ToLower(strings.TrimSpace(e))
		if le == "" {
			continue
		}
		if _, ok := seen[le]; !ok {
			seen[le] = struct{}{}
			deduped = append(deduped, le)
		}
	}

	// Normalize subject
	normSubject := NormalizeSubjectForKey(subject)

	// Build input string
	var sb strings.Builder
	for i, e := range deduped {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(e)
	}
	sb.WriteByte('|')
	sb.WriteString(normSubject)

	expectedHash := sha256.Sum256([]byte(sb.String()))
	expectedKey := hex.EncodeToString(expectedHash[:])

	actualKey := GenerateThreadKey(emails, subject)

	if actualKey != expectedKey {
		t.Errorf("key mismatch:\n  expected: %s\n  actual:   %s", expectedKey, actualKey)
	}
}

// TestGenerateThreadKeyEmptySubject verifies behavior with empty subject.
func TestGenerateThreadKeyEmptySubject(t *testing.T) {
	emails := []string{"alice@example.com"}

	key1 := GenerateThreadKey(emails, "")
	key2 := GenerateThreadKey(emails, "")

	if key1 != key2 {
		t.Error("empty subject should still be deterministic")
	}

	if len(key1) != 64 {
		t.Errorf("expected key length 64, got %d", len(key1))
	}
}

// TestGenerateThreadKeyNoParticipants verifies behavior with no participants.
func TestGenerateThreadKeyNoParticipants(t *testing.T) {
	key1 := GenerateThreadKey([]string{}, "Subject only")
	key2 := GenerateThreadKey([]string{}, "Subject only")

	if key1 != key2 {
		t.Error("no participants should still be deterministic")
	}

	if len(key1) != 64 {
		t.Errorf("expected key length 64, got %d", len(key1))
	}
}
