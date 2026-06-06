// Package notify handles push notification dispatch via FCM, APNS, and WebSocket.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
	"github.com/decisionstack/sync/internal/websocket"
	"github.com/google/uuid"
)

// ============================================================================
// NotificationDispatcher — Routes notifications to correct channels
// ============================================================================

// NotificationDispatcher is the central hub for routing notifications to
// the correct delivery channels (FCM, APNS, WebSocket). It respects user
// preferences, quiet hours, and notification priorities.
type NotificationDispatcher struct {
	fcm        *FCMClient
	apns       *APNSClient
	store      *NotificationStore
	prefs      *PreferenceManager
	quietHours *QuietHoursChecker
	hub        *websocket.Hub

	batchBuilder     *BatchNotificationBuilder
	interruptBuilder *InterruptNotificationBuilder
	temporalBuilder  *TemporalNotificationBuilder

	log *slog.Logger
}

// NotificationDispatcherConfig holds constructor dependencies.
type NotificationDispatcherConfig struct {
	FCM              *FCMClient
	APNS             *APNSClient
	Store            *NotificationStore
	Prefs            *PreferenceManager
	QuietHours       *QuietHoursChecker
	Hub              *websocket.Hub
	BatchBuilder     *BatchNotificationBuilder
	InterruptBuilder *InterruptNotificationBuilder
	TemporalBuilder  *TemporalNotificationBuilder
}

// NewNotificationDispatcher creates a new notification dispatcher.
func NewNotificationDispatcher(cfg NotificationDispatcherConfig) *NotificationDispatcher {
	log := logger.L().With("component", "notification_dispatcher")

	d := &NotificationDispatcher{
		fcm:              cfg.FCM,
		apns:             cfg.APNS,
		store:            cfg.Store,
		prefs:            cfg.Prefs,
		quietHours:       cfg.QuietHours,
		hub:              cfg.Hub,
		batchBuilder:     cfg.BatchBuilder,
		interruptBuilder: cfg.InterruptBuilder,
		temporalBuilder:  cfg.TemporalBuilder,
		log:              log,
	}

	// Apply defaults for optional builders
	if d.batchBuilder == nil {
		d.batchBuilder = NewBatchNotificationBuilder()
	}
	if d.interruptBuilder == nil {
		d.interruptBuilder = NewInterruptNotificationBuilder()
	}
	if d.temporalBuilder == nil {
		d.temporalBuilder = NewTemporalNotificationBuilder()
	}

	return d
}

// ============================================================================
// MAIN DISPATCH
// ============================================================================

// Dispatch sends a notification to all appropriate channels for the user.
//
// Flow:
//  1. Persist the notification to PostgreSQL
//  2. Check quiet hours — defer if needed (unless priority >= 8)
//  3. Check user preferences — skip if type disabled
//  4. Get device tokens for the user
//  5. Route to FCM (Android) and APNS (iOS)
//  6. Send to WebSocket if user is online
//  7. Mark as sent
func (d *NotificationDispatcher) Dispatch(ctx context.Context, notif *models.Notification) error {
	if notif.ID == uuid.Nil {
		notif.ID = uuid.New()
	}

	// 1. Persist notification
	if err := d.store.InsertNotification(ctx, notif); err != nil {
		d.log.Error("failed to persist notification", "error", err, "notif_id", notif.ID)
		return fmt.Errorf("dispatch: persist: %w", err)
	}

	// 2. Check quiet hours
	priority := d.getPriority(notif.Type)
	if shouldDefer, resumeTime := d.quietHours.ShouldDefer(notif.UserID, priority); shouldDefer {
		d.log.Info("deferring notification due to quiet hours",
			"notif_id", notif.ID,
			"user_id", notif.UserID,
			"resume_time", resumeTime,
		)
		if err := d.store.InsertDeferred(ctx, notif.UserID, notif, resumeTime); err != nil {
			d.log.Error("failed to defer notification", "error", err)
		}
		return nil
	}

	// 3. Check user preferences
	if !d.prefs.IsTypeEnabled(ctx, notif.UserID, notif.Type) {
		d.log.Debug("notification type disabled for user, skipping",
			"user_id", notif.UserID,
			"type", notif.Type,
		)
		return d.store.MarkSent(ctx, notif.ID) // mark as sent (dropped)
	}

	// 4. Get device tokens
	devices, err := d.store.GetDevices(ctx, notif.UserID)
	if err != nil {
		d.log.Error("failed to get devices", "error", err, "user_id", notif.UserID)
		return fmt.Errorf("dispatch: get devices: %w", err)
	}

	// 5. Route to push channels
	if len(devices) > 0 {
		if err := d.dispatchToDevices(ctx, notif, devices); err != nil {
			d.log.Warn("some device sends failed", "error", err, "notif_id", notif.ID)
		}
	}

	// 6. Send to WebSocket if user is online
	if d.hub != nil && d.hub.IsUserOnline(notif.UserID) {
		d.dispatchToWebSocket(notif)
	}

	// 7. Mark as sent
	if err := d.store.MarkSent(ctx, notif.ID); err != nil {
		d.log.Warn("failed to mark notification as sent", "error", err, "notif_id", notif.ID)
	}

	d.log.Debug("notification dispatched",
		"notif_id", notif.ID,
		"user_id", notif.UserID,
		"type", notif.Type,
		"priority", priority,
		"device_count", len(devices),
	)

	return nil
}

