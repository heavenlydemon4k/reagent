// Package nats provides NATS JetStream consumer and publisher for cross-context messaging.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
	natsgo "github.com/nats-io/nats.go"
)

// Consumer listens for events from other bounded contexts (e.g., Intelligence).
type Consumer struct {
	conn         *natsgo.Conn
	js           natsgo.JetStreamContext
	cfg          *config.Config
	handlers     map[string]MessageHandler
	subscriptions []natsgo.Subscription
	maxDeliver   int
	dlqSubject   string
}

// MessageHandler is a function that processes NATS messages.
type MessageHandler func(ctx context.Context, msg *natsgo.Msg) error

// NewConsumer creates a new NATS consumer with JetStream support.
func NewConsumer(cfg *config.Config) (*Consumer, error) {
	opts := []natsgo.Option{
		natsgo.Name("sync-consumer"),
		natsgo.ReconnectWait(2 * time.Second),
		natsgo.MaxReconnects(10),
		natsgo.DisconnectErrHandler(func(nc *natsgo.Conn, err error) {
			logger.Warn("nats disconnected", "error", err)
		}),
		natsgo.ReconnectHandler(func(nc *natsgo.Conn) {
			logger.Info("nats reconnected", "url", nc.ConnectedUrl())
		}),
	}

	conn, err := natsgo.Connect(cfg.NATSURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect to nats: %w", err)
	}

	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("create jetstream context: %w", err)
	}

	c := &Consumer{
		conn:       conn,
		js:         js,
		cfg:        cfg,
		handlers:   make(map[string]MessageHandler),
		maxDeliver: cfg.NATSMaxDeliver,
		dlqSubject: cfg.NATSSubjectDLQ,
	}

	// Register default handlers
	c.RegisterHandler("intelligence.card.created", c.handleCardCreated)
	c.RegisterHandler("intelligence.draft.generated", c.handleDraftGenerated)

	return c, nil
}

// RegisterHandler registers a message handler for a specific subject.
func (c *Consumer) RegisterHandler(subject string, handler MessageHandler) {
	c.handlers[subject] = handler
}

// Subscribe subscribes to all registered handlers.
func (c *Consumer) Subscribe(ctx context.Context) error {
	// Ensure streams exist
	if err := c.ensureStreams(); err != nil {
		return fmt.Errorf("ensure streams: %w", err)
	}

	for subject, handler := range c.handlers {
		sub, err := c.js.Subscribe(subject, func(msg *natsgo.Msg) {
			msgCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			logger.Debug("nats message received", "subject", msg.Subject, "data_len", len(msg.Data))

			if err := handler(msgCtx, msg); err != nil {
				logger.Error("nats message handler failed",
					"subject", msg.Subject,
					"error", err)

				// Check if max deliveries exceeded → send to DLQ
				if md, mdErr := msg.Metadata(); mdErr == nil && uint64(c.maxDeliver) > 0 && md.NumDelivered >= uint64(c.maxDeliver) {
					c.sendToDLQ(msg)
					return
				}

				msg.Nak()
				return
			}

			msg.Ack()
		}, natsgo.Durable("sync-"+sanitizeSubject(subject)),
			natsgo.ManualAck(),
			natsgo.MaxDeliver(c.maxDeliver),
			natsgo.AckWait(30*time.Second),
		)

		if err != nil {
			return fmt.Errorf("subscribe to %s: %w", subject, err)
		}

		c.subscriptions = append(c.subscriptions, sub)
		logger.Info("nats subscribed", "subject", subject)
	}

	return nil
}

// Close closes the NATS connection and all subscriptions.
func (c *Consumer) Close() {
	for _, sub := range c.subscriptions {
		sub.Unsubscribe()
	}
	if c.conn != nil {
		c.conn.Close()
	}
	logger.Info("nats consumer closed")
}

// sendToDLQ forwards a failed message to the dead-letter subject.
func (c *Consumer) sendToDLQ(msg *natsgo.Msg) {
	log := logger.With("dlq_subject", c.dlqSubject, "original_subject", msg.Subject)
	if err := c.js.Publish(c.dlqSubject, msg.Data); err != nil {
		log.Error("dlq publish failed", "error", err)
		msg.Nak()
		return
	}
	msg.Ack()
	log.Warn("message moved to DLQ after max deliveries")
}

// ensureStreams creates JetStream streams if they don't exist.
func (c *Consumer) ensureStreams() error {
	streams := []string{"intelligence", "notifications"}
	for _, stream := range streams {
		_, err := c.js.StreamInfo(stream)
		if err != nil {
			_, err = c.js.AddStream(&natsgo.StreamConfig{
				Name:     stream,
				Subjects: []string{stream + ".*>"},
				Retention: natsgo.WorkQueuePolicy,
				MaxMsgs:  100000,
				MaxAge:   7 * 24 * time.Hour,
				Storage:  natsgo.FileStorage,
			})
			if err != nil {
				logger.Warn("stream may already exist", "stream", stream, "error", err)
			}
		}
	}
	return nil
}

// ============================================================================
// DEFAULT HANDLERS
// ============================================================================

// handleCardCreated processes new card creation events from Intelligence.
func (c *Consumer) handleCardCreated(ctx context.Context, msg *natsgo.Msg) error {
	var payload struct {
		CardID      uuid.UUID `json:"card_id"`
		UserID      uuid.UUID `json:"user_id"`
		ThreadID    uuid.UUID `json:"thread_id"`
		FromField   []byte    `json:"from_field"`
		TheyWant    string    `json:"they_want"`
		Context     []byte    `json:"context"`
		NeedFromUser string   `json:"need_from_user"`
		UrgencyScore float64  `json:"urgency_score"`
		SuggestedDeadline *time.Time `json:"suggested_deadline,omitempty"`
	}

	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		return fmt.Errorf("unmarshal card created payload: %w", err)
	}

	logger.Info("card created event received",
		"card_id", payload.CardID,
		"user_id", payload.UserID,
		"urgency_score", payload.UrgencyScore,
	)

	// The actual persistence is handled by the sync service layer.
	// This handler simply logs and acknowledges for now.
	// In production, this would call a service method to persist the card.

	return nil
}

// handleDraftGenerated processes draft generation events from Intelligence.
func (c *Consumer) handleDraftGenerated(ctx context.Context, msg *natsgo.Msg) error {
	var payload struct {
		DraftID    uuid.UUID `json:"draft_id"`
		CardID     uuid.UUID `json:"card_id"`
		UserID     uuid.UUID `json:"user_id"`
		ThreadID   uuid.UUID `json:"thread_id"`
		DraftBody  string    `json:"draft_body"`
		SubjectLine *string  `json:"subject_line,omitempty"`
		ToneProfile *string  `json:"tone_profile,omitempty"`
		ModelUsed  *string   `json:"model_used,omitempty"`
		TokensUsed *int      `json:"tokens_used,omitempty"`
	}

	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		return fmt.Errorf("unmarshal draft generated payload: %w", err)
	}

	logger.Info("draft generated event received",
		"draft_id", payload.DraftID,
		"card_id", payload.CardID,
		"user_id", payload.UserID,
	)

	return nil
}

// sanitizeSubject converts NATS subject to a valid consumer name.
func sanitizeSubject(subject string) string {
	result := make([]byte, len(subject))
	for i, c := range subject {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			result[i] = byte(c)
		} else {
			result[i] = '_'
		}
	}
	return string(result)
}
