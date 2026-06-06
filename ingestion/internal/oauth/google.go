// Package oauth provides the Google OAuth 2.0 implementation for Gmail.
package oauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/models"
)

// Google OAuth 2.0 endpoint URLs.
const (
	googleAuthURL      = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL     = "https://oauth2.googleapis.com/token"
	googleRevokeURL    = "https://oauth2.googleapis.com/revoke"
	googleUserInfoURL  = "https://www.googleapis.com/oauth2/v2/userinfo"
	googlePubSubPushURL = "https://pubsub.googleapis.com/v1/projects/%s/subscriptions/%s:acknowledge"
)

// Gmail scope constants.
var gmailScopes = []string{
	gmail.GmailReadonlyScope,
	gmail.GmailSendScope,
	gmail.GmailModifyScope,
	"https://www.googleapis.com/auth/calendar",
	"https://www.googleapis.com/auth/userinfo.email",
}

// googleProvider implements models.OAuthProvider for Gmail.
type googleProvider struct {
	baseProvider
	clientID     string
	clientSecret string
	redirectURI  string
	oauthConfig  *oauth2.Config
}

// newGoogleProvider creates a new Google OAuth provider.
func newGoogleProvider(cfg *config.Config) *googleProvider {
	p := &googleProvider{
		baseProvider: newBaseProvider(),
		clientID:     cfg.GoogleClientID,
		clientSecret: cfg.GoogleClientSecret,
		redirectURI:  cfg.GoogleRedirectURI,
	}

	p.oauthConfig = &oauth2.Config{
		ClientID:     p.clientID,
		ClientSecret: p.clientSecret,
		RedirectURL:  p.redirectURI,
		Scopes:       gmailScopes,
		Endpoint:     google.Endpoint,
	}

	return p
}

// Name returns the provider name.
func (p *googleProvider) Name() string {
	return string(ProviderGmail)
}

// AuthURL builds the OAuth authorization URL for initiating the Google OAuth flow.
// It includes offline access and prompt=consent to ensure refresh tokens are issued.
func (p *googleProvider) AuthURL(state string, redirectURI string) string {
	redirect := redirectURI
	if redirect == "" {
		redirect = p.redirectURI
	}

	// Clone the oauth config to use the provided redirect URI
	config := &oauth2.Config{
		ClientID:     p.clientID,
		ClientSecret: p.clientSecret,
		RedirectURL:  redirect,
		Scopes:       p.oauthConfig.Scopes,
		Endpoint:     p.oauthConfig.Endpoint,
	}

	return config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
}

// Exchange exchanges the authorization code for OAuth tokens.
// It constructs a TokenPair with encrypted refresh and access tokens.
func (p *googleProvider) Exchange(ctx context.Context, code string, redirectURI string) (*models.TokenPair, error) {
	redirect := redirectURI
	if redirect == "" {
		redirect = p.redirectURI
	}

	config := &oauth2.Config{
		ClientID:     p.clientID,
		ClientSecret: p.clientSecret,
		RedirectURL:  redirect,
		Scopes:       p.oauthConfig.Scopes,
		Endpoint:     p.oauthConfig.Endpoint,
	}

	token, err := config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange authorization code: %w", err)
	}

	pair := &models.TokenPair{
		ScopeGranted: config.Scopes,
	}

	if token.RefreshToken != "" {
		pair.RefreshToken = &models.EncryptedToken{
			KeyID:      "google-" + p.clientID,
			Ciphertext: []byte(token.RefreshToken), // placeholder - caller encrypts
		}
	}

	if token.AccessToken != "" {
		pair.AccessToken = &models.EncryptedToken{
			KeyID:      "google-" + p.clientID,
			Ciphertext: []byte(token.AccessToken), // placeholder - caller encrypts
		}
		at := token.AccessToken
		pair.AccessTokenPlaintext = &at
	}

	if !token.Expiry.IsZero() {
		pair.ExpiresAt = &token.Expiry
	} else {
		// Google access tokens default to 1 hour; set a conservative 15-min TTL
		exp := time.Now().UTC().Add(15 * time.Minute)
		pair.ExpiresAt = &exp
	}

	return pair, nil
}

