// Package rules provides HTTP handlers for the CRUD rules API.
package rules

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/decisionstack/classification/internal/logger"
	"github.com/decisionstack/classification/internal/middleware"
	"github.com/decisionstack/classification/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Handler implements the /rules REST API.
type Handler struct {
	store *Store
	log   *logger.Logger
}

// NewHandler creates a rules HTTP handler.
func NewHandler(store *Store, log *logger.Logger) *Handler {
	return &Handler{
		store: store,
		log:   log.WithComponent("rules-handler"),
	}
}

// Routes mounts the rules CRUD endpoints.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/", h.listRules)
	r.Post("/", h.createRule)
	r.Get("/{ruleID}", h.getRule)
	r.Put("/{ruleID}/activate", h.activateRule)
	r.Delete("/{ruleID}", h.revokeRule)
}

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request) {
	userID, err := extractUserID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid user_id")
		return
	}

	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	rules, err := h.store.ListByUser(r.Context(), userID, status, limit, offset)
	if err != nil {
		h.log.Error("list rules failed", "error", err)
		respondError(w, r, http.StatusInternalServerError, "failed to list rules")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"data":   rules,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *Handler) getRule(w http.ResponseWriter, r *http.Request) {
	ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid rule_id")
		return
	}

	rule, err := h.store.GetByID(r.Context(), ruleID)
	if err != nil {
		h.log.Error("get rule failed", "error", err)
		respondError(w, r, http.StatusNotFound, "rule not found")
		return
	}

	respondJSON(w, http.StatusOK, rule)
}

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request) {
	userID, err := extractUserID(r)
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid user_id")
		return
	}

	var req struct {
		Name                string              `json:"name"`
		Predicate           models.RulePredicate `json:"predicate"`
		ActionType          string              `json:"action_type"`
		ActionConfig        json.RawMessage     `json:"action_config"`
		ConfidenceThreshold float64             `json:"confidence_threshold"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate
	if req.Name == "" {
		respondError(w, r, http.StatusBadRequest, "name is required")
		return
	}
	if req.ActionType == "" {
		respondError(w, r, http.StatusBadRequest, "action_type is required")
		return
	}
	if req.ConfidenceThreshold == 0 {
		req.ConfidenceThreshold = 0.92
	}
	if req.ConfidenceThreshold < 0.92 {
		respondError(w, r, http.StatusBadRequest, "confidence_threshold below hard floor of 0.92")
		return
	}

	rule, err := h.store.Create(r.Context(), userID, req.Name, req.Predicate, req.ActionType, req.ActionConfig, req.ConfidenceThreshold)
	if err != nil {
		h.log.Error("create rule failed", "error", err)
		respondError(w, r, http.StatusInternalServerError, "failed to create rule")
		return
	}

	respondJSON(w, http.StatusCreated, rule)
}

func (h *Handler) activateRule(w http.ResponseWriter, r *http.Request) {
	ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid rule_id")
		return
	}

	if err := h.store.Activate(r.Context(), ruleID); err != nil {
		h.log.Error("activate rule failed", "error", err)
		respondError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "activated"})
}

func (h *Handler) revokeRule(w http.ResponseWriter, r *http.Request) {
	ruleID, err := uuid.Parse(chi.URLParam(r, "ruleID"))
	if err != nil {
		respondError(w, r, http.StatusBadRequest, "invalid rule_id")
		return
	}

	if err := h.store.Revoke(r.Context(), ruleID); err != nil {
		h.log.Error("revoke rule failed", "error", err)
		respondError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func extractUserID(r *http.Request) (uuid.UUID, error) {
	// In production this would come from an auth middleware
	// For now, check header or query param
	uidStr := r.URL.Query().Get("user_id")
	if uidStr == "" {
		uidStr = r.Header.Get("X-User-ID")
	}
	if uidStr == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(uidStr)
}

func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func respondError(w http.ResponseWriter, r *http.Request, status int, message string) {
	respondJSON(w, status, map[string]string{
		"error":      message,
		"request_id": middleware.GetRequestID(r.Context()),
	})
}
