// Package batch provides queue management for accumulating and delivering
// decision cards to clients in urgency-priority order.
package batch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

// BatchThresholdDefault is the default number of pending cards that triggers
// a batch notification to the user.
const BatchThresholdDefault = 5

// UrgentThreshold is the urgency_score floor above which a card is considered
// urgent and may trigger immediate notification regardless of batch size.
const UrgentThreshold = 0.7

// QueueManager orchestrates card lifecycle: accumulation, ordering, batching,
// and delivery. It keeps PostgreSQL as the source of truth and uses Redis for
// lightweight caching and pub/sub triggers.
type QueueManager struct {
	db        *sqlx.DB
	store     *CardStore
	estimator *ClearTimeEstimator
	redis     redis.UniversalClient
}

// NewQueueManager creates a new QueueManager with all required dependencies.
func NewQueueManager(db *sqlx.DB, redis redis.UniversalClient) *QueueManager {
	return &QueueManager{
		db:        db,
		store:     NewCardStore(db),
		estimator: NewClearTimeEstimator(redis),
		redis:     redis,
	}
}

// DB returns the underlying sqlx.DB for store operations needed by handlers.
func (qm *QueueManager) DB() *sqlx.DB {
	return qm.db
}

// ---------------------------------------------------------------------------
// BATCH QUERIES
// ---------------------------------------------------------------------------

// GetBatch returns the current pending batch for a user with estimated clear time.
// Cards are ordered by urgency_score DESC, then created_at ASC.
// The limit parameter caps the number of cards returned (default 20, max 100).
func (qm *QueueManager) GetBatch(ctx context.Context, userID uuid.UUID, limit int) (*models.BatchInfo, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	cards, err := qm.store.GetPendingOrdered(ctx, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get pending cards: %w", err)
	}

	pendingCount, err := qm.store.GetPendingCount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get pending count: %w", err)
	}

	estMin := qm.estimator.Estimate(ctx, userID, pendingCount)

	batch := &models.BatchInfo{
		Size:                      pendingCount,
		EstimatedClearTimeMinutes: estMin,
		Cards:                     cards,
	}

	return batch, nil
}

// GetNextCard returns the single highest-urgency pending card for sequential
// processing workflows. Returns models.ErrCodeQueueEmpty when no cards pending.
func (qm *QueueManager) GetNextCard(ctx context.Context, userID uuid.UUID) (*models.DecisionCard, error) {
	cards, err := qm.store.GetPendingOrdered(ctx, userID, 1)
	if err != nil {
		return nil, fmt.Errorf("get next card: %w", err)
	}
	if len(cards) == 0 {
		return nil, &models.SyncError{
			Code:    models.ErrCodeQueueEmpty,
			Message: "no pending cards in queue",
			Retry:   false,
		}
	}
	return &cards[0], nil
}

// GetCounts returns a quick summary of the user's queue: total pending and
// urgent (urgency_score >= UrgentThreshold) counts.
func (qm *QueueManager) GetCounts(ctx context.Context, userID uuid.UUID) (pending int, urgent int, err error) {
	pending, err = qm.store.GetPendingCount(ctx, userID)
	if err != nil {
		return 0, 0, fmt.Errorf("get pending count: %w", err)
	}

	urgent, err = qm.store.GetUrgentCount(ctx, userID, UrgentThreshold)
	if err != nil {
		return 0, 0, fmt.Errorf("get urgent count: %w", err)
	}

	return pending, urgent, nil
}

// ---------------------------------------------------------------------------
// SERVER VERSION
// ---------------------------------------------------------------------------

// IncrementServerVersion atomically increments the server version for a user
// and returns the new value. This signals to sync clients that new data is
// available. The increment happens in PostgreSQL as the source of truth.
func (qm *QueueManager) IncrementServerVersion(ctx context.Context, userID uuid.UUID) (int, error) {
	version, err := qm.store.IncrementServerVersion(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("increment server version: %w", err)
	}

	// Sync Redis with the authoritative PostgreSQL version.
	versionKey := fmt.Sprintf("version:%s", userID.String())
	if rerr := qm.redis.Set(ctx, versionKey, version, 0).Err(); rerr != nil {
		logger.Warn("failed to sync version to redis", "error", rerr, "user_id", userID)
	}

	return version, nil
}

// ---------------------------------------------------------------------------
// CARD LIFECYCLE HOOKS
// ---------------------------------------------------------------------------

