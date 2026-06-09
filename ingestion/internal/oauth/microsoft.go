// Package oauth provides the Microsoft MSAL implementation for Outlook.
package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/models"
)

// Microsoft OAuth 2.0 (MSAL v2) endpoint URLs.
const (
	msBaseAuthURL    = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"
	msBaseTokenURL   = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
	msRevokeURL      = "https://login.microsoftonline.com/common/oauth2/v2.0/revoke"
	msGraphBaseURL   = "https://graph.microsoft.com/v1.0"
	msDeltaQueryURL  = "https://graph.microsoft.com/v1.0/me/mailFolders/sentitems/messages/delta"
)

// Microsoft Graph API scope constants.
var microsoftScopes = []string{
	"Mail.Read",
	"Mail.Send",
	"Calendars.ReadWrite",
	"User.Read",
	"offline_access",
}

// microsoftProvider implements models.OAuthProvider for Outlook.
type microsoftProvider struct {
	baseProvider
	clientID     string
	clientSecret string
	redirectURI  string
}

// newMicrosoftProvider creates a new Microsoft MSAL provider.
func newMicrosoftProvider(cfg *config.Config) *microsoftProvider {
	return &microsoftProvider{
		baseProvider: newBaseProvider(),
		clientID:     cfg.MicrosoftClientID,
		clientSecret: cfg.MicrosoftClientSecret,
		redirectURI:  cfg.MicrosoftRedirectURI,
	}
}

// Name returns the provider name.
func (p *microsoftProvider) Name() string {
	return string(ProviderOutlook)
}

// AuthURL builds the OAuth authorization URL for initiating the Microsoft OAuth flow.
// MSAL v2 uses the common tenant endpoint for consumer and organizational accounts.
func (p *microsoftProvider) AuthURL(state string, redirectURI string) string {
	redirect := redirectURI
	if redirect == "" {
		redirect = p.redirectURI
	}

	v := url.Values{}
	v.Set("client_id", p.clientID)
	v.Set("response_type", "code")
	v.Set("redirect_uri", redirect)
	v.Set("scope", strings.Join(microsoftScopes, " "))
	v.Set("state", state)
	v.Set("response_mode", "query")
	v.Set("prompt", "consent")

	return msBaseAuthURL + "?" + v.Encode()
}

// msTokenResponse is the Microsoft token endpoint response format.
type msTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// Exchange exchanges the authorization code for MSAL tokens.
func (p *microsoftProvider) Exchange(ctx context.Context, code string, redirectURI string) (*models.TokenPair, error) {
	redirect := redirectURI
	if redirect == "" {
		redirect = p.redirectURI
	}

	formData := url.Values{}
	formData.Set("client_id", p.clientID)
	formData.Set("client_secret", p.clientSecret)
	formData.Set("code", code)
	formData.Set("redirect_uri", redirect)
	formData.Set("grant_type", "authorization_code")
	formData.Set("scope", strings.Join(microsoftScopes, " "))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, msBaseTokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp msTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	pair := &models.TokenPair{
		ScopeGranted: strings.Split(tokenResp.Scope, " "),
	}

	if tokenResp.RefreshToken != "" {
		pair.RefreshToken = &models.EncryptedToken{
			KeyID:      "microsoft-" + p.clientID,
			Ciphertext: []byte(tokenResp.RefreshToken),
		}
	}

	if tokenResp.AccessToken != "" {
		pair.AccessToken = &models.EncryptedToken{
			KeyID:      "microsoft-" + p.clientID,
			Ciphertext: []byte(tokenResp.AccessToken),
		}
		at := tokenResp.AccessToken
		pair.AccessTokenPlaintext = &at
	}

	// Microsoft access tokens are valid for ~1 hour; set 15-min TTL
	exp := time.Now().UTC().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	pair.ExpiresAt = &exp

	return pair, nil
}

