// Package websocket provides a WebSocket hub for real-time client communication.
package websocket

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
)

// ============================================================================
// EVENT TYPE DEFINITIONS — Extended from models.WSEventType
// ============================================================================

// Server-side event types pushed from hub to clients.
const (
	// WSEventParagraphStream — streamed paragraph chunk from spawn engine
	WSEventParagraphStream models.WSEventType = "paragraph_stream"

	// WSEventParagraphComplete — signals end of paragraph stream
	WSEventParagraphComplete models.WSEventType = "paragraph_complete"

	// WSEventDraftUpdated — broadcast when draft body changes
	WSEventDraftUpdated models.WSEventType = "draft_updated"

	// WSEventCardStateChanged — card state transitioned (pending→consulting, etc.)
	WSEventCardStateChanged models.WSEventType = "card_state_changed"

	// WSEventError — server-side error delivered over WS
	WSEventError models.WSEventType = "error"

	// WSEventTyping — signals another device on this account is typing
	WSEventTyping models.WSEventType = "typing"

	// WSEventSyncTrigger — requests client to initiate a sync
	WSEventSyncTrigger models.WSEventType = "sync_trigger"
)

// ============================================================================
// TYPED EVENT ENVELOPES
// ============================================================================

// ServerEvent is the envelope for all server→client messages.
type ServerEvent struct {
	Type      models.WSEventType `json:"type"`
	CardID    uuid.UUID          `json:"card_id,omitempty"`
	Payload   json.RawMessage    `json:"payload,omitempty"`
	Timestamp time.Time          `json:"timestamp"`
	RequestID string             `json:"request_id,omitempty"` // for correlation
}

// ClientEvent is the envelope for all client→server messages.
type ClientEvent struct {
	Type           models.WSEventType `json:"type"`
	CardID         uuid.UUID          `json:"card_id"`
	Text           string             `json:"text,omitempty"`
	TriggerWord    string             `json:"trigger_word,omitempty"`
	CursorPosition int                `json:"cursor_position,omitempty"`
	Timestamp      time.Time          `json:"timestamp"`
}

// ============================================================================
// TYPED PAYLOADS
// ============================================================================

// ParagraphStreamPayload carries a chunk of generated text.
type ParagraphStreamPayload struct {
	ParagraphID string `json:"paragraph_id"`
	Chunk       string `json:"chunk"`           // incremental text
	IsFinal     bool   `json:"is_final"`        // true when stream completes
	FullText    string `json:"full_text"`       // complete paragraph so far
}

// DraftUpdatedPayload notifies clients that a draft has been modified.
type DraftUpdatedPayload struct {
	DraftID   uuid.UUID `json:"draft_id"`
	DraftBody string    `json:"draft_body"`
	EditedBy  string    `json:"edited_by"`    // device_id of editor
}

// CardStateChangedPayload signals a card transitioned to a new state.
type CardStateChangedPayload struct {
	OldState string `json:"old_state"`
	NewState string `json:"new_state"`
}

// WSErrorPayload carries a structured error to the client.
type WSErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Retry   bool   `json:"retry"`
}

// TypingPayload signals that a user is actively typing on another device.
type TypingPayload struct {
	DeviceID string `json:"device_id"`
	CardID   uuid.UUID `json:"card_id"`
}

// ============================================================================
// EVENT BUILDERS
// ============================================================================

// NewParagraphStreamEvent builds a paragraph_stream server event.
func NewParagraphStreamEvent(cardID uuid.UUID, payload ParagraphStreamPayload) ServerEvent {
	data, _ := json.Marshal(payload)
	return ServerEvent{
		Type:      WSEventParagraphStream,
		CardID:    cardID,
		Payload:   data,
		Timestamp: time.Now().UTC(),
	}
}

// NewDraftUpdatedEvent builds a draft_updated server event.
func NewDraftUpdatedEvent(cardID uuid.UUID, payload DraftUpdatedPayload) ServerEvent {
	data, _ := json.Marshal(payload)
	return ServerEvent{
		Type:      WSEventDraftUpdated,
		CardID:    cardID,
		Payload:   data,
		Timestamp: time.Now().UTC(),
	}
}

// NewCardStateChangedEvent builds a card_state_changed server event.
func NewCardStateChangedEvent(cardID uuid.UUID, oldState, newState string) ServerEvent {
	payload, _ := json.Marshal(CardStateChangedPayload{
		OldState: oldState,
		NewState: newState,
	})
	return ServerEvent{
		Type:      WSEventCardStateChanged,
		CardID:    cardID,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}
}

// NewWSErrorEvent builds an error server event.
func NewWSErrorEvent(cardID uuid.UUID, code, message string, retry bool) ServerEvent {
	payload, _ := json.Marshal(WSErrorPayload{
		Code:    code,
		Message: message,
		Retry:   retry,
	})
	return ServerEvent{
		Type:      WSEventError,
		CardID:    cardID,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}
}

// NewPongEvent builds a pong response for a client ping.
func NewPongEvent(cardID uuid.UUID) ServerEvent {
	return ServerEvent{
		Type:      models.WSEventPong,
		CardID:    cardID,
		Timestamp: time.Now().UTC(),
	}
}

// NewSyncTriggerEvent builds a sync_trigger event to force client sync.
func NewSyncTriggerEvent(cardID uuid.UUID) ServerEvent {
	return ServerEvent{
		Type:      WSEventSyncTrigger,
		CardID:    cardID,
		Timestamp: time.Now().UTC(),
	}
}

// ============================================================================
// SERIALIZATION
// ============================================================================

// MarshalServerEvent serializes a ServerEvent to JSON bytes.
func MarshalServerEvent(evt ServerEvent) ([]byte, error) {
	return json.Marshal(evt)
}

// UnmarshalClientEvent parses a client event from JSON bytes.
func UnmarshalClientEvent(data []byte) (ClientEvent, error) {
	var evt ClientEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return evt, fmt.Errorf("unmarshal client event: %w", err)
	}
	return evt, nil
}

// ============================================================================
// EVENT ROUTING HELPERS
// ============================================================================

// IsClientEventType returns true if the given event type can be sent by clients.
func IsClientEventType(t models.WSEventType) bool {
	switch t {
	case models.WSEventSpawn, models.WSEventAccept, models.WSEventEdit,
		models.WSEventDelegate, models.WSEventPing:
		return true
	}
	return false
}

// IsServerEventType returns true if the given event type is server-originated.
func IsServerEventType(t models.WSEventType) bool {
	switch t {
	case WSEventParagraphStream, WSEventParagraphComplete, WSEventDraftUpdated,
		WSEventCardStateChanged, WSEventError, WSEventTyping,
		WSEventSyncTrigger, models.WSEventPong:
		return true
	}
	return false
}

// RequiresCardID returns true if the event type requires a card_id field.
func RequiresCardID(t models.WSEventType) bool {
	switch t {
	case models.WSEventSpawn, models.WSEventAccept, models.WSEventEdit,
		models.WSEventDelegate, WSEventParagraphStream, WSEventDraftUpdated,
		WSEventCardStateChanged:
		return true
	}
	return false
}
