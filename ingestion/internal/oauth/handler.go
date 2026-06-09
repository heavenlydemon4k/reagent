package oauth

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

// SuccessCallback inverts the dependency: oauth knows nothing about backfill.
type SuccessCallback func(ctx context.Context, userID uuid.UUID) error

type Handler struct {
	db         *sql.DB
	log        *slog.Logger
	tokenStore TokenStore
	onSuccess  SuccessCallback
}

func NewHandler(
	db *sql.DB,
	log *slog.Logger,
	tokenStore TokenStore,
	onSuccess SuccessCallback,
) *Handler {
	return &Handler{
		db:         db,
		log:        log,
		tokenStore: tokenStore,
		onSuccess:  onSuccess,
	}
}

func (h *Handler) handleCallback(w http.ResponseWriter, r *http.Request) {
	// MERGE YOUR EXISTING TOKEN EXCHANGE LOGIC HERE.
	// This placeholder compiles immediately. Replace the uuid.Parse
	// block below with your real state/session lookup and ID-token extraction.
	var userID uuid.UUID
	if raw := r.URL.Query().Get("user_id"); raw != "" {
		if parsed, err := uuid.Parse(raw); err == nil {
			userID = parsed
		}
	}

	if userID == uuid.Nil {
		h.log.Error("oauth callback missing userID")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// --- end of your existing logic ---

	if h.onSuccess != nil {
		if err := h.onSuccess(r.Context(), userID); err != nil {
			h.log.Error("post-auth callback failed", "user_id", userID, "error", err)
		}
	}

	http.Redirect(w, r, "/", http.StatusFound)
}