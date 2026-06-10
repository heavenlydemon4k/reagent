// Package fetch provides real API fetchers for the Ingestion Mesh.
// This file implements the OutlookFetcher interface using direct HTTP calls
// to the Microsoft Graph API.
package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/decisionstack/ingestion/internal/poll"
)

const (
	graphAPIBaseURL = "https://graph.microsoft.com/v1.0"

	// selectParams lists the fields we request from the Graph API.
	// This minimizes response size and improves performance.
	selectParams = "id,conversationId,subject,from,sender,toRecipients,ccRecipients,bccRecipients,body,bodyPreview,internetMessageId,internetMessageHeaders,hasAttachments,isDraft,isRead,importance,flag,categories,receivedDateTime,sentDateTime"
)

// graphDeltaResponse matches the JSON structure returned by the Graph API
// for /me/messages/delta queries.
type graphDeltaResponse struct {
	OdataDeltaLink string                   `json:"@odata.deltaLink"`
	OdataNextLink  string                   `json:"@odata.nextLink"`
	Context        string                   `json:"@odata.context"`
	Value          []graphMessage           `json:"value"`
	Error          *graphError              `json:"error,omitempty"`
}

// graphMessage is the raw Graph API message format used for JSON unmarshalling.
// It mirrors poll.OutlookMessage but with proper JSON tags.
type graphMessage struct {
	ID                     string                   `json:"id"`
	ConversationID         string                   `json:"conversationId"`
	Subject                string                   `json:"subject"`
	Sender                 poll.OutlookRecipient    `json:"sender"`
	From                   poll.OutlookRecipient    `json:"from"`
	ToRecipients           []poll.OutlookRecipient  `json:"toRecipients"`
	CcRecipients           []poll.OutlookRecipient  `json:"ccRecipients"`
	BccRecipients          []poll.OutlookRecipient  `json:"bccRecipients"`
	Body                   poll.OutlookBody         `json:"body"`
	BodyPreview            string                   `json:"bodyPreview"`
	InternetMessageID      string                   `json:"internetMessageId"`
	InternetMessageHeaders []poll.OutlookMessageHeader `json:"internetMessageHeaders"`
	HasAttachments         bool                     `json:"hasAttachments"`
	Attachments            []poll.OutlookAttachment `json:"attachments"`
	IsDraft                bool                     `json:"isDraft"`
	IsRead                 bool                     `json:"isRead"`
	Importance             string                   `json:"importance"`
	Flag                   poll.OutlookFlag         `json:"flag"`
	Categories             []string                 `json:"categories"`
	ReceivedDateTime       time.Time                `json:"receivedDateTime"`
	SentDateTime           time.Time                `json:"sentDateTime"`
	Removed                *graphRemovedReason      `json:"@removed,omitempty"`
}

// graphRemovedReason carries the deletion reason from @removed.
type graphRemovedReason struct {
	Reason string `json:"reason"`
}

// graphError is the standard Graph API error payload.
type graphError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	InnerError *struct {
		RequestID string `json:"request-id"`
		Date      string `json:"date"`
		ClientRequestID string `json:"client-request-id"`
	} `json:"innerError,omitempty"`
}

// ---------------------------------------------------------------------------
// OutlookAPIFetcher
// ---------------------------------------------------------------------------

// OutlookAPIFetcher implements poll.OutlookFetcher using direct HTTP calls
// to the Microsoft Graph API. It handles delta queries, pagination, rate
// limiting, and error classification.
type OutlookAPIFetcher struct {
	httpClient *http.Client
	log        *slog.Logger
}

// NewOutlookAPIFetcher creates a new OutlookAPIFetcher with a default HTTP
// client and timeout. Pass nil for the logger to disable logging.
func NewOutlookAPIFetcher(log *slog.Logger) *OutlookAPIFetcher {
	return &OutlookAPIFetcher{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: log,
	}
}

// DeltaQuery executes a Microsoft Graph Delta Query for the user's messages.
//
// If deltaLink is empty, it initiates a new delta query by calling
// /me/messages/delta. If deltaLink is set, it follows the provided URL
// directly (which may be a @odata.nextLink or @odata.deltaLink from a
// previous response).
//
// Pagination is handled internally: if the response contains @odata.nextLink,
// this method follows it until @odata.deltaLink is returned, accumulating
// all messages into a single result.
//
// Deleted messages (indicated by @removed in the Graph API response) are
// included in the result with ChangeType set to "deleted".
func (f *OutlookAPIFetcher) DeltaQuery(ctx context.Context, accessToken, deltaLink string) (*poll.DeltaQueryResult, error) {
	return f.deltaQueryInternal(ctx, accessToken, deltaLink)
}

