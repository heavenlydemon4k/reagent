package webhook

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/fetch"
	ingestionnats "github.com/decisionstack/ingestion/internal/nats"
	"github.com/decisionstack/ingestion/internal/logutil"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
)

// GmailPubSubRequest is the incoming request body from Gmail Pub/Sub push.
type GmailPubSubRequest struct {
	Message *GmailPubSubMessageData `json:"message"`
}

// GmailPubSubMessageData contains the base64-encoded data.
type GmailPubSubMessageData struct {
	Data        string            `json:"data"` // base64-encoded JSON
	Attributes  map[string]string `json:"attributes,omitempty"`
	MessageID   string            `json:"messageId"`   // Pub/Sub message ID for dedup
	PublishTime string            `json:"publishTime"` // RFC3339
}

// GmailHistoryData is the decoded inner payload from Gmail.
type GmailHistoryData struct {
	EmailAddress string `json:"emailAddress"`
	HistoryID    uint64 `json:"historyId"`
}

// OutlookWebhookRequest is the incoming request from Outlook Graph.
type OutlookWebhookRequest struct {
	Value []OutlookNotification `json:"value"`
}

// OutlookNotification is a single change notification from Outlook.
type OutlookNotification struct {
	ChangeType     string `json:"changeType"`
	Resource       string `json:"resource"`
	SubscriptionID string `json:"subscriptionId"`
	ClientState    string `json:"clientState"`
	ID             string `json:"id"` // notification ID for dedup
}

// WebhookHandler handles HTTP requests for Gmail and Outlook webhooks.
type WebhookHandler struct {
	verifier  *Verifier
	dedup     *DedupChecker
	enqueuer  *fetch.Enqueuer
	publisher ingestionnats.Publisher
	log       *slog.Logger
}

// NewWebhookHandler creates a new WebhookHandler with all dependencies.
func NewWebhookHandler(
	verifier *Verifier,
	dedup *DedupChecker,
	enqueuer *fetch.Enqueuer,
	publisher ingestionnats.Publisher,
	log *slog.Logger,
) *WebhookHandler {
	return &WebhookHandler{
		verifier:  verifier,
		dedup:     dedup,
		enqueuer:  enqueuer,
		publisher: publisher,
		log:       log,
	}
}

// NewHandler is a convenience constructor that creates a WebhookHandler
// from the core service dependencies.
func NewHandler(
	cfg *config.Config,
	redisClient redis.Cmdable,
	publisher ingestionnats.Publisher,
	enqueuer *fetch.Enqueuer,
	log *slog.Logger,
) *WebhookHandler {
	verifier := NewVerifier()
	dedup := NewDedupChecker(redisClient)
	return NewWebhookHandler(verifier, dedup, enqueuer, publisher, log)
}

// Routes returns a chi.Router with all webhook routes mounted.
// Use this to Mount("/webhooks", webhookHandler.Routes()).
func (h *WebhookHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/gmail", h.HandleGmail)
	r.Post("/outlook", h.HandleOutlook)
	return r
}

// ==========================================
// Gmail Webhook Handler
// ==========================================

