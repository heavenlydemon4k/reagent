package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/decisionstack/sync/internal/circuitbreaker"
	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
)

// ConsultProxy proxies consultation requests to the Intelligence Layer.
type ConsultProxy struct {
	intelligenceURL string
	httpClient      *http.Client
	breaker         *circuitbreaker.CircuitBreaker
}

// ConsultProxyConfig holds configuration for the consult proxy.
type ConsultProxyConfig struct {
	IntelligenceURL string
	Timeout         time.Duration
}

// NewConsultProxy creates a new proxy to the Intelligence Layer consultation service.
func NewConsultProxy(cfg ConsultProxyConfig) *ConsultProxy {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &ConsultProxy{
		intelligenceURL: cfg.IntelligenceURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		breaker: circuitbreaker.NewWithPreset("intelligence"),
	}
}

// ---------------------------------------------------------------------------
// Request/Response types for Intelligence Layer
// ---------------------------------------------------------------------------

type consultRequestPayload struct {
	CardID   string `json:"card_id"`
	UserID   string `json:"user_id"`
	Question string `json:"question"`
}

type consultResponsePayload struct {
	Answer         string                   `json:"answer"`
	Citations      []citationPayload        `json:"citations"`
	TurnsRemaining int                      `json:"turns_remaining"`
	Error          *string                  `json:"error,omitempty"`
}

type citationPayload struct {
	ChunkID         string `json:"chunk_id"`
	VerbatimSnippet string `json:"verbatim_snippet"`
	EmailID         string `json:"email_id"`
	ParagraphIndex  int    `json:"paragraph_index"`
}

// ---------------------------------------------------------------------------
// Consultation
// ---------------------------------------------------------------------------

// ErrConsultationTurnsExceeded is returned when the user has exhausted their consultation turns.
type ErrConsultationTurnsExceeded struct{ CardID uuid.UUID }

func (e ErrConsultationTurnsExceeded) Error() string {
	return fmt.Sprintf("consultation turns exceeded for card %s", e.CardID)
}

// ErrConsultationRejected is returned when the Intelligence Layer rejects the consultation.
type ErrConsultationRejected struct{ Reason string }

func (e ErrConsultationRejected) Error() string { return fmt.Sprintf("consultation rejected: %s", e.Reason) }

// MaxConsultationTurns is the maximum number of consultation turns allowed.
const MaxConsultationTurns = 10

// Ask forwards a consultation question to the Intelligence Layer.
// It calls POST /v1/chat/consult with a 30-second timeout.
// Protected by circuit breaker — fails fast if intelligence layer is down.
func (p *ConsultProxy) Ask(ctx context.Context, cardID uuid.UUID, userID uuid.UUID, question string, turnsUsed int) (*models.ConsultResponse, error) {
	// Enforce max turns on the server side as well
	if turnsUsed >= MaxConsultationTurns {
		return nil, ErrConsultationTurnsExceeded{CardID: cardID}
	}

	reqBody := consultRequestPayload{
		CardID:   cardID.String(),
		UserID:   userID.String(),
		Question: question,
	}

	var resp consultResponsePayload
	if err := p.breaker.Call(func() error {
		return p.post(ctx, "/v1/chat/consult", reqBody, &resp)
	}); err != nil {
		return nil, fmt.Errorf("intelligence layer consult: %w", err)
	}

	if resp.Error != nil && *resp.Error != "" {
		return nil, ErrConsultationRejected{Reason: *resp.Error}
	}

	citations := make([]models.ChunkCitation, len(resp.Citations))
	for i, c := range resp.Citations {
		chunkID, _ := uuid.Parse(c.ChunkID)
		emailID, _ := uuid.Parse(c.EmailID)
		citations[i] = models.ChunkCitation{
			ChunkID:         chunkID,
			VerbatimSnippet: c.VerbatimSnippet,
			EmailID:         emailID,
			ParagraphIndex:  c.ParagraphIndex,
		}
	}

	turnsRemaining := resp.TurnsRemaining
	if turnsRemaining < 0 {
		turnsRemaining = 0
	}

	return &models.ConsultResponse{
		Answer:         resp.Answer,
		Citations:      citations,
		TurnsRemaining: turnsRemaining,
	}, nil
}

// ---------------------------------------------------------------------------
// HTTP helper
// ---------------------------------------------------------------------------

func (p *ConsultProxy) post(ctx context.Context, path string, reqBody any, respDest any) error {
	url := p.intelligenceURL + path

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	httpResp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer httpResp.Body.Close()

	respBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("intelligence layer returned %d: %s", httpResp.StatusCode, string(respBytes))
	}

	if err := json.Unmarshal(respBytes, respDest); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	return nil
}