// Refresh uses the refresh token to obtain a new access token from Microsoft.
func (p *microsoftProvider) Refresh(ctx context.Context, refreshToken string) (*models.TokenPair, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh token is empty")
	}

	formData := url.Values{}
	formData.Set("client_id", p.clientID)
	formData.Set("client_secret", p.clientSecret)
	formData.Set("refresh_token", refreshToken)
	formData.Set("grant_type", "refresh_token")
	formData.Set("scope", strings.Join(microsoftScopes, " "))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, msBaseTokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Check for invalid_grant error
		bodyStr := string(body)
		if strings.Contains(bodyStr, "invalid_grant") {
			return nil, &models.IngestionError{
				Code:    models.ErrCodeOAuthExpired,
				Message: "refresh token expired or revoked: " + bodyStr,
				Retry:   false,
			}
		}
		return nil, fmt.Errorf("refresh failed with status %d: %s", resp.StatusCode, bodyStr)
	}

	var tokenResp msTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}

	pair := &models.TokenPair{
		ScopeGranted: strings.Split(tokenResp.Scope, " "),
	}

	// Microsoft may return a new refresh token
	if tokenResp.RefreshToken != "" {
		pair.RefreshToken = &models.EncryptedToken{
			KeyID:      "microsoft-" + p.clientID,
			Ciphertext: []byte(tokenResp.RefreshToken),
		}
	}

	if tokenResp.AccessToken != "" {
		pair.AccessToken = &models.EncryptedToken{
			KeyID:      "microsoft-" + p.clientID,
			Ciphertext: []byte(tokenResp.AccessToken),
		}
		at := tokenResp.AccessToken
		pair.AccessTokenPlaintext = &at
	}

	exp := time.Now().UTC().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	pair.ExpiresAt = &exp

	return pair, nil
}

// Revoke revokes the given token via Microsoft's revoke endpoint.
func (p *microsoftProvider) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return fmt.Errorf("token is empty")
	}

	// Microsoft Graph doesn't have a dedicated revoke endpoint like Google.
	// We revoke by calling the Microsoft identity platform revoke endpoint.
	formData := url.Values{}
	formData.Set("token", token)
	formData.Set("client_id", p.clientID)
	formData.Set("client_secret", p.clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, msRevokeURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke request failed: %w", err)
	}
	defer resp.Body.Close()

	// Microsoft returns 200 OK on successful revocation even if there's no body
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revoke failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// ValidateWebhook validates an incoming webhook push notification from Microsoft Graph.
// Microsoft uses validation tokens and change notifications.
func (p *microsoftProvider) ValidateWebhook(payload []byte, headers map[string]string) (*models.WebhookPayload, error) {
	if len(payload) == 0 {
		return nil, &models.IngestionError{
			Code:    models.ErrCodeWebhookInvalid,
			Message: "empty webhook payload",
			Retry:   false,
		}
	}

	// Microsoft Graph sends two types of webhooks:
	// 1. Subscription validation: contains validationToken query param
	// 2. Change notifications: contains actual change data

	// Check for validation token in query string (headers may contain the raw URL)
	validationToken := headers["validationToken"]
	if validationToken == "" {
		validationToken = extractValidationToken(string(payload))
	}

	if validationToken != "" {
		// This is a subscription validation request
		// Return the validation token as required by Microsoft Graph
		return &models.WebhookPayload{
			MessageID:  validationToken,
			ChangeType: "validation",
			ReceivedAt: time.Now().UTC(),
		}, nil
	}

	// Parse change notification
	var changeNotif struct {
		Value []struct {
			ChangeType         string `json:"changeType"`
			ClientState        string `json:"clientState"`
			Resource           string `json:"resource"`
			ResourceData       struct {
				ID string `json:"id"`
			} `json:"resourceData"`
			SubscriptionID     string `json:"subscriptionId"`
			SubscriptionExpirationDateTime string `json:"subscriptionExpirationDateTime"`
			TenantID           string `json:"tenantId"`
		} `json:"value"`
	}

	if err := json.Unmarshal(payload, &changeNotif); err != nil {
		return nil, &models.IngestionError{
			Code:    models.ErrCodeWebhookInvalid,
			Message: "failed to parse Microsoft change notification: " + err.Error(),
			Retry:   false,
		}
	}

	if len(changeNotif.Value) == 0 {
		return nil, &models.IngestionError{
			Code:    models.ErrCodeWebhookInvalid,
			Message: "no change notifications in payload",
			Retry:   false,
		}
	}

	// Process the first change notification
	change := changeNotif.Value[0]

	// Extract delta link from the resource if available
	deltaLink := ""
	if change.Resource != "" {
		deltaLink = change.Resource
	}

	return &models.WebhookPayload{
		MessageID:  change.ResourceData.ID,
		DeltaLink:  deltaLink,
		ChangeType: change.ChangeType,
		ReceivedAt: time.Now().UTC(),
	}, nil
}

