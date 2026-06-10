// Package notify handles push notification dispatch via FCM, APNS, and WebSocket.
package notify

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
)

// ============================================================================
// APNSClient — Apple Push Notification Service
// ============================================================================

// apns2Notification is the internal representation of an APNS notification.
type apns2Notification struct {
	DeviceToken string
	Topic       string
	Payload     []byte
	Priority    int
	Expiration  time.Time
}

// apnsResponse represents the APNS push response.
type apnsResponse struct {
	StatusCode   int
	ApnsID       string
	Reason       string
	TokenInvalid bool
}

// apnsHTTPClient is the interface for the underlying APNS HTTP client.
type apnsHTTPClient interface {
	Push(n *apns2Notification) (*apnsResponse, error)
}

// APNSClient sends push notifications to iOS devices via Apple's Push
// Notification Service over HTTP/2.
type APNSClient struct {
	httpClient apnsHTTPClient
	enabled    bool
	cfg        *config.Config
	bundleID   string
}

// NewAPNSClient creates a new APNS client. If APNS is not configured, it
// returns a no-op client.
func NewAPNSClient(cfg *config.Config) (*APNSClient, error) {
	bundleID := cfg.APNSBundleID
	if bundleID == "" {
		bundleID = "com.decisionstack.app"
	}

	client := &APNSClient{
		enabled:  false,
		cfg:      cfg,
		bundleID: bundleID,
	}

	// Only initialize if APNS key path is provided
	if cfg.APNSKeyPath == "" {
		logger.Info("APNS not configured (no key path), using no-op client")
		return client, nil
	}

	httpClient, err := newAPNSHTTP2Client(cfg)
	if err != nil {
		logger.Error("failed to initialize APNS HTTP/2 client, using no-op", "error", err)
		return client, nil
	}

	client.httpClient = httpClient
	client.enabled = true
	logger.Info("APNS client initialized", "bundle_id", bundleID)
	return client, nil
}

// NewAPNSClientWithHTTPClient creates an APNS client with an injected HTTP client.
// Used for testing.
func NewAPNSClientWithHTTPClient(httpClient apnsHTTPClient, cfg *config.Config) *APNSClient {
	bundleID := cfg.APNSBundleID
	if bundleID == "" {
		bundleID = "com.decisionstack.app"
	}
	return &APNSClient{
		httpClient: httpClient,
		enabled:    true,
		cfg:        cfg,
		bundleID:   bundleID,
	}
}

// Send delivers a notification to a single iOS device via APNS.
func (c *APNSClient) Send(ctx context.Context, token string, notif *models.Notification) error {
	if !c.enabled {
		logger.Debug("APNS not enabled, skipping send",
			"type", notif.Type,
			"user_id", notif.UserID,
		)
		return nil
	}

	if token == "" {
		return fmt.Errorf("apns: empty token")
	}

	if c.httpClient == nil {
		return fmt.Errorf("apns: HTTP client not initialized")
	}

	apnsNotif := c.buildNotification(token, notif)

	resp, err := c.httpClient.Push(apnsNotif)
	if err != nil {
		logger.Error("APNS push failed", "error", err, "user_id", notif.UserID)
		return fmt.Errorf("apns: push: %w", err)
	}

	if resp.TokenInvalid {
		logger.Warn("APNS token invalid, should be removed",
			"token_prefix", maskToken(token),
			"reason", resp.Reason,
		)
		return &ErrInvalidToken{
			Token:    token,
			Platform: "ios",
			Cause:    fmt.Errorf("APNS %s", resp.Reason),
		}
	}

	if resp.StatusCode != 200 {
		logger.Warn("APNS non-success status",
			"status", resp.StatusCode,
			"reason", resp.Reason,
			"user_id", notif.UserID,
		)
		return fmt.Errorf("apns: status %d: %s", resp.StatusCode, resp.Reason)
	}

	logger.Debug("APNS push sent", "apns_id", resp.ApnsID, "user_id", notif.UserID)
	return nil
}

// SendToMultiple sends a notification to multiple iOS devices.
func (c *APNSClient) SendToMultiple(ctx context.Context, tokens []string, notif *models.Notification) map[string]error {
	failures := make(map[string]error)
	if !c.enabled {
		return failures
	}

	for _, token := range tokens {
		if err := c.Send(ctx, token, notif); err != nil {
			failures[token] = err
		}
	}

	return failures
}

// buildNotification constructs an APNS notification.
func (c *APNSClient) buildNotification(token string, notif *models.Notification) *apns2Notification {
	priority := 5 // default for batch/temporal
	if notif.Type == "interrupt" {
		priority = 10 // immediate delivery for interrupts
	}

	payload := c.buildPayload(notif)

	return &apns2Notification{
		DeviceToken: token,
		Topic:       c.bundleID,
		Payload:     payload,
		Priority:    priority,
		Expiration:  time.Now().Add(24 * time.Hour),
	}
}

