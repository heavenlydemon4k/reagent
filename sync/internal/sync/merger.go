package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ---------------------------------------------------------------------------
// SyncEngine — CRDT merge engine (server-side)
// ---------------------------------------------------------------------------

// SyncEngine is the core of the server-side sync protocol. It implements a
// CRDT-style merge: client and server each have their own view of the data,
// and the engine deterministically resolves conflicts according to the
// ConflictRules table.
//
// The engine is stateless except for its stores and logger. All mutable
// state lives in PostgreSQL. It is safe to use from multiple goroutines.
type SyncEngine struct {
	cardStore  *SyncStore
	draftStore *SyncStore
	cursor     *VersionCursor
	log        *slog.Logger
}

// NewSyncEngine creates a new SyncEngine with the given dependencies.
// NewSyncEngine creates a new SyncEngine with the given database.
// If log is nil, uses the global logger.
func NewSyncEngine(db *sqlx.DB, log *slog.Logger) *SyncEngine {
	store := NewSyncStore(db)
	if log == nil {
		log = logger.L()
	}
	return &SyncEngine{
		cardStore:  store,
		draftStore: store,
		cursor:     NewVersionCursor(db),
		log:        log,
	}
}

// ---------------------------------------------------------------------------
// applyResult — internal result of applying a single local change
// ---------------------------------------------------------------------------

type applyResult struct {
	accepted    bool
	reason      string
	serverState string
}

// ---------------------------------------------------------------------------
// Process — main sync entry point (3-phase protocol)
// ---------------------------------------------------------------------------