// HandleGmail processes Gmail Pub/Sub push notifications.
// Steps:
//  1. Read and parse the request body
//  2. Decode base64 payload
//  3. Extract historyId
//  4. Verify JWT (if Authorization header present)
//  5. Check dedup
//  6. Enqueue fetch job
//  7. Return 200 immediately
func (h *WebhookHandler) HandleGmail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.log.WarnContext(ctx, "failed to read gmail webhook body", slog.String("error", err.Error()))
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse outer Pub/Sub envelope
	var req GmailPubSubRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.log.WarnContext(ctx, "failed to parse gmail webhook body", slog.String("error", err.Error()))
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	if req.Message == nil {
		h.log.WarnContext(ctx, "gmail webhook missing message")
		http.Error(w, `{"error":"missing message"}`, http.StatusBadRequest)
		return
	}

	// Decode base64 data
	dataBytes, err := base64.StdEncoding.DecodeString(req.Message.Data)
	if err != nil {
		h.log.WarnContext(ctx, "failed to decode gmail data", slog.String("error", err.Error()))
		http.Error(w, `{"error":"invalid base64"}`, http.StatusBadRequest)
		return
	}

	// Parse inner history data
	var historyData GmailHistoryData
	if err := json.Unmarshal(dataBytes, &historyData); err != nil {
		h.log.WarnContext(ctx, "failed to parse gmail history data", slog.String("error", err.Error()))
		http.Error(w, `{"error":"invalid history data"}`, http.StatusBadRequest)
		return
	}

	// JWT verification (Gmail Pub/Sub pushes include a JWT in the Authorization header)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		token := extractBearerToken(authHeader)
		if token != "" {
			claims, err := h.verifier.VerifyGmailJWT(token)
			if err != nil {
				h.log.WarnContext(ctx, "gmail jwt verification failed",
					slog.String("error", err.Error()),
				)
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			h.log.DebugContext(ctx, "gmail jwt verified",
				slog.String("email", logutil.New().RedactEmail(claims.Email)),
			)
		}
	} else {
		h.log.DebugContext(ctx, "gmail webhook: no authorization header, skipping jwt verify")
	}

	// Use Pub/Sub message ID for dedup, falling back to historyId
	dedupKey := req.Message.MessageID
	if dedupKey == "" {
		dedupKey = DedupKeyGmail(historyData.HistoryID)
	} else {
		dedupKey = "gmail:msg:" + dedupKey
	}

	isDup, err := h.dedup.IsDuplicate(ctx, dedupKey)
	if err != nil {
		h.log.ErrorContext(ctx, "dedup check failed", slog.String("error", err.Error()))
		// Non-fatal: continue processing rather than drop
	}
	if isDup {
		h.log.DebugContext(ctx, "duplicate gmail webhook dropped",
			slog.Uint64("history_id", historyData.HistoryID),
		)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Extract account identifier from the email address
	// In production, look up the account ID from the email -> account mapping
	accountID := historyData.EmailAddress // placeholder; will be resolved by the fetch worker

	// Create and enqueue fetch job
	job := fetch.NewGmailFetchJob(historyData.EmailAddress, accountID, historyData.HistoryID)

	if err := h.enqueuer.EnqueueFetchJob(ctx, *job); err != nil {
		h.log.ErrorContext(ctx, "failed to enqueue gmail fetch job",
			slog.String("error", err.Error()),
			slog.Uint64("history_id", historyData.HistoryID),
		)
		// Return 200 anyway — Pub/Sub will retry if we return error,
		// but we already dedup'd so retry would be dropped.
		// Log for monitoring/alerting.
	}

	h.log.InfoContext(ctx, "gmail webhook processed",
		slog.String("email", logutil.New().RedactEmail(historyData.EmailAddress)),
		slog.Uint64("history_id", historyData.HistoryID),
		slog.String("job_id", job.ID),
	)

	w.WriteHeader(http.StatusOK)
}

// ==========================================
// Outlook Webhook Handler
// ==========================================