// dispatchToDevices sends the notification to each device via the appropriate
// push service (FCM for Android, APNS for iOS).
func (d *NotificationDispatcher) dispatchToDevices(ctx context.Context, notif *models.Notification, devices []DeviceToken) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(devices))

	for _, device := range devices {
		device := device // capture range variable
		wg.Add(1)
		go func() {
			defer wg.Done()

			switch device.DeviceType {
			case "android":
				if device.FCMToken == nil || *device.FCMToken == "" {
					return
				}
				if err := d.fcm.Send(ctx, *device.FCMToken, notif); err != nil {
					if IsErrInvalidToken(err) {
						// Remove invalid token
						_ = d.store.InvalidateFCMToken(ctx, device.UserID, device.DeviceID)
					}
					errCh <- fmt.Errorf("fcm:%s: %w", device.DeviceID, err)
				}

			case "ios":
				if device.APNSToken == nil || *device.APNSToken == "" {
					return
				}
				if err := d.apns.Send(ctx, *device.APNSToken, notif); err != nil {
					if IsErrInvalidToken(err) {
						// Remove invalid token
						_ = d.store.InvalidateAPNSToken(ctx, device.UserID, device.DeviceID)
					}
					errCh <- fmt.Errorf("apns:%s: %w", device.DeviceID, err)
				}

			default:
				errCh <- fmt.Errorf("unknown device type: %s", device.DeviceType)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("dispatch devices: %d failures", len(errs))
	}
	return nil
}

// dispatchToWebSocket sends the notification as a WebSocket event to the user.
func (d *NotificationDispatcher) dispatchToWebSocket(notif *models.Notification) {
	wsEvent := models.WSEvent{
		Type:      models.WSEventType(notif.Type),
		CardID:    uuid.Nil, // set from notification data if available
		Text:      string(notif.Data),
		Timestamp: time.Now().UTC(),
	}

	// Try to extract card_id from notification data
	if len(notif.Data) > 0 {
		var dataMap map[string]interface{}
		if err := json.Unmarshal(notif.Data, &dataMap); err == nil {
			if cardIDStr, ok := dataMap["card_id"].(string); ok {
				if cardID, err := uuid.Parse(cardIDStr); err == nil {
					wsEvent.CardID = cardID
				}
			}
		}
	}

	if err := d.hub.BroadcastToUser(notif.UserID, &wsEvent); err != nil {
		d.log.Warn("failed to broadcast to websocket",
			"error", err,
			"user_id", notif.UserID,
		)
	}
}

// ============================================================================
// TYPED DISPATCH HELPERS
// ============================================================================

// DispatchBatch sends a batch notification for a user's queue.
func (d *NotificationDispatcher) DispatchBatch(ctx context.Context, userID uuid.UUID, batchInfo models.BatchInfo) error {
	notif := d.batchBuilder.Build(userID, batchInfo)
	return d.Dispatch(ctx, notif)
}

// DispatchInterrupt sends an interrupt notification for an urgent card.
func (d *NotificationDispatcher) DispatchInterrupt(ctx context.Context, userID uuid.UUID, card models.DecisionCard) error {
	notif := d.interruptBuilder.Build(userID, card)
	return d.Dispatch(ctx, notif)
}

// DispatchTemporal sends a temporal notification (briefing, digest, etc.).
func (d *NotificationDispatcher) DispatchTemporal(ctx context.Context, notif *models.Notification) error {
	return d.Dispatch(ctx, notif)
}

// ============================================================================
// DEFERRED NOTIFICATION PROCESSING
// ============================================================================

// ProcessDeferred processes all due deferred notifications.
// This should be called periodically by a background worker.
func (d *NotificationDispatcher) ProcessDeferred(ctx context.Context) error {
	deferred, err := d.store.GetDueDeferred(ctx)
	if err != nil {
		return fmt.Errorf("process deferred: %w", err)
	}

	for _, def := range deferred {
		var notif models.Notification
		if err := json.Unmarshal(def.Notification, &notif); err != nil {
			d.log.Error("failed to unmarshal deferred notification", "error", err, "id", def.ID)
			_ = d.store.MarkDeferredProcessed(ctx, def.ID)
			continue
		}

		d.log.Debug("processing deferred notification",
			"id", def.ID,
			"user_id", def.UserID,
			"type", notif.Type,
		)

		if err := d.Dispatch(ctx, &notif); err != nil {
			d.log.Error("failed to dispatch deferred notification",
				"error", err,
				"id", def.ID,
			)
			continue
		}

		_ = d.store.MarkDeferredProcessed(ctx, def.ID)
	}

	return nil
}

// ============================================================================
// PRIORITY MATRIX
// ============================================================================

// getPriority returns the numeric priority for a notification type.
// Higher values bypass quiet hours and get faster delivery.
//
// Priority matrix:
//
//	Type      | Priority | Bypass Quiet Hours | FCM Priority | APNS Priority
//	----------|----------|--------------------|--------------|--------------
//	interrupt | 10       | Yes (>=8)          | high         | 10
//	batch     | 5        | No                 | normal       | 5
//	temporal  | 3        | No                 | normal       | 5
//	staging   | 2        | No                 | normal       | 5
//	default   | 1        | No                 | normal       | 5
func (d *NotificationDispatcher) getPriority(notifType string) int {
	switch notifType {
	case "interrupt":
		return 10
	case "batch":
		return 5
	case "temporal":
		return 3
	case "staging":
		return 2
	default:
		return 1
	}
}

// PriorityMatrix returns the complete priority configuration as a map.
// Useful for health checks and admin endpoints.
func (d *NotificationDispatcher) PriorityMatrix() map[string]map[string]interface{} {
	return map[string]map[string]interface{}{
		"interrupt": {
			"priority":             10,
			"bypass_quiet_hours":   true,
			"fcm_priority":         "high",
			"apns_priority":        10,
			"description":          "Urgent decision cards — bypass quiet hours",
		},
		"batch": {
			"priority":             5,
			"bypass_quiet_hours":   false,
			"fcm_priority":         "normal",
			"apns_priority":        5,
			"description":          "Queue summaries — respect quiet hours",
		},
		"temporal": {
			"priority":             3,
			"bypass_quiet_hours":   false,
			"fcm_priority":         "normal",
			"apns_priority":        5,
			"description":          "Briefings, digests, reminders — respect quiet hours",
		},
		"staging": {
			"priority":             2,
			"bypass_quiet_hours":   false,
			"fcm_priority":         "normal",
			"apns_priority":        5,
			"description":          "Rule staging notifications — respect quiet hours",
		},
	}
}

// ============================================================================
// WEBSOCKET EVENT TYPES
// ============================================================================

// WSNotificationEventTypes returns the WebSocket event types used for
// real-time notification delivery.
func (d *NotificationDispatcher) WSNotificationEventTypes() map[string]string {
	return map[string]string{
		"batch":     "batch",
		"interrupt": "interrupt",
		"temporal":  "temporal",
		"staging":   "staging",
	}
}

// ============================================================================
// HEALTH
// ============================================================================

// Health returns the health status of the notification dispatcher.
func (d *NotificationDispatcher) Health() map[string]interface{} {
	return map[string]interface{}{
		"fcm_enabled":    d.fcm != nil,
		"apns_enabled":   d.apns != nil,
		"websocket":      d.hub != nil,
		"priority_types": len(d.PriorityMatrix()),
	}
}
