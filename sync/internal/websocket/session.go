// Package websocket provides a WebSocket hub for real-time client communication.
package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
)

// ============================================================================
// SpawnEngine — proxy interface to Intelligence Layer
// ============================================================================

// SpawnEngine defines the interface for AI paragraph generation.
// This is a proxy to the Intelligence Layer bounded context.
// In production this calls the spawn service via NATS or gRPC.
type SpawnEngine interface {
	// GenerateParagraph requests AI-generated paragraph text for a card draft.
	// Returns a channel that streams text chunks and a channel for errors.
	GenerateParagraph(ctx context.Context, cardID uuid.UUID, triggerWord string, cursorPosition int, existingDraft string) (<-chan ParagraphChunk, <-chan error)

	// CancelGeneration aborts an in-flight generation request.
	CancelGeneration(cardID uuid.UUID)
}

// ParagraphChunk represents a single chunk of streamed paragraph text.
type ParagraphChunk struct {
	ParagraphID string    `json:"paragraph_id"`
	Text        string    `json:"text"`         // incremental chunk
	IsFinal     bool      `json:"is_final"`     // true when streaming completes
	FullText    string    `json:"full_text"`    // cumulative text so far
	Timestamp   time.Time `json:"timestamp"`
}

// ============================================================================
// SendingSession — per-connection drafting session
// ============================================================================

// SessionStore defines the interface for persisting session state.
type SessionStore interface {
	// GetDraft retrieves the current draft for a card.
	GetDraft(ctx context.Context, cardID uuid.UUID) (*models.Draft, error)

	// UpdateDraftBody updates the draft body text.
	UpdateDraftBody(ctx context.Context, cardID uuid.UUID, userID uuid.UUID, body string) error

	// ApproveDraft marks a draft as user-approved.
	ApproveDraft(ctx context.Context, draftID uuid.UUID) error

	// CreateStagedRule creates a staged auto-handle rule from a delegation.
	CreateStagedRule(ctx context.Context, cardID uuid.UUID, userID uuid.UUID, delegationType string) (uuid.UUID, error)

	// UpdateCardState transitions a card to a new state.
	UpdateCardState(ctx context.Context, cardID uuid.UUID, userID uuid.UUID, newState string) error
}

// SendingSession manages a single user's real-time drafting session for a card.
type SendingSession struct {
	cardID      uuid.UUID
	userID      uuid.UUID
	draftID     uuid.UUID
	draftBody   string
	deviceID    string
	hub         *Hub
	spawnEngine SpawnEngine
	store       SessionStore

	// generationCancel cancels the current generation context
	generationCancel context.CancelFunc
}

// NewSendingSession creates a new sending session.
func NewSendingSession(cardID, userID uuid.UUID, deviceID string, hub *Hub, engine SpawnEngine, store SessionStore) *SendingSession {
	return &SendingSession{
		cardID:      cardID,
		userID:      userID,
		deviceID:    deviceID,
		hub:         hub,
		spawnEngine: engine,
		store:       store,
	}
}

// CardID returns the card ID for this session.
func (s *SendingSession) CardID() uuid.UUID { return s.cardID }

// UserID returns the user ID for this session.
func (s *SendingSession) UserID() uuid.UUID { return s.userID }

// DraftID returns the draft ID for this session.
func (s *SendingSession) DraftID() uuid.UUID { return s.draftID }

// ============================================================================
// EVENT HANDLER
// ============================================================================

// HandleEvent routes client events to the appropriate handler.
func (s *SendingSession) HandleEvent(event ClientEvent) error {
	logger.Debug("session handling event",
		"type", event.Type,
		"card_id", s.cardID,
		"user_id", s.userID,
	)

	switch event.Type {
	case models.WSEventSpawn:
		return s.handleSpawn(event)
	case models.WSEventAccept:
		return s.handleAccept(event)
	case models.WSEventEdit:
		return s.handleEdit(event)
	case models.WSEventDelegate:
		return s.handleDelegate(event)
	default:
		return fmt.Errorf("unsupported event type: %s", event.Type)
	}
}