// OnCardCreated handles the full server-side flow when a new decision card is
// created: insert the card, update queue metadata, increment server version,
// and optionally trigger a batch notification if thresholds are met.
func (qm *QueueManager) OnCardCreated(ctx context.Context, card *models.DecisionCard) error {
	if card.ID == uuid.Nil {
		card.ID = uuid.New()
	}
	if card.CardState == "" {
		card.CardState = "pending"
	}
	now := time.Now().UTC()
	if card.CreatedAt.IsZero() {
		card.CreatedAt = now
	}
	if card.UpdatedAt.IsZero() {
		card.UpdatedAt = now
	}

	// Step 1: Insert card and ensure user_queues row (atomic tx in store).
	if err := qm.store.Insert(ctx, card); err != nil {
		return fmt.Errorf("insert card: %w", err)
	}

	// Step 2: Increment queue counters and server version atomically.
	tx, err := qm.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for queue update: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		UPDATE user_queues
		SET pending_count = pending_count + 1,
		    server_version = server_version + 1,
		    updated_at = $2
		WHERE user_id = $1
	`, card.UserID, now)
	if err != nil {
		return fmt.Errorf("update user queue on create: %w", err)
	}

	// Fetch updated version.
	var newVersion int
	if err := tx.GetContext(ctx, &newVersion, `
		SELECT server_version FROM user_queues WHERE user_id = $1
	`, card.UserID); err != nil {
		return fmt.Errorf("fetch updated version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit card created: %w", err)
	}

	// Step 3: Sync version to Redis.
	versionKey := fmt.Sprintf("version:%s", card.UserID.String())
	if rerr := qm.redis.Set(ctx, versionKey, newVersion, 0).Err(); rerr != nil {
		logger.Warn("failed to sync version to redis", "error", rerr, "user_id", card.UserID)
	}

	// Step 4: Add to Redis sorted set for fast sync lookups.
	queueKey := fmt.Sprintf("queue:%s", card.UserID.String())
	member := fmt.Sprintf(`{"id":"%s","v":%d}`, card.ID.String(), newVersion)
	if rerr := qm.redis.ZAdd(ctx, queueKey, redis.Z{
		Score:  float64(newVersion),
		Member: member,
	}).Err(); rerr != nil {
		logger.Warn("failed to add card to redis queue", "error", rerr, "user_id", card.UserID)
	}

	// Step 5: Check threshold and trigger notification if needed.
	shouldNotify, err := qm.shouldTriggerNotification(ctx, card.UserID)
	if err != nil {
		logger.Warn("failed to check notification threshold", "error", err, "user_id", card.UserID)
		return nil // non-fatal
	}

	if shouldNotify {
		if nerr := qm.triggerNotification(ctx, card.UserID); nerr != nil {
			logger.Warn("failed to trigger batch notification", "error", nerr, "user_id", card.UserID)
		}
	}

	logger.Info("card created and queued",
		"card_id", card.ID,
		"user_id", card.UserID,
		"urgency_score", card.UrgencyScore,
		"server_version", newVersion,
	)

	return nil
}

// OnCardCleared handles the full server-side flow when a card is cleared
// (moved from pending to sent). Updates card state, decrements queue counters,
// increments server version, and records timing for estimation.
func (qm *QueueManager) OnCardCleared(ctx context.Context, userID uuid.UUID, cardID uuid.UUID) error {
	// Step 1: Mark card as sent and update queue atomically via store.
	// MarkCardSent handles: card_state='sent', pending_count--, server_version++
	if err := qm.store.MarkCardSent(ctx, userID, cardID); err != nil {
		return fmt.Errorf("mark card sent: %w", err)
	}

	// Step 2: Fetch the new server version for Redis sync.
	newVersion, err := qm.store.GetServerVersion(ctx, userID)
	if err != nil {
		logger.Warn("failed to fetch version after clear", "error", err, "user_id", userID)
	}

	// Step 3: Sync version to Redis.
	versionKey := fmt.Sprintf("version:%s", userID.String())
	if rerr := qm.redis.Set(ctx, versionKey, newVersion, 0).Err(); rerr != nil {
		logger.Warn("failed to sync version to redis", "error", rerr, "user_id", userID)
	}

	// Step 4: Remove card from Redis queue.
	queueKey := fmt.Sprintf("queue:%s", userID.String())
	// Remove by pattern — we stored as JSON string with card ID inside.
	members, err := qm.redis.ZRange(ctx, queueKey, 0, -1).Result()
	if err == nil {
		for _, m := range members {
			// Simple contains check for card ID in the JSON member string.
			if strings.Contains(m, cardID.String()) {
				if rerr := qm.redis.ZRem(ctx, queueKey, m).Err(); rerr != nil {
					logger.Warn("failed to remove card from redis queue", "error", rerr)
				}
				break
			}
		}
	}

	// Step 5: Record timing for clear-time estimation.
	// We don't have exact elapsed time here — the caller should provide it.
	// For server-side tracking, we record a default since the client reports actuals.
	qm.estimator.RecordCardCleared(ctx, userID, DefaultSecondsPerCard)

	logger.Info("card cleared",
		"card_id", cardID,
		"user_id", userID,
		"server_version", newVersion,
	)

	return nil
}

// OnCardClearedWithTiming handles card clearing with an explicit elapsed time
// from the client for accurate clear-time estimation. Does NOT record default timing.
func (qm *QueueManager) OnCardClearedWithTiming(ctx context.Context, userID uuid.UUID, cardID uuid.UUID, elapsedSeconds float64) error {
	// Step 1: Mark card as sent and update queue atomically via store.
	if err := qm.store.MarkCardSent(ctx, userID, cardID); err != nil {
		return fmt.Errorf("mark card sent: %w", err)
	}

	// Step 2: Fetch the new server version for Redis sync.
	newVersion, err := qm.store.GetServerVersion(ctx, userID)
	if err != nil {
		logger.Warn("failed to fetch version after clear", "error", err, "user_id", userID)
	}

	// Step 3: Sync version to Redis.
	versionKey := fmt.Sprintf("version:%s", userID.String())
	if rerr := qm.redis.Set(ctx, versionKey, newVersion, 0).Err(); rerr != nil {
		logger.Warn("failed to sync version to redis", "error", rerr, "user_id", userID)
	}

	// Step 4: Remove card from Redis queue.
	queueKey := fmt.Sprintf("queue:%s", userID.String())
	members, err := qm.redis.ZRange(ctx, queueKey, 0, -1).Result()
	if err == nil {
		for _, m := range members {
			if strings.Contains(m, cardID.String()) {
				if rerr := qm.redis.ZRem(ctx, queueKey, m).Err(); rerr != nil {
					logger.Warn("failed to remove card from redis queue", "error", rerr)
				}
				break
			}
		}
	}

	// Step 5: Record ACTUAL timing from client for accurate estimation.
	qm.estimator.RecordCardCleared(ctx, userID, elapsedSeconds)

	logger.Info("card cleared with timing",
		"card_id", cardID,
		"user_id", userID,
		"elapsed_seconds", elapsedSeconds,
		"server_version", newVersion,
	)

	return nil
}

// ---------------------------------------------------------------------------
// NOTIFICATION THRESHOLD LOGIC
// ---------------------------------------------------------------------------

// shouldTriggerNotification checks whether the batch notification should fire
// based on configurable thresholds: default 5 cards OR at least 1 urgent card.
func (qm *QueueManager) shouldTriggerNotification(ctx context.Context, userID uuid.UUID) (bool, error) {
	pending, urgent, err := qm.GetCounts(ctx, userID)
	if err != nil {
		return false, err
	}

	// Check quiet hours before triggering.
	hour := time.Now().Hour()
	if hour >= 22 || hour < 8 {
		logger.Debug("notification suppressed during quiet hours", "user_id", userID, "hour", hour)
		return false, nil
	}

	// Check last notification time — throttle to at most 1 per 15 minutes.
	var lastNotif *time.Time
	err = qm.db.GetContext(ctx, &lastNotif, `
		SELECT last_notification_at FROM user_queues WHERE user_id = $1
	`, userID)
	if err == nil && lastNotif != nil {
		if time.Since(*lastNotif) < 15*time.Minute {
			return false, nil
		}
	}

	if pending >= BatchThresholdDefault {
		return true, nil
	}
	if urgent >= 1 {
		return true, nil
	}

	return false, nil
}

// triggerNotification records the notification intent and publishes a pub/sub
// event for the push notification dispatcher.
func (qm *QueueManager) triggerNotification(ctx context.Context, userID uuid.UUID) error {
	now := time.Now().UTC()

	// Update last notification timestamp.
	_, err := qm.db.ExecContext(ctx, `
		UPDATE user_queues
		SET last_notification_at = $2, updated_at = $2
		WHERE user_id = $1
	`, userID, now)
	if err != nil {
		return fmt.Errorf("update last_notification_at: %w", err)
	}

	// Publish to Redis pub/sub for the notification worker.
	channel := fmt.Sprintf("notify:%s", userID.String())
	payload := fmt.Sprintf(`{"type":"batch","user_id":"%s","triggered_at":"%s"}`,
		userID.String(), now.Format(time.RFC3339))

	if err := qm.redis.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("publish notification event: %w", err)
	}

	logger.Info("batch notification triggered", "user_id", userID, "channel", channel)
	return nil
}


