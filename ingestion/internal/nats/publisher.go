// Package nats provides the NATS JetStream publisher implementation for the
// Ingestion Mesh. It handles event publishing with retry logic and DLQ fallback.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	// maxPublishRetries is the number of publish attempts before DLQ.
	maxPublishRetries = 3
	// retryBaseDelay is the initial backoff delay between retries.
	retryBaseDelay = 500 * time.Millisecond
	// retryMaxDelay caps the exponential backoff.
	retryMaxDelay = 5 * time.Second
)

// ReliablePublisher wraps JetStreamPublisher with retry logic and DLQ fallback.
// It implements the Publisher interface with enhanced reliability.
type ReliablePublisher struct {
	inner *JetStreamPublisher
}

// NewPublisher connects to NATS, creates/ensures all streams, and returns a Publisher
// with retry logic and DLQ fallback.
func NewPublisher(natsURL string) (Publisher, error) {
	inner, err := NewJetStreamPublisher(natsURL)
	if err != nil {
		return nil, fmt.Errorf("new jetstream publisher: %w", err)
	}
	return &ReliablePublisher{inner: inner}, nil
}

// PublishEmailIngested publishes an email.ingested event with retry and DLQ fallback.
// It attempts the publish up to 3 times with exponential backoff, then sends to DLQ.
func (p *ReliablePublisher) PublishEmailIngested(ctx context.Context, event EmailIngestedEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	// Attempt publish with exponential backoff retries
	var lastErr error
	for attempt := 0; attempt < maxPublishRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 500ms, 1s, 2s
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			if delay > retryMaxDelay {
				delay = retryMaxDelay
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("publish cancelled: %w", ctx.Err())
			case <-time.After(delay):
			}
		}

		_, err = p.inner.js.Publish(SubjectEmailIngested, data)
		if err == nil {
			return nil
		}

		lastErr = err
	}

	// All retries exhausted — send to DLQ
	if dlqErr := p.publishToDLQ(ctx, data); dlqErr != nil {
		return fmt.Errorf("publish failed after %d retries and DLQ also failed: primary=%w, dlq=%v",
			maxPublishRetries, lastErr, dlqErr)
	}

	return fmt.Errorf("published to DLQ after %d failed attempts: %w", maxPublishRetries, lastErr)
}

// publishToDLQ sends a failed message to the dead-letter queue.
func (p *ReliablePublisher) publishToDLQ(ctx context.Context, data []byte) error {
	dlqMsg := map[string]interface{}{
		"original_subject": SubjectEmailIngested,
		"data":             json.RawMessage(data),
		"failed_at":        time.Now().UTC().Format(time.RFC3339),
		"reason":           "max retries exceeded",
	}

	dlqData, err := json.Marshal(dlqMsg)
	if err != nil {
		return fmt.Errorf("marshal dlq message: %w", err)
	}

	_, err = p.inner.js.Publish(SubjectEmailIngestedDLQ, dlqData)
	if err != nil {
		return fmt.Errorf("publish to dlq: %w", err)
	}

	return nil
}

// PublishSendJob publishes a send job with retry and DLQ fallback.
func (p *ReliablePublisher) PublishSendJob(ctx context.Context, payload SendJobPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal send job: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < maxPublishRetries; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			if delay > retryMaxDelay {
				delay = retryMaxDelay
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("publish cancelled: %w", ctx.Err())
			case <-time.After(delay):
			}
		}
		_, err = p.inner.js.Publish(SubjectEmailSend, data)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("publish send job failed after %d retries: %w", maxPublishRetries, lastErr)
}

// HealthCheck verifies NATS connection and stream health.
func (p *ReliablePublisher) HealthCheck() error {
	if p.inner.nc == nil || !p.inner.nc.IsConnected() {
		return fmt.Errorf("nats: not connected")
	}
	// Verify all streams exist
	for name := range StreamConfigs {
		_, err := p.inner.js.StreamInfo(name)
		if err != nil {
			return fmt.Errorf("nats stream %s: %w", name, err)
		}
	}
	return nil
}

// Close closes the NATS connection.
func (p *ReliablePublisher) Close() error {
	return p.inner.Close()
}
