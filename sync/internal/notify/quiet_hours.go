// Package notify handles push notification dispatch via FCM, APNS, and WebSocket.
package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ============================================================================
// QuietHoursChecker — Quiet hours enforcement
// ============================================================================

// QuietHoursChecker determines whether quiet hours are active for a user,
// respecting per-user timezone and preference overrides.
type QuietHoursChecker struct {
	db      *sqlx.DB
	cfg     *config.Config
	prefs   *PreferenceManager
}

// NewQuietHoursChecker creates a new quiet-hours checker.
func NewQuietHoursChecker(db *sqlx.DB, cfg *config.Config, prefs *PreferenceManager) *QuietHoursChecker {
	return &QuietHoursChecker{
		db:    db,
		cfg:   cfg,
		prefs: prefs,
	}
}

// IsQuietHours returns true if the user is currently in quiet hours.
//
// Algorithm:
//  1. Check user's preferences for quiet-hours settings
//  2. If user has no preferences, use global defaults from config
//  3. Convert current UTC time to user's timezone
//  4. Check if the local hour falls within the quiet-hours range
func (q *QuietHoursChecker) IsQuietHours(userID uuid.UUID) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	userPrefs, err := q.prefs.GetPreferences(ctx, userID)
	if err != nil {
		// On error, fall back to global default
		logger.Warn("failed to load user preferences for quiet hours, using defaults",
			"user_id", userID,
			"error", err,
		)
		return q.isDefaultQuietHours()
	}

	// If the user has disabled quiet hours entirely, return false
	if !userPrefs.QuietHoursEnabled {
		return false
	}

	// Determine the quiet hours window
	startHour := q.cfg.QuietHoursStart
	endHour := q.cfg.QuietHoursEnd

	if userPrefs.QuietHoursStartHour != nil {
		startHour = *userPrefs.QuietHoursStartHour
	}
	if userPrefs.QuietHoursEndHour != nil {
		endHour = *userPrefs.QuietHoursEndHour
	}

	tz := userPrefs.Timezone
	if tz == "" {
		tz = "America/New_York" // fallback
	}

	// Get current time in user's timezone
	loc, err := time.LoadLocation(tz)
	if err != nil {
		logger.Warn("invalid user timezone, falling back to local",
			"user_id", userID,
			"timezone", tz,
			"error", err,
		)
		loc = time.Local
	}

	now := time.Now().In(loc)
	return q.isHourInRange(now.Hour(), startHour, endHour)
}

// IsQuietHoursContext is the same as IsQuietHours but accepts a context.
func (q *QuietHoursChecker) IsQuietHoursContext(ctx context.Context, userID uuid.UUID) bool {
	return q.IsQuietHours(userID)
}

// isDefaultQuietHours returns true if the current local time falls within
// the globally configured quiet hours.
func (q *QuietHoursChecker) isDefaultQuietHours() bool {
	now := time.Now()
	return q.isHourInRange(now.Hour(), q.cfg.QuietHoursStart, q.cfg.QuietHoursEnd)
}

// isHourInRange checks if the given hour falls within [start, end).
// Handles overnight ranges (e.g., 22 to 7 spans midnight).
func (q *QuietHoursChecker) isHourInRange(hour, start, end int) bool {
	if start < end {
		// Simple range, e.g., 22-23 (10pm to 11pm)
		return hour >= start && hour < end
	}
	// Overnight range, e.g., 22-7 (10pm to 7am)
	return hour >= start || hour < end
}

// NextQuietHoursEnd returns the time when quiet hours will end for the user.
// Used to schedule deferred notifications.
func (q *QuietHoursChecker) NextQuietHoursEnd(userID uuid.UUID) time.Time {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	userPrefs, err := q.prefs.GetPreferences(ctx, userID)
	if err != nil {
		// Use defaults
		return q.nextDefaultQuietHoursEnd()
	}

	endHour := q.cfg.QuietHoursEnd
	if userPrefs.QuietHoursEndHour != nil {
		endHour = *userPrefs.QuietHoursEndHour
	}

	tz := userPrefs.Timezone
	if tz == "" {
		tz = "America/New_York"
	}

	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.Local
	}

	now := time.Now().In(loc)
	endTime := time.Date(now.Year(), now.Month(), now.Day(), endHour, 0, 0, 0, loc)

	// If quiet hours end time has already passed today, it will end tomorrow
	// (for overnight ranges) or has already ended (for same-day ranges)
	if !q.isHourInRange(now.Hour(),
		q.cfg.QuietHoursStart,
		q.cfg.QuietHoursEnd) {
		// Not currently in quiet hours — return next occurrence
		if endHour <= now.Hour() {
			endTime = endTime.Add(24 * time.Hour)
		}
	}

	return endTime.UTC()
}

// nextDefaultQuietHoursEnd returns when the next quiet hours period ends
// using the default timezone.
func (q *QuietHoursChecker) nextDefaultQuietHoursEnd() time.Time {
	now := time.Now()
	endTime := time.Date(now.Year(), now.Month(), now.Day(), q.cfg.QuietHoursEnd, 0, 0, 0, now.Location())
	if endTime.Before(now) {
		endTime = endTime.Add(24 * time.Hour)
	}
	return endTime.UTC()
}

// ShouldDefer checks if a notification should be deferred due to quiet hours.
// Returns (shouldDefer, resumeTime) where resumeTime is when the notification
// can be delivered. If shouldDefer is false, resumeTime is zero.
func (q *QuietHoursChecker) ShouldDefer(userID uuid.UUID, priority int) (bool, time.Time) {
	// High-priority notifications (priority >= 8) bypass quiet hours
	if priority >= 8 {
		return false, time.Time{}
	}

	if !q.IsQuietHours(userID) {
		return false, time.Time{}
	}

	resumeTime := q.NextQuietHoursEnd(userID)
	return true, resumeTime
}

// String returns a human-readable description of the quiet hours window.
func (q *QuietHoursChecker) String() string {
	return fmt.Sprintf("%02d:00 - %02d:00", q.cfg.QuietHoursStart, q.cfg.QuietHoursEnd)
}