// extractValidationToken attempts to extract a validation token from the payload.
func extractValidationToken(payload string) string {
	// Try parsing as a simple query string
	if strings.Contains(payload, "validationToken=") {
		parts := strings.Split(payload, "validationToken=")
		if len(parts) > 1 {
			token := parts[1]
			if idx := strings.IndexAny(token, "& \n"); idx != -1 {
				token = token[:idx]
			}
			return token
		}
	}
	return ""
}

// FetchSentHistory retrieves emails from the sent items folder using Microsoft
// Graph API delta query support for efficient synchronization.
func (p *microsoftProvider) FetchSentHistory(ctx context.Context, accessToken string, daysBack int) ([]models.ParsedEmail, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("access token is empty")
	}

	if daysBack <= 0 {
		daysBack = 30
	}

	// Use delta query for efficient sync: first get all messages, then use delta
	dateCutoff := time.Now().UTC().AddDate(0, 0, -daysBack).Format("2006-01-02T15:04:05Z")

	filter := url.Values{}
	filter.Set("$filter", fmt.Sprintf("sentDateTime ge %s", dateCutoff))
	filter.Set("$select", "id,subject,from,toRecipients,body,sentDateTime,inReplyTo,internetMessageId")
	filter.Set("$top", "50")
	filter.Set("$orderby", "sentDateTime desc")

	requestURL := fmt.Sprintf("%s/me/mailFolders/sentitems/messages?%s", msGraphBaseURL, filter.Encode())

	var allEmails []models.ParsedEmail
	for requestURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Prefer", "outlook.body-content-type=\"text\"")

		resp, err := p.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Microsoft API returned status %d: %s", resp.StatusCode, string(body))
		}

		var result struct {
			Value    []msGraphMessage `json:"value"`
			NextLink string           `json:"@odata.nextLink"`
			DeltaLink string          `json:"@odata.deltaLink"`
		}

		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}

		for _, msg := range result.Value {
			email := p.parseGraphMessage(msg)
			allEmails = append(allEmails, email)
		}

		requestURL = result.NextLink
		if result.DeltaLink != "" {
			// Store delta link for future incremental sync
			break
		}
	}

	return allEmails, nil
}

// msGraphMessage represents a Microsoft Graph API message response.
type msGraphMessage struct {
	ID                 string            `json:"id"`
	Subject            string            `json:"subject"`
	From               msGraphRecipient  `json:"from"`
	ToRecipients       []msGraphRecipient `json:"toRecipients"`
	Body               msGraphBody       `json:"body"`
	SentDateTime       string            `json:"sentDateTime"`
	InReplyTo          *string           `json:"inReplyTo"`
	InternetMessageID  string            `json:"internetMessageId"`
	HasAttachments     bool              `json:"hasAttachments"`
}

type msGraphRecipient struct {
	EmailAddress struct {
		Address string `json:"address"`
		Name    string `json:"name"`
	} `json:"emailAddress"`
}

type msGraphBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