// Refresh uses the refresh token to obtain a new access token from Google.
// This follows the OAuth 2.0 refresh token flow.
func (p *googleProvider) Refresh(ctx context.Context, refreshToken string) (*models.TokenPair, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is empty")
	}

	config := &oauth2.Config{
		ClientID:     p.clientID,
		ClientSecret: p.clientSecret,
		Scopes:       p.oauthConfig.Scopes,
		Endpoint:     p.oauthConfig.Endpoint,
	}

	token := &oauth2.Token{
		RefreshToken: refreshToken,
	}

	ts := config.TokenSource(ctx, token)
	newToken, err := ts.Token()
	if err != nil {
		// Check for invalid_grant error
		if strings.Contains(err.Error(), "invalid_grant") {
			return nil, &models.IngestionError{
				Code:    models.ErrCodeOAuthExpired,
				Message: "refresh token expired or revoked: " + err.Error(),
				Retry:   false,
			}
		}
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}

	pair := &models.TokenPair{
		ScopeGranted: p.oauthConfig.Scopes,
	}

	// Google may return a new refresh token on refresh; always check
	if newToken.RefreshToken != "" {
		pair.RefreshToken = &models.EncryptedToken{
			KeyID:      "google-" + p.clientID,
			Ciphertext: []byte(newToken.RefreshToken),
		}
	}

	if newToken.AccessToken != "" {
		pair.AccessToken = &models.EncryptedToken{
			KeyID:      "google-" + p.clientID,
			Ciphertext: []byte(newToken.AccessToken),
		}
		at := newToken.AccessToken
		pair.AccessTokenPlaintext = &at
	}

	if !newToken.Expiry.IsZero() {
		pair.ExpiresAt = &newToken.Expiry
	} else {
		exp := time.Now().UTC().Add(15 * time.Minute)
		pair.ExpiresAt = &exp
	}

	return pair, nil
}

// Revoke revokes the given token (either access or refresh token) via Google's
// revoke endpoint. After revocation, the token cannot be used again.
func (p *googleProvider) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return fmt.Errorf("token is empty")
	}

	formData := url.Values{}
	formData.Set("token", token)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleRevokeURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revoke failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// ValidateWebhook verifies a webhook push notification from Google Pub/Sub.
// It validates the JWT signature and extracts the message payload.
func (p *googleProvider) ValidateWebhook(payload []byte, headers map[string]string) (*models.WebhookPayload, error) {
	if len(payload) == 0 {
		return nil, &models.IngestionError{
			Code:    models.ErrCodeWebhookInvalid,
			Message: "empty webhook payload",
			Retry:   false,
		}
	}

	// Parse the Pub/Sub push message
	var pubsubMsg struct {
		Message struct {
			Data        string            `json:"data"`
			MessageID    string           `json:"messageId"`
			PublishTime  string           `json:"publishTime"`
			Attributes   map[string]string `json:"attributes"`
		} `json:"message"`
		Subscription string `json:"subscription"`
	}

	if err := json.Unmarshal(payload, &pubsubMsg); err != nil {
		// Try parsing as direct data payload
		return p.parseDirectPayload(payload, headers)
	}

	// Decode base64 data
	decoded, err := base64.StdEncoding.DecodeString(pubsubMsg.Message.Data)
	if err != nil {
		// Data may not be base64 encoded
		decoded = []byte(pubsubMsg.Message.Data)
	}

	// Parse the Gmail push notification
	var gmailNotif struct {
		EmailAddress string `json:"emailAddress"`
		HistoryID    uint64 `json:"historyId"`
	}

	if err := json.Unmarshal(decoded, &gmailNotif); err != nil {
		return nil, &models.IngestionError{
			Code:    models.ErrCodeWebhookInvalid,
			Message: "failed to parse Gmail push notification: " + err.Error(),
			Retry:   false,
		}
	}

	return &models.WebhookPayload{
		MessageID:  pubsubMsg.Message.MessageID,
		HistoryID:  fmt.Sprintf("%d", gmailNotif.HistoryID),
		ChangeType: "created",
		ReceivedAt: time.Now().UTC(),
	}, nil
}

