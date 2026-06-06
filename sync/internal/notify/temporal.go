// Package notify handles push notification dispatch via FCM, APNS, and WebSocket.
package notify

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
)

// ============================================================================
// TemporalNotificationBuilder — Temporal notification builder
// ============================================================================

// TemporalNotificationBuilder constructs time-based notifications including
// pre-event briefings, daily digests, conflict alerts, and scheduled reminders.
type TemporalNotificationBuilder struct{}

// NewTemporalNotificationBuilder creates a new temporal notification builder.
func NewTemporalNotificationBuilder() *TemporalNotificationBuilder {
	return &TemporalNotificationBuilder{}
}

// ============================================================================
// PRE-EVENT BRIEFING
// ============================================================================

// BuildPreEventBriefing creates a notification with context cards for an
// upcoming calendar event.
func (b *TemporalNotificationBuilder) BuildPreEventBriefing(userID uuid.UUID, eventTitle string, startTime time.Time, relatedCards []models.DecisionCard) *models.Notification {
	timeUntil := time.Until(startTime)
	minutesUntil := int(timeUntil.Minutes())

	title := fmt.Sprintf("Upcoming: %s", eventTitle)
	if len(title) > 60 {
		title = title[:57] + "..."
	}

	var body string
	if len(relatedCards) > 0 {
		body = fmt.Sprintf("Starts in %d min — %d related decisions", minutesUntil, len(relatedCards))
	} else {
		body = fmt.Sprintf("Starts in %d min", minutesUntil)
	}

	cardIDs := make([]string, len(relatedCards))
	for i, c := range relatedCards {
		cardIDs[i] = c.ID.String()
	}

	data, _ := json.Marshal(map[string]interface{}{
		"temporal_type":  "pre_event",
		"event_title":    eventTitle,
		"start_time":     startTime.UTC().Format(time.RFC3339),
		"minutes_until":  minutesUntil,
		"related_cards":  cardIDs,
		"related_count":  len(relatedCards),
	})

	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "temporal",
		Title:     title,
		Body:      body,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
}

// ============================================================================
// DAILY DIGEST
// ============================================================================

// BuildDailyDigest creates a morning digest notification summarizing the
// day's queue and calendar events.
func (b *TemporalNotificationBuilder) BuildDailyDigest(userID uuid.UUID, pendingCount int, events []models.CalendarEvent) *models.Notification {
	var title string
	if pendingCount > 0 {
		title = fmt.Sprintf("Morning briefing: %d decisions today", pendingCount)
	} else {
		title = "Morning briefing"
	}

	var body string
	eventCount := len(events)
	switch {
	case pendingCount > 0 && eventCount > 0:
		body = fmt.Sprintf("%d decisions waiting, %d events scheduled", pendingCount, eventCount)
	case pendingCount > 0:
		body = fmt.Sprintf("%d decisions in your queue", pendingCount)
	case eventCount > 0:
		body = fmt.Sprintf("%d events scheduled today", eventCount)
	default:
		body = "No pending decisions or events today"
	}

	eventIDs := make([]string, len(events))
	for i, e := range events {
		eventIDs[i] = e.ID.String()
	}

	data, _ := json.Marshal(map[string]interface{}{
		"temporal_type": "daily_digest",
		"pending_count": pendingCount,
		"event_count":   eventCount,
		"event_ids":     eventIDs,
	})

	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "temporal",
		Title:     title,
		Body:      body,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
}

// ============================================================================
// CONFLICT ALERT
// ============================================================================

// BuildConflictAlert creates a notification when the user has overlapping
// calendar events that also have pending decision cards.
func (b *TemporalNotificationBuilder) BuildConflictAlert(userID uuid.UUID, events []models.CalendarEvent, cardCount int) *models.Notification {
	eventTitles := make([]string, 0, len(events))
	for _, e := range events {
		eventTitles = append(eventTitles, e.Title)
	}

	title := "Calendar conflict detected"

	var body string
	if cardCount > 0 {
		body = fmt.Sprintf("%d overlapping events with %d pending decisions", len(events), cardCount)
	} else {
		body = fmt.Sprintf("%d overlapping events", len(events))
	}

	data, _ := json.Marshal(map[string]interface{}{
		"temporal_type": "conflict_alert",
		"event_count":   len(events),
		"card_count":    cardCount,
		"event_titles":  eventTitles,
	})

	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "temporal",
		Title:     title,
		Body:      body,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
}

// ============================================================================
// REMINDER
// ============================================================================

// BuildReminder creates a reminder notification for a specific decision card
// that has been pending for a long time.
func (b *TemporalNotificationBuilder) BuildReminder(userID uuid.UUID, cardID uuid.UUID, cardTitle string, hoursPending int) *models.Notification {
	title := fmt.Sprintf("Reminder: %s", cardTitle)
	if len(title) > 60 {
		title = title[:57] + "..."
	}

	body := fmt.Sprintf("Pending for %d hours — tap to decide", hoursPending)
	if hoursPending >= 24 {
		days := hoursPending / 24
		body = fmt.Sprintf("Pending for %d days — tap to decide", days)
	}

	data, _ := json.Marshal(map[string]interface{}{
		"temporal_type": "reminder",
		"card_id":       cardID.String(),
		"hours_pending": hoursPending,
	})

	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "temporal",
		Title:     title,
		Body:      body,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
}

// ============================================================================
// SCHEDULED BRIEFING
// ============================================================================

// BuildScheduledBriefing creates a time-of-day briefing notification that
// combines queue status and upcoming meetings.
func (b *TemporalNotificationBuilder) BuildScheduledBriefing(userID uuid.UUID, pendingCount int, nextEvent *models.CalendarEvent) *models.Notification {
	title := "Your briefing"

	var body string
	if nextEvent != nil {
		timeUntil := time.Until(nextEvent.StartAt)
		if timeUntil < time.Hour {
			body = fmt.Sprintf("%d decisions, next meeting in %d min", pendingCount, int(timeUntil.Minutes()))
		} else {
			body = fmt.Sprintf("%d decisions, next meeting at %s", pendingCount, nextEvent.StartAt.Format("3:04 PM"))
		}
	} else {
		body = fmt.Sprintf("%d decisions in your queue", pendingCount)
	}

	var eventID *string
	if nextEvent != nil {
		eventID = strPtr(nextEvent.ID.String())
	}

	data, _ := json.Marshal(map[string]interface{}{
		"temporal_type":  "scheduled_briefing",
		"pending_count":  pendingCount,
		"next_event_id":  eventID,
	})

	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "temporal",
		Title:     title,
		Body:      body,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
}

func strPtr(s string) *string {
	return &s
}
