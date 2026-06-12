// Package models defines the shared data structures for the Classification Core.
// These structs are the contracts between all classification components and
// downstream bounded contexts (Intelligence Layer, Sync).
// DO NOT MODIFY without coordination.
package models

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// CLASSIFICATION INPUT — Consumed from NATS "email.ingested"
// ============================================================================

// EmailIngestedEvent is the input to Classification Core.
// Defined here as a copy to avoid cross-module import cycles.
// Must match ingestion/internal/nats/events.go exactly.
type EmailIngestedEvent struct {
	EventID            uuid.UUID   `json:"event_id"`
	UserID             uuid.UUID   `json:"user_id"`
	Source             string      `json:"source"` // "gmail" | "outlook"
	AccountID          uuid.UUID   `json:"account_id"`
	ThreadID           uuid.UUID   `json:"thread_id"`
	RawEmailID         uuid.UUID   `json:"raw_email_id"`
	S3URI              string      `json:"s3_uri"`
	HasAttachments     bool        `json:"has_attachments"`
	SenderEmail        string      `json:"sender_email"`
	ReceivedAt         time.Time   `json:"received_at"`
	ClassificationHint string      `json:"classification_hint"` // always "pending"
	ContactIDs         []uuid.UUID `json:"contact_ids"`
}

// ============================================================================
// CLASSIFICATION OUTPUT — Routing Decisions
// ============================================================================

// RouteType is the tri-state routing decision.
type RouteType string

const (
	RouteExtract RouteType = "extract" // Extract-Only: datum extracted, notify user
	RouteAuto    RouteType = "auto"    // Auto-Handle: rule match, execute action
	RouteDecision RouteType = "decision" // Decision Stack: send to Intelligence
)

// ClassificationResult is the output of the classification pipeline.
type ClassificationResult struct {
	RawEmailID       uuid.UUID       `json:"raw_email_id"`
	UserID           uuid.UUID       `json:"user_id"`
	ThreadID         uuid.UUID       `json:"thread_id"`
	Route            RouteType       `json:"route"`
	Confidence       float64         `json:"confidence"`       // 0.0-1.0
	MatchedRuleID    *uuid.UUID      `json:"matched_rule_id,omitempty"`  // for auto
	ExtractedData    *ExtractedDatum `json:"extracted_data,omitempty"`   // for extract
	LLMMatched       bool            `json:"llm_matched,omitempty"`      // matched via LLM not rule
	ProcessedAt      time.Time       `json:"processed_at"`
}

// ExtractedDatum holds data extracted by the Extract-Only pipeline.
type ExtractedDatum struct {
	Type             string `json:"type"`             // "2fa" | "tracking" | "calendar" | "receipt"
	Value            string `json:"value"`            // the extracted value
	NotificationText string `json:"notification_text"` // text for push notification
}

// ============================================================================
// INTELLIGENCE COMPRESS EVENT — Published to "intelligence.compress"
// ============================================================================

// IntelligenceCompressEvent is the schema expected by the Intelligence Layer.
// Classification Core must translate ClassificationResult into this event.
type IntelligenceCompressEvent struct {
	EventID       uuid.UUID   `json:"event_id"`
	UserID        uuid.UUID   `json:"user_id"`
	ThreadID      uuid.UUID   `json:"thread_id"`
	RawEmailIDs   []uuid.UUID `json:"raw_email_ids"`
	PriorityScore float64     `json:"priority_score"`
	Source        string      `json:"source"`
}

// ============================================================================
// AUTO-HANDLE RULES — Structured user delegation
// ============================================================================

