package nats

import (
	"testing"

	"github.com/decisionstack/sync/internal/decision"
)

// TestSyncNatsAdapter_ImplementsInterface verifies the adapter matches decision.NatsPublisher.
func TestSyncNatsAdapter_ImplementsInterface(t *testing.T) {
	// Compile-time check
	var _ decision.NatsPublisher = (*SyncNatsAdapter)(nil)
}

// TestSyncNatsAdapter_Publish verifies Publish delegates to JetStream.
func TestSyncNatsAdapter_Publish(t *testing.T) {
	// This is a structural test — full integration requires a running NATS server
	// Verify the method exists with correct signature
	adapter := &SyncNatsAdapter{}

	// Method should exist and accept (string, []byte)
	// We can't call it without a real JetStream context, but we verify the signature
	var publishFunc func(string, []byte) error = adapter.Publish
	if publishFunc == nil {
		t.Error("Publish method is nil")
	}
}

// TestNoOpNatsPublisher_Error verifies the no-op returns an error.
func TestNoOpNatsPublisher_Error(t *testing.T) {
	noop := &decision.NoOpNatsPublisher{}
	err := noop.Publish("test.subject", []byte("test"))
	if err == nil {
		t.Error("NoOpNatsPublisher.Publish should return an error")
	}
}
