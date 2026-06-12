package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/decisionstack/ingestion/internal/nats"
)

// SendHandler handles POST /api/v1/send from the Intelligence service.
// It validates the request, builds a SendJobPayload, and publishes it to the
// email.send NATS stream. The send_consumer worker handles actual dispatch.
type SendHandler struct {
	publisher nats.Publisher
}

// NewSendHandler creates a SendHandler.
func NewSendHandler(publisher nats.Publisher) *SendHandler {
	return &SendHandler{publisher: publisher}
}

type sendRequest struct {
	UserID     string   `json:"user_id"`
	ThreadID   string   `json:"thread_id"`
	To         string   `json:"to"`
	AccountID  string   `json:"account_id"`
	Body       string   `json:"body"`
	Subject    string   `json:"subject"`
	InReplyTo  string   `json:"in_reply_to"`
	References []string `json:"references"`
}

type sendResponse struct {
	Queued  bool      `json:"queued"`
	DraftID uuid.UUID `json:"draft_id"`
}

// HandleSend processes the send request and enqueues the job.
func (h *SendHandler) HandleSend(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		http.Error(w, `{"error":"user_id must be a valid UUID"}`, http.StatusBadRequest)
		return
	}

	threadID, err := uuid.Parse(req.ThreadID)
	if err != nil {
		threadID = uuid.Nil
	}

	if req.Body == "" {
		http.Error(w, `{"error":"body is required"}`, http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.To) == "" {
		http.Error(w, `{"error":"to is required"}`, http.StatusBadRequest)
		return
	}

	// Synthetic draft ID for tracing only. The recipient and sending account are
	// carried explicitly on the payload (To/AccountID), so the consumer does not
	// depend on this ID existing in the drafts table.
	draftID := uuid.New()

	var inReplyTo *string
	if req.InReplyTo != "" {
		inReplyTo = &req.InReplyTo
	}

	payload := nats.SendJobPayload{
		DraftID:    draftID,
		UserID:     userID,
		ThreadID:   threadID,
		To:         strings.TrimSpace(req.To),
		AccountID:  strings.TrimSpace(req.AccountID),
		DraftBody:  req.Body,
		Subject:    req.Subject,
		InReplyTo:  inReplyTo,
		References: req.References,
	}

	if err := h.publisher.PublishSendJob(r.Context(), payload); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"error":"failed to queue send job: %s"}`+"\n", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(sendResponse{Queued: true, DraftID: draftID})
}
