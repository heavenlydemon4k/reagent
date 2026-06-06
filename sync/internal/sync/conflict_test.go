// Package sync_test provides unit tests for conflict resolution rules.
package sync

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Helper assertions
// ---------------------------------------------------------------------------

func assertEqualString(t *testing.T, want, got, msg string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %q, got %q", msg, want, got)
	}
}

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

func assertEqualWinner(t *testing.T, want, got Winner, msg string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %q, got %q", msg, want, got)
	}
}

// ---------------------------------------------------------------------------
// Tests: ConflictRules table completeness
// ---------------------------------------------------------------------------

func TestConflictRules_ContainsAllFields(t *testing.T) {
	expectedFields := []FieldName{FieldCardState, FieldDraftBody, FieldUserApproved, FieldUrgencyScore}
	for _, field := range expectedFields {
		rule, ok := ConflictRules[field]
		if !ok {
			t.Errorf("ConflictRules missing entry for %q", field)
			continue
		}
		if rule.Field != field {
			t.Errorf("rule.Field mismatch: want %q, got %q", field, rule.Field)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: ResolveConflict
// ---------------------------------------------------------------------------

func TestResolveConflict_CardState_UserWins(t *testing.T) {
	// card_state: user wins by default
	result := ResolveConflict(FieldCardState, "pending", false)
	assertEqualWinner(t, WinnerUser, result, "card_state with non-terminal server")
}

func TestResolveConflict_CardState_ServerTerminalWins(t *testing.T) {
	// card_state: server wins when terminal
	result := ResolveConflict(FieldCardState, "sent", true)
	assertEqualWinner(t, WinnerServer, result, "card_state with terminal server state")
}

func TestResolveConflict_CardState_ServerArchivedWins(t *testing.T) {
	result := ResolveConflict(FieldCardState, "archived", true)
	assertEqualWinner(t, WinnerServer, result, "card_state with archived server")
}

func TestResolveConflict_CardState_ServerExpiredWins(t *testing.T) {
	result := ResolveConflict(FieldCardState, "expired", true)
	assertEqualWinner(t, WinnerServer, result, "card_state with expired server")
}

func TestResolveConflict_DraftBody_ServerWins(t *testing.T) {
	// draft_body: server always wins
	result := ResolveConflict(FieldDraftBody, "any-state", false)
	assertEqualWinner(t, WinnerServer, result, "draft_body always server")
}

func TestResolveConflict_DraftBody_ServerWinsEvenTerminal(t *testing.T) {
	result := ResolveConflict(FieldDraftBody, "sent", true)
	assertEqualWinner(t, WinnerServer, result, "draft_body server even terminal")
}

func TestResolveConflict_UserApproved_UserWins(t *testing.T) {
	// user_approved: user always wins
	result := ResolveConflict(FieldUserApproved, "any-state", false)
	assertEqualWinner(t, WinnerUser, result, "user_approved always user")
}

func TestResolveConflict_UserApproved_UserWinsEvenTerminal(t *testing.T) {
	result := ResolveConflict(FieldUserApproved, "sent", true)
	assertEqualWinner(t, WinnerUser, result, "user_approved user even terminal")
}

func TestResolveConflict_UrgencyScore_ServerWins(t *testing.T) {
	// urgency_score: server always wins
	result := ResolveConflict(FieldUrgencyScore, "pending", false)
	assertEqualWinner(t, WinnerServer, result, "urgency_score always server")
}

func TestResolveConflict_UnknownField_DefaultsToServer(t *testing.T) {
	// Unknown field → server wins (defensive)
	result := ResolveConflict(FieldName("unknown_field"), "pending", false)
	assertEqualWinner(t, WinnerServer, result, "unknown field defaults to server")
}

// ---------------------------------------------------------------------------
// Table-driven: ResolveConflict
// ---------------------------------------------------------------------------

func TestResolveConflict_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		field          FieldName
		serverState    string
		serverTerminal bool
		want           Winner
	}{
		// card_state rules
		{"card_state pending non-terminal", FieldCardState, "pending", false, WinnerUser},
		{"card_state drafting non-terminal", FieldCardState, "drafting", false, WinnerUser},
		{"card_state consulting non-terminal", FieldCardState, "consulting", false, WinnerUser},
		{"card_state approved non-terminal", FieldCardState, "approved", false, WinnerUser},
		{"card_state sent terminal", FieldCardState, "sent", true, WinnerServer},
		{"card_state archived terminal", FieldCardState, "archived", true, WinnerServer},
		{"card_state expired terminal", FieldCardState, "expired", true, WinnerServer},
		{"card_state pending but marked terminal", FieldCardState, "pending", true, WinnerServer},

		// draft_body rules (always server)
		{"draft_body any", FieldDraftBody, "pending", false, WinnerServer},
		{"draft_body terminal", FieldDraftBody, "sent", true, WinnerServer},
		{"draft_body drafting", FieldDraftBody, "drafting", false, WinnerServer},

		// user_approved rules (always user)
		{"user_approved any", FieldUserApproved, "pending", false, WinnerUser},
		{"user_approved terminal", FieldUserApproved, "sent", true, WinnerUser},

		// urgency_score rules (always server)
		{"urgency_score any", FieldUrgencyScore, "pending", false, WinnerServer},
		{"urgency_score terminal", FieldUrgencyScore, "sent", true, WinnerServer},

		// unknown field (defensive: server wins)
		{"unknown field", FieldName("foobar"), "pending", false, WinnerServer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveConflict(tt.field, tt.serverState, tt.serverTerminal)
			if got != tt.want {
				t.Errorf("ResolveConflict(%q, %q, %v) = %q, want %q",
					tt.field, tt.serverState, tt.serverTerminal, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: IsTerminal
// ---------------------------------------------------------------------------

func TestIsTerminal_TrueCases(t *testing.T) {
	terminalStates := []string{"sent", "archived", "expired"}
	for _, state := range terminalStates {
		if !IsTerminal(state) {
			t.Errorf("IsTerminal(%q) should be true", state)
		}
	}
}

func TestIsTerminal_FalseCases(t *testing.T) {
	nonTerminalStates := []string{"pending", "consulting", "drafting", "approved"}
	for _, state := range nonTerminalStates {
		if IsTerminal(state) {
			t.Errorf("IsTerminal(%q) should be false", state)
		}
	}
}

func TestIsTerminal_EmptyString(t *testing.T) {
	if IsTerminal("") {
		t.Error("IsTerminal(\"\") should be false")
	}
}

func TestIsTerminal_UnknownState(t *testing.T) {
	if IsTerminal("unknown_state") {
		t.Error("IsTerminal(\"unknown_state\") should be false")
	}
}

// ---------------------------------------------------------------------------
// Tests: ValidCardStates
// ---------------------------------------------------------------------------

func TestValidCardStates(t *testing.T) {
	states := ValidCardStates()
	want := []string{"pending", "consulting", "drafting", "approved", "sent", "archived", "expired"}
	if len(states) != len(want) {
		t.Fatalf("want %d states, got %d", len(want), len(states))
	}
	for i, s := range want {
		if states[i] != s {
			t.Errorf("state[%d]: want %q, got %q", i, s, states[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: IsValidCardState
// ---------------------------------------------------------------------------

func TestIsValidCardState_AllValid(t *testing.T) {
	for _, state := range ValidCardStates() {
		if !IsValidCardState(state) {
			t.Errorf("IsValidCardState(%q) should be true", state)
		}
	}
}

func TestIsValidCardState_Invalid(t *testing.T) {
	invalidStates := []string{"", "deleted", "unknown", "random"}
	for _, state := range invalidStates {
		if IsValidCardState(state) {
			t.Errorf("IsValidCardState(%q) should be false", state)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: TerminalStates map
// ---------------------------------------------------------------------------

func TestTerminalStates_Contents(t *testing.T) {
	if len(TerminalStates) != 3 {
		t.Errorf("want 3 terminal states, got %d", len(TerminalStates))
	}
	for _, state := range []string{"sent", "archived", "expired"} {
		if !TerminalStates[state] {
			t.Errorf("TerminalStates should contain %q", state)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: ConflictRule struct
// ---------------------------------------------------------------------------

func TestConflictRule_Fields(t *testing.T) {
	rule := ConflictRules[FieldCardState]
	assertEqualString(t, string(FieldCardState), string(rule.Field), "field")
	assertEqualWinner(t, WinnerUser, rule.Winner, "winner")
	assertEqualString(t, string(ExceptionServerIfTerminal), string(rule.Exception), "exception")
	if rule.Description == "" {
		t.Error("description should not be empty")
	}
}

// ---------------------------------------------------------------------------
// Tests: Winner and Exception constants
// ---------------------------------------------------------------------------

func TestWinnerConstants(t *testing.T) {
	assertEqualString(t, "user", string(WinnerUser), "WinnerUser")
	assertEqualString(t, "server", string(WinnerServer), "WinnerServer")
}

func TestExceptionConstants(t *testing.T) {
	assertEqualString(t, "none", string(ExceptionNone), "ExceptionNone")
	assertEqualString(t, "server_if_terminal", string(ExceptionServerIfTerminal), "ExceptionServerIfTerminal")
	assertEqualString(t, "newer_timestamp", string(ExceptionNewerTimestamp), "ExceptionNewerTimestamp")
}

func TestFieldNameConstants(t *testing.T) {
	assertEqualString(t, "card_state", string(FieldCardState), "FieldCardState")
	assertEqualString(t, "draft_body", string(FieldDraftBody), "FieldDraftBody")
	assertEqualString(t, "user_approved", string(FieldUserApproved), "FieldUserApproved")
	assertEqualString(t, "urgency_score", string(FieldUrgencyScore), "FieldUrgencyScore")
}

// ---------------------------------------------------------------------------
// Tests: NonTerminal server state + terminal flag combination
// ---------------------------------------------------------------------------

func TestResolveConflict_NonTerminalStateWithTerminalFlag(t *testing.T) {
	// Even if the state name isn't terminal, if serverIsTerminal is true,
	// the server wins (the flag is the authoritative signal).
	result := ResolveConflict(FieldCardState, "drafting", true)
	assertEqualWinner(t, WinnerServer, result, "terminal flag overrides state name")
}

func TestResolveConflict_TerminalStateWithoutTerminalFlag(t *testing.T) {
	// If the state is terminal but flag says false, user still wins
	// (the flag is the authoritative signal from the engine)
	result := ResolveConflict(FieldCardState, "sent", false)
	assertEqualWinner(t, WinnerUser, result, "non-terminal flag means user wins")
}