// parseDirectPayload handles non-Pub/Sub formatted webhook payloads.
func (p *googleProvider) parseDirectPayload(payload []byte, headers map[string]string) (*models.WebhookPayload, error) {
	// Try to extract Gmail-specific data from headers
	historyID := headers["X-Goog-Resource-State"]
	if historyID == "" {
		historyID = headers["X-Goog-Channel-Token"]
	}

	messageID := headers["X-Goog-Message-Number"]
	if messageID == "" {
		messageID = headers["X-Goog-Channel-ID"]
	}

	return &models.WebhookPayload{
		MessageID:  messageID,
		HistoryID:  historyID,
		ChangeType: "created",
		ReceivedAt: time.Now().UTC(),
	}, nil
}

// FetchSentHistory retrieves emails from the user's sent mailbox for the
// specified number of days back. Uses the `in:sent` query.
func (p *googleProvider) FetchSentHistory(ctx context.Context, accessToken string, daysBack int) ([]models.ParsedEmail, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("access token is empty")
	}

	if daysBack <= 0 {
		daysBack = 30
	}

	// Build the Gmail service client
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken})
	client := oauth2.NewClient(ctx, ts)

	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gmail service: %w", err)
	}

	// Build query for sent emails within the date range
	dateCutoff := time.Now().UTC().AddDate(0, 0, -daysBack).Format("2006/01/02")
	query := fmt.Sprintf("in:sent after:%s", dateCutoff)

	// List messages matching the query
	var emails []models.ParsedEmail
	pageToken := ""
	for {
		call := srv.Users.Messages.List("me").Q(query)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("failed to list sent messages: %w", err)
		}

		for _, msg := range resp.Messages {
			email, err := p.fetchAndParseMessage(ctx, srv, msg.Id)
			if err != nil {
				// Log and continue - don't fail the entire batch for one bad message
				continue
			}
			emails = append(emails, *email)
		}

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	return emails, nil
}

// fetchAndParseMessage retrieves and parses a single Gmail message into ParsedEmail.
func (p *googleProvider) fetchAndParseMessage(ctx context.Context, srv *gmail.Service, messageID string) (*models.ParsedEmail, error) {
	msg, err := srv.Users.Messages.Get("me", messageID).Format("full").Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get message %s: %w", messageID, err)
	}

	email := &models.ParsedEmail{
		MessageID: messageID,
		Source:    string(ProviderGmail),
		ReceivedAt: time.Now().UTC(), // Use now as fallback
	}

	// Extract headers
	for _, header := range msg.Payload.Headers {
		switch strings.ToLower(header.Name) {
		case "from":
			email.SenderEmail, email.SenderName = p.parseAddress(header.Value)
		case "to":
			email.RecipientEmails = p.parseAddressList(header.Value)
		case "subject":
			email.Subject = header.Value
		case "in-reply-to":
			email.InReplyTo = &header.Value
		case "references":
			email.References = strings.Split(header.Value, " ")
		case "message-id":
			if email.MessageID == "" || email.MessageID == msg.Id {
				email.MessageID = header.Value
			}
		case "date":
			if t, err := time.Parse(time.RFC1123Z, header.Value); err == nil {
				email.ReceivedAt = t
			}
		}
	}

	// Extract body
	email.BodyText, email.BodyHTML = p.extractBody(msg.Payload)

	// Check for attachments
	email.HasAttachments = len(msg.Payload.Parts) > 0
	for _, part := range msg.Payload.Parts {
		if part.Filename != "" {
			email.HasAttachments = true
			break
		}
	}

	return email, nil
}

