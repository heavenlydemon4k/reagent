// Package decision provides HTTP handlers for the decision processing API.
package decision

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Handler — HTTP handlers for decision endpoints
// ---------------------------------------------------------------------------

// Processor is the interface satisfied by DecisionProcessor (and mocks in tests).
type Processor interface {
	ProcessDecision(ctx context.Context, userID, cardID uuid.UUID, decision string, input *string) (*models.DecideResponse, error)
	ProcessDraftModification(ctx context.Context, userID, cardID uuid.UUID, instruction string) (*models.DecideResponse, error)
	ProcessConsultation(ctx context.Context, userID uuid.UUID, req *models.ConsultRequest) (*models.ConsultResponse, error)
	ProcessEdit(ctx context.Context, userID, draftID uuid.UUID, body string) (*models.DecideResponse, error)
	ProcessApproval(ctx context.Context, userID, draftID uuid.UUID) error
	GetSourceCitations(ctx context.Context, userID, cardID uuid.UUID) ([]models.ChunkCitation, error)
}

// Handler holds all HTTP handlers for the decision API.
type Handler struct {
	processor    Processor
	approvalFlow *ApprovalFlow
	meshClient   IngestionMeshClient
	log          *slog.Logger
}

// NewHandler creates a new Handler.
// When processor is a *DecisionProcessor its approvalFlow is used automatically.
func NewHandler(processor Processor, meshClient IngestionMeshClient, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{processor: processor, meshClient: meshClient, log: log}
	if dp, ok := processor.(*DecisionProcessor); ok && dp != nil {
		h.approvalFlow = dp.approvalFlow
	}
	return h
}

// Routes registers all decision routes on the given router.
// Authentication middleware should be applied before these routes.
func (h *Handler) Routes(r chi.Router) {
	r.Post("/cards/{id}/decide", h.Decide)
	r.Post("/cards/{id}/draft", h.RequestDraft)
	r.Get("/cards/{id}/source", h.GetSource)
	r.Post("/drafts/{id}/approve", h.ApproveDraft)
	r.Post("/drafts/{id}/edit", h.EditDraft)
	r.Post("/consult", h.Consult)
	r.Post("/send", h.Send)
}

// ---------------------------------------------------------------------------
// Request/Response types
// ---------------------------------------------------------------------------

type decideRequestBody struct {
	Decision string  `json:"decision"`        // "approve", "edit", "consult"
	Input    *string `json:"input,omitempty"` // user instruction or question
}

type decideResponseBody struct {
	DraftID     uuid.UUID `json:"draft_id"`
	DraftBody   string    `json:"draft_body"`
	SubjectLine *string   `json:"subject_line,omitempty"`
}

type requestDraftBody struct {
	Instruction string `json:"instruction"` // e.g., "make it shorter"
}

type approveDraftBody struct {
	Approved bool `json:"approved"`
}

type editDraftBody struct {
	DraftBody string `json:"draft_body"`
}

type editDraftResponse struct {
	DraftID     uuid.UUID `json:"draft_id"`
	DraftBody   string    `json:"draft_body"`
	SubjectLine *string   `json:"subject_line,omitempty"`
}

type citationsResponse struct {
	Citations []citationItem `json:"citations"`
}

type citationItem struct {
	ChunkID         uuid.UUID `json:"chunk_id"`
	VerbatimSnippet string    `json:"verbatim_snippet"`
	EmailID         uuid.UUID `json:"email_id"`
	ParagraphIndex  int       `json:"paragraph_index"`
}

type consultRequestBody struct {
	CardID   uuid.UUID `json:"card_id"`
	Question string    `json:"question"`
}

type consultResponseBody struct {
	Answer         string         `json:"answer"`
	Citations      []citationItem `json:"citations"`
	TurnsRemaining int            `json:"turns_remaining"`
}

type sendRequestBody struct {
	DraftID uuid.UUID `json:"draft_id"`
}

type sendResponseBody struct {
	SentAt    time.Time `json:"sent_at"`
	MessageID string    `json:"message_id"`
}

// ---------------------------------------------------------------------------
// Common response helpers
// ---------------------------------------------------------------------------

