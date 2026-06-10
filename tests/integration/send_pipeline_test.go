// Package integration provides end-to-end tests for the send pipeline.
//
// NOTE ON IMPORTS: this module cannot import service packages directly —
// Go's internal-package rule forbids importing
// github.com/decisionstack/ingestion/internal/... or
// github.com/decisionstack/sync/internal/... from outside those module
// trees, replace directives notwithstanding. The payload structs below are
// therefore local mirrors of the canonical definitions. Keep them in sync:
//
//   SendJobPayload   → sync/internal/decision        (approval flow)
//   SubjectEmailSend → ingestion/internal/nats       (subject constant)
//
// The test is skipped unless live infrastructure (NATS, Postgres, a mock
// Gmail API) is available; it exists to document the Phase 8 verification
// flow and to keep the module compiling in CI.
package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

// subjectEmailSend mirrors nats.SubjectEmailSend in the ingestion service.
const subjectEmailSend = "email.send"

// sendJobPayload mirrors decision.SendJobPayload in the sync service.
type sendJobPayload struct {
	DraftID   string   `json:"draft_id"`
	UserID    string   `json:"user_id"`
	ThreadID  string   `json:"thread_id"`
	DraftBody string   `json:"draft_body"`
	Subject   string   `json:"subject"`
	InReplyTo *string  `json:"in_reply_to,omitempty"`
	Refs      []string `json:"references,omitempty"`
}

// newID generates a random hex ID (stand-in for uuid.New, avoiding the
// dependency so this module needs no go.sum).
func newID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// TestSendPipeline_E2E verifies the complete send flow:
// draft approval → NATS email.send → ingestion consumer → Gmail API →
// email.sent → sync handler.
//
// Phase 8 checkpoints (see PLAN.md §8):
//  1. Approve draft via sync REST  → SendJobPayload built
//  2. Publish to email.send        → JetStream EMAIL_SEND stream
//  3. Ingestion send consumer      → provider.SendEmail called
//  4. message_id returned          → email.sent published
//  5. Sync handler                 → draft marked sent
func TestSendPipeline_E2E(t *testing.T) {
	t.Skip("Requires running NATS, PostgreSQL, and mock Gmail API")

	ctx := context.Background()

	// Step 1: Approve draft (sync service builds the send job).
	payload := sendJobPayload{
		DraftID:   newID(t),
		UserID:    newID(t),
		ThreadID:  newID(t),
		DraftBody: "Test email body",
		Subject:   "Re: Test Thread",
	}
	jobBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	// Step 2: Publish to email.send (simulating sync approval).
	_ = jobBytes // natsPub.Publish(subjectEmailSend, jobBytes)
	_ = subjectEmailSend

	// Step 3: Verify ingestion send consumer receives it.
	_ = ctx // sendConsumer.HandleSendMessage(ctx, msg)

	// Step 4: Verify provider.SendEmail called with correct params.
	// assert.Equal(t, "recipient@example.com", req.To)
	// assert.Equal(t, "Re: Test Thread", req.Subject)
	// assert.Equal(t, "Test email body", req.BodyText)

	// Step 5: Verify message_id returned and email.sent published.
	// assert.Eventually(t, func() bool { return emailSentPublished },
	//     5*time.Second, 100*time.Millisecond)

	// Step 6: Verify sync handler marks the draft sent.
	// assert.Eventually(t, func() bool { return draftMarkedSent },
	//     5*time.Second, 100*time.Millisecond)

	_ = time.Second // referenced by the commented assertions above
}