// AutoHandleRule is a user-created (or mid-session extracted) rule.
type AutoHandleRule struct {
	ID                  uuid.UUID       `json:"id" db:"id"`
	UserID              uuid.UUID       `json:"user_id" db:"user_id"`
	Name                string          `json:"name" db:"name"`
	Predicate           RulePredicate   `json:"predicate" db:"predicate"`
	ActionType          string          `json:"action_type" db:"action_type"` // "reply_template" | "forward" | "calendar_accept" | "delete" | "extract_notify"
	ActionConfig        json.RawMessage `json:"action_config" db:"action_config"`
	ConfidenceThreshold float64         `json:"confidence_threshold" db:"confidence_threshold"` // default 0.92, hard floor
	Status              string          `json:"status" db:"status"` // "staged" | "active" | "revoked"
	StagedAt            *time.Time      `json:"staged_at" db:"staged_at"`
	ActivatedAt         *time.Time      `json:"activated_at" db:"activated_at"`
	RevokedAt           *time.Time      `json:"revoked_at" db:"revoked_at"`
	UsageCount          int             `json:"usage_count" db:"usage_count"`
	CreatedAt           time.Time       `json:"created_at" db:"created_at"`
}

// RulePredicate is the structured condition for matching emails.
type RulePredicate struct {
	AllOf []Condition `json:"allOf,omitempty"` // AND conditions
	AnyOf []Condition `json:"anyOf,omitempty"` // OR conditions
}

// Condition is a single predicate condition.
type Condition struct {
	Field    string      `json:"field"`    // "sender_email" | "sender_domain" | "subject" | "body" | "recipient" | "has_attachment" | "thread_participant_count"
	Operator string      `json:"operator"` // "eq" | "ne" | "contains" | "regex" | "gt" | "lt" | "in" | "not_in"
	Value    interface{} `json:"value"`    // string, number, bool, []string
}

// Evaluate applies the predicate against an email's attributes.
func (p RulePredicate) Evaluate(attrs EmailAttributes) (bool, error) {
	// "allOf" is AND — all must match
	for _, c := range p.AllOf {
		match, err := c.match(attrs)
		if err != nil || !match {
			return false, err
		}
	}
	// "anyOf" is OR — at least one must match (if present)
	if len(p.AnyOf) > 0 {
		anyMatched := false
		for _, c := range p.AnyOf {
			match, err := c.match(attrs)
			if err != nil {
				return false, err
			}
			if match {
				anyMatched = true
				break
			}
		}
		if !anyMatched {
			return false, nil
		}
	}
	return true, nil
}

func (c Condition) match(attrs EmailAttributes) (bool, error) {
	fieldVal := attrs.Get(c.Field)
	switch c.Operator {
	case "eq":
		fs, fok := fieldVal.(string)
		vs, vok := c.Value.(string)
		if fok && vok {
			return strings.EqualFold(fs, vs), nil
		}
		return fieldVal == c.Value, nil
	case "ne":
		fs, fok := fieldVal.(string)
		vs, vok := c.Value.(string)
		if fok && vok {
			return !strings.EqualFold(fs, vs), nil
		}
		return fieldVal != c.Value, nil
	case "contains":
		s, ok := fieldVal.(string)
		v, vok := c.Value.(string)
		return ok && vok && containsIgnoreCase(s, v), nil
	case "regex":
		s, ok := fieldVal.(string)
		v, vok := c.Value.(string)
		if !ok || !vok {
			return false, nil
		}
		matched, err := matchRegex(s, v)
		return matched, err
	case "gt":
		return compareNumeric(fieldVal, c.Value) > 0, nil
	case "lt":
		return compareNumeric(fieldVal, c.Value) < 0, nil
	case "in":
		s, ok := fieldVal.(string)
		arr, aok := c.Value.([]string)
		return ok && aok && stringInSlice(s, arr), nil
	case "not_in":
		s, ok := fieldVal.(string)
		arr, aok := c.Value.([]string)
		return ok && aok && !stringInSlice(s, arr), nil
	default:
		return false, nil
	}
}

