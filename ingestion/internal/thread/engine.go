// Package thread provides thread reconstruction for the Ingestion Mesh.
// engine.go implements the primary threading logic with a 3-tier fallback:
//   1. In-Reply-To header match
//   2. References header match
//   3. Fuzzy subject + participant overlap + 7-day window
package thread

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/decisionstack/ingestion/internal/models"
	"github.com/google/uuid"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Engine reconstructs email threads using header-based matching and fuzzy fallback.
type Engine struct {
	db    *sql.DB
	neo4j neo4jdriver.DriverWithContext
	log   *slog.Logger
}

// NewEngine creates a new thread reconstruction engine.
func NewEngine(db *sql.DB, neo4j neo4jdriver.DriverWithContext, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{db: db, neo4j: neo4j, log: log}
}

// FindOrCreateThread locates an existing thread for the given parsed email
// using a 3-tier strategy, or creates a new one if no match is found.
//
// Strategy:
//  1. Primary: In-Reply-To header -> raw_emails.message_id lookup
//  2. Secondary: References headers -> raw_emails.message_id lookup
//  3. Tertiary: Fuzzy subject match (Levenshtein < 3) + sender overlap (>=1 common participant) + 7-day window
//  4. New: INSERT new thread with ON CONFLICT upsert
func (e *Engine) FindOrCreateThread(ctx context.Context, email *models.ParsedEmail) (*models.ThreadMatchResult, error) {
	// Tier 1: In-Reply-To header match
	if email.InReplyTo != nil && *email.InReplyTo != "" {
		threadID, err := e.findThreadByMessageID(ctx, *email.InReplyTo, email.UserID)
		if err == nil && threadID != uuid.Nil {
			return e.incrementAndReturn(ctx, threadID, "in_reply_to")
		}
		if err != nil && err != sql.ErrNoRows {
			e.log.Error("in-reply-to lookup failed", "error", err, "message_id", *email.InReplyTo)
		}
	}

	// Tier 2: References header match
	for _, ref := range email.References {
		if ref == "" {
			continue
		}
		threadID, err := e.findThreadByMessageID(ctx, ref, email.UserID)
		if err == nil && threadID != uuid.Nil {
			return e.incrementAndReturn(ctx, threadID, "references")
		}
		if err != nil && err != sql.ErrNoRows {
			e.log.Error("references lookup failed", "error", err, "message_id", ref)
		}
	}

	// Tier 3: Fuzzy subject + participant overlap + 7-day window
	fuzzyResult, err := e.fuzzyMatch(ctx, email)
	if err != nil {
		e.log.Error("fuzzy match failed", "error", err)
	}
	if fuzzyResult != nil {
		return fuzzyResult, nil
	}

	// Tier 4: Create new thread
	return e.createNewThread(ctx, email)
}

// findThreadByMessageID looks up a thread_id from raw_emails by Message-ID header.
func (e *Engine) findThreadByMessageID(ctx context.Context, messageID string, userID uuid.UUID) (uuid.UUID, error) {
	var threadID uuid.UUID
	query := `
		SELECT thread_id FROM raw_emails
		WHERE message_id = $1 AND user_id = $2
		ORDER BY received_at DESC
		LIMIT 1
	`
	err := e.db.QueryRowContext(ctx, query, messageID, userID).Scan(&threadID)
	if err != nil {
		return uuid.Nil, err
	}
	return threadID, nil
}