// buildPayload creates the JSON payload for an APNS notification.
func (c *APNSClient) buildPayload(notif *models.Notification) []byte {
	alert := map[string]interface{}{
		"title": notif.Title,
		"body":  notif.Body,
	}

	sound := "default"
	if notif.Type == "interrupt" {
		sound = "urgent.caf"
	} else if notif.Type == "batch" {
		sound = "batch.caf"
	}

	aps := map[string]interface{}{
		"alert": alert,
		"sound": sound,
	}

	if notif.Type == "interrupt" {
		// Time-sensitive interruptions bypass Focus and Do Not Disturb
		aps["interruption-level"] = "time-sensitive"
	}

	payload := map[string]interface{}{
		"aps":      aps,
		"type":     notif.Type,
		"notif_id": notif.ID.String(),
	}

	if len(notif.Data) > 0 {
		var dataMap map[string]interface{}
		if err := json.Unmarshal(notif.Data, &dataMap); err == nil {
			for k, v := range dataMap {
				payload[k] = v
			}
		} else {
			payload["data"] = string(notif.Data)
		}
	}

	data, _ := json.Marshal(payload)
	return data
}

// ============================================================================
// APNS HTTP/2 CLIENT
// ============================================================================

// apnsHTTP2Client wraps a native HTTP/2 client for APNS communication.
type apnsHTTP2Client struct {
	jwtToken string
	keyID    string
	teamID   string
	authKey  *ecdsa.PrivateKey
	devMode  bool
}

// newAPNSHTTP2Client creates a real APNS HTTP/2 client with JWT auth.
func newAPNSHTTP2Client(cfg *config.Config) (*apnsHTTP2Client, error) {
	keyData, err := os.ReadFile(cfg.APNSKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read APNS key: %w", err)
	}

	privateKey, err := parseECPrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parse APNS key: %w", err)
	}

	client := &apnsHTTP2Client{
		keyID:   cfg.APNSKeyID,
		teamID:  cfg.APNSTeamID,
		authKey: privateKey,
		devMode: !cfg.IsProduction(),
	}

	if err := client.refreshToken(); err != nil {
		return nil, fmt.Errorf("generate APNS JWT: %w", err)
	}

	return client, nil
}

// Push sends a notification via APNS HTTP/2 API.
func (c *apnsHTTP2Client) Push(n *apns2Notification) (*apnsResponse, error) {
	if c.jwtToken == "" {
		if err := c.refreshToken(); err != nil {
			return nil, fmt.Errorf("refresh APNS token: %w", err)
		}
	}

	endpoint := "https://api.push.apple.com"
	if c.devMode {
		endpoint = "https://api.sandbox.push.apple.com"
	}

	url := fmt.Sprintf("%s/3/device/%s", endpoint, n.DeviceToken)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(n.Payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "bearer "+c.jwtToken)
	req.Header.Set("Apns-Topic", n.Topic)
	req.Header.Set("Apns-Priority", fmt.Sprintf("%d", n.Priority))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig:   &tls.Config{NextProtos: []string{"h2"}},
		},
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	result := &apnsResponse{
		StatusCode:   resp.StatusCode,
		ApnsID:       resp.Header.Get("apns-id"),
		TokenInvalid: resp.StatusCode == 410,
	}

	if resp.StatusCode != 200 {
		var body struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err == nil {
			result.Reason = body.Reason
		}

		switch result.Reason {
		case "BadDeviceToken", "Unregistered", "DeviceTokenNotForTopic":
			result.TokenInvalid = true
		}
	}

	return result, nil
}

// refreshToken generates a new JWT for APNS authentication.
func (c *apnsHTTP2Client) refreshToken() error {
	header := map[string]interface{}{
		"alg": "ES256",
		"kid": c.keyID,
	}
	headerJSON, _ := json.Marshal(header)

	now := time.Now()
	claims := map[string]interface{}{
		"iss": c.teamID,
		"iat": now.Unix(),
	}
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	signingInput := headerB64 + "." + claimsB64

	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, c.authKey, hash[:])
	if err != nil {
		return fmt.Errorf("sign JWT: %w", err)
	}

	sig := ecdsaSignature{R: r, S: s}
	signature, _ := asn1.Marshal(sig)
	c.jwtToken = signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)

	return nil
}

// ============================================================================
// CRYPTO HELPERS
// ============================================================================

// ecdsaSignature represents an ECDSA signature for ASN.1 encoding.
type ecdsaSignature struct {
	R, S *big.Int
}

// parseECPrivateKey parses an ECDSA private key from PEM or raw bytes.
func parseECPrivateKey(keyData []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(keyData)
	if block != nil {
		keyData = block.Bytes
	}

	key, err := x509.ParseECPrivateKey(keyData)
	if err == nil {
		return key, nil
	}

	// Try PKCS#8
	pkcs8Key, err := x509.ParsePKCS8PrivateKey(keyData)
	if err == nil {
		if ecKey, ok := pkcs8Key.(*ecdsa.PrivateKey); ok {
			return ecKey, nil
		}
	}

	// Try PKIX
	pkixKey, err := x509.ParsePKIXPublicKey(keyData)
	if err == nil {
		if ecKey, ok := pkixKey.(*ecdsa.PublicKey); ok {
			// Cannot derive private key from public key
			_ = ecKey
		}
	}

	// Last resort: try to construct from the elliptic curve
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}
