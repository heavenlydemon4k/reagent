// Package fetch provides real implementations of the API fetcher interfaces
// defined in the poll package. These hit the actual Gmail and Outlook APIs.
package fetch

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/decisionstack/ingestion/internal/models"
	"github.com/decisionstack/ingestion/internal/poll"
)

// GmailAPIFetcher implements poll.GmailFetcher using the real Gmail API.
type GmailAPIFetcher struct {
	log *slog.Logger
}

// NewGmailAPIFetcher creates a new GmailAPIFetcher.
func NewGmailAPIFetcher(log *slog.Logger) *GmailAPIFetcher {
	return &GmailAPIFetcher{
		log: log.With("component", "gmail_api_fetcher"),
	}
}

// ---------------------------------------------------------------------------
// Helper: build an authenticated Gmail service from an access token
// ---------------------------------------------------------------------------

func (f *GmailAPIFetcher) newService(ctx context.Context, accessToken string) (*gmail.Service, error) {
	token := &oauth2.Token{AccessToken: accessToken}
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create gmail service: %w", err)
	}
	return srv, nil
}

// ---------------------------------------------------------------------------
// Helper: map Gmail API errors to domain errors
// ---------------------------------------------------------------------------

func (f *GmailAPIFetcher) mapError(apiErr error, action string) error {
	// Try to extract the googleapi.Error for status code inspection.
	if gErr, ok := apiErr.(*googleapi.Error); ok {
		switch gErr.Code {
		case http.StatusUnauthorized:
			return models.IngestionError{
				Code:    models.ErrCodeOAuthExpired,
				Message: fmt.Sprintf("gmail %s: OAuth token expired or revoked (HTTP 401)", action),
				Retry:   false,
			}
		case http.StatusForbidden:
			// 403 from Gmail usually means rate-limit or insufficient permissions.
			return models.IngestionError{
				Code:    models.ErrCodeRateLimited,
				Message: fmt.Sprintf("gmail %s: rate limited or forbidden (HTTP 403)", action),
				Retry:   true,
			}
		case http.StatusNotFound:
			// Return a sentinel so callers can distinguish "not found".
			return &notFoundError{action: action}
		default:
			return models.IngestionError{
				Code:    fmt.Sprintf("gmail_api_error_%d", gErr.Code),
				Message: fmt.Sprintf("gmail %s: %v", action, gErr),
				Retry:   true,
			}
		}
	}
	// Non-API error (network, context cancelled, etc.) — treat as retryable.
	return fmt.Errorf("gmail %s: %w", action, apiErr)
}

// notFoundError is an internal sentinel used to signal HTTP 404.
type notFoundError struct {
	action string
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("gmail %s: not found (HTTP 404)", e.action)
}

// ---------------------------------------------------------------------------
// HistoryList — calls users.history.list
// ---------------------------------------------------------------------------

// HistoryList fetches history records starting from the given historyID.
func (f *GmailAPIFetcher) HistoryList(ctx context.Context, accessToken, historyID string) (*poll.HistoryListResult, error) {
	srv, err := f.newService(ctx, accessToken)
	if err != nil {
		return nil, err
	}

	histID, err := strconv.ParseUint(historyID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse historyID %q: %w", historyID, err)
	}

	call := srv.Users.History.List("me").StartHistoryId(histID)
	resp, err := call.Do()
	if err != nil {
		return nil, f.mapError(err, "history.list")
	}

	return f.convertHistoryResponse(resp), nil
}

// ---------------------------------------------------------------------------
// HistoryListPage — paginated history.list
// ---------------------------------------------------------------------------

// HistoryListPage fetches a specific page of history using a page token.
func (f *GmailAPIFetcher) HistoryListPage(ctx context.Context, accessToken, historyID, pageToken string) (*poll.HistoryListResult, error) {
	srv, err := f.newService(ctx, accessToken)
	if err != nil {
		return nil, err
	}

	histID, err := strconv.ParseUint(historyID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse historyID %q: %w", historyID, err)
	}

	call := srv.Users.History.List("me").
		StartHistoryId(histID).
		PageToken(pageToken)
	resp, err := call.Do()
	if err != nil {
		return nil, f.mapError(err, "history.list page")
	}

	return f.convertHistoryResponse(resp), nil
}

// ---------------------------------------------------------------------------
// MessagesGet — calls users.messages.get with format=raw
// ---------------------------------------------------------------------------

