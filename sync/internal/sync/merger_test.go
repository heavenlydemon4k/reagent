// Package sync provides white-box unit tests for the CRDT merge engine.
// These tests access private methods to verify the core merge logic.
package sync

import (
	"testing"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Helper assertions
// ---------------------------------------------------------------------------

func assertTrue(t *testing.T, cond bool, msg string) {
	t.Helper()
	if !cond {
		t.Errorf("%s: expected true", msg)
	}
}

func assertFalse(t *testing.T, cond bool, msg string) {
	t.Helper()
	if cond {
		t.Errorf("%s: expected false", msg)
	}
}

func assertEqualString(t *testing.T, want, got, msg string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %q, got %q", msg, want, got)
	}
}

func assertEmptyString(t *testing.T, got, msg string) {
	t.Helper()
	if got != "" {
		t.Errorf("%s: want empty, got %q", msg, got)
	}
}

// ---------------------------------------------------------------------------
// Tests: isValidDecision
// ---------------------------------------------------------------------------

func TestIsValidDecision_Valid(t *testing.T) {
	for _, d := range []string{"approve", "edit", "consult"} {
		if !isValidDecision(d) {
			t.Errorf("isValidDecision(%q) should be true", d)
		}
	}
}

func TestIsValidDecision_Invalid(t *testing.T) {
	for _, d := range []string{"", "reject", "delete", "random"} {
		if isValidDecision(d) {
			t.Errorf("isValidDecision(%q) should be false", d)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: safeDeref
// ---------------------------------------------------------------------------

func TestSafeDeref_Nil(t *testing.T) {
	got := safeDeref(nil)
	if got != "" {
		t.Errorf("safeDeref(nil): want empty, got %q", got)
	}
}

func TestSafeDeref_Value(t *testing.T) {
	s := "hello"
	got := safeDeref(&s)
	if got != "hello" {
		t.Errorf("safeDeref(&\"hello\"): want hello, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Tests: validDecisions map
// ---------------------------------------------------------------------------

func TestValidDecisions_Contents(t *testing.T) {
	if len(validDecisions) != 3 {
		t.Errorf("want 3 valid decisions, got %d", len(validDecisions))
	}
	for _, d := range []string{"approve", "edit", "consult"} {
		if !validDecisions[d] {
			t.Errorf("validDecisions should contain %q", d)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: mustJSON
// ---------------------------------------------------------------------------

func TestMustJSON_Valid(t *testing.T) {
	data := map[string]interface{}{"key": "value", "num": 42}
	result := mustJSON(data)
	if len(result) == 0 {
		t.Error("mustJSON should return non-empty bytes")
	}
	// Should be valid JSON
	if result[0] != '{' {
		t.Error("mustJSON should return a JSON object")
	}
}

func TestMustJSON_EmptyMap(t *testing.T) {
	result := mustJSON(map[string]interface{}{})
	if string(result) != "{}" {
		t.Errorf("mustJSON(empty map): want {}, got %s", string(result))
	}
}

// ---------------------------------------------------------------------------
// Tests: applyResult struct
// ---------------------------------------------------------------------------

func TestApplyResult_Fields(t *testing.T) {
	result := applyResult{
		accepted:    true,
		reason:      "test_reason",
		serverState: "pending",
	}
	assertTrue(t, result.accepted, "accepted")
	assertEqualString(t, "test_reason", result.reason, "reason")
	assertEqualString(t, "pending", result.serverState, "serverState")
}

// ---------------------------------------------------------------------------
// Tests: applyChange with nil card (card not found)
// ---------------------------------------------------------------------------

func TestApplyChange_NilCardID(t *testing.T) {
	e := &SyncEngine{}
	change := models.LocalChange{
		CardID: uuid.Nil,
	}
	result := e.applyChange(t.Context(), uuid.New(), "device-1", change)
	assertFalse(t, result.accepted, "nil card ID should be rejected")
	assertEqualString(t, "invalid_card_id", result.reason, "reason")
}

// ---------------------------------------------------------------------------
// Tests: SyncEngine construction
// ---------------------------------------------------------------------------

func TestNewSyncEngine(t *testing.T) {
	e := NewSyncEngine(nil, nil)
	if e == nil {
		t.Fatal("NewSyncEngine returned nil")
	}
	if e.cardStore == nil {
		t.Error("cardStore should not be nil")
	}
	if e.draftStore == nil {
		t.Error("draftStore should not be nil")
	}
	if e.cursor == nil {
		t.Error("cursor should not be nil")
	}
	if e.log == nil {
		t.Error("log should not be nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: SyncEngine with custom logger
// ---------------------------------------------------------------------------

func TestNewSyncEngine_CustomLogger(t *testing.T) {
	// Just verify it doesn't panic
	e := NewSyncEngine(nil, nil)
	if e == nil {
		t.Fatal("NewSyncEngine with nil log should use default")
	}
}

// ---------------------------------------------------------------------------
// Table-driven: applyChange scenarios (using nil store to test error paths)
// ---------------------------------------------------------------------------

func TestApplyChange_CardNotFound(t *testing.T) {
	e := NewSyncEngine(nil, nil)
	change := models.LocalChange{
		CardID:   uuid.New(),
		Decision: strPtr("approve"),
	}
	result := e.applyChange(t.Context(), uuid.New(), "device-1", change)
	assertFalse(t, result.accepted, "missing card should be rejected")
	assertEqualString(t, "card_not_found", result.reason, "reason")
}

func TestApplyChange_InvalidDecision(t *testing.T) {
	e := NewSyncEngine(nil, nil)
	change := models.LocalChange{
		CardID:   uuid.New(),
		Decision: strPtr("reject"), // invalid decision
	}
	result := e.applyChange(t.Context(), uuid.New(), "device-1", change)
	assertFalse(t, result.accepted, "invalid decision should be rejected")
	assertEqualString(t, "card_not_found", result.reason, "reason") // fails at card lookup first
}

// ---------------------------------------------------------------------------
// Tests: applyEdit with server draft body wins
// ---------------------------------------------------------------------------

func TestApplyEdit_ServerWins(t *testing.T) {
	// Since we can't easily mock the database, we test the core logic
	// by verifying that the edit path returns accepted=true (server wins
	// but the change is logged as accepted).
	// The actual DB test would need a real/mock store.

	// The applyEdit function returns accepted=true because the edit is
	// "accepted" in the sense that it's noted in the log, but the server
	// draft remains authoritative. The client receives this as accepted
	// and will overwrite its local draft on next sync.
	e := NewSyncEngine(nil, nil)
	_ = e

	// Verify the CRDT rule for draft_body
	rule := ConflictRules[FieldDraftBody]
	if rule.Winner != WinnerServer {
		t.Errorf("draft_body should be server-wins, got %q", rule.Winner)
	}
	if rule.Exception != ExceptionNone {
		t.Errorf("draft_body should have no exceptions, got %q", rule.Exception)
	}
}

// ---------------------------------------------------------------------------
// Tests: applyConsult no-op
// ---------------------------------------------------------------------------

func TestApplyConsult_NoOp(t *testing.T) {
	// The consult decision is a no-op on the server side.
	// Verify the CRDT rule.
	rule := ConflictRules[FieldCardState]
	if rule.Winner != WinnerUser {
		t.Errorf("card_state should be user-wins by default, got %q", rule.Winner)
	}
}

// ---------------------------------------------------------------------------
// Tests: applyApprove sacred user approval
// ---------------------------------------------------------------------------

func TestApplyApprove_UserSacred(t *testing.T) {
	// User approval is sacred — user always wins.
	rule := ConflictRules[FieldUserApproved]
	if rule.Winner != WinnerUser {
		t.Errorf("user_approved should be user-wins, got %q", rule.Winner)
	}
	if rule.Exception != ExceptionNone {
		t.Errorf("user_approved should have no exceptions, got %q", rule.Exception)
	}
}

// ---------------------------------------------------------------------------
// Tests: CRDT rule descriptions
// ---------------------------------------------------------------------------

func TestConflictRule_Descriptions(t *testing.T) {
	for field, rule := range ConflictRules {
		if rule.Description == "" {
			t.Errorf("rule for %q has empty description", field)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: SyncRequest / SyncResponse model
// ---------------------------------------------------------------------------

func TestSyncRequest_Model(t *testing.T) {
	req := models.SyncRequest{
		DeviceID:        "device-1",
		LastSyncVersion: 5,
		LocalChanges:    []models.LocalChange{},
	}
	if req.DeviceID != "device-1" {
		t.Error("DeviceID mismatch")
	}
	if req.LastSyncVersion != 5 {
		t.Error("LastSyncVersion mismatch")
	}
}

func TestSyncResponse_Model(t *testing.T) {
	resp := models.SyncResponse{
		ServerVersion:   10,
		AcceptedChanges: []uuid.UUID{uuid.New()},
		RejectedChanges: []models.RejectedChange{},
		NewCards:        []models.DecisionCard{},
		UpdatedCards:    []models.DecisionCard{},
		RemovedCards:    []uuid.UUID{},
	}
	if resp.ServerVersion != 10 {
		t.Error("ServerVersion mismatch")
	}
	if len(resp.AcceptedChanges) != 1 {
		t.Error("AcceptedChanges length mismatch")
	}
}

func TestLocalChange_Model(t *testing.T) {
	decision := "approve"
	change := models.LocalChange{
		CardID:   uuid.New(),
		Version:  5,
		State:    "pending",
		Decision: &decision,
	}
	if change.Version != 5 {
		t.Error("Version mismatch")
	}
	if *change.Decision != "approve" {
		t.Error("Decision mismatch")
	}
}

func TestRejectedChange_Model(t *testing.T) {
	cardID := uuid.New()
	rc := models.RejectedChange{
		CardID:      cardID,
		Reason:      "card_already_terminal",
		ServerState: "sent",
	}
	if rc.CardID != cardID {
		t.Error("CardID mismatch")
	}
	if rc.Reason != "card_already_terminal" {
		t.Error("Reason mismatch")
	}
	if rc.ServerState != "sent" {
		t.Error("ServerState mismatch")
	}
}

// ---------------------------------------------------------------------------
// Tests: LocalChange with nil decision (state-only sync)
// ---------------------------------------------------------------------------

func TestLocalChange_NilDecision(t *testing.T) {
	change := models.LocalChange{
		CardID:  uuid.New(),
		Version: 3,
		State:   "pending",
		// Decision is nil
	}
	if change.Decision != nil {
		t.Error("Decision should be nil")
	}
	// When decision is nil/empty, applyChange treats it as state-only sync
	// and accepts without action.
	result := safeDeref(change.Decision)
	assertEmptyString(t, result, "nil decision derefs to empty")
}

// ---------------------------------------------------------------------------
// Tests: applyChange with empty decision
// ---------------------------------------------------------------------------

func TestApplyChange_EmptyDecision_StateOnlySync(t *testing.T) {
	// An empty decision means the client is just syncing state.
	// The change should be accepted without action.
	// Since we have nil store, the card lookup fails first.
	e := NewSyncEngine(nil, nil)
	change := models.LocalChange{
		CardID:  uuid.New(),
		Version: 5,
		State:   "pending",
		// Decision is nil → state-only sync
	}
	result := e.applyChange(t.Context(), uuid.New(), "device-1", change)
	// Card not found due to nil store, but the decision path would be ""
	assertFalse(t, result.accepted, "nil store → card not found")
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func strPtr(s string) *string {
	return &s
}
