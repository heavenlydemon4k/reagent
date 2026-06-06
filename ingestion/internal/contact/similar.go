// Package contact provides contact deduplication for the Ingestion Mesh.
// similar.go implements similarity scoring and SIMILAR_TO edge management.
package contact

import (
	"strings"
	"unicode"

	"github.com/decisionstack/ingestion/internal/models"
	"github.com/google/uuid"
)

// SimilarMatcher compares two contacts and decides whether they are similar
// enough to warrant a SIMILAR_TO edge and human review.
type SimilarMatcher struct {
	threshold float64 // minimum combined score to flag (default 0.6)
}

// NewSimilarMatcher creates a matcher with the given confidence threshold.
// Typical values: 0.5 (lenient) to 0.8 (strict).
func NewSimilarMatcher(threshold float64) *SimilarMatcher {
	if threshold <= 0 || threshold > 1 {
		threshold = 0.6
	}
	return &SimilarMatcher{threshold: threshold}
}

// CheckSimilarity compares two contacts and returns:
//   - match: true if the combined similarity score exceeds the threshold
//   - score: a value in [0, 1] where higher means more similar
//
// The score is computed from:
//   - Name similarity (Jaro-Winkler-like heuristic) — 60% weight
//   - Domain similarity (exact match) — 25% weight
//   - Name variant overlap — 15% weight
func (m *SimilarMatcher) CheckSimilarity(contactA, contactB *models.Contact) (bool, float64) {
	if contactA == nil || contactB == nil {
		return false, 0
	}

	nameScore := nameSimilarity(contactA.NameVariants, contactB.NameVariants)
	domainScore := domainSimilarity(contactA.CanonicalEmail, contactB.CanonicalEmail)
	variantScore := variantOverlap(contactA.NameVariants, contactB.NameVariants)

	// Weighted combination
	combined := 0.6*nameScore + 0.25*domainScore + 0.15*variantScore

	return combined >= m.threshold, combined
}

// FlagForReview marks a contact pair for user review.
// In production this would create a review queue entry; for now it returns
// the IDs that should be reviewed.
func (m *SimilarMatcher) FlagForReview(contactID uuid.UUID) uuid.UUID {
	return contactID
}

// nameSimilarity computes a normalized similarity score between two sets of
// name variants using a heuristic based on longest common prefix and length ratio.
func nameSimilarity(variantsA, variantsB []string) float64 {
	if len(variantsA) == 0 || len(variantsB) == 0 {
		return 0
	}

	// Use the primary (first) variant for each
	a := strings.ToLower(variantsA[0])
	b := strings.ToLower(variantsB[0])

	if a == b {
		return 1.0
	}

	// Longest common prefix
	lcp := 0
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] == b[i] {
			lcp++
		} else {
			break
		}
	}

	prefixScore := float64(lcp) / float64(maxLen)

	// Length ratio
	lenA, lenB := len([]rune(a)), len([]rune(b))
	var lenRatio float64
	if lenA > lenB && lenA > 0 {
		lenRatio = float64(lenB) / float64(lenA)
	} else if lenB > 0 {
		lenRatio = float64(lenA) / float64(lenB)
	}

	// Jaro-Winkler style: emphasize prefix match
	score := 0.7*prefixScore + 0.3*lenRatio
	if score > 1.0 {
		score = 1.0
	}
	return score
}

// domainSimilarity returns 1.0 if the email domains match exactly, 0 otherwise.
func domainSimilarity(emailA, emailB string) float64 {
	da := ExtractDomain(emailA)
	db := ExtractDomain(emailB)
	if da == "" || db == "" {
		return 0
	}
	if strings.EqualFold(da, db) {
		return 1.0
	}
	return 0
}

// variantOverlap computes the Jaccard-like overlap between two sets of name variants.
func variantOverlap(variantsA, variantsB []string) float64 {
	if len(variantsA) == 0 || len(variantsB) == 0 {
		return 0
	}

	setA := make(map[string]struct{}, len(variantsA))
	for _, v := range variantsA {
		setA[strings.ToLower(v)] = struct{}{}
	}

	intersection := 0
	for _, v := range variantsB {
		if _, ok := setA[strings.ToLower(v)]; ok {
			intersection++
		}
	}

	union := len(variantsA) + len(variantsB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// Initials returns the uppercase initials of a name.
func Initials(name string) string {
	var sb strings.Builder
	inWord := false
	for _, r := range name {
		if unicode.IsLetter(r) {
			if !inWord {
				sb.WriteRune(unicode.ToUpper(r))
				inWord = true
			}
		} else {
			inWord = false
		}
	}
	return sb.String()
}