// deltaQueryInternal performs the actual delta query work, including
// recursive pagination following.
func (f *OutlookAPIFetcher) deltaQueryInternal(ctx context.Context, accessToken, deltaLink string) (*poll.DeltaQueryResult, error) {
	if f.log != nil {
		f.log.Debug("outlook delta query", "deltaLink", truncate(deltaLink, 60))
	}

	// Build the request URL
	reqURL := deltaLink
	if reqURL == "" {
		// Initial delta query — construct the base URL with $select
		reqURL = graphAPIBaseURL + "/me/messages/delta?$select=" + selectParams
	}

	// Create the HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Prefer", "odata.maxpagesize=50")

	// Execute the request
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	// Handle non-2xx status codes before attempting to parse body
	if resp.StatusCode != http.StatusOK {
		return f.handleErrorResponse(resp, deltaLink), nil
	}

	// Parse the JSON response
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MiB max
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	var graphResp graphDeltaResponse
	if err := json.Unmarshal(body, &graphResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Check for Graph API error payload (returned with 200 in some edge cases)
	if graphResp.Error != nil {
		return f.graphErrorToResult(graphResp.Error), nil
	}

	// Convert graph messages to poll.OutlookMessage, detecting deletions
	var messages []poll.OutlookMessage
	for _, gm := range graphResp.Value {
		msg := f.toOutlookMessage(gm)
		messages = append(messages, msg)
	}

	result := &poll.DeltaQueryResult{
		Messages:  messages,
		DeltaLink: graphResp.OdataDeltaLink,
		NextLink:  graphResp.OdataNextLink,
	}

	// Pagination: if we have a nextLink but no deltaLink yet, follow it
	if result.NextLink != "" && result.DeltaLink == "" {
		return f.followPagination(ctx, accessToken, result)
	}

	if f.log != nil {
		f.log.Debug("delta query page complete",
			"messages", len(result.Messages),
			"has_delta_link", result.DeltaLink != "",
			"has_next_link", result.NextLink != "",
		)
	}

	return result, nil
}

// followPagination recursively follows @odata.nextLink until we get a
// response with @odata.deltaLink, accumulating all messages.
func (f *OutlookAPIFetcher) followPagination(ctx context.Context, accessToken string, acc *poll.DeltaQueryResult) (*poll.DeltaQueryResult, error) {
	nextLink := acc.NextLink
	allMessages := acc.Messages
	var finalDeltaLink string

	pageCount := 1
	for nextLink != "" {
		pageCount++
		if f.log != nil {
			f.log.Debug("following pagination link", "page", pageCount)
		}

		// Create request for the next page
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextLink, nil)
		if err != nil {
			return nil, fmt.Errorf("create pagination request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")

		resp, err := f.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("execute pagination request: %w", err)
		}

		// Handle non-2xx on pagination
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			// Return partial result with what we have so far
			return &poll.DeltaQueryResult{
				Messages:   allMessages,
				DeltaLink:  finalDeltaLink,
				ErrorCode:  classifyStatusCode(resp.StatusCode, string(body)),
				RateLimited: resp.StatusCode == http.StatusTooManyRequests,
				RetryAfter:  parseRetryAfterHeader(resp.Header.Get("Retry-After")),
			}, nil
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read pagination body: %w", err)
		}

		var graphResp graphDeltaResponse
		if err := json.Unmarshal(body, &graphResp); err != nil {
			return nil, fmt.Errorf("unmarshal pagination response: %w", err)
		}

		if graphResp.Error != nil {
			return f.graphErrorToResult(graphResp.Error), nil
		}

		for _, gm := range graphResp.Value {
			allMessages = append(allMessages, f.toOutlookMessage(gm))
		}

		nextLink = graphResp.OdataNextLink
		if graphResp.OdataDeltaLink != "" {
			finalDeltaLink = graphResp.OdataDeltaLink
		}
	}

	if f.log != nil {
		f.log.Debug("pagination complete", "pages", pageCount, "total_messages", len(allMessages))
	}

	return &poll.DeltaQueryResult{
		Messages:  allMessages,
		DeltaLink: finalDeltaLink,
	}, nil
}

// handleErrorResponse processes non-2xx HTTP responses and converts them
// into a DeltaQueryResult with appropriate error classification.
func (f *OutlookAPIFetcher) handleErrorResponse(resp *http.Response, deltaLink string) *poll.DeltaQueryResult {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if f.log != nil {
		f.log.Warn("graph API error response",
			"status", resp.StatusCode,
			"deltaLink", truncate(deltaLink, 60),
			"body", truncate(string(body), 200),
		)
	}

	result := &poll.DeltaQueryResult{
		RetryAfter: parseRetryAfterHeader(resp.Header.Get("Retry-After")),
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		result.ErrorCode = "oauth_expired"

	case http.StatusTooManyRequests:
		result.RateLimited = true
		if result.RetryAfter <= 0 {
			result.RetryAfter = 60 * time.Second
		}

	case http.StatusNotFound:
		// Log warning and return empty result — the folder/message may have
		// been deleted or the delta token expired.
		if f.log != nil {
			f.log.Warn("graph API returned 404, returning empty result", "deltaLink", truncate(deltaLink, 60))
		}
		return &poll.DeltaQueryResult{
			Messages:  []poll.OutlookMessage{},
			DeltaLink: "", // Force a full re-sync on next poll
		}

	default:
		// 5xx and other errors are treated as retryable
		if resp.StatusCode >= 500 {
			result.ErrorCode = fmt.Sprintf("server_error_%d", resp.StatusCode)
		} else {
			result.ErrorCode = fmt.Sprintf("client_error_%d", resp.StatusCode)
		}
	}

	return result
}

