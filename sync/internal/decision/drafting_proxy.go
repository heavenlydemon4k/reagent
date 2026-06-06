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

// DraftingProxy proxies draft generation requests to the Intelligence Layer.
type DraftingProxy struct {
	intelligenceURL string
	httpClient      *http.Client
	breaker         *circuitbreaker.CircuitBreaker
}

// DraftingProxyConfig holds configuration for the drafting proxy.
type DraftingProxyConfig struct {
	IntelligenceURL string
	Timeout         time.Duration
}

// NewDraftingProxy creates a new proxy to the Intelligence Layer.
func NewDraftingProxy(cfg DraftingProxyConfig) *DraftingProxy {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &DraftingProxy{
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

type generateDraftRequest struct {
	CardID    string  `json:"card_id"`
	UserID    string  `json:"user_id"`
	ThreadID  string  `json:"thread_id"`
	UserInput *string `json:"user_input,omitempty"`
}

type generateDraftResponse struct {
	DraftID     string  `json:"draft_id"`
	DraftBody   string  `json:"draft_body"`
	SubjectLine *string `json:"subject_line,omitempty"`
	ToneProfile *string `json:"tone_profile,omitempty"`
	InReplyTo   *string `json:"in_reply_to,omitempty"`
	References  []string `json:"references,omitempty"`
	ModelUsed   *string `json:"model_used,omitempty"`
	TokensUsed  *int    `json:"tokens_used,omitempty"`
}

type modifyDraftRequest struct {
	DraftID     string `json:"draft_id"`
	Instruction string `json:"instruction"`
}

type modifyDraftResponse struct {
	DraftID     string  `json:"draft_id"`
	DraftBody   string  `json:"draft_body"`
	SubjectLine *string `json:"subject_line,omitempty"`
	ToneProfile *string `json:"tone_profile,omitempty"`
	ModelUsed   *string `json:"model_used,omitempty"`
	TokensUsed  *int    `json:"tokens_used,omitempty"`
}

// ---------------------------------------------------------------------------
// Draft generation
// ---------------------------------------------------------------------------

// GenerateDraft forwards a draft generation request to the Intelligence Layer.
// It calls POST /drafting/generate with a 30-second timeout.
// Protected by circuit breaker — fails fast if intelligence layer is down.
func (p *DraftingProxy) GenerateDraft(ctx context.Context, cardID uuid.UUID, userID uuid.UUID, threadID uuid.UUID, userInput *string) (*models.Draft, error) {
	reqBody := generateDraftRequest{
		CardID:    cardID.String(),
		UserID:    userID.String(),
		ThreadID:  threadID.String(),
		UserInput: userInput,
	}

	var resp generateDraftResponse
	if err := p.breaker.Call(func() error {
		return p.post(ctx, "/drafting/generate", reqBody, &resp)
	}); err != nil {
		return nil, fmt.Errorf("intelligence layer generate draft: %w", err)
	}

	draftID, err := uuid.Parse(resp.DraftID)
	if err != nil {
		draftID = uuid.New()
	}

	draft := &models.Draft{
		ID:           draftID,
		CardID:       cardID,
		UserID:       userID,
		ThreadID:     threadID,
		DraftBody:    resp.DraftBody,
		SubjectLine:  resp.SubjectLine,
		ToneProfile:  resp.ToneProfile,
		InReplyTo:    resp.InReplyTo,
		References:   resp.References,
		ModelUsed:    resp.ModelUsed,
		TokensUsed:   resp.TokensUsed,
		UserApproved: false,
		CreatedAt:    time.Now().UTC(),
	}

	return draft, nil
}

// ---------------------------------------------------------------------------
// Draft modification
// ---------------------------------------------------------------------------

// ModifyDraft requests a modified version of an existing draft.
// It calls POST /drafting/modify with the user's instruction.
// Protected by circuit breaker — fails fast if intelligence layer is down.
func (p *DraftingProxy) ModifyDraft(ctx context.Context, draftID uuid.UUID, cardID uuid.UUID, userID uuid.UUID, threadID uuid.UUID, instruction string) (*models.Draft, error) {
	reqBody := modifyDraftRequest{
		DraftID:     draftID.String(),
		Instruction: instruction,
	}

	var resp modifyDraftResponse
	if err := p.breaker.Call(func() error {
		return p.post(ctx, "/drafting/modify", reqBody, &resp)
	}); err != nil {
		return nil, fmt.Errorf("intelligence layer modify draft: %w", err)
	}

	newDraftID, err := uuid.Parse(resp.DraftID)
	if err != nil {
		newDraftID = uuid.New()
	}

	draft := &models.Draft{
		ID:           newDraftID,
		CardID:       cardID,
		UserID:       userID,
		ThreadID:     threadID,
		DraftBody:    resp.DraftBody,
		SubjectLine:  resp.SubjectLine,
		ToneProfile:  resp.ToneProfile,
		ModelUsed:    resp.ModelUsed,
		TokensUsed:   resp.TokensUsed,
		UserApproved: false,
		CreatedAt:    time.Now().UTC(),
	}

	return draft, nil
}

// ---------------------------------------------------------------------------
// HTTP helper
// ---------------------------------------------------------------------------

func (p *DraftingProxy) post(ctx context.Context, path string, reqBody any, respDest any) error {
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