// fuzzyMatch attempts to find a thread by normalized subject similarity,
// participant overlap, and recency (7-day window).
func (e *Engine) fuzzyMatch(ctx context.Context, email *models.ParsedEmail) (*models.ThreadMatchResult, error) {
	participants := e.collectParticipants(email)
	if len(participants) == 0 {
		return nil, nil
	}

	windowStart := email.ReceivedAt.Add(-7 * 24 * time.Hour)

	// Query candidate threads from the last 7 days with overlapping participants
	query := `
		SELECT t.id, t.thread_key, t.subject, t.participant_emails, t.message_count
		FROM threads t
		WHERE t.user_id = $1
		  AND t.last_message_at >= $2
		  AND t.participant_emails && $3
		ORDER BY t.last_message_at DESC
		LIMIT 50
	`
	rows, err := e.db.QueryContext(ctx, query, email.UserID, windowStart, participants)
	if err != nil {
		return nil, fmt.Errorf("query candidate threads: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		id        uuid.UUID
		threadKey string
		subject   *string
		participants []string
		msgCount  int
	}
	var candidates []candidate

	for rows.Next() {
		var c candidate
		var sub sql.NullString
		err := rows.Scan(&c.id, &c.threadKey, &sub, &c.participants, &c.msgCount)
		if err != nil {
			continue
		}
		if sub.Valid {
			c.subject = &sub.String
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Score each candidate by subject similarity
	bestScore := float64(999)
	var best *candidate
	for i := range candidates {
		c := &candidates[i]
		if c.subject == nil {
			continue
		}
		matched, dist := FuzzySubjectMatch(email.Subject, *c.subject)
		if matched && dist < bestScore {
			bestScore = dist
			best = c
		}
	}

	if best != nil {
		return e.incrementAndReturn(ctx, best.id, "fuzzy_subject")
	}

	return nil, nil
}

// collectParticipants gathers all unique email addresses from sender + recipients.
func (e *Engine) collectParticipants(email *models.ParsedEmail) []string {
	seen := make(map[string]struct{})
	var result []string

	add := func(email string) {
		le := strings.ToLower(strings.TrimSpace(email))
		if le == "" {
			return
		}
		if _, ok := seen[le]; !ok {
			seen[le] = struct{}{}
			result = append(result, le)
		}
	}

	add(email.SenderEmail)
	for _, r := range email.RecipientEmails {
		add(r)
	}

	return result
}

// incrementAndReturn bumps the message count and last_message_at for an
// existing thread and returns the match result.
func (e *Engine) incrementAndReturn(ctx context.Context, threadID uuid.UUID, method string) (*models.ThreadMatchResult, error) {
	var threadKey string
	query := `
		UPDATE threads
		SET message_count = message_count + 1,
		    last_message_at = NOW()
		WHERE id = $1
		RETURNING thread_key
	`
	err := e.db.QueryRowContext(ctx, query, threadID).Scan(&threadKey)
	if err != nil {
		return nil, fmt.Errorf("increment thread %s: %w", threadID, err)
	}

	return &models.ThreadMatchResult{
		ThreadID:    threadID,
		ThreadKey:   threadKey,
		IsNewThread: false,
		MatchMethod: method,
	}, nil
}

// createNewThread inserts a new thread row. Uses ON CONFLICT in case of
// concurrent creation with the same thread_key.
func (e *Engine) createNewThread(ctx context.Context, email *models.ParsedEmail) (*models.ThreadMatchResult, error) {
	participants := e.collectParticipants(email)
	threadKey := GenerateThreadKey(participants, email.Subject)

	subject := email.Subject
	if subject == "" {
		subject = "(no subject)"
	}

	threadID := uuid.Must(uuid.NewRandom())

	query := `
		INSERT INTO threads (id, user_id, thread_key, source_account_id, subject, participant_emails, message_count, last_message_at, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 1, $7, 'active', NOW())
		ON CONFLICT (user_id, thread_key) DO UPDATE SET
			message_count = threads.message_count + 1,
			last_message_at = EXCLUDED.last_message_at,
			participant_emails = (
				SELECT ARRAY(
					SELECT DISTINCT unnest(array_cat(threads.participant_emails, EXCLUDED.participant_emails))
				)
			)
		RETURNING id, thread_key
	`

	var resultID uuid.UUID
	var resultKey string
	err := e.db.QueryRowContext(ctx, query,
		threadID, email.UserID, threadKey, email.AccountID, subject, participants, email.ReceivedAt,
	).Scan(&resultID, &resultKey)
	if err != nil {
		return nil, fmt.Errorf("upsert thread: %w", err)
	}

	// Determine if this was actually a new thread or a conflict resolution
	isNew := resultID == threadID

	return &models.ThreadMatchResult{
		ThreadID:    resultID,
		ThreadKey:   resultKey,
		IsNewThread: isNew,
		MatchMethod: map[bool]string{true: "new", false: "concurrent_upsert"}[isNew],
	}, nil
}

// GetThreadParticipants returns the current participant list for a thread.
func (e *Engine) GetThreadParticipants(ctx context.Context, threadID uuid.UUID) ([]string, error) {
	var participants []string
	query := `SELECT participant_emails FROM threads WHERE id = $1`
	err := e.db.QueryRowContext(ctx, query, threadID).Scan(&participants)
	if err != nil {
		return nil, err
	}
	return participants, nil
}
