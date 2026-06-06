// Package models_test provides unit tests for shared data models.
package models

import (
	"encoding/json"
	"testing"
	"time"

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

func assertEqualInt(t *testing.T, want, got int, msg string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %d, got %d", msg, want, got)
	}
}

func assertEqualFloat(t *testing.T, want, got, delta float64, msg string) {
	t.Helper()
	if got < want-delta || got > want+delta {
		t.Errorf("%s: want %f ±%f, got %f", msg, want, delta, got)
	}
}

func assertNotNil(t *testing.T, v interface{}, msg string) {
	t.Helper()
	if v == nil {
		t.Errorf("%s: expected non-nil", msg)
	}
}

// ---------------------------------------------------------------------------
// Tests: DecisionCard model
// ---------------------------------------------------------------------------

func TestDecisionCard_Model(t *testing.T) {
	now := time.Now().UTC()
	card := DecisionCard{
		ID:              uuid.New(),
		UserID:          uuid.New(),
		ThreadID:        uuid.New(),
		SourceAccountID: uuid.New(),
		CardState:       "pending",
		TheyWant:        "schedule a meeting",
		NeedFromUser:    "approve or edit the draft",
		UrgencyScore:    0.85,
		ServerVersion:   3,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if card.CardState != "pending" {
		t.Errorf("CardState: want pending, got %q", card.CardState)
	}
	assertEqualFloat(t, 0.85, card.UrgencyScore, 0.001, "UrgencyScore")
	assertEqualInt(t, 3, card.ServerVersion, "ServerVersion")
	assertEqualString(t, "schedule a meeting", card.TheyWant, "TheyWant")
}

func TestDecisionCard_JSONSerialization(t *testing.T) {
	card := DecisionCard{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		CardState: "pending",
		TheyWant:  "test want",
	}

	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded DecisionCard
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ID != card.ID {
		t.Error("ID mismatch after round-trip")
	}
	if decoded.CardState != card.CardState {
		t.Error("CardState mismatch after round-trip")
	}
	if decoded.TheyWant != card.TheyWant {
		t.Error("TheyWant mismatch after round-trip")
	}
}

func TestDecisionCard_ValidCardStates(t *testing.T) {
	validStates := []string{"pending", "consulting", "drafting", "approved", "sent", "archived", "expired"}
	for _, state := range validStates {
		card := DecisionCard{CardState: state}
		if card.CardState != state {
			t.Errorf("state %q not set correctly", state)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: Draft model
// ---------------------------------------------------------------------------

func TestDraft_Model(t *testing.T) {
	now := time.Now().UTC()
	subject := "Test Subject"
	draft := Draft{
		ID:           uuid.New(),
		CardID:       uuid.New(),
		UserID:       uuid.New(),
		ThreadID:     uuid.New(),
		DraftBody:    "This is a draft email body.",
		SubjectLine:  &subject,
		UserApproved: false,
		CreatedAt:    now,
	}

	assertEqualString(t, "This is a draft email body.", draft.DraftBody, "DraftBody")
	assertFalse(t, draft.UserApproved, "UserApproved")
	if draft.SubjectLine == nil || *draft.SubjectLine != subject {
		t.Error("SubjectLine mismatch")
	}
}

func TestDraft_JSONSerialization(t *testing.T) {
	subject := "Re: Meeting"
	draft := Draft{
		ID:        uuid.New(),
		DraftBody: "Hello, let's meet.",
		SubjectLine: &subject,
	}

	data, err := json.Marshal(draft)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Draft
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.DraftBody != draft.DraftBody {
		t.Error("DraftBody mismatch")
	}
}

// ---------------------------------------------------------------------------
// Tests: SyncRequest model
// ---------------------------------------------------------------------------

func TestSyncRequest_Model(t *testing.T) {
	decision := "approve"
	req := SyncRequest{
		DeviceID:        "device-ios-001",
		LastSyncVersion: 42,
		LocalChanges: []LocalChange{
			{
				CardID:   uuid.New(),
				Version:  5,
				State:    "pending",
				Decision: &decision,
			},
		},
	}

	assertEqualString(t, "device-ios-001", req.DeviceID, "DeviceID")
	assertEqualInt(t, 42, req.LastSyncVersion, "LastSyncVersion")
	assertEqualInt(t, 1, len(req.LocalChanges), "LocalChanges count")
}

func TestSyncRequest_JSONSerialization(t *testing.T) {
	req := SyncRequest{
		DeviceID:        "device-1",
		LastSyncVersion: 10,
		LocalChanges:    []LocalChange{},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded SyncRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.DeviceID != req.DeviceID {
		t.Error("DeviceID mismatch")
	}
	if decoded.LastSyncVersion != req.LastSyncVersion {
		t.Error("LastSyncVersion mismatch")
	}
}

// ---------------------------------------------------------------------------
// Tests: LocalChange model
// ---------------------------------------------------------------------------

func TestLocalChange_WithDecision(t *testing.T) {
	decision := "approve"
	draftBody := "edited draft"
	approvedDraftID := uuid.New()
	change := LocalChange{
		CardID:          uuid.New(),
		Version:         7,
		State:           "drafting",
		Decision:        &decision,
		DraftBody:       &draftBody,
		ApprovedDraftID: &approvedDraftID,
	}

	if change.Decision == nil || *change.Decision != "approve" {
		t.Error("Decision mismatch")
	}
	if change.DraftBody == nil || *change.DraftBody != "edited draft" {
		t.Error("DraftBody mismatch")
	}
	if change.ApprovedDraftID == nil || *change.ApprovedDraftID != approvedDraftID {
		t.Error("ApprovedDraftID mismatch")
	}
	assertEqualInt(t, 7, change.Version, "Version")
}

func TestLocalChange_NilDecision(t *testing.T) {
	change := LocalChange{
		CardID:  uuid.New(),
		Version: 3,
		State:   "pending",
	}
	if change.Decision != nil {
		t.Error("Decision should be nil")
	}
	if change.DraftBody != nil {
		t.Error("DraftBody should be nil")
	}
	if change.ApprovedDraftID != nil {
		t.Error("ApprovedDraftID should be nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: SyncResponse model
// ---------------------------------------------------------------------------

func TestSyncResponse_Model(t *testing.T) {
	resp := SyncResponse{
		ServerVersion:   100,
		AcceptedChanges: []uuid.UUID{uuid.New(), uuid.New()},
		RejectedChanges: []RejectedChange{
			{CardID: uuid.New(), Reason: "terminal", ServerState: "sent"},
		},
		NewCards:     []DecisionCard{{ID: uuid.New(), CardState: "pending"}},
		UpdatedCards: []DecisionCard{},
		RemovedCards: []uuid.UUID{uuid.New()},
	}

	assertEqualInt(t, 100, resp.ServerVersion, "ServerVersion")
	assertEqualInt(t, 2, len(resp.AcceptedChanges), "AcceptedChanges")
	assertEqualInt(t, 1, len(resp.RejectedChanges), "RejectedChanges")
	assertEqualInt(t, 1, len(resp.NewCards), "NewCards")
	assertEqualInt(t, 0, len(resp.UpdatedCards), "UpdatedCards")
	assertEqualInt(t, 1, len(resp.RemovedCards), "RemovedCards")
}

// ---------------------------------------------------------------------------
// Tests: RejectedChange model
// ---------------------------------------------------------------------------

func TestRejectedChange_Model(t *testing.T) {
	cardID := uuid.New()
	rc := RejectedChange{
		CardID:      cardID,
		Reason:      "card_already_terminal",
		ServerState: "sent",
	}
	assertEqualString(t, "card_already_terminal", rc.Reason, "Reason")
	assertEqualString(t, "sent", rc.ServerState, "ServerState")
	if rc.CardID != cardID {
		t.Error("CardID mismatch")
	}
}

// ---------------------------------------------------------------------------
// Tests: BatchInfo model
// ---------------------------------------------------------------------------

func TestBatchInfo_Model(t *testing.T) {
	bi := BatchInfo{
		Size:                      5,
		EstimatedClearTimeMinutes: 8,
		Cards:                     []DecisionCard{},
	}
	assertEqualInt(t, 5, bi.Size, "Size")
	assertEqualInt(t, 8, bi.EstimatedClearTimeMinutes, "EstimatedClearTimeMinutes")
}

// ---------------------------------------------------------------------------
// Tests: DeviceSession model
// ---------------------------------------------------------------------------

func TestDeviceSession_Model(t *testing.T) {
	now := time.Now().UTC()
	fcm := "fcm-token-123"
	apns := "apns-token-456"
	session := DeviceSession{
		ID:           uuid.New(),
		UserID:       uuid.New(),
		DeviceID:     "device-ios-001",
		DeviceType:   "ios",
		DeviceName:   "Test iPhone",
		FCMToken:     &fcm,
		APNSToken:    &apns,
		LastActiveAt: now,
		CreatedAt:    now,
	}

	assertEqualString(t, "device-ios-001", session.DeviceID, "DeviceID")
	assertEqualString(t, "ios", session.DeviceType, "DeviceType")
	assertEqualString(t, "Test iPhone", session.DeviceName, "DeviceName")
	if session.FCMToken == nil || *session.FCMToken != fcm {
		t.Error("FCMToken mismatch")
	}
	if session.APNSToken == nil || *session.APNSToken != apns {
		t.Error("APNSToken mismatch")
	}
}

func TestDeviceSession_ValidDeviceTypes(t *testing.T) {
	for _, deviceType := range []string{"ios", "android"} {
		s := DeviceSession{DeviceType: deviceType}
		if s.DeviceType != deviceType {
			t.Errorf("device type %q not preserved", deviceType)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: TokenResponse model
// ---------------------------------------------------------------------------

func TestTokenResponse_Model(t *testing.T) {
	now := time.Now().UTC()
	tr := TokenResponse{
		AccessToken:  "access-token-123",
		RefreshToken: "refresh-token-456",
		ExpiresAt:    now,
	}
	assertEqualString(t, "access-token-123", tr.AccessToken, "AccessToken")
	assertEqualString(t, "refresh-token-456", tr.RefreshToken, "RefreshToken")
	if tr.ExpiresAt != now {
		t.Error("ExpiresAt mismatch")
	}
}

// ---------------------------------------------------------------------------
// Tests: UserQueue model
// ---------------------------------------------------------------------------

func TestUserQueue_Model(t *testing.T) {
	now := time.Now().UTC()
	lastNotif := now.Add(-time.Hour)
	uq := UserQueue{
		UserID:             uuid.New(),
		PendingCount:       5,
		ServerVersion:      42,
		LastNotificationAt: &lastNotif,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	assertEqualInt(t, 5, uq.PendingCount, "PendingCount")
	assertEqualInt(t, 42, uq.ServerVersion, "ServerVersion")
	if uq.LastNotificationAt == nil || !uq.LastNotificationAt.Equal(lastNotif) {
		t.Error("LastNotificationAt mismatch")
	}
}

// ---------------------------------------------------------------------------
// Tests: Notification model
// ---------------------------------------------------------------------------

func TestNotification_Model(t *testing.T) {
	now := time.Now().UTC()
	n := Notification{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		Type:      "batch",
		Title:     "5 cards pending",
		Body:      "You have 5 decision cards to clear",
		CreatedAt: now,
	}
	assertEqualString(t, "batch", n.Type, "Type")
	assertEqualString(t, "5 cards pending", n.Title, "Title")
	assertEqualString(t, "You have 5 decision cards to clear", n.Body, "Body")
	if n.SentAt != nil {
		t.Error("SentAt should be nil")
	}
	if n.ReadAt != nil {
		t.Error("ReadAt should be nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: WSEvent model
// ---------------------------------------------------------------------------

func TestWSEvent_Model(t *testing.T) {
	now := time.Now().UTC()
	evt := WSEvent{
		Type:           WSEventSpawn,
		CardID:         uuid.New(),
		Text:           "hello world",
		TriggerWord:    "make",
		CursorPosition: 42,
		Timestamp:      now,
	}
	assertEqualString(t, string(WSEventSpawn), string(evt.Type), "Type")
	assertEqualString(t, "hello world", evt.Text, "Text")
	assertEqualString(t, "make", evt.TriggerWord, "TriggerWord")
	assertEqualInt(t, 42, evt.CursorPosition, "CursorPosition")
}

func TestWSEventType_Constants(t *testing.T) {
	assertEqualString(t, "spawn", string(WSEventSpawn), "WSEventSpawn")
	assertEqualString(t, "paragraph", string(WSEventParagraph), "WSEventParagraph")
	assertEqualString(t, "accept", string(WSEventAccept), "WSEventAccept")
	assertEqualString(t, "edit", string(WSEventEdit), "WSEventEdit")
	assertEqualString(t, "delegate", string(WSEventDelegate), "WSEventDelegate")
	assertEqualString(t, "ping", string(WSEventPing), "WSEventPing")
}

// ---------------------------------------------------------------------------
// Tests: SyncError model
// ---------------------------------------------------------------------------

func TestSyncError_Model(t *testing.T) {
	err := SyncError{
		Code:    "auth_expired",
		Message: "Your session has expired",
		Retry:   false,
	}
	assertEqualString(t, "auth_expired", err.Code, "Code")
	assertEqualString(t, "Your session has expired", err.Message, "Message")
	assertFalse(t, err.Retry, "Retry")
	if err.Error() != "Your session has expired" {
		t.Errorf("Error() method: want %q, got %q", "Your session has expired", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Tests: Error code constants
// ---------------------------------------------------------------------------

func TestErrCodeConstants(t *testing.T) {
	assertEqualString(t, "auth_expired", ErrCodeAuthExpired, "ErrCodeAuthExpired")
	assertEqualString(t, "version_conflict", ErrCodeVersionConflict, "ErrCodeVersionConflict")
	assertEqualString(t, "card_not_found", ErrCodeCardNotFound, "ErrCodeCardNotFound")
	assertEqualString(t, "draft_not_found", ErrCodeDraftNotFound, "ErrCodeDraftNotFound")
	assertEqualString(t, "queue_empty", ErrCodeQueueEmpty, "ErrCodeQueueEmpty")
	assertEqualString(t, "rate_limited", ErrCodeRateLimited, "ErrCodeRateLimited")
}

// ---------------------------------------------------------------------------
// Tests: DecideRequest / DecideResponse models
// ---------------------------------------------------------------------------

func TestDecideRequest_Model(t *testing.T) {
	cardID := uuid.New()
	input := "make it shorter"
	req := DecideRequest{
		CardID:   cardID,
		Decision: "edit",
		Input:    &input,
	}
	if req.CardID != cardID {
		t.Error("CardID mismatch")
	}
	assertEqualString(t, "edit", req.Decision, "Decision")
	if req.Input == nil || *req.Input != "make it shorter" {
		t.Error("Input mismatch")
	}
}

func TestDecideResponse_Model(t *testing.T) {
	draftID := uuid.New()
	subject := "Re: Test"
	resp := DecideResponse{
		DraftID:     draftID,
		DraftBody:   "This is the draft.",
		SubjectLine: &subject,
	}
	if resp.DraftID != draftID {
		t.Error("DraftID mismatch")
	}
	assertEqualString(t, "This is the draft.", resp.DraftBody, "DraftBody")
}

// ---------------------------------------------------------------------------
// Tests: ConsultRequest / ConsultResponse models
// ---------------------------------------------------------------------------

func TestConsultRequest_Model(t *testing.T) {
	cardID := uuid.New()
	req := ConsultRequest{
		CardID:   cardID,
		Question: "What does this person want?",
	}
	if req.CardID != cardID {
		t.Error("CardID mismatch")
	}
	assertEqualString(t, "What does this person want?", req.Question, "Question")
}

func TestConsultResponse_Model(t *testing.T) {
	resp := ConsultResponse{
		Answer:         "They want to schedule a meeting.",
		Citations:      []ChunkCitation{{ChunkID: uuid.New(), VerbatimSnippet: "meeting"}},
		TurnsRemaining: 3,
	}
	assertEqualString(t, "They want to schedule a meeting.", resp.Answer, "Answer")
	assertEqualInt(t, 1, len(resp.Citations), "Citations")
	assertEqualInt(t, 3, resp.TurnsRemaining, "TurnsRemaining")
}

func TestChunkCitation_Model(t *testing.T) {
	c := ChunkCitation{
		ChunkID:         uuid.New(),
		VerbatimSnippet: "Hello world",
		EmailID:         uuid.New(),
		ParagraphIndex:  2,
	}
	assertEqualString(t, "Hello world", c.VerbatimSnippet, "VerbatimSnippet")
	assertEqualInt(t, 2, c.ParagraphIndex, "ParagraphIndex")
}

// ---------------------------------------------------------------------------
// Tests: CalendarEvent model
// ---------------------------------------------------------------------------

func TestCalendarEvent_Model(t *testing.T) {
	now := time.Now().UTC()
	evt := CalendarEvent{
		ID:              uuid.New(),
		UserID:          uuid.New(),
		SourceAccountID: uuid.New(),
		ExternalEventID: "evt-google-123",
		Title:           "Team Standup",
		StartAt:         now,
		EndAt:           now.Add(time.Hour),
		IsConfirmed:     false,
	}
	assertEqualString(t, "Team Standup", evt.Title, "Title")
	assertEqualString(t, "evt-google-123", evt.ExternalEventID, "ExternalEventID")
	assertFalse(t, evt.IsConfirmed, "IsConfirmed")
}

// ---------------------------------------------------------------------------
// Tests: ReminderJob model
// ---------------------------------------------------------------------------

func TestReminderJob_Model(t *testing.T) {
	now := time.Now().UTC()
	rj := ReminderJob{
		ID:           uuid.New(),
		UserID:       uuid.New(),
		EventID:      uuid.New(),
		ReminderType: "pre_event",
		ScheduledFor: now.Add(time.Hour),
	}
	assertEqualString(t, "pre_event", rj.ReminderType, "ReminderType")
	if rj.ProcessedAt != nil {
		t.Error("ProcessedAt should be nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: NotificationPreference model
// ---------------------------------------------------------------------------

func TestNotificationPreference_Model(t *testing.T) {
	np := NotificationPreference{
		UserID:           uuid.New(),
		QuietHoursStart:  22,
		QuietHoursEnd:    7,
		BatchThreshold:   5,
		DailyDigestTime:  "08:00",
		InterruptEnabled: true,
	}
	assertEqualInt(t, 22, np.QuietHoursStart, "QuietHoursStart")
	assertEqualInt(t, 7, np.QuietHoursEnd, "QuietHoursEnd")
	assertEqualInt(t, 5, np.BatchThreshold, "BatchThreshold")
	assertEqualString(t, "08:00", np.DailyDigestTime, "DailyDigestTime")
	assertTrue(t, np.InterruptEnabled, "InterruptEnabled")
}

// ---------------------------------------------------------------------------
// Tests: BatchNotificationPayload model
// ---------------------------------------------------------------------------

func TestBatchNotificationPayload_Model(t *testing.T) {
	payload := BatchNotificationPayload{
		BatchSize:                 5,
		EstimatedClearTimeMinutes: 8,
	}
	assertEqualInt(t, 5, payload.BatchSize, "BatchSize")
	assertEqualInt(t, 8, payload.EstimatedClearTimeMinutes, "EstimatedClearTimeMinutes")
}

// ---------------------------------------------------------------------------
// Tests: JSON round-trip for all key models
// ---------------------------------------------------------------------------

func TestModels_JSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		obj  interface{}
	}{
		{
			name: "DecisionCard",
			obj: DecisionCard{
				ID: uuid.New(), CardState: "pending", TheyWant: "meet",
				UrgencyScore: 0.5, ServerVersion: 1,
			},
		},
		{
			name: "Draft",
			obj: Draft{
				ID: uuid.New(), DraftBody: "Hello", UserApproved: false,
			},
		},
		{
			name: "SyncRequest",
			obj: SyncRequest{
				DeviceID: "d1", LastSyncVersion: 5, LocalChanges: []LocalChange{},
			},
		},
		{
			name: "SyncResponse",
			obj: SyncResponse{
				ServerVersion: 10,
			},
		},
		{
			name: "DeviceSession",
			obj: DeviceSession{
				ID: uuid.New(), DeviceID: "d1", DeviceType: "ios", DeviceName: "iPhone",
			},
		},
		{
			name: "UserQueue",
			obj: UserQueue{
				UserID: uuid.New(), PendingCount: 3, ServerVersion: 7,
			},
		},
		{
			name: "WSEvent",
			obj: WSEvent{
				Type: WSEventSpawn, CardID: uuid.New(), Text: "hello",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.obj)
			if err != nil {
				t.Fatalf("marshal %s: %v", tt.name, err)
			}
			if len(data) == 0 {
				t.Error("marshaled data is empty")
			}
			// Verify it's valid JSON
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("unmarshal to raw: %v", err)
			}
		})
	}
}