// MessagesGet retrieves a full message via users.messages.get with format=raw.
// The Raw field contains the base64url-encoded RFC 822 message.
func (f *GmailAPIFetcher) MessagesGet(ctx context.Context, accessToken, messageID string) (*poll.GmailMessage, error) {
	srv, err := f.newService(ctx, accessToken)
	if err != nil {
		return nil, err
	}

	// Use format=raw to get the base64url-encoded RFC 822 message in the Raw field.
	msg, err := srv.Users.Messages.Get("me", messageID).Format("raw").Do()
	if err != nil {
		err = f.mapError(err, "messages.get")
		if _, isNotFound := err.(*notFoundError); isNotFound {
			f.log.Warn("message not found, may have been deleted",
				"message_id", messageID,
			)
			return nil, nil
		}
		return nil, err
	}

	return &poll.GmailMessage{
		ID:       msg.Id,
		ThreadID: msg.ThreadId,
		Raw:      msg.Raw,
		Snippet:  msg.Snippet,
	}, nil
}

// ---------------------------------------------------------------------------
// MessagesList — calls users.messages.list
// ---------------------------------------------------------------------------

// MessagesList retrieves a list of message IDs via users.messages.list.
// The query parameter supports Gmail search syntax (e.g., "newer_than:90d").
func (f *GmailAPIFetcher) MessagesList(ctx context.Context, accessToken, query, pageToken string) (*poll.MessagesListResult, error) {
	srv, err := f.newService(ctx, accessToken)
	if err != nil {
		return nil, err
	}

	call := srv.Users.Messages.List("me")
	if query != "" {
		call = call.Q(query)
	}
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}

	resp, err := call.Do()
	if err != nil {
		return nil, f.mapError(err, "messages.list")
	}

	result := &poll.MessagesListResult{
		NextPageToken:      resp.NextPageToken,
		ResultSizeEstimate: resp.ResultSizeEstimate,
	}

	if len(resp.Messages) > 0 {
		result.Messages = make([]poll.MessageListItem, 0, len(resp.Messages))
		for _, m := range resp.Messages {
			result.Messages = append(result.Messages, poll.MessageListItem{
				ID:       m.Id,
				ThreadID: m.ThreadId,
			})
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Response conversion helpers
// ---------------------------------------------------------------------------

// convertHistoryResponse converts a Gmail API ListHistoryResponse to our
// domain type poll.HistoryListResult.
func (f *GmailAPIFetcher) convertHistoryResponse(resp *gmail.ListHistoryResponse) *poll.HistoryListResult {
	result := &poll.HistoryListResult{
		NextPageToken: resp.NextPageToken,
		HistoryID:     strconv.FormatUint(resp.HistoryId, 10),
	}

	if len(resp.History) == 0 {
		return result
	}

	result.HistoryRecords = make([]poll.HistoryRecord, 0, len(resp.History))
	for _, h := range resp.History {
		record := poll.HistoryRecord{
			ID: strconv.FormatUint(h.Id, 10),
		}

		// Messages added
		if len(h.MessagesAdded) > 0 {
			record.MessagesAdded = make([]poll.MessageAdded, 0, len(h.MessagesAdded))
			for _, ma := range h.MessagesAdded {
				if ma.Message != nil {
					record.MessagesAdded = append(record.MessagesAdded, poll.MessageAdded{
						MessageID: ma.Message.Id,
						ThreadID:  ma.Message.ThreadId,
					})
				}
			}
		}

		// Messages deleted
		if len(h.MessagesDeleted) > 0 {
			record.MessagesDeleted = make([]poll.MessageDeleted, 0, len(h.MessagesDeleted))
			for _, md := range h.MessagesDeleted {
				if md.Message != nil {
					record.MessagesDeleted = append(record.MessagesDeleted, poll.MessageDeleted{
						MessageID: md.Message.Id,
					})
				}
			}
		}

		// Labels added
		if len(h.LabelsAdded) > 0 {
			record.LabelsAdded = make([]poll.LabelChange, 0, len(h.LabelsAdded))
			for _, la := range h.LabelsAdded {
				change := poll.LabelChange{
					LabelIDs: la.LabelIds,
				}
				if la.Message != nil {
					change.MessageID = la.Message.Id
				}
				record.LabelsAdded = append(record.LabelsAdded, change)
			}
		}

		// Labels removed
		if len(h.LabelsRemoved) > 0 {
			record.LabelsRemoved = make([]poll.LabelChange, 0, len(h.LabelsRemoved))
			for _, lr := range h.LabelsRemoved {
				change := poll.LabelChange{
					LabelIDs: lr.LabelIds,
				}
				if lr.Message != nil {
					change.MessageID = lr.Message.Id
				}
				record.LabelsRemoved = append(record.LabelsRemoved, change)
			}
		}

		result.HistoryRecords = append(result.HistoryRecords, record)
	}

	return result
}
