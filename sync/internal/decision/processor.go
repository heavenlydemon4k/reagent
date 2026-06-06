package decision

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// DecisionProcessor — core decision processing orchestrator
// ---------------------------------------------------------------------------

// DecisionProcessor coordinates decision handling: draft generation, consultation,
// modification, and routing to the approval flow.
type DecisionProcessor struct {
	draftingProxy *DraftingProxy
	consultProxy  *ConsultProxy
	approvalFlow  *ApprovalFlow
	cardStore     *CardStore
	draftStore    *DraftStore
	log           *slog.Logger
}

// NewDecisionProcessor creates a new DecisionProcessor.
func NewDecisionProcessor(
	draftingProxy *DraftingProxy,
	consultProxy *ConsultProxy,
	approvalFlow *ApprovalFlow,
	cardStore *CardStore,
	draftStore *DraftStore,
	log *slog.Logger,
) *DecisionProcessor {
	if log == nil {
		log = slog.Default()
	}
	return &DecisionProcessor{
		draftingProxy: draftingProxy,
		consultProxy:  consultProxy,
		approvalFlow:  approvalFlow,
		cardStore:     cardStore,
		draftStore:    draftStore,
		log:           log,
	}
}

// ---------------------------------------------------------------------------
// ProcessDecision — handles approve, edit, and consult decisions
// ---------------------------------------------------------------------------

// ErrInvalidDecision is returned when the decision type is not recognized.
type ErrInvalidDecision struct{ Decision string }

func (e ErrInvalidDecision) Error() string { return fmt.Sprintf("invalid decision: %s", e.Decision) }

// ErrCardStateConflict is returned when a card is not in a valid state for the operation.
type ErrCardStateConflict struct {
	CardID       uuid.UUID
	CurrentState string
	Required     string
}

func (e ErrCardStateConflict) Error() string {
	return fmt.Sprintf("card %s in state %q, required %q", e.CardID, e.CurrentState, e.Required)
}