// EmailAttributes are the fields available for rule matching.
type EmailAttributes struct {
	SenderEmail           string   `json:"sender_email"`
	SenderDomain          string   `json:"sender_domain"`
	Subject               string   `json:"subject"`
	Body                  string   `json:"body"` // first 500 chars only
	Recipient             string   `json:"recipient"`
	HasAttachment         bool     `json:"has_attachment"`
	ThreadParticipantCount int     `json:"thread_participant_count"`
	TimeOfDay             int      `json:"time_of_day"`    // 0-23
	DayOfWeek             int      `json:"day_of_week"`    // 0-6
}

// Get retrieves a field by name for predicate evaluation.
func (a EmailAttributes) Get(field string) interface{} {
	switch field {
	case "sender_email":
		return a.SenderEmail
	case "sender_domain":
		return a.SenderDomain
	case "subject":
		return a.Subject
	case "body":
		return a.Body
	case "recipient":
		return a.Recipient
	case "has_attachment":
		return a.HasAttachment
	case "thread_participant_count":
		return a.ThreadParticipantCount
	case "time_of_day":
		return a.TimeOfDay
	case "day_of_week":
		return a.DayOfWeek
	default:
		return nil
	}
}

// ============================================================================
// STAGING — 48-hour rule activation
// ============================================================================

// StagingRule represents a rule in the 48-hour staging window.
type StagingRule struct {
	RuleID      uuid.UUID `json:"rule_id"`
	UserID      uuid.UUID `json:"user_id"`
	StagedAt    time.Time `json:"staged_at"`
	ActivatesAt time.Time `json:"activates_at"` // StagedAt + 48h
	Status      string    `json:"status"`         // "staged" | "activated" | "revoked"
}

// ============================================================================
// LLM FALLBACK — Claude 3 Haiku pattern matching
// ============================================================================

// LLMPatternMatchRequest is sent to Claude 3 Haiku for unstructured pattern detection.
type LLMPatternMatchRequest struct {
	RuleNames       []string        `json:"rule_names"`        // names of user's active rules
	SenderEmail     string          `json:"sender_email"`
	Subject         string          `json:"subject"`
	BodyPreview     string          `json:"body_preview"`      // first 500 chars
	Recipient       string          `json:"recipient"`
	HasAttachment   bool            `json:"has_attachment"`
	ParticipantCount int           `json:"participant_count"`
}

// LLMPatternMatchResponse is what Haiku returns.
type LLMPatternMatchResponse struct {
	Match      string  `json:"match"`       // matched rule name or "none"
	Confidence float64 `json:"confidence"`  // 0.0-1.0
	Reason     string  `json:"reason"`      // brief explanation
}

// ============================================================================
// ERROR TYPES
// ============================================================================

type ClassificationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Retry   bool   `json:"retry"`
}

func (e ClassificationError) Error() string { return e.Message }

const (
	ErrCodePredicateEval   = "predicate_eval_failed"
	ErrCodeLLMUnavailable  = "llm_unavailable"
	ErrCodeRuleNotFound    = "rule_not_found"
	ErrCodeConfidenceBelow = "confidence_below_floor"
)

// ============================================================================
// HELPERS
// ============================================================================

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func matchRegex(s, pattern string) (bool, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(s), nil
}

func compareNumeric(a, b interface{}) int {
	toFloat := func(v interface{}) (float64, bool) {
		switch n := v.(type) {
		case int:
			return float64(n), true
		case int64:
			return float64(n), true
		case float64:
			return n, true
		case float32:
			return float64(n), true
		case json.Number:
			f, err := n.Float64()
			return f, err == nil
		}
		return 0, false
	}
	fa, aok := toFloat(a)
	fb, bok := toFloat(b)
	if !aok || !bok {
		return 0
	}
	switch {
	case fa > fb:
		return 1
	case fa < fb:
		return -1
	default:
		return 0
	}
}


func stringInSlice(s string, arr []string) bool {
	for _, v := range arr {
		if v == s {
			return true
		}
	}
	return false
}
