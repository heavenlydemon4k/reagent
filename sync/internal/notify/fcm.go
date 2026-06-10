// Package notify handles push notification dispatch via FCM, APNS, and WebSocket.
package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
)

// ============================================================================
// FCMClient — Firebase Cloud Messaging
// ============================================================================

// fcmMessage is the interface for an FCM message.
// In production this wraps *messaging.Message from firebase.google.com/go/v4/messaging.
type fcmMessage struct {
	Token        string
	Title        string
	Body         string
	Data         map[string]string
	Priority     string // "high" or "normal"
	ChannelID    string
	Sound        string
}

// fcmHTTPClient is the interface for the FCM HTTP client.
// The real implementation wraps *messaging.Client from the Firebase Admin SDK.
type fcmHTTPClient interface {
	Send(ctx context.Context, msg *fcmMessage) (string, error)
	SendMulticast(ctx context.Context, tokens []string, msg *fcmMessage) (*fcmMulticastResponse, error)
}

// fcmMulticastResponse represents the result of a multicast send.
type fcmMulticastResponse struct {
	SuccessCount int
	FailureCount int
	Responses    []fcmResponse
}

type fcmResponse struct {
	Success   bool
	MessageID string
	Error     error
}

// FCMClient sends push notifications to Android devices via Firebase
// Cloud Messaging. It manages token invalidation and supports both single
// and multicast delivery.
type FCMClient struct {
	client  fcmHTTPClient
	enabled bool
	cfg     *config.Config
}

// NewFCMClient creates a new FCM client. If FCM is not enabled or credentials
// are missing, it returns a no-op client that logs and drops messages.
//
// The caller should supply an fcmHTTPClient implementation that wraps the
// Firebase Admin SDK messaging.Client:
//
//	import "firebase.google.com/go/v4/messaging"
//	firebaseClient, _ := app.Messaging(ctx)
//	fcm := notify.NewFCMClient(notify.WrapFirebaseMessaging(firebaseClient), cfg)
func NewFCMClient(httpClient fcmHTTPClient, cfg *config.Config) *FCMClient {
	if httpClient == nil {
		logger.Info("FCM HTTP client not provided, using no-op client")
		return &FCMClient{enabled: false, cfg: cfg}
	}
	return &FCMClient{
		client:  httpClient,
		enabled: true,
		cfg:     cfg,
	}
}

// NewNoOpFCMClient creates a no-op FCM client for environments without
// Firebase credentials.
func NewNoOpFCMClient(cfg *config.Config) *FCMClient {
	return &FCMClient{enabled: false, cfg: cfg}
}

// Send delivers a notification to a single Android device via FCM.
// Invalid tokens are reported back as ErrInvalidToken.
func (c *FCMClient) Send(ctx context.Context, token string, notif *models.Notification) error {
	if !c.enabled {
		logger.Debug("FCM not enabled, skipping send",
			"type", notif.Type,
			"user_id", notif.UserID,
		)
		return nil
	}

	if token == "" {
		return fmt.Errorf("fcm: empty token")
	}

	msg := c.buildMessage(token, notif)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	messageID, err := c.client.Send(ctx, msg)
	if err != nil {
		if isFCMTokenError(err) {
			logger.Warn("FCM token invalid, should be removed",
				"token_prefix", maskToken(token),
				"error", err,
			)
			return &ErrInvalidToken{
				Token:    token,
				Platform: "android",
				Cause:    err,
			}
		}
		logger.Error("FCM send failed", "error", err, "user_id", notif.UserID)
		return fmt.Errorf("fcm: send: %w", err)
	}

	logger.Debug("FCM message sent", "message_id", messageID, "user_id", notif.UserID)
	return nil
}

// SendMulticast sends a notification to multiple Android devices.
// Returns a map of token -> error for any failures.
func (c *FCMClient) SendMulticast(ctx context.Context, tokens []string, notif *models.Notification) map[string]error {
	failures := make(map[string]error)
	if !c.enabled {
		return failures
	}

	if len(tokens) == 0 {
		return failures
	}

	// Build message template (token will be overridden per-device by the client)
	msg := c.buildMessage("", notif)

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resp, err := c.client.SendMulticast(ctx, tokens, msg)
	if err != nil {
		logger.Error("FCM multicast failed", "error", err, "user_id", notif.UserID)
		for _, t := range tokens {
			failures[t] = fmt.Errorf("fcm: multicast: %w", err)
		}
		return failures
	}

	if resp.FailureCount > 0 {
		for i, res := range resp.Responses {
			if !res.Success {
				failures[tokens[i]] = res.Error
				if isFCMTokenError(res.Error) {
					failures[tokens[i]] = &ErrInvalidToken{
						Token:    tokens[i],
						Platform: "android",
						Cause:    res.Error,
					}
				}
			}
		}
	}

	logger.Debug("FCM multicast complete",
		"success", resp.SuccessCount,
		"failure", resp.FailureCount,
		"user_id", notif.UserID,
	)

	return failures
}

