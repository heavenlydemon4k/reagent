package nats

import (
	"github.com/decisionstack/sync/internal/decision"
	natsgo "github.com/nats-io/nats.go"
)

// SyncNatsAdapter wraps a JetStream context to match the decision.NatsPublisher
// interface (Publish(subject string, data []byte) error).
type SyncNatsAdapter struct {
	js natsgo.JetStreamContext
}

// NewSyncNatsAdapter creates an adapter from a JetStream context.
func NewSyncNatsAdapter(js natsgo.JetStreamContext) *SyncNatsAdapter {
	return &SyncNatsAdapter{js: js}
}

// Publish implements decision.NatsPublisher by delegating to JetStream.
func (a *SyncNatsAdapter) Publish(subject string, data []byte) error {
	_, err := a.js.Publish(subject, data)
	return err
}

// Compile-time check that SyncNatsAdapter implements decision.NatsPublisher.
var _ decision.NatsPublisher = (*SyncNatsAdapter)(nil)
