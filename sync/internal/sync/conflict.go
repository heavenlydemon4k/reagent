// Package sync provides the server-side sync endpoint for offline-first
// CRDT merge, conflict resolution, and batch sync.
package sync

// ---------------------------------------------------------------------------
// Conflict Resolution Rules — CRDT policy for the Decision Stack
// ---------------------------------------------------------------------------

// FieldName identifies a field in the decision card / draft that may conflict
// during a sync operation between client and server.
type FieldName string

const (
	FieldCardState   FieldName = "card_state"
	FieldDraftBody   FieldName = "draft_body"
	FieldUserApproved FieldName = "user_approved"
	FieldUrgencyScore FieldName = "urgency_score"
)

// Winner indicates who wins a conflict for a given field.
type Winner string

const (
	WinnerUser   Winner = "user"   // client's value wins
	WinnerServer Winner = "server" // server's value wins
)

// Exception is a conditional override for a conflict rule.
type Exception string

const (
	ExceptionNone             Exception = "none"               // no exception; rule always applies
	ExceptionServerIfTerminal Exception = "server_if_terminal" // server wins if card is in terminal state
	ExceptionNewerTimestamp   Exception = "newer_timestamp"    // whichever side has the newer timestamp wins
)

// ConflictRule defines the resolution policy for a single field.
type ConflictRule struct {
	// Field is the field this rule applies to.
	Field FieldName `json:"field"`
	// Winner is who wins by default when both client and server have a value.
	Winner Winner `json:"winner"`
	// Exception is a conditional override that may change the winner.
	Exception Exception `json:"exception"`
	// Description is a human-readable explanation of the rule.
	Description string `json:"description"`
}

// ---------------------------------------------------------------------------
// Conflict Rules Table — authoritative CRDT policy
// ---------------------------------------------------------------------------

// ConflictRules is the authoritative conflict resolution policy table.
// It is read-only after initialization and is safe for concurrent access.
//
// Rules (from spec):
//
//   card_state:   user wins — the user's explicit decision is sacred.
//                 Exception: if server has terminal state (sent/archived/expired),
//                 server wins because the card has already been processed.
//
//   draft_body:   server wins — LLM-generated draft is authoritative.
//                 No exceptions. User edits are noted but overwritten.
//
//   user_approved: user wins — explicit approval is sacred.
//                 No exceptions.
//
//   urgency_score: server wins — computed by the server from email analysis.
//                 No exceptions.
var ConflictRules = map[FieldName]ConflictRule{
	FieldCardState: {
		Field:       FieldCardState,
		Winner:      WinnerUser,
		Exception:   ExceptionServerIfTerminal,
		Description: "User decision wins on card state, unless server has already processed the card (sent/archived/expired)",
	},
	FieldDraftBody: {
		Field:       FieldDraftBody,
		Winner:      WinnerServer,
		Exception:   ExceptionNone,
		Description: "Server draft body (LLM-generated) is always authoritative. User edits are noted but overwritten.",
	},
	FieldUserApproved: {
		Field:       FieldUserApproved,
		Winner:      WinnerUser,
		Exception:   ExceptionNone,
		Description: "User explicit approval is sacred and always wins.",
	},
	FieldUrgencyScore: {
		Field:       FieldUrgencyScore,
		Winner:      WinnerServer,
		Exception:   ExceptionNone,
		Description: "Server-computed urgency score is authoritative.",
	},
}

// TerminalStates are card states that are immutable on the server.
// Once a card reaches one of these states, no client changes are accepted.
var TerminalStates = map[string]bool{
	"sent":     true,
	"archived": true,
	"expired":  true,
}

// ResolveConflict evaluates the conflict rules for a given field and server
// state, returning who wins: "user" or "server".
//
// This function is pure (no side effects) and may be called concurrently.
func ResolveConflict(field FieldName, serverState string, serverIsTerminal bool) Winner {
	rule, ok := ConflictRules[field]
	if !ok {
		// Unknown field: default to server wins (defensive).
		return WinnerServer
	}

	// Check exception first.
	if rule.Exception == ExceptionServerIfTerminal && serverIsTerminal {
		return WinnerServer
	}

	// Default winner from the rule table.
	return rule.Winner
}

// IsTerminal returns true if the given card state is terminal (immutable).
func IsTerminal(state string) bool {
	return TerminalStates[state]
}

// ValidCardStates returns all valid card states for validation.
func ValidCardStates() []string {
	return []string{
		"pending",
		"consulting",
		"drafting",
		"approved",
		"sent",
		"archived",
		"expired",
	}
}

// IsValidCardState returns true if the given state is a valid card state.
func IsValidCardState(state string) bool {
	for _, s := range ValidCardStates() {
		if s == state {
			return true
		}
	}
	return false
}