// handleSpawn processes a spawn request: the user typed a trigger word.
// It requests paragraph generation from the Intelligence Layer and streams
// the response back via WebSocket.
func (s *SendingSession) handleSpawn(event ClientEvent) error {
	// Cancel any in-flight generation
	if s.generationCancel != nil {
		s.generationCancel()
		s.generationCancel = nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	s.generationCancel = cancel
	defer func() {
		s.generationCancel = nil
	}()

	chunkCh, errCh := s.spawnEngine.GenerateParagraph(
		ctx,
		s.cardID,
		event.TriggerWord,
		event.CursorPosition,
		s.draftBody,
	)

	paragraphID := uuid.New().String()
	var fullText strings.Builder

	for {
		select {
		case chunk, ok := <-chunkCh:
			if !ok {
				// Stream complete — send completion event
				s.broadcastParagraphComplete(event.CardID, paragraphID, fullText.String())
				return nil
			}

			fullText.WriteString(chunk.Text)

			// Stream the chunk to all user's devices
			payload := ParagraphStreamPayload{
				ParagraphID: paragraphID,
				Chunk:       chunk.Text,
				IsFinal:     chunk.IsFinal,
				FullText:    fullText.String(),
			}
			if chunk.IsFinal {
				payload.IsFinal = true
			}

			evt := NewParagraphStreamEvent(s.cardID, payload)
			s.broadcastEvent(evt)

			if chunk.IsFinal {
				return nil
			}

		case err := <-errCh:
			if err != nil {
				logger.Error("spawn generation error",
					"card_id", s.cardID,
					"error", err,
				)
				evt := NewWSErrorEvent(s.cardID, "spawn_failed", err.Error(), true)
				s.broadcastEvent(evt)
				return fmt.Errorf("spawn generation failed: %w", err)
			}

		case <-ctx.Done():
			evt := NewWSErrorEvent(s.cardID, "spawn_timeout", "Paragraph generation timed out", true)
			s.broadcastEvent(evt)
			return fmt.Errorf("spawn generation timed out")
		}
	}
}

// handleAccept processes an accept event: the user accepted a generated paragraph.
// The paragraph text is appended to the draft body.
func (s *SendingSession) handleAccept(event ClientEvent) error {
	if event.Text == "" {
		return fmt.Errorf("accept event requires text")
	}

	// Append accepted text to draft body
	if s.draftBody != "" {
		s.draftBody += "\n\n" + event.Text
	} else {
		s.draftBody = event.Text
	}

	// Persist the updated draft
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.store.UpdateDraftBody(ctx, s.cardID, s.userID, s.draftBody); err != nil {
		evt := NewWSErrorEvent(s.cardID, "draft_save_failed", "Failed to save draft", true)
		s.broadcastEvent(evt)
		return fmt.Errorf("update draft body: %w", err)
	}

	// Broadcast draft update to all user's devices
	payload := DraftUpdatedPayload{
		DraftID:   s.draftID,
		DraftBody: s.draftBody,
		EditedBy:  s.deviceID,
	}
	evt := NewDraftUpdatedEvent(s.cardID, payload)
	s.broadcastEvent(evt)

	logger.Debug("draft updated after accept",
		"card_id", s.cardID,
		"draft_id", s.draftID,
		"device_id", s.deviceID,
	)

	return nil
}

// handleEdit processes an edit event: the user manually edited the draft text.
func (s *SendingSession) handleEdit(event ClientEvent) error {
	s.draftBody = event.Text

	// Persist the updated draft
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.store.UpdateDraftBody(ctx, s.cardID, s.userID, s.draftBody); err != nil {
		evt := NewWSErrorEvent(s.cardID, "draft_save_failed", "Failed to save draft", true)
		s.broadcastEvent(evt)
		return fmt.Errorf("update draft body: %w", err)
	}

	// Broadcast draft update to all user's devices (including the editor,
	// so other devices see the live cursor position and text)
	payload := DraftUpdatedPayload{
		DraftID:   s.draftID,
		DraftBody: s.draftBody,
		EditedBy:  s.deviceID,
	}
	evt := NewDraftUpdatedEvent(s.cardID, payload)
	s.broadcastEvent(evt)

	return nil
}

// handleDelegate processes a delegate event: the user delegated the decision.
// This creates a staged auto-handle rule for future similar decisions.
func (s *SendingSession) handleDelegate(event ClientEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create staged auto-handle rule
	ruleID, err := s.store.CreateStagedRule(ctx, s.cardID, s.userID, event.TriggerWord)
	if err != nil {
		evt := NewWSErrorEvent(s.cardID, "delegate_failed", "Failed to create staged rule", false)
		s.broadcastEvent(evt)
		return fmt.Errorf("create staged rule: %w", err)
	}

	// Transition card to approved state (delegation = auto-approve via rule)
	if err := s.store.UpdateCardState(ctx, s.cardID, s.userID, "approved"); err != nil {
		logger.Warn("failed to update card state after delegate",
			"card_id", s.cardID,
			"error", err,
		)
	}

	// Broadcast state change
	evt := NewCardStateChangedEvent(s.cardID, "consulting", "approved")
	s.broadcastEvent(evt)

	logger.Info("decision delegated",
		"card_id", s.cardID,
		"user_id", s.userID,
		"rule_id", ruleID,
		"delegation_type", event.TriggerWord,
	)

	return nil
}

// ============================================================================
// BROADCAST HELPERS
// ============================================================================

// broadcastEvent sends a server event to all of the user's connected devices.
func (s *SendingSession) broadcastEvent(evt ServerEvent) {
	if s.hub == nil {
		return
	}

	data, err := MarshalServerEvent(evt)
	if err != nil {
		logger.Error("failed to marshal server event", "error", err)
		return
	}

	// Convert ServerEvent bytes to a WSEvent-compatible broadcast via the hub
	wsEvent := models.WSEvent{
		Type:      evt.Type,
		CardID:    evt.CardID,
		Text:      string(evt.Payload),
		Timestamp: evt.Timestamp,
	}

	if err := s.hub.BroadcastToUser(s.userID, &wsEvent); err != nil {
		logger.Error("failed to broadcast event", "error", err, "user_id", s.userID)
	}

	_ = data
}

// broadcastParagraphComplete sends the paragraph_complete event.
func (s *SendingSession) broadcastParagraphComplete(cardID uuid.UUID, paragraphID, fullText string) {
	payload := ParagraphStreamPayload{
		ParagraphID: paragraphID,
		IsFinal:     true,
		FullText:    fullText,
	}
	evt := ServerEvent{
		Type:      WSEventParagraphComplete,
		CardID:    cardID,
		Payload:   mustMarshalJSON(payload),
		Timestamp: time.Now().UTC(),
	}
	s.broadcastEvent(evt)
}

// mustMarshalJSON marshals v to JSON, panicking on error.
// Safe to use for internal, known-good structs only.
func mustMarshalJSON(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal JSON: %v", err))
	}
	return json.RawMessage(data)
}