// ProcessDecision handles the user's decision on a card.
//
// Decision types:
//   - "approve": Generate a draft, return it for review
//   - "edit":    Generate a draft with user input, return it for review
//   - "consult": Forward question to Intelligence Layer, return consultation response
func (p *DecisionProcessor) ProcessDecision(ctx context.Context, userID uuid.UUID, cardID uuid.UUID, decision string, input *string) (*models.DecideResponse, error) {
	start := time.Now()
	defer func() {
		p.log.Info("process_decision_complete",
			"card_id", cardID,
			"user_id", userID,
			"decision", decision,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	// 1. Verify card ownership and existence
	card, err := p.cardStore.GetCardOwnedBy(ctx, cardID, userID)
	if err != nil {
		return nil, fmt.Errorf("verify card: %w", err)
	}

	// 2. Update card state to "drafting" (for approve/edit) or "consulting"
	switch decision {
	case "approve", "edit":
		if err := p.cardStore.UpdateCardState(ctx, cardID, "drafting"); err != nil {
			return nil, fmt.Errorf("set card drafting: %w", err)
		}

		// 3. Forward to Intelligence Layer for draft generation
		draft, err := p.draftingProxy.GenerateDraft(ctx, cardID, userID, card.ThreadID, input)
		if err != nil {
			// Revert card state on failure
			if rbErr := p.cardStore.UpdateCardState(ctx, cardID, card.CardState); rbErr != nil {
				p.log.Warn("failed to rollback card state", "card_id", cardID, "error", rbErr)
			}
			return nil, fmt.Errorf("generate draft: %w", err)
		}

		// 4. Store draft in PostgreSQL
		if err := p.draftStore.CreateDraft(ctx, draft); err != nil {
			return nil, fmt.Errorf("store draft: %w", err)
		}

		// 5. Log the decision
		logDetails := fmt.Sprintf(`{"action":"%s","draft_id":"%s"}`, decision, draft.ID)
		if err := p.draftStore.LogDecision(ctx, userID, cardID, decision, logDetails); err != nil {
			p.log.Warn("failed to log decision", "error", err, "card_id", cardID)
		}

		p.log.Info("draft generated",
			"draft_id", draft.ID,
			"card_id", cardID,
			"model", draft.ModelUsed,
		)

		return &models.DecideResponse{
			DraftID:     draft.ID,
			DraftBody:   draft.DraftBody,
			SubjectLine: draft.SubjectLine,
		}, nil

	case "consult":
		if input == nil || *input == "" {
			return nil, fmt.Errorf("consult decision requires input (question)")
		}
		if err := p.cardStore.UpdateCardState(ctx, cardID, "consulting"); err != nil {
			return nil, fmt.Errorf("set card consulting: %w", err)
		}

		consultResp, err := p.consultProxy.Ask(ctx, cardID, userID, *input, 0)
		if err != nil {
			// Revert card state on failure
			if rbErr := p.cardStore.UpdateCardState(ctx, cardID, card.CardState); rbErr != nil {
				p.log.Warn("failed to rollback card state", "card_id", cardID, "error", rbErr)
			}
			return nil, fmt.Errorf("consultation: %w", err)
		}

		// Log the consult decision
		logDetails := fmt.Sprintf(`{"action":"consult","turns_remaining":%d}`, consultResp.TurnsRemaining)
		if err := p.draftStore.LogDecision(ctx, userID, cardID, "consult", logDetails); err != nil {
			p.log.Warn("failed to log consult decision", "error", err, "card_id", cardID)
		}

		return &models.DecideResponse{
			DraftID:   uuid.Nil,
			DraftBody: consultResp.Answer,
		}, nil

	default:
		return nil, ErrInvalidDecision{Decision: decision}
	}
}

// ---------------------------------------------------------------------------
// ProcessDraftModification — regenerate a draft with instructions
// ---------------------------------------------------------------------------

// ProcessDraftModification requests a new or modified draft based on user instructions.
// This is used after the user has edited or rejected a previous draft.
func (p *DecisionProcessor) ProcessDraftModification(ctx context.Context, userID uuid.UUID, cardID uuid.UUID, instruction string) (*models.DecideResponse, error) {
	start := time.Now()
	defer func() {
		p.log.Info("process_draft_modification_complete",
			"card_id", cardID,
			"user_id", userID,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	if instruction == "" {
		return nil, fmt.Errorf("modification instruction is required")
	}

	// 1. Verify card ownership
	card, err := p.cardStore.GetCardOwnedBy(ctx, cardID, userID)
	if err != nil {
		return nil, fmt.Errorf("verify card: %w", err)
	}

	// 2. Update card state to "drafting"
	if err := p.cardStore.UpdateCardState(ctx, cardID, "drafting"); err != nil {
		return nil, fmt.Errorf("set card drafting: %w", err)
	}

	// 3. Get the latest draft for this card to use as base
	latestDraft, err := p.draftStore.GetLatestDraftForCard(ctx, cardID)
	if err != nil {
		// No previous draft — that's ok, generate fresh
		p.log.Info("no previous draft found, generating fresh", "card_id", cardID)
	}

	// 4. Forward to Intelligence Layer for modification
	var draft *models.Draft
	if latestDraft != nil {
		draft, err = p.draftingProxy.ModifyDraft(ctx, latestDraft.ID, cardID, userID, card.ThreadID, instruction)
	} else {
		draft, err = p.draftingProxy.GenerateDraft(ctx, cardID, userID, card.ThreadID, &instruction)
	}
	if err != nil {
		// Revert card state on failure
		if rbErr := p.cardStore.UpdateCardState(ctx, cardID, card.CardState); rbErr != nil {
			p.log.Warn("failed to rollback card state", "card_id", cardID, "error", rbErr)
		}
		return nil, fmt.Errorf("modify draft: %w", err)
	}

	// 5. Store the new draft
	if err := p.draftStore.CreateDraft(ctx, draft); err != nil {
		return nil, fmt.Errorf("store modified draft: %w", err)
	}

	// 6. Log
	logDetails := fmt.Sprintf(`{"action":"draft_modify","draft_id":"%s","instruction":"%s"}`, draft.ID, instruction)
	if err := p.draftStore.LogDecision(ctx, userID, cardID, "draft_modify", logDetails); err != nil {
		p.log.Warn("failed to log draft modification", "error", err, "card_id", cardID)
	}

	return &models.DecideResponse{
		DraftID:     draft.ID,
		DraftBody:   draft.DraftBody,
		SubjectLine: draft.SubjectLine,
	}, nil
}

// ---------------------------------------------------------------------------
// ProcessConsultation — standalone consultation handler
// ---------------------------------------------------------------------------

// ProcessConsultation handles a consultation request about a card.
func (p *DecisionProcessor) ProcessConsultation(ctx context.Context, userID uuid.UUID, req *models.ConsultRequest) (*models.ConsultResponse, error) {
	start := time.Now()
	defer func() {
		p.log.Info("process_consultation_complete",
			"card_id", req.CardID,
			"user_id", userID,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	if req.Question == "" {
		return nil, fmt.Errorf("consultation question is required")
	}

	// 1. Verify card ownership
	if _, err := p.cardStore.GetCardOwnedBy(ctx, req.CardID, userID); err != nil {
		return nil, fmt.Errorf("verify card: %w", err)
	}

	// 2. Forward to Intelligence Layer
	resp, err := p.consultProxy.Ask(ctx, req.CardID, userID, req.Question, 0)
	if err != nil {
		return nil, fmt.Errorf("consultation: %w", err)
	}

	// 3. Log
	logDetails := fmt.Sprintf(`{"action":"consult","turns_remaining":%d}`, resp.TurnsRemaining)
	if err := p.draftStore.LogDecision(ctx, userID, req.CardID, "consult", logDetails); err != nil {
		p.log.Warn("failed to log consultation", "error", err, "card_id", req.CardID)
	}

	return resp, nil
}

// ---------------------------------------------------------------------------
// ProcessEdit — user edits a draft directly
// ---------------------------------------------------------------------------

// ProcessEdit stores a user-edited draft body. This creates a new draft record
// with the edited content, preserving the original.
func (p *DecisionProcessor) ProcessEdit(ctx context.Context, userID uuid.UUID, draftID uuid.UUID, newBody string) (*models.DecideResponse, error) {
	if newBody == "" {
		return nil, fmt.Errorf("draft body cannot be empty")
	}

	// 1. Verify draft ownership
	draft, err := p.draftStore.GetDraftOwnedBy(ctx, draftID, userID)
	if err != nil {
		return nil, fmt.Errorf("verify draft: %w", err)
	}

	// 2. Update the draft body
	if err := p.draftStore.UpdateDraftBody(ctx, draftID, userID, newBody); err != nil {
		return nil, fmt.Errorf("update draft body: %w", err)
	}

	// 3. Log
	logDetails := fmt.Sprintf(`{"action":"edit","draft_id":"%s"}`, draftID)
	if err := p.draftStore.LogDecision(ctx, userID, draft.CardID, "edit", logDetails); err != nil {
		p.log.Warn("failed to log edit", "error", err, "draft_id", draftID)
	}

	p.log.Info("draft edited by user", "draft_id", draftID, "card_id", draft.CardID)

	return &models.DecideResponse{
		DraftID:     draftID,
		DraftBody:   newBody,
		SubjectLine: draft.SubjectLine,
	}, nil
}

// ---------------------------------------------------------------------------
// ProcessApproval — delegate to ApprovalFlow
// ---------------------------------------------------------------------------

// ProcessApproval delegates to the ApprovalFlow to approve a draft.
func (p *DecisionProcessor) ProcessApproval(ctx context.Context, userID uuid.UUID, draftID uuid.UUID) error {
	if err := p.approvalFlow.Approve(ctx, draftID, userID); err != nil {
		return fmt.Errorf("approval: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// GetSourceCitations — retrieve verbatim citations for a card
// ---------------------------------------------------------------------------

// GetSourceCitations returns the chunk citations for a card's sources.
func (p *DecisionProcessor) GetSourceCitations(ctx context.Context, userID uuid.UUID, cardID uuid.UUID) ([]models.ChunkCitation, error) {
	citations, err := p.cardStore.GetCardCitations(ctx, cardID, userID)
	if err != nil {
		return nil, fmt.Errorf("get citations: %w", err)
	}
	return citations, nil
}