// HandleOutlook processes Outlook Graph change notifications.
// Steps:
//  1. Handle validation token (subscription creation handshake)
//  2. Parse notification payload
//  3. For each notification: extract changeType + resource, dedup, enqueue
//  4. Return 202 Accepted
func (h *WebhookHandler) HandleOutlook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Handle validation token (subscription creation handshake) — query param
	validationToken := r.URL.Query().Get("validationToken")
	if validationToken == "" {
		// Also check body for validation token format
		body, err := io.ReadAll(r.Body)
		if err != nil {
			h.log.WarnContext(ctx, "failed to read outlook webhook body", slog.String("error", err.Error()))
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		r.Body.Close()

		// Try to parse as validation token request
		var valReq struct {
			ValidationToken string `json:"validationToken"`
		}
		if err := json.Unmarshal(body, &valReq); err == nil && valReq.ValidationToken != "" {
			validationToken = valReq.ValidationToken
		}

		// If no validation token, restore body for notification parsing
		if validationToken == "" {
			r.Body = io.NopCloser(&byteReader{data: body, pos: 0})
		}
	}

	// Respond with validation token as plaintext within 10 seconds (Outlook requirement)
	if validationToken != "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(validationToken))
		h.log.DebugContext(ctx, "outlook validation token responded")
		return
	}

	// Read and parse the notification body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.log.WarnContext(ctx, "failed to read outlook notification body", slog.String("error", err.Error()))
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var envelope OutlookWebhookRequest
	if err := json.Unmarshal(body, &envelope); err != nil {
		h.log.WarnContext(ctx, "failed to parse outlook notification", slog.String("error", err.Error()))
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	if len(envelope.Value) == 0 {
		h.log.DebugContext(ctx, "outlook notification with no values")
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Process each notification
	processed := 0
	skipped := 0
	for _, notification := range envelope.Value {
		// Dedup by notification ID
		if notification.ID == "" {
			h.log.WarnContext(ctx, "outlook notification missing ID, generating dedup key from resource")
			notification.ID = notification.Resource
		}

		isDup, err := h.dedup.IsDuplicate(ctx, DedupKeyOutlook(notification.ID))
		if err != nil {
			h.log.ErrorContext(ctx, "dedup check failed for outlook notification",
				slog.String("error", err.Error()),
				slog.String("notification_id", notification.ID),
			)
			// Continue processing — don't drop on dedup check failure
		}
		if isDup {
			skipped++
			continue
		}

		// Extract user info from resource (e.g., "Users('user-id')/Messages('msg-id')")
		userID := extractUserFromResource(notification.Resource)
		accountID := userID // placeholder; resolved by fetch worker

		// Enqueue fetch job
		job := fetch.NewOutlookFetchJob(userID, accountID, notification.Resource)
		if err := h.enqueuer.EnqueueFetchJob(ctx, *job); err != nil {
			h.log.ErrorContext(ctx, "failed to enqueue outlook fetch job",
				slog.String("error", err.Error()),
				slog.String("notification_id", notification.ID),
			)
			// Continue — don't fail the entire batch for one job
			continue
		}

		h.log.InfoContext(ctx, "outlook notification processed",
			slog.String("notification_id", notification.ID),
			slog.String("change_type", notification.ChangeType),
			slog.String("resource", notification.Resource),
			slog.String("job_id", job.ID),
		)
		processed++
	}

	h.log.DebugContext(ctx, "outlook webhook batch processed",
		slog.Int("total", len(envelope.Value)),
		slog.Int("processed", processed),
		slog.Int("skipped_dup", skipped),
	)

	w.WriteHeader(http.StatusAccepted)
}

// ==========================================
// Health Handler
// ==========================================

// HealthResponse is the response body for health checks.
type HealthResponse struct {
	Status  string            `json:"status"`
	Version string            `json:"version,omitempty"`
	Checks  map[string]string `json:"checks,omitempty"`
	Time    time.Time         `json:"time"`
}

// HandleHealth returns the health status of the service.
func (h *WebhookHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	checks := make(map[string]string)

	// Check NATS
	natsStatus := "ok"
	if h.publisher != nil {
		if err := h.publisher.HealthCheck(); err != nil {
			natsStatus = fmt.Sprintf("error: %v", err)
			h.log.WarnContext(ctx, "health check: nats unhealthy", slog.String("error", err.Error()))
		}
	} else {
		natsStatus = "not configured"
	}
	checks["nats"] = natsStatus

	status := http.StatusOK
	overall := "healthy"
	for _, v := range checks {
		if v != "ok" {
			overall = "degraded"
			status = http.StatusServiceUnavailable
			break
		}
	}

	resp := HealthResponse{
		Status: overall,
		Checks: checks,
		Time:   time.Now().UTC(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// ==========================================
// Helpers
// ==========================================

// extractBearerToken extracts the token from an Authorization header.
func extractBearerToken(authHeader string) string {
	const prefix = "Bearer "
	if len(authHeader) > len(prefix) && authHeader[:len(prefix)] == prefix {
		return authHeader[len(prefix):]
	}
	return ""
}

// extractUserFromResource extracts the user ID from an Outlook resource URI.
// Example: "Users('user-id')/Messages('msg-id')" -> "user-id"
func extractUserFromResource(resource string) string {
	start := 0
	for i := 0; i < len(resource)-7; i++ {
		if resource[i:i+7] == "Users('" {
			start = i + 7
			break
		}
		if resource[i:i+7] == "users('" {
			start = i + 7
			break
		}
	}
	if start == 0 {
		return resource // fallback
	}
	end := start
	for end < len(resource) && resource[end] != '\'' {
		end++
	}
	return resource[start:end]
}

// byteReader is a simple io.Reader that reads from a byte slice.
type byteReader struct {
	data []byte
	pos  int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

