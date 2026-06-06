// Package thread provides thread reconstruction for the Ingestion Mesh.
// fuzzy.go implements fuzzy subject matching using Levenshtein distance.
package thread

import (
	"regexp"
	"strings"
	"unicode"
)

// subjectPrefixRe matches common email subject prefixes like "re:", "fwd:", "fw:",
// and tags like "[external]" that should be stripped for comparison.
var subjectPrefixRe = regexp.MustCompile(`(?i)^\s*(re|fwd|fw|aw|wg)\s*[:\]]+\s*`)
var externalTagRe = regexp.MustCompile(`(?i)\[external\]`)
var whitespaceCollapseRe = regexp.MustCompile(`\s+`)

// FuzzySubjectMatch determines whether two subjects match fuzzily.
// It normalizes both subjects, computes the Levenshtein distance, and
// returns true if the distance is strictly less than the threshold (3).
// The returned float64 is the distance as a score (lower = more similar).
func FuzzySubjectMatch(a, b string) (bool, float64) {
	na := NormalizeSubject(a)
	nb := NormalizeSubject(b)

	// Exact match after normalization
	if na == nb {
		return true, 0
	}

	dist := LevenshteinDistance(na, nb)
	return dist < 3, float64(dist)
}

// NormalizeSubject canonicalizes a subject line for comparison:
//   - lowercases
//   - strips re:/fwd:/fw:/aw:/wg: prefixes (with optional bracket forms)
//   - strips [external] tags
//   - trims
//   - collapses consecutive whitespace to a single space
func NormalizeSubject(s string) string {
	s = strings.ToLower(s)

	// Strip [external] tags
	s = externalTagRe.ReplaceAllString(s, "")

	// Iteratively strip prefixes until none remain (handles "re: re: subject")
	for {
		next := subjectPrefixRe.ReplaceAllString(s, "")
		if next == s {
			break
		}
		s = next
	}

	// Strip any remaining leading/trailing whitespace artifacts
	s = strings.TrimSpace(s)

	// Collapse all consecutive whitespace to a single space
	s = whitespaceCollapseRe.ReplaceAllString(s, " ")

	return s
}

// LevenshteinDistance computes the edit distance between two strings
// using the classic dynamic programming algorithm (O(|a|*|b|)).
// The distance is the minimum number of single-character insertions,
// deletions, or substitutions required to transform a into b.
func LevenshteinDistance(a, b string) int {
	// Fast paths
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len([]rune(b))
	}
	if len(b) == 0 {
		return len([]rune(a))
	}

	ra := []rune(a)
	rb := []rune(b)
	alen := len(ra)
	blen := len(rb)

	// Use only two rows to keep space O(min(alen, blen))
	// Ensure the inner loop iterates over the shorter dimension
	if alen < blen {
		return LevenshteinDistance(b, a)
	}

	prev := make([]int, blen+1)
	curr := make([]int, blen+1)

	for j := 0; j <= blen; j++ {
		prev[j] = j
	}

	for i := 1; i <= alen; i++ {
		curr[0] = i
		for j := 1; j <= blen; j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			deletion := prev[j] + 1
			insertion := curr[j-1] + 1
			substitution := prev[j-1] + cost
			curr[j] = min(deletion, insertion, substitution)
		}
		prev, curr = curr, prev
	}

	return prev[blen]
}

// min returns the minimum of a variadic number of ints.
func min(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// NormalizeSubjectForKey normalizes a subject specifically for inclusion in a
// thread key: it is more aggressive than NormalizeSubject, stripping all
// non-alphanumeric characters so that "Invoice #123" and "Invoice 123" converge.
func NormalizeSubjectForKey(s string) string {
	s = NormalizeSubject(s)
	// Strip all non-alphanumeric characters (except spaces)
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			sb.WriteRune(r)
		}
	}
	result := strings.TrimSpace(sb.String())
	return whitespaceCollapseRe.ReplaceAllString(result, " ")
}
