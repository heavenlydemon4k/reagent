// Package contact provides contact deduplication for the Ingestion Mesh.
// dedup.go is the main deduplication engine that orchestrates normalization,
// exact/fuzzy matching, and new contact creation.
package contact

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/decisionstack/ingestion/internal/models"
	"github.com/google/uuid"
)

// DedupEngine orchestrates contact deduplication for ingested emails.
type DedupEngine struct {
	neo4j   *Neo4jStore
	matcher *SimilarMatcher
	log     *slog.Logger
}

// NewDedupEngine creates a new deduplication engine.
func NewDedupEngine(neo4j *Neo4jStore, log *slog.Logger) *DedupEngine {
	if log == nil {
		log = slog.Default()
	}
	return &DedupEngine{
		neo4j:   neo4j,
		matcher: NewSimilarMatcher(0.6),
		log:     log,
	}
}

// Dedup resolves an email address + name to a single Contact identity.
// It implements a 4-tier strategy:
//
//  1. Exact match on canonical_email → return existing contact
//  2. Name variant match on different email → fuzzy: create SIMILAR_TO edge, flag for review
//  3. No match → create new Contact node
//
// The returned DedupResult indicates whether the contact is new, fuzzy-matched,
// and which existing contacts it was found similar to.
func (e *DedupEngine) Dedup(ctx context.Context, userID uuid.UUID, email string, name string) (*models.DedupResult, error) {
	// 1. Normalize
	canonical := NormalizeEmail(email)
	normalizedName := NormalizeName(name)

	// 2. Exact match on canonical_email
	existing, err := e.neo4j.FindContactByEmail(ctx, userID, canonical)
	if err != nil {
		return nil, fmt.Errorf("dedup: exact lookup failed: %w", err)
	}
	if existing != nil {
		// Found exact match — update name variants if new info provided
		if normalizedName != "" && !hasVariant(existing.NameVariants, normalizedName) {
			// We don't mutate here; name variant updates are async
			// through the intelligence layer to avoid races.
			e.log.Debug("exact contact match with new name variant",
				"contact_id", existing.ID,
				"new_name", normalizedName,
			)
		}
		return &models.DedupResult{
			ContactID:    existing.ID,
			IsNewContact: false,
			IsFuzzyMatch: false,
		}, nil
	}

	// 3. No exact match — search by name variants for fuzzy matching
	if normalizedName != "" {
		nameMatches, err := e.neo4j.FindContactsByName(ctx, userID, normalizedName)
		if err != nil {
			return nil, fmt.Errorf("dedup: name lookup failed: %w", err)
		}

		var similarContacts []uuid.UUID
		for _, candidate := range nameMatches {
			// Skip self (same canonical email should have been caught above)
			if strings.EqualFold(candidate.CanonicalEmail, canonical) {
				continue
			}

			// Check similarity between the new contact and candidate
			newContact := &models.Contact{
				UserID:         userID,
				CanonicalEmail: canonical,
				NameVariants:   GenerateNameVariants(normalizedName),
			}

			matched, confidence := e.matcher.CheckSimilarity(newContact, candidate)
			if matched {
				// Create SIMILAR_TO edge — but never auto-merge
				if err := e.neo4j.CreateSimilarToEdge(ctx, candidate.ID, uuid.Nil, confidence); err != nil {
					e.log.Error("failed to create SIMILAR_TO edge",
						"error", err,
						"candidate_id", candidate.ID,
					)
				}
				similarContacts = append(similarContacts, candidate.ID)
				e.log.Info("fuzzy contact match flagged for review",
					"new_email", canonical,
					"existing_id", candidate.ID,
					"confidence", confidence,
				)
			}
		}

		if len(similarContacts) > 0 {
			// Create the new contact (we don't merge — we link)
			newContact, err := e.neo4j.CreateContact(ctx, userID, canonical, normalizedName)
			if err != nil {
				return nil, fmt.Errorf("dedup: create contact after fuzzy match: %w", err)
			}

			// Link the new contact to all similar existing contacts
			for _, similarID := range similarContacts {
				if err := e.neo4j.CreateSimilarToEdge(ctx, newContact.ID, similarID, 0.75); err != nil {
					e.log.Error("failed to link similar contact", "error", err, "similar_id", similarID)
				}
			}

			return &models.DedupResult{
				ContactID:    newContact.ID,
				IsNewContact: true,
				IsFuzzyMatch: true,
				SimilarToIDs: similarContacts,
			}, nil
		}
	}

	// 4. No match at all — create new Contact node
	newContact, err := e.neo4j.CreateContact(ctx, userID, canonical, normalizedName)
	if err != nil {
		return nil, fmt.Errorf("dedup: create new contact: %w", err)
	}

	return &models.DedupResult{
		ContactID:    newContact.ID,
		IsNewContact: true,
		IsFuzzyMatch: false,
	}, nil
}

// DedupAll deduplicates all participants (sender + recipients) of an email
// and returns a map from canonical email to DedupResult.
func (e *DedupEngine) DedupAll(ctx context.Context, userID uuid.UUID, senderEmail, senderName string, recipientEmails []string) (map[string]*models.DedupResult, error) {
	results := make(map[string]*models.DedupResult)

	// Dedup sender
	senderResult, err := e.Dedup(ctx, userID, senderEmail, senderName)
	if err != nil {
		return nil, fmt.Errorf("dedup sender: %w", err)
	}
	results[NormalizeEmail(senderEmail)] = senderResult

	// Dedup recipients (names unknown at this stage, use empty string)
	for _, recp := range recipientEmails {
		canonical := NormalizeEmail(recp)
		if canonical == "" {
			continue
		}
		if _, alreadyDone := results[canonical]; alreadyDone {
			continue
		}
		result, err := e.Dedup(ctx, userID, recp, "")
		if err != nil {
			return nil, fmt.Errorf("dedup recipient %s: %w", recp, err)
		}
		results[canonical] = result
	}

	return results, nil
}

// hasVariant checks if a name variant already exists in the list.
func hasVariant(variants []string, name string) bool {
	lower := strings.ToLower(name)
	for _, v := range variants {
		if strings.EqualFold(v, lower) {
			return true
		}
	}
	return false
}