// graphErrorToResult converts a Graph API error payload into a
// DeltaQueryResult with proper error classification.
func (f *OutlookAPIFetcher) graphErrorToResult(gErr *graphError) *poll.DeltaQueryResult {
	if f.log != nil {
		f.log.Warn("graph API returned error payload",
			"code", gErr.Code,
			"message", gErr.Message,
		)
	}

	result := &poll.DeltaQueryResult{}

	// Classify known Graph API error codes
	switch gErr.Code {
	case "InvalidAuthenticationToken", "AuthenticationError",
		"OrganizationFromTenantGuidNotFound", "TokenExpired":
		result.ErrorCode = "oauth_expired"

	case "ErrorThrottleLimitExceeded", "ActivityLimitReached",
		"ApplicationThrottled", "TooManyRequests":
		result.RateLimited = true
		result.RetryAfter = 60 * time.Second

	case "ErrorItemNotFound", "ErrorInvalidIdMalformed",
		"ResourceNotFound":
		result.ErrorCode = "not_found"

	case "ErrorInternalServerError", "ErrorInternalServerTransientError":
		result.ErrorCode = "server_error"

	default:
		result.ErrorCode = gErr.Code
	}

	return result
}

// toOutlookMessage converts a graphMessage (raw API response) to a
// poll.OutlookMessage, detecting deletions via @removed.
func (f *OutlookAPIFetcher) toOutlookMessage(gm graphMessage) poll.OutlookMessage {
	msg := poll.OutlookMessage{
		ID:                     gm.ID,
		ConversationID:         gm.ConversationID,
		Subject:                gm.Subject,
		Sender:                 gm.Sender,
		From:                   gm.From,
		ToRecipients:           gm.ToRecipients,
		CcRecipients:           gm.CcRecipients,
		BccRecipients:          gm.BccRecipients,
		Body:                   gm.Body,
		BodyPreview:            gm.BodyPreview,
		InternetMessageID:      gm.InternetMessageID,
		InternetMessageHeaders: gm.InternetMessageHeaders,
		HasAttachments:         gm.HasAttachments,
		Attachments:            gm.Attachments,
		IsDraft:                gm.IsDraft,
		IsRead:                 gm.IsRead,
		Importance:             gm.Importance,
		Flag:                   gm.Flag,
		Categories:             gm.Categories,
		ReceivedDateTime:       gm.ReceivedDateTime,
		SentDateTime:           gm.SentDateTime,
	}

	// Detect deleted messages via @removed
	if gm.Removed != nil {
		msg.ChangeType = "deleted"
	}

	return msg
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseRetryAfterHeader parses the Retry-After header value into a duration.
// It handles both delta-seconds and HTTP-date formats.
func parseRetryAfterHeader(value string) time.Duration {
	if value == "" {
		return 0
	}

	// Try parsing as integer seconds
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try parsing as HTTP-date (RFC 1123, RFC 850, or ANSI C's asctime)
	for _, layout := range []string{
		http.TimeFormat,              // RFC 1123
		time.RFC850,                  // RFC 850
		time.RFC1123,                 // RFC 1123
		"Mon Jan _2 15:04:05 2006", // ANSI C's asctime()
	} {
		if t, err := time.Parse(layout, value); err == nil {
			d := time.Until(t)
			if d > 0 {
				return d
			}
			return 0
		}
	}

	// Default: 60 seconds
	return 60 * time.Second
}

// classifyStatusCode maps an HTTP status code to an error classification
// for DeltaQueryResult.ErrorCode.
func classifyStatusCode(code int, body string) string {
	switch code {
	case http.StatusUnauthorized:
		return "oauth_expired"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		if code >= 500 {
			return fmt.Sprintf("server_error_%d", code)
		}
		return fmt.Sprintf("client_error_%d", code)
	}
}

// truncate truncates a string to maxLen characters, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ---------------------------------------------------------------------------
// HTTP Client Customization
// ---------------------------------------------------------------------------

// WithHTTPClient allows overriding the default HTTP client. Useful for
// testing with a mock transport or adjusting timeouts.
func (f *OutlookAPIFetcher) WithHTTPClient(client *http.Client) *OutlookAPIFetcher {
	f.httpClient = client
	return f
}

// IsGraphURLLocal is a test helper that reports whether a URL is a
// Microsoft Graph API endpoint. It is exported so tests can use it.
func IsGraphURLLocal(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	return parsed.Host == "graph.microsoft.com" ||
		parsed.Host == "graph.microsoft.us" ||
		parsed.Host == "dod-graph.microsoft.us" ||
		parsed.Host == "microsoftgraph.chinacloudapi.cn"
}