// ============================================================================
// MOCK SPAWN ENGINE — for integration testing
// ============================================================================

// MockSpawnEngine is a test double that returns predefined paragraph chunks.
type MockSpawnEngine struct {
	chunks []ParagraphChunk
	err    error
}

// NewMockSpawnEngine creates a mock engine with predefined chunks.
func NewMockSpawnEngine(chunks []ParagraphChunk, err error) *MockSpawnEngine {
	return &MockSpawnEngine{chunks: chunks, err: err}
}

// GenerateParagraph returns the predefined chunks over a channel.
func (m *MockSpawnEngine) GenerateParagraph(ctx context.Context, cardID uuid.UUID, triggerWord string, cursorPosition int, existingDraft string) (<-chan ParagraphChunk, <-chan error) {
	chunkCh := make(chan ParagraphChunk, len(m.chunks))
	errCh := make(chan error, 1)

	go func() {
		defer close(chunkCh)
		if m.err != nil {
			errCh <- m.err
			close(errCh)
			return
		}
		close(errCh)
		for _, chunk := range m.chunks {
			select {
			case chunkCh <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()

	return chunkCh, errCh
}

// CancelGeneration is a no-op for the mock.
func (m *MockSpawnEngine) CancelGeneration(cardID uuid.UUID) {}
