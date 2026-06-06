// Package nats provides NATS publishing capabilities for notifications.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/logger"
	natsgo "github.com/nats-io/nats.go"

	"github.com/decisionstack/sync/internal/circuitbreaker"
)

// Publisher sends events to NATS JetStream for cross-context communication.
type Publisher struct {
	conn    *natsgo.Conn
	js      natsgo.JetStreamContext
	cfg     *config.Config
	breaker *circuitbreaker.CircuitBreaker
}

// NewPublisher creates a new NATS publisher.
func NewPublisher(cfg *config.Config) (*Publisher, error) {
	opts := []natsgo.Option{
		natsgo.Name("sync-publisher"),
		natsgo.ReconnectWait(2 * time.Second),
		natsgo.MaxReconnects(10),
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

	logger.Info("nats publisher connected", "url", cfg.NATSURL)

	return &Publisher{
		conn:    conn,
		js:      js,
		cfg:     cfg,
		breaker: circuitbreaker.NewWithPreset("nats"),
	}, nil
}

// Close closes the publisher connection.
func (p *Publisher) Close() {
	if p.conn != nil {
		p.conn.Close()
	}
	logger.Info("nats publisher closed")
}

// Publish sends a message to a NATS subject.
// Protected by circuit breaker — fails fast if NATS is unreachable.
func (p *Publisher) Publish(ctx context.Context, subject string, data []byte) error {
	return p.breaker.Call(func() error {
		_, err := p.js.Publish(subject, data)
		if err != nil {
			return fmt.Errorf("publish to %s: %w", subject, err)
		}
		return nil
	})
}

// PublishJSON marshals and sends a JSON payload to a NATS subject.
// Protected by circuit breaker — fails fast if NATS is unreachable.
func (p *Publisher) PublishJSON(ctx context.Context, subject string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	return p.Publish(ctx, subject, data)
}

// PublishNotification sends a notification event to the notifications stream.
func (p *Publisher) PublishNotification(ctx context.Context, notifType string, payload any) error {
	subject := fmt.Sprintf("notifications.%s", notifType)
	return p.PublishJSON(ctx, subject, payload)
}

// PublishCardDecision sends a card decision event to the decisions stream.
func (p *Publisher) PublishCardDecision(ctx context.Context, payload any) error {
	return p.PublishJSON(ctx, "intelligence.decision.made", payload)
}

// PublishConsultationRequest sends a consultation request to the intelligence service.
func (p *Publisher) PublishConsultationRequest(ctx context.Context, payload any) error {
	return p.PublishJSON(ctx, "intelligence.consult.request", payload)
}