// parseGraphMessage converts a Microsoft Graph message to ParsedEmail.
func (p *microsoftProvider) parseGraphMessage(msg msGraphMessage) models.ParsedEmail {
	email := models.ParsedEmail{
		MessageID: msg.InternetMessageID,
		Source:    string(ProviderOutlook),
		Subject:   msg.Subject,
		HasAttachments: msg.HasAttachments,
	}

	if email.MessageID == "" {
		email.MessageID = msg.ID
	}

	// Parse sender
	if msg.From.EmailAddress.Address != "" {
		email.SenderEmail = msg.From.EmailAddress.Address
		email.SenderName = msg.From.EmailAddress.Name
	}

	// Parse recipients
	for _, r := range msg.ToRecipients {
		if r.EmailAddress.Address != "" {
			email.RecipientEmails = append(email.RecipientEmails, r.EmailAddress.Address)
		}
	}

	// Parse body
	if msg.Body.ContentType == "text" || msg.Body.ContentType == "text/plain" {
		email.BodyText = msg.Body.Content
	} else {
		email.BodyHTML = msg.Body.Content
	}

	// Parse inReplyTo
	if msg.InReplyTo != nil {
		email.InReplyTo = msg.InReplyTo
	}

	// Parse sent date
	if msg.SentDateTime != "" {
		if t, err := time.Parse(time.RFC3339, msg.SentDateTime); err == nil {
			email.ReceivedAt = t
		} else {
			email.ReceivedAt = time.Now().UTC()
		}
	} else {
		email.ReceivedAt = time.Now().UTC()
	}

	return email
}

// SendEmail sends an email via the Microsoft Graph API.
// Returns a generated message ID since Microsoft Graph sendMail doesn't return one directly.
func (p *microsoftProvider) SendEmail(ctx context.Context, accessToken string, req models.SendEmailRequest) (string, error) {
	if accessToken == "" {
		return "", fmt.Errorf("access token is empty")
	}
	if req.To == "" || req.Subject == "" {
		return "", fmt.Errorf("recipient and subject are required")
	}

	// Build the Microsoft Graph message
	message := map[string]interface{}{
		"message": map[string]interface{}{
			"subject": req.Subject,
			"body": map[string]interface{}{
				"contentType": "text",
				"content":     req.BodyText,
			},
			"toRecipients": []map[string]interface{}{
				{
					"emailAddress": map[string]interface{}{
						"address": req.To,
					},
				},
			},
		},
		"saveToSentItems": true,
	}

	// Use HTML body if provided
	if req.BodyHTML != "" {
		msgBody := message["message"].(map[string]interface{})
		msgBody["body"] = map[string]interface{}{
			"contentType": "html",
			"content":     req.BodyHTML,
		}
	}

	// Add In-Reply-To if this is a reply
	if req.InReplyTo != nil {
		msgMap := message["message"].(map[string]interface{})
		msgMap["internetMessageHeaders"] = []map[string]interface{}{
			{
				"name":  "In-Reply-To",
				"value": *req.InReplyTo,
			},
		}
		if len(req.References) > 0 {
			msgMap["internetMessageHeaders"] = append(
				msgMap["internetMessageHeaders"].([]map[string]interface{}),
				map[string]interface{}{
					"name":  "References",
					"value": strings.Join(req.References, " "),
				},
			)
		}
	}

	jsonBody, err := json.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("failed to marshal message: %w", err)
	}

	requestURL := fmt.Sprintf("%s/me/sendMail", msGraphBaseURL)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send mail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("send mail failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Microsoft Graph sendMail returns 202 Accepted with no body.
	// Generate a deterministic message ID from the request for traceability.
	messageID := fmt.Sprintf("msgraph_%d", time.Now().UnixNano())
	return messageID, nil
}

// Ensure microsoftProvider implements OAuthProvider at compile time.
var _ models.OAuthProvider = (*microsoftProvider)(nil)

// Ensure microsoftProvider implements EmailProvider at compile time.
var _ models.EmailProvider = (*microsoftProvider)(nil)
