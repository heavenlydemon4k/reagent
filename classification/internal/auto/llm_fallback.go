package auto

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/decisionstack/classification/internal/models"
)

const (
	// Default model identifier for Claude 3 Haiku.
	defaultHaikuModel = "claude-3-haiku-20240307"

	// anthropicAPIVersion is the Anthropic API version header.
	anthropicAPIVersion = "2023-06-01"

	// Minimum confidence threshold for auto-handle (HARD floor).
	confidenceFloor = 0.92
)

// LLMFallback provides unstructured pattern matching via Claude 3 Haiku.
// It is used when no structured rule matches an email.
type LLMFallback struct {
	anthropicAPIKey string
	model           string
	httpClient      *http.Client
	log             *slog.Logger
}

// NewLLMFallback creates an LLM fallback client.
func NewLLMFallback(apiKey string, log *slog.Logger) *LLMFallback {
	return &LLMFallback{
		anthropicAPIKey: apiKey,
		model:           defaultHaikuModel,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		log: log,
	}
}

// SetHTTPClient allows overriding the default HTTP client (useful for tests).
func (f *LLMFallback) SetHTTPClient(client *http.Client) {
	f.httpClient = client
}

// Match asks Claude 3 Haiku to match an email against known rule names.
// Returns a match only if confidence >= 0.92 (hard floor).
func (f *LLMFallback) Match(ctx context.Context, req models.LLMPatternMatchRequest) (*models.LLMPatternMatchResponse, error) {
	if len(req.RuleNames) == 0 {
		return &models.LLMPatternMatchResponse{
			Match:      "none",
			Confidence: 0.0,
			Reason:     "no active rules to match against",
		}, nil
	}

	prompt := buildPrompt(req)

	body, err := f.callAnthropic(ctx, prompt)
	if err != nil {
		return nil, models.ClassificationError{
			Code:    models.ErrCodeLLMUnavailable,
			Message: fmt.Sprintf("anthropic api call failed: %v", err),
			Retry:   true,
		}
	}

	resp, err := parseResponse(body)
	if err != nil {
		return nil, models.ClassificationError{
			Code:    models.ErrCodeLLMUnavailable,
			Message: fmt.Sprintf("parse llm response: %v", err),
			Retry:   false,
		}
	}

	// Hard confidence floor enforcement.
	if resp.Confidence < confidenceFloor {
		resp.Match = "none"
		resp.Reason = fmt.Sprintf("confidence %.2f below floor %.2f", resp.Confidence, confidenceFloor)
		resp.Confidence = 0.0
	}

	// Validate match against known rule names.
	if resp.Match != "none" && !stringSliceContains(req.RuleNames, resp.Match) {
		f.log.Warn("llm returned unknown rule name",
			"match", resp.Match,
			"known_rules", req.RuleNames,
		)
		resp.Match = "none"
		resp.Confidence = 0.0
		resp.Reason = "matched unknown rule name"
	}

	return resp, nil
}

// buildPrompt constructs the Jinja2-like prompt for Claude 3 Haiku.
func buildPrompt(req models.LLMPatternMatchRequest) string {
	var b strings.Builder

	b.WriteString("You are an email routing assistant. The user has these active rules:\n")
	for i, name := range req.RuleNames {
		fmt.Fprintf(&b, "%d. %s\n", i+1, name)
	}

	b.WriteString("\nBased ONLY on the email below, which rule matches? If none match well, respond 'none'.\n\n")
	fmt.Fprintf(&b, "Email subject: %s\n", req.Subject)
	if req.SenderEmail != "" {
		fmt.Fprintf(&b, "From: %s\n", req.SenderEmail)
	}
	if req.Recipient != "" {
		fmt.Fprintf(&b, "To: %s\n", req.Recipient)
	}
	fmt.Fprintf(&b, "Body preview: %s\n", req.BodyPreview)
	if req.HasAttachment {
		b.WriteString("Has attachment: yes\n")
	}
	if req.ParticipantCount > 0 {
		fmt.Fprintf(&b, "Thread participants: %d\n", req.ParticipantCount)
	}

	b.WriteString("\nRespond with ONLY a JSON object in this exact format:\n")
	b.WriteString(`{"match": "rule_name_or_none", "confidence": 0.0-1.0, "reason": "brief explanation"}`)
	b.WriteString("\n\nThe confidence field must be a number between 0.0 and 1.0. ")
	b.WriteString("Use 0.95+ only if you are very certain. Be conservative.")

	return b.String()
}

// anthropicRequest is the request body for the Anthropic Messages API.
type anthropicRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse is the response body from the Anthropic Messages API.
type anthropicResponse struct {
	Content []contentBlock `json:"content"`
	Usage   usage          `json:"usage"`
	Error   *anthropicErr  `json:"error,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicErr struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e *anthropicErr) Error() string {
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// callAnthropic sends the prompt to the Anthropic Messages API and returns the raw text.
func (f *LLMFallback) callAnthropic(ctx context.Context, prompt string) (string, error) {
	reqBody := anthropicRequest{
		Model:     f.model,
		MaxTokens: 256,
		Messages: []message{
			{Role: "user", Content: prompt},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", f.anthropicAPIKey)
	httpReq.Header.Set("Anthropic-Version", anthropicAPIVersion)

	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var anthResp anthropicResponse
	if err := json.Unmarshal(respBody, &anthResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if anthResp.Error != nil {
		return "", anthResp.Error
	}

	if len(anthResp.Content) == 0 {
		return "", fmt.Errorf("empty response content")
	}

	// Log token usage for observability.
	f.log.Debug("llm fallback token usage",
		"input_tokens", anthResp.Usage.InputTokens,
		"output_tokens", anthResp.Usage.OutputTokens,
	)

	return anthResp.Content[0].Text, nil
}

// parseResponse extracts the JSON object from the LLM's text response.
func parseResponse(body string) (*models.LLMPatternMatchResponse, error) {
	// Try to find JSON object in the response.
	body = strings.TrimSpace(body)

	// If the response contains markdown code blocks, extract the JSON.
	if idx := strings.Index(body, "```json"); idx != -1 {
		start := idx + len("```json")
		end := strings.Index(body[start:], "```")
		if end != -1 {
			body = strings.TrimSpace(body[start : start+end])
		}
	} else if idx := strings.Index(body, "```"); idx != -1 {
		start := idx + len("```")
		end := strings.Index(body[start:], "```")
		if end != -1 {
			body = strings.TrimSpace(body[start : start+end])
		}
	}

	var resp models.LLMPatternMatchResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal json response: %w (body: %s)", err, body)
	}

	// Clamp confidence to [0, 1].
	if resp.Confidence < 0 {
		resp.Confidence = 0
	}
	if resp.Confidence > 1 {
		resp.Confidence = 1
	}

	return &resp, nil
}

// stringSliceContains checks if a string slice contains a value (case-insensitive).
func stringSliceContains(slice []string, val string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, val) {
			return true
		}
	}
	return false
}