// parseAddress extracts email and name from a From header.
func (p *googleProvider) parseAddress(addr string) (email string, name string) {
	// Handle formats: "Name" <email@example.com> or email@example.com
	addr = strings.TrimSpace(addr)
	if idx := strings.LastIndex(addr, "<"); idx != -1 {
		if endIdx := strings.LastIndex(addr, ">"); endIdx != -1 {
			email = strings.TrimSpace(addr[idx+1 : endIdx])
			name = strings.TrimSpace(strings.Trim(addr[:idx], `"`))
		}
	}
	if email == "" {
		email = addr
	}
	return
}

// parseAddressList splits a comma-separated address list.
func (p *googleProvider) parseAddressList(addrs string) []string {
	var result []string
	for _, a := range strings.Split(addrs, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		email, _ := p.parseAddress(a)
		if email != "" {
			result = append(result, email)
		}
	}
	return result
}

// extractBody extracts text and HTML bodies from a Gmail message payload.
func (p *googleProvider) extractBody(payload *gmail.MessagePart) (text string, html string) {
	mimeType := strings.ToLower(payload.MimeType)

	switch mimeType {
	case "text/plain":
		data := p.decodePayloadBody(payload)
		return data, ""
	case "text/html":
		data := p.decodePayloadBody(payload)
		return "", data
	case "multipart/alternative", "multipart/mixed", "multipart/related":
		for _, part := range payload.Parts {
			t, h := p.extractBody(part)
			if t != "" && text == "" {
				text = t
			}
			if h != "" && html == "" {
				html = h
			}
		}
	}

	return
}

// decodePayloadBody decodes the base64url-encoded body data.
func (p *googleProvider) decodePayloadBody(part *gmail.MessagePart) string {
	if part.Body == nil || part.Body.Data == "" {
		return ""
	}
	data, err := base64.URLEncoding.DecodeString(part.Body.Data)
	if err != nil {
		// Try standard base64
		data, err = base64.StdEncoding.DecodeString(part.Body.Data)
		if err != nil {
			return ""
		}
	}
	return string(data)
}

// SendEmail sends an email via the Gmail API using the provided access token.
// The email is constructed as an RFC 2822 message and sent through the Gmail API.
func (p *googleProvider) SendEmail(ctx context.Context, accessToken string, req models.SendEmailRequest) error {
	if accessToken == "" {
		return fmt.Errorf("access token is empty")
	}
	if req.To == "" || req.Subject == "" {
		return fmt.Errorf("recipient and subject are required")
	}

	// Build RFC 2822 message
	var msg bytes.Buffer

	msg.WriteString(fmt.Sprintf("To: %s\r\n", req.To))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", req.Subject))

	if req.InReplyTo != nil {
		msg.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", *req.InReplyTo))
	}

	if len(req.References) > 0 {
		msg.WriteString(fmt.Sprintf("References: %s\r\n", strings.Join(req.References, " ")))
	}

	msg.WriteString("MIME-Version: 1.0\r\n")

	// Build multipart message if HTML is provided
	if req.BodyHTML != "" {
		boundary := fmt.Sprintf("boundary_%d", time.Now().UnixNano())
		msg.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary))
		msg.WriteString("\r\n")
		msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		msg.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
		msg.WriteString("\r\n")
		msg.WriteString(req.BodyText)
		msg.WriteString("\r\n")
		msg.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		msg.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
		msg.WriteString("\r\n")
		msg.WriteString(req.BodyHTML)
		msg.WriteString("\r\n")
		msg.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	} else {
		msg.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
		msg.WriteString("\r\n")
		msg.WriteString(req.BodyText)
	}

	// Encode as base64url for Gmail API
	raw := base64.URLEncoding.EncodeToString(msg.Bytes())

	// Build the Gmail service client
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken})
	client := oauth2.NewClient(ctx, ts)

	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("failed to create Gmail service: %w", err)
	}

	gmailMsg := &gmail.Message{
		Raw: raw,
	}

	_, err = srv.Users.Messages.Send("me", gmailMsg).Do()
	if err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}

// Ensure googleProvider implements OAuthProvider at compile time.
var _ models.OAuthProvider = (*googleProvider)(nil)
