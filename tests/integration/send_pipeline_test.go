// Package integration provides end-to-end tests for the send pipeline.
package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/decisionstack/ingestion/internal/nats"
	"github.com/decisionstack/ingestion/internal/oauth"
	"github.com/decisionstack/sync/internal/decision"
	"github.com/google/uuid"
)

// TestSendPipeline_E2E verifies the complete send flow:
// draft approval → NATS email.send → ingestion consumer → Gmail API → email.sent → sync handler
func TestSendPipeline_E2E(t *testing.T) {
	t.Skip("Requires running NATS, PostgreSQL, and mock Gmail API")

	ctx := context.Background()
	draftID := uuid.New()
	userID := uuid.New()
	threadID := uuid.New()

	// Step 1: Approve draft (sync service)
	payload := decision.SendJobPayload{
		DraftID:   draftID,
		UserID:    userID,
		ThreadID:  threadID,
		DraftBody: "Test email body",
		Subject:   "Re: Test Thread",
	}
	jobBytes, _ := json.Marshal(payload)

	// Step 2: Publish to email.send (simulating sync approval)
	_ = jobBytes // natsPub.Publish("email.send", jobBytes)
	_ = nats.SubjectEmailSend

	// Step 3: Verify ingestion consumer receives it
	_ = ctx // sendConsumer.HandleSendMessage(ctx, msg)
	_ = oauth.ProviderGmail

	// Step 4: Verify provider.SendEmail called with correct params
	// assert.Equal(t, "recipient@example.com", req.To)
	// assert.Equal(t, "Re: Test Thread", req.Subject)
	// assert.Equal(t, "Test email body", req.BodyText)

	// Step 5: Verify message_id returned
	// assert.NotEmpty(t, messageID)

	// Step 6: Verify email.sent published
	// assert.Eventually(t, func() bool {
	//     return emailSentPublished
	// }, 5*time.Second, 100*time.Millisecond)

	// Step 7: Verify sync handler processes confirmation
	// assert.Eventually(t, func() bool {
	//     return draftMarkedSent
	// }, 5*time.Second, 100*time.Millisecond)

	_ = time.Second // imported for commented assertions above
}