// Process executes the full 3-phase sync protocol:
//
//   PHASE 1: Accept local changes from client. For each LocalChange, the engine
//            applies CRDT rules to determine acceptance or rejection.
//
//   PHASE 2: Send server updates to client. All cards with server_version >
//            client's last_sync_version are returned as new/updated/removed.
//
//   PHASE 3: Compute new server_version for the client to use as its
//            last_sync_version on the next sync.
//
// The entire operation is logged to the sync_log table for audit.
// The operation is idempotent: the same request twice produces the same result.
func (e *SyncEngine) Process(ctx context.Context, userID uuid.UUID, deviceID string, req *models.SyncRequest) (*models.SyncResponse, error) {
	start := time.Now().UTC()

	// Log the sync session start.
	if err := e.cardStore.LogSessionStart(ctx, userID, deviceID, req.LastSyncVersion); err != nil {
		e.log.Warn("failed to log sync start", "error", err, "user_id", userID)
	}

	resp := &models.SyncResponse{
		AcceptedChanges: make([]uuid.UUID, 0),
		RejectedChanges: make([]models.RejectedChange, 0),
		NewCards:        make([]models.DecisionCard, 0),
		UpdatedCards:    make([]models.DecisionCard, 0),
		RemovedCards:    make([]uuid.UUID, 0),
	}

	// -------------------------------------------------------------------------
	// PHASE 1: Accept local changes from client
	// -------------------------------------------------------------------------
	for _, change := range req.LocalChanges {
		result := e.applyChange(ctx, userID, deviceID, change)
		if result.accepted {
			resp.AcceptedChanges = append(resp.AcceptedChanges, change.CardID)
		} else {
			resp.RejectedChanges = append(resp.RejectedChanges, models.RejectedChange{
				CardID:      change.CardID,
				Reason:      result.reason,
				ServerState: result.serverState,
			})
		}
	}

	// -------------------------------------------------------------------------
	// PHASE 2: Send server updates to client
	// -------------------------------------------------------------------------
	changes, err := e.cursor.GetChangesSince(ctx, userID, req.LastSyncVersion)
	if err != nil {
		e.log.Error("failed to get changes since", "error", err, "user_id", userID, "since", req.LastSyncVersion)
		return nil, fmt.Errorf("get changes since: %w", err)
	}
	resp.NewCards = changes.NewCards
	resp.UpdatedCards = changes.UpdatedCards
	resp.RemovedCards = changes.RemovedCards

	// -------------------------------------------------------------------------
	// PHASE 3: Compute new server_version
	// -------------------------------------------------------------------------
	currentVersion, err := e.cursor.GetCurrentVersion(ctx, userID)
	if err != nil {
		e.log.Error("failed to get current version", "error", err, "user_id", userID)
		return nil, fmt.Errorf("get current version: %w", err)
	}
	resp.ServerVersion = currentVersion

	e.log.Info("sync completed",
		"user_id", userID,
		"device_id", deviceID,
		"client_version", req.LastSyncVersion,
		"server_version", resp.ServerVersion,
		"accepted", len(resp.AcceptedChanges),
		"rejected", len(resp.RejectedChanges),
		"new_cards", len(resp.NewCards),
		"updated_cards", len(resp.UpdatedCards),
		"removed", len(resp.RemovedCards),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return resp, nil
}

// ---------------------------------------------------------------------------
// applyChange — CRDT merge for a single local change
// ---------------------------------------------------------------------------

// applyChange processes one LocalChange from the client according to the
// CRDT conflict resolution rules. It returns an applyResult indicating whether
// the change was accepted and, if rejected, the reason and current server state.
//
// CRDT RULES (in priority order):
//
//   1. If card does not exist → reject (card_not_found)
//   2. If card is in terminal state (sent/archived/expired) → reject
//      with reason "card_already_terminal" (server wins — immutable)
//   3. If card is owned by a different user → reject (ownership_violation)
//   4. Apply decision based on the ConflictRules table:
//      - "approve": mark card approved, mark draft as user_approved
//      - "edit": note user's edit but do NOT overwrite draft_body (server wins)
//      - "consult": no-op (card stays in current state)
//
// This method is idempotent: applying the same change twice produces the same
// result because card state transitions are monotonic.
func (e *SyncEngine) applyChange(ctx context.Context, userID uuid.UUID, deviceID string, change models.LocalChange) applyResult {
	// --- Rule 0: Validate the change itself ---
	if change.CardID == uuid.Nil {
		return applyResult{accepted: false, reason: "invalid_card_id"}
	}

	// --- Rule 1: Card must exist ---
	card, err := e.cardStore.GetCardOwnedBy(ctx, change.CardID, userID)
	if err != nil {
		// Card not found or not owned by this user
		if card == nil {
			_ = e.cardStore.LogChange(ctx, &SyncLogEntry{
				UserID:     userID,
				DeviceID:   deviceID,
				CardID:     &change.CardID,
				Operation:  "reject",
				ChangeType: safeDeref(change.Decision),
				Reason:     "card_not_found",
			})
			e.log.Debug("change rejected: card not found",
				"card_id", change.CardID,
				"user_id", userID,
			)
		}
		return applyResult{accepted: false, reason: "card_not_found"}
	}

	// --- Rule 2: Terminal states are immutable (server wins) ---
	if IsTerminal(card.CardState) {
		_ = e.cardStore.LogChange(ctx, &SyncLogEntry{
			UserID:        userID,
			DeviceID:      deviceID,
			CardID:        &change.CardID,
			Operation:     "reject",
			ChangeType:    safeDeref(change.Decision),
			Reason:        "card_already_terminal",
			ServerVersion: card.ServerVersion,
			Details: mustJSON(map[string]interface{}{
				"server_state": card.CardState,
				"client_state": change.State,
			}),
		})
		e.log.Debug("change rejected: card already terminal",
			"card_id", change.CardID,
			"card_state", card.CardState,
			"user_id", userID,
		)
		return applyResult{
			accepted:    false,
			reason:      "card_already_terminal",
			serverState: card.CardState,
		}
	}

	// --- Rule 3: Validate decision type ---
	decision := safeDeref(change.Decision)
	if decision != "" && !isValidDecision(decision) {
		return applyResult{accepted: false, reason: "invalid_decision", serverState: card.CardState}
	}

	// --- Rule 4: Apply the change based on CRDT rules ---
	switch decision {
	case "approve":
		return e.applyApprove(ctx, userID, deviceID, change, card)
	case "edit":
		return e.applyEdit(ctx, userID, deviceID, change, card)
	case "consult":
		return e.applyConsult(ctx, userID, deviceID, change, card)
	case "":
		// No decision specified — client is just syncing state.
		// Accept without action (the card state is already what the server has).
		return applyResult{accepted: true}
	default:
		return applyResult{accepted: false, reason: "invalid_decision", serverState: card.CardState}
	}
}

// applyApprove handles the "approve" decision: user approved a draft.
//
// CRDT policy: user_approved is sacred — user wins (ConflictRule.Exception = none).
// The card state transitions to "approved" and the draft is marked as user_approved.
//
// If the client provides an ApprovedDraftID, we validate it belongs to this card
// and mark it as approved. If no ApprovedDraftID, we try to find and approve the
// latest draft for the card.
func (e *SyncEngine) applyApprove(ctx context.Context, userID uuid.UUID, deviceID string, change models.LocalChange, card *models.DecisionCard) applyResult {
	// Execute approve as a transaction for atomicity.
	err := e.cardStore.WithTx(ctx, func(tx *sqlx.Tx) error {
		// Step 1: Mark the card as approved.
		if err := e.cardStore.MarkCardApprovedTx(ctx, tx, change.CardID); err != nil {
			return fmt.Errorf("mark card approved: %w", err)
		}

		// Step 2: Mark the draft as approved.
		if change.ApprovedDraftID != nil {
			if err := e.draftStore.ApproveDraftTx(ctx, tx, *change.ApprovedDraftID); err != nil {
				// Draft not found is non-fatal — the card state change is what matters.
				e.log.Warn("approve: draft not found, continuing",
					"draft_id", *change.ApprovedDraftID,
					"card_id", change.CardID,
				)
			}
		} else {
			// Client didn't specify a draft ID — try to find and approve the latest.
			latestDraft, err := e.draftStore.GetLatestDraftForCard(ctx, change.CardID)
			if err == nil && latestDraft != nil {
				if aerr := e.draftStore.ApproveDraftTx(ctx, tx, latestDraft.ID); aerr != nil {
					e.log.Warn("approve: failed to approve latest draft",
						"draft_id", latestDraft.ID,
						"card_id", change.CardID,
						"error", aerr,
					)
				}
			}
		}

		// Step 3: Log the accepted change.
		return e.cardStore.LogChangeTx(ctx, tx, &SyncLogEntry{
			UserID:        userID,
			DeviceID:      deviceID,
			CardID:        &change.CardID,
			Operation:     "accept",
			ChangeType:    "approve",
			Reason:        "user_approved",
			ServerVersion: card.ServerVersion + 1,
		})
	})
	if err != nil {
		e.log.Error("approve transaction failed",
			"error", err,
			"card_id", change.CardID,
			"user_id", userID,
		)
		return applyResult{accepted: false, reason: "transaction_failed", serverState: card.CardState}
	}

	e.log.Info("change accepted: approve",
		"card_id", change.CardID,
		"user_id", userID,
		"draft_id", change.ApprovedDraftID,
	)
	return applyResult{accepted: true}
}

// applyEdit handles the "edit" decision: user edited a draft body.
//
// CRDT policy: draft_body is server-authoritative — server wins (WinnerServer).
// The user's edit is logged but NOT applied to the draft. On the next sync,
// the client will receive the server's draft version and should overwrite
// the local edit.
//
// This preserves the LLM-generated draft as the single source of truth while
// still recording that the user attempted an edit (for analytics / feedback).
func (e *SyncEngine) applyEdit(ctx context.Context, userID uuid.UUID, deviceID string, change models.LocalChange, card *models.DecisionCard) applyResult {
	// Log the edit attempt for analytics (server wins, but we note the edit).
	details := map[string]interface{}{
		"client_draft_body_length": 0,
		"server_state":             card.CardState,
	}
	if change.DraftBody != nil {
		details["client_draft_body_length"] = len(*change.DraftBody)
		// Store a truncated preview for the log (don't store the full body — privacy).
		preview := *change.DraftBody
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		details["client_draft_preview"] = preview
	}

	_ = e.cardStore.LogChange(ctx, &SyncLogEntry{
		UserID:        userID,
		DeviceID:      deviceID,
		CardID:        &change.CardID,
		Operation:     "accept",
		ChangeType:    "edit",
		Reason:        "draft_body_server_wins",
		ServerVersion: card.ServerVersion,
		Details:       mustJSON(details),
	})

	e.log.Info("change accepted (server wins): edit",
		"card_id", change.CardID,
		"user_id", userID,
		"note", "client edit logged but not applied; server draft is authoritative",
	)

	// Accept the change (the edit was noted) but the server draft remains.
	// The client will receive the server's draft on next sync.
	return applyResult{accepted: true}
}

// applyConsult handles the "consult" decision: user wants more information.
//
// CRDT policy: consult is a no-op on the server — it doesn't change card state.
// The client may transition the card to "consulting" locally for UI feedback,
// but the server doesn't track this transient state.
func (e *SyncEngine) applyConsult(ctx context.Context, userID uuid.UUID, deviceID string, change models.LocalChange, card *models.DecisionCard) applyResult {
	_ = e.cardStore.LogChange(ctx, &SyncLogEntry{
		UserID:        userID,
		DeviceID:      deviceID,
		CardID:        &change.CardID,
		Operation:     "accept",
		ChangeType:    "consult",
		Reason:        "user_consulting",
		ServerVersion: card.ServerVersion,
	})

	e.log.Debug("change accepted: consult (no-op)",
		"card_id", change.CardID,
		"user_id", userID,
	)
	return applyResult{accepted: true}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// validDecisions is the set of decision strings the engine recognises.
var validDecisions = map[string]bool{
	"approve": true,
	"edit":    true,
	"consult": true,
}

func isValidDecision(d string) bool {
	return validDecisions[d]
}

func safeDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func mustJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