type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
	Retry bool   `json:"retry,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// Fallback: if encoding fails, write a minimal error
		http.Error(w, `{"error":"internal encoding error"}`, http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string, retry bool) {
	writeJSON(w, status, errorResponse{Error: message, Code: code, Retry: retry})
}

// ---------------------------------------------------------------------------
// Context key for auth user ID
// ---------------------------------------------------------------------------

type ctxKey string

const ctxKeyUserID ctxKey = "user_id"

// WithUserID attaches a user ID to the context.
func WithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, userID)
}

// UserIDFromContext retrieves the user ID from context.
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(ctxKeyUserID).(uuid.UUID)
	return v, ok
}

// ---------------------------------------------------------------------------
// POST /cards/{id}/decide — Submit decision for a card
// ---------------------------------------------------------------------------

func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "auth_required", "authentication required", false)
		return
	}

	cardIDStr := chi.URLParam(r, "id")
	cardID, err := uuid.Parse(cardIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_card_id", "invalid card ID format", false)
		return
	}

	var body decideRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body", false)
		return
	}

	// Validate decision type
	if body.Decision != "approve" && body.Decision != "edit" && body.Decision != "consult" {
		writeError(w, http.StatusBadRequest, "invalid_decision",
			"decision must be one of: approve, edit, consult", false)
		return
	}

	h.log.Info("decide request", "card_id", cardID, "user_id", userID, "decision", body.Decision)

	resp, err := h.processor.ProcessDecision(ctx, userID, cardID, body.Decision, body.Input)
	if err != nil {
		switch e := err.(type) {
		case ErrCardNotFound:
			writeError(w, http.StatusNotFound, models.ErrCodeCardNotFound, e.Error(), false)
		case ErrCardOwnership:
			writeError(w, http.StatusForbidden, "forbidden", e.Error(), false)
		default:
			h.log.Error("process decision failed", "error", err, "card_id", cardID)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"failed to process decision", true)
		}
		return
	}

	writeJSON(w, http.StatusOK, decideResponseBody{
		DraftID:     resp.DraftID,
		DraftBody:   resp.DraftBody,
		SubjectLine: resp.SubjectLine,
	})
}

// ---------------------------------------------------------------------------
// POST /cards/{id}/draft — Request a new draft (after edit/rejection)
// ---------------------------------------------------------------------------

func (h *Handler) RequestDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "auth_required", "authentication required", false)
		return
	}

	cardIDStr := chi.URLParam(r, "id")
	cardID, err := uuid.Parse(cardIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_card_id", "invalid card ID format", false)
		return
	}

	var body requestDraftBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body", false)
		return
	}

	if body.Instruction == "" {
		writeError(w, http.StatusBadRequest, "missing_instruction", "instruction is required", false)
		return
	}

	h.log.Info("draft request", "card_id", cardID, "user_id", userID, "instruction", "[REDACTED:instruction]")

	resp, err := h.processor.ProcessDraftModification(ctx, userID, cardID, body.Instruction)
	if err != nil {
		switch e := err.(type) {
		case ErrCardNotFound:
			writeError(w, http.StatusNotFound, models.ErrCodeCardNotFound, e.Error(), false)
		case ErrCardOwnership:
			writeError(w, http.StatusForbidden, "forbidden", e.Error(), false)
		default:
			h.log.Error("draft modification failed", "error", err, "card_id", cardID)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"failed to generate draft", true)
		}
		return
	}

	writeJSON(w, http.StatusOK, decideResponseBody{
		DraftID:     resp.DraftID,
		DraftBody:   resp.DraftBody,
		SubjectLine: resp.SubjectLine,
	})
}

// ---------------------------------------------------------------------------
// POST /drafts/{id}/approve — Approve a draft (queue for send)
// ---------------------------------------------------------------------------

func (h *Handler) ApproveDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "auth_required", "authentication required", false)
		return
	}

	draftIDStr := chi.URLParam(r, "id")
	draftID, err := uuid.Parse(draftIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_draft_id", "invalid draft ID format", false)
		return
	}

	var body approveDraftBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body", false)
		return
	}

	if !body.Approved {
		writeError(w, http.StatusBadRequest, "not_approved", "approved must be true", false)
		return
	}

	h.log.Info("approve request", "draft_id", draftID, "user_id", userID)

	if err := h.processor.ProcessApproval(ctx, userID, draftID); err != nil {
		switch e := err.(type) {
		case ErrDraftNotFound:
			writeError(w, http.StatusNotFound, models.ErrCodeDraftNotFound, e.Error(), false)
		case ErrAlreadyApproved:
			writeError(w, http.StatusConflict, "already_approved", e.Error(), false)
		case ErrAlreadySent:
			writeError(w, http.StatusConflict, "already_sent", e.Error(), false)
		case ErrCardOwnership:
			writeError(w, http.StatusForbidden, "forbidden", e.Error(), false)
		default:
			h.log.Error("approve failed", "error", err, "draft_id", draftID)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"failed to approve draft", true)
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "approved", "draft_id": draftID.String()})
}

// ---------------------------------------------------------------------------
// POST /drafts/{id}/edit — Submit edited draft
// ---------------------------------------------------------------------------

func (h *Handler) EditDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "auth_required", "authentication required", false)
		return
	}

	draftIDStr := chi.URLParam(r, "id")
	draftID, err := uuid.Parse(draftIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_draft_id", "invalid draft ID format", false)
		return
	}

	var body editDraftBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body", false)
		return
	}

	if body.DraftBody == "" {
		writeError(w, http.StatusBadRequest, "empty_body", "draft body cannot be empty", false)
		return
	}

	h.log.Info("edit request", "draft_id", draftID, "user_id", userID)

	resp, err := h.processor.ProcessEdit(ctx, userID, draftID, body.DraftBody)
	if err != nil {
		switch e := err.(type) {
		case ErrDraftNotFound:
			writeError(w, http.StatusNotFound, models.ErrCodeDraftNotFound, e.Error(), false)
		case ErrCardOwnership:
			writeError(w, http.StatusForbidden, "forbidden", e.Error(), false)
		default:
			h.log.Error("edit failed", "error", err, "draft_id", draftID)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"failed to update draft", true)
		}
		return
	}

	writeJSON(w, http.StatusOK, editDraftResponse{
		DraftID:     resp.DraftID,
		DraftBody:   resp.DraftBody,
		SubjectLine: resp.SubjectLine,
	})
}

// ---------------------------------------------------------------------------
// GET /cards/{id}/source — Get verbatim citations for a card
// ---------------------------------------------------------------------------

func (h *Handler) GetSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "auth_required", "authentication required", false)
		return
	}

	cardIDStr := chi.URLParam(r, "id")
	cardID, err := uuid.Parse(cardIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_card_id", "invalid card ID format", false)
		return
	}

	citations, err := h.processor.GetSourceCitations(ctx, userID, cardID)
	if err != nil {
		switch e := err.(type) {
		case ErrCardNotFound, ErrCardOwnership:
			writeError(w, http.StatusNotFound, models.ErrCodeCardNotFound, e.Error(), false)
		default:
			h.log.Error("get citations failed", "error", err, "card_id", cardID)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"failed to retrieve citations", true)
		}
		return
	}

	items := make([]citationItem, len(citations))
	for i, c := range citations {
		items[i] = citationItem{
			ChunkID:         c.ChunkID,
			VerbatimSnippet: c.VerbatimSnippet,
			EmailID:         c.EmailID,
			ParagraphIndex:  c.ParagraphIndex,
		}
	}

	writeJSON(w, http.StatusOK, citationsResponse{Citations: items})
}

// ---------------------------------------------------------------------------
// POST /consult — Ask a consultation question about a card
// ---------------------------------------------------------------------------

func (h *Handler) Consult(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "auth_required", "authentication required", false)
		return
	}

	var body consultRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body", false)
		return
	}

	if body.CardID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "missing_card_id", "card_id is required", false)
		return
	}
	if body.Question == "" {
		writeError(w, http.StatusBadRequest, "missing_question", "question is required", false)
		return
	}

	h.log.Info("consult request", "card_id", body.CardID, "user_id", userID)

	req := &models.ConsultRequest{
		CardID:   body.CardID,
		Question: body.Question,
	}

	resp, err := h.processor.ProcessConsultation(ctx, userID, req)
	if err != nil {
		switch e := err.(type) {
		case ErrCardNotFound:
			writeError(w, http.StatusNotFound, models.ErrCodeCardNotFound, e.Error(), false)
		case ErrCardOwnership:
			writeError(w, http.StatusForbidden, "forbidden", e.Error(), false)
		case ErrConsultationTurnsExceeded:
			writeError(w, http.StatusTooManyRequests, "turns_exceeded", e.Error(), false)
		case ErrConsultationRejected:
			writeError(w, http.StatusBadRequest, "consult_rejected", e.Error(), false)
		default:
			h.log.Error("consult failed", "error", err, "card_id", body.CardID)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"consultation failed", true)
		}
		return
	}

	items := make([]citationItem, len(resp.Citations))
	for i, c := range resp.Citations {
		items[i] = citationItem{
			ChunkID:         c.ChunkID,
			VerbatimSnippet: c.VerbatimSnippet,
			EmailID:         c.EmailID,
			ParagraphIndex:  c.ParagraphIndex,
		}
	}

	writeJSON(w, http.StatusOK, consultResponseBody{
		Answer:         resp.Answer,
		Citations:      items,
		TurnsRemaining: resp.TurnsRemaining,
	})
}

// ---------------------------------------------------------------------------
// POST /send — Execute send for approved draft
// ---------------------------------------------------------------------------

func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "auth_required", "authentication required", false)
		return
	}

	var body sendRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid request body", false)
		return
	}

	if body.DraftID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "missing_draft_id", "draft_id is required", false)
		return
	}

	h.log.Info("send request", "draft_id", body.DraftID, "user_id", userID)

	// Execute send via the approval flow's direct send path
	if h.approvalFlow == nil {
		writeError(w, http.StatusInternalServerError, "server_error", "approval flow not configured", false)
		return
	}
	result, err := h.approvalFlow.ExecuteSend(ctx, h.meshClient, body.DraftID, userID)
	if err != nil {
		switch e := err.(type) {
		case ErrDraftNotFound:
			writeError(w, http.StatusNotFound, models.ErrCodeDraftNotFound, e.Error(), false)
		case ErrNotApproved:
			writeError(w, http.StatusConflict, "not_approved", e.Error(), false)
		case ErrAlreadySent:
			writeError(w, http.StatusConflict, "already_sent", e.Error(), false)
		default:
			h.log.Error("send failed", "error", err, "draft_id", body.DraftID)
			writeError(w, http.StatusInternalServerError, "internal_error",
				"failed to send email", true)
		}
		return
	}

	writeJSON(w, http.StatusOK, sendResponseBody{
		SentAt:    result.SentAt,
		MessageID: result.MessageID,
	})
}