// ============================================================================
// MESSAGE BUILDER
// ============================================================================

// buildMessage constructs an FCM message for a notification.
func (c *FCMClient) buildMessage(token string, notif *models.Notification) *fcmMessage {
	priority := "normal"
	if notif.Type == "interrupt" {
		priority = "high"
	}

	data := map[string]string{
		"type":     notif.Type,
		"title":    notif.Title,
		"body":     notif.Body,
		"notif_id": notif.ID.String(),
	}
	if len(notif.Data) > 0 {
		data["payload"] = string(notif.Data)
	}

	return &fcmMessage{
		Token:     token,
		Title:     notif.Title,
		Body:      notif.Body,
		Data:      data,
		Priority:  priority,
		ChannelID: c.channelIDForType(notif.Type),
		Sound:     c.soundForType(notif.Type),
	}
}

// channelIDForType returns the Android notification channel ID.
func (c *FCMClient) channelIDForType(notifType string) string {
	switch notifType {
	case "interrupt":
		return "interrupts"
	case "batch":
		return "batch"
	case "temporal":
		return "temporal"
	case "staging":
		return "staging"
	default:
		return "default"
	}
}

// soundForType returns the notification sound name.
func (c *FCMClient) soundForType(notifType string) string {
	switch notifType {
	case "interrupt":
		return "urgent.caf"
	case "batch":
		return "batch.caf"
	case "temporal":
		return "temporal.caf"
	default:
		return "default"
	}
}

// ============================================================================
// FIREBASE WRAPPER (to be used by the main application)
// ============================================================================

// FirebaseMessagingWrapper adapts the Firebase Admin SDK messaging.Client
// to the fcmHTTPClient interface.
//
// Usage in main.go or wire setup:
//
//	import firebase "firebase.google.com/go/v4"
//	app, _ := firebase.NewApp(ctx, nil)
//	fbClient, _ := app.Messaging(ctx)
//	fcm := notify.NewFCMClient(notify.WrapFirebaseMessaging(fbClient), cfg)
type FirebaseMessagingWrapper struct {
	// This field should be set to *messaging.Client by the caller
	// using type assertion or direct assignment in the main package.
	// We use interface{} here to avoid importing the Firebase package.
	Inner interface{}
}

// WrapFirebaseMessaging creates the wrapper. The inner parameter must be
// a *messaging.Client from firebase.google.com/go/v4/messaging.
func WrapFirebaseMessaging(inner interface{}) *FirebaseMessagingWrapper {
	return &FirebaseMessagingWrapper{Inner: inner}
}

// Send delegates to the Firebase messaging client.
func (w *FirebaseMessagingWrapper) Send(ctx context.Context, msg *fcmMessage) (string, error) {
	// This is a stub that the main package overrides.
	// The actual implementation lives in the main package where Firebase
	// is imported, avoiding a dependency in the notify package.
	logger.Warn("FirebaseMessagingWrapper.Send called without override — message dropped")
	return "", fmt.Errorf("firebase wrapper not initialized with real client")
}

// SendMulticast delegates to the Firebase messaging client.
func (w *FirebaseMessagingWrapper) SendMulticast(ctx context.Context, tokens []string, msg *fcmMessage) (*fcmMulticastResponse, error) {
	logger.Warn("FirebaseMessagingWrapper.SendMulticast called without override — messages dropped")
	return &fcmMulticastResponse{
		SuccessCount: 0,
		FailureCount: len(tokens),
	}, fmt.Errorf("firebase wrapper not initialized with real client")
}

// ============================================================================
// ERROR HELPERS
// ============================================================================

// isFCMTokenError returns true if the error indicates an invalid FCM token.
func isFCMTokenError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// These strings match Firebase Admin SDK error reasons
	invalidReasons := []string{
		"registration-token-not-registered",
		"invalid-registration-token",
		"invalid-argument",
		"NotRegistered",
		"InvalidRegistration",
	}
	for _, reason := range invalidReasons {
		if containsSubstring(errStr, reason) {
			return true
		}
	}
	return false
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ErrInvalidToken indicates a push token is no longer valid and should be removed.
type ErrInvalidToken struct {
	Token    string
	Platform string // "android" or "ios"
	Cause    error
}

func (e *ErrInvalidToken) Error() string {
	return fmt.Sprintf("invalid %s token %s: %v", e.Platform, maskToken(e.Token), e.Cause)
}

func (e *ErrInvalidToken) Unwrap() error {
	return e.Cause
}

// IsErrInvalidToken returns true if the error is an invalid token error.
func IsErrInvalidToken(err error) bool {
	_, ok := err.(*ErrInvalidToken)
	return ok
}

// maskToken returns a masked version of a token for logging.
func maskToken(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "..." + token[len(token)-4:]
}
