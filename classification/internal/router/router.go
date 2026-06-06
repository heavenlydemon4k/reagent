package router

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/decisionstack/classification/internal/models"
)

// ---------------------------------------------------------------------------
// Dependency interfaces — satisfied by extract and auto packages.
// ---------------------------------------------------------------------------

// Extractor is implemented by the Extract-Only pipeline.
type Extractor interface {
	// Process attempts to extract structured data from the email.
	// A non-nil result means the email matched an extraction pattern.
	Process(ctx context.Context, event *models.EmailIngestedEvent) (*models.ExtractedDatum, error)
}

// AutoEngine evaluates active auto-handle rules against an email.
type AutoEngine interface {
	// Evaluate checks active rules only (staged rules are NOT matched).
	// Returns (result, handled, error). handled==true means an active rule fired.
	Evaluate(ctx context.Context, event *models.EmailIngestedEvent, attrs models.EmailAttributes) (*models.ClassificationResult, bool, error)
}

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

// Router is the entry point for the tri-state classification pipeline.
// Order: Extract → Auto → Decision (never skip, never reorder).
type Router struct {
	extract    Extractor
	autoEngine AutoEngine
	log        *slog.Logger
	metrics    *Metrics
}

// NewRouter creates a Router. All dependencies are required.
func NewRouter(extract Extractor, autoEngine AutoEngine, log *slog.Logger, metrics *Metrics) *Router {
	return &Router{
		extract:    extract,
		autoEngine: autoEngine,
		log:        log.With("component", "router"),
		metrics:    metrics,
	}
}

// Route processes a single email through the classification pipeline.
// Invariants:
//   - Extract is tried first; if it matches, RouteExtract is returned immediately.
//   - Auto-Handle only considers *active* rules; staged rules do NOT auto-fire.
//   - Decision Stack is the unconditional default when nothing else matches.
func (r *Router) Route(ctx context.Context, event *models.EmailIngestedEvent) (*models.ClassificationResult, error) {
	start := time.Now()
	logger := r.log.With(
		"event_id", event.EventID,
		"raw_email_id", event.RawEmailID,
		"user_id", event.UserID,
	)

	// -----------------------------------------------------------------------
	// 1. Build EmailAttributes from the ingested event.
	// -----------------------------------------------------------------------
	attrs := buildAttributes(event)
	logger.Debug("built attributes", "sender_domain", attrs.SenderDomain)

	// -----------------------------------------------------------------------
	// 2. Stage 1: Extract-Only
	// -----------------------------------------------------------------------
	datum, err := r.extract.Process(ctx, event)
	if err != nil {
		// Extraction failure is non-fatal; log and continue to Auto.
		logger.Warn("extract stage failed", "error", err)
	} else if datum != nil {
		// Extraction matched — route to Extract-Only pipeline.
		result := &models.ClassificationResult{
			RawEmailID:    event.RawEmailID,
			UserID:        event.UserID,
			ThreadID:      event.ThreadID,
			Route:         models.RouteExtract,
			Confidence:    1.0, // extraction is deterministic
			ExtractedData: datum,
			ProcessedAt:   time.Now().UTC(),
		}
		r.metrics.RecordClassification("extract")
		r.metrics.ObserveClassification(time.Since(start).Seconds())
		logger.Info("routed to extract",
			"route", result.Route,
			"extract_type", datum.Type,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return result, nil
	}

	// -----------------------------------------------------------------------
	// 3. Stage 2: Auto-Handle (active rules only)
	// -----------------------------------------------------------------------
	result, handled, err := r.autoEngine.Evaluate(ctx, event, attrs)
	if err != nil {
		// Auto-engine failure is non-fatal; log and fall through to Decision.
		logger.Warn("auto stage failed", "error", err)
	} else if handled && result != nil {
		// An active auto-handle rule fired.
		r.metrics.RecordClassification("auto")
		r.metrics.ObserveClassification(time.Since(start).Seconds())
		r.metrics.RecordAutoHandleAction(result.Route) // result.Route carries action type
		logger.Info("routed to auto",
			"route", result.Route,
			"matched_rule_id", result.MatchedRuleID,
			"confidence", result.Confidence,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return result, nil
	}

	// -----------------------------------------------------------------------
	// 4. Stage 3: Decision Stack (default)
	// -----------------------------------------------------------------------
	result = &models.ClassificationResult{
		RawEmailID:  event.RawEmailID,
		UserID:      event.UserID,
		ThreadID:    event.ThreadID,
		Route:       models.RouteDecision,
		Confidence:  0.0, // no rule matched
		ProcessedAt: time.Now().UTC(),
	}
	r.metrics.RecordClassification("decision")
	r.metrics.ObserveClassification(time.Since(start).Seconds())
	logger.Info("routed to decision",
		"route", result.Route,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return result, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildAttributes constructs EmailAttributes from the ingested event.
// Body and Subject are fetched from downstream storage (S3) in a real system;
// here we prepare the structure with what we have inline.
func buildAttributes(event *models.EmailIngestedEvent) models.EmailAttributes {
	senderDomain := ""
	if parts := strings.Split(event.SenderEmail, "@"); len(parts) == 2 {
		senderDomain = parts[1]
	}

	return models.EmailAttributes{
		SenderEmail:            event.SenderEmail,
		SenderDomain:           senderDomain,
		HasAttachment:          event.HasAttachments,
		ThreadParticipantCount: len(event.ContactIDs),
		TimeOfDay:              event.ReceivedAt.Hour(),
		DayOfWeek:              int(event.ReceivedAt.Weekday()),
		// Subject and Body are populated by the AutoEngine via S3 fetch
		// when evaluating predicates that need them.
	}
}

// ---------------------------------------------------------------------------
// Result helpers
// ---------------------------------------------------------------------------

// IsTerminalRoute returns true for routes that end processing.
func IsTerminalRoute(r models.RouteType) bool {
	switch r {
	case models.RouteExtract, models.RouteAuto, models.RouteDecision:
		return true
	default:
		return false
	}
}

// RouteForRuleID builds a ClassificationResult for a specific auto-handle rule match.
func RouteForRuleID(event *models.EmailIngestedEvent, ruleID uuid.UUID, confidence float64, actionType string) *models.ClassificationResult {
	return &models.ClassificationResult{
		RawEmailID:    event.RawEmailID,
		UserID:        event.UserID,
		ThreadID:      event.ThreadID,
		Route:         models.RouteType(actionType), // e.g. "reply_template"
		Confidence:    confidence,
		MatchedRuleID: &ruleID,
		ProcessedAt:   time.Now().UTC(),
	}
}

// ValidateResult checks invariants on a ClassificationResult.
func ValidateResult(r *models.ClassificationResult) error {
	if r.RawEmailID == uuid.Nil {
		return fmt.Errorf("classification result missing RawEmailID")
	}
	if r.UserID == uuid.Nil {
		return fmt.Errorf("classification result missing UserID")
	}
	if !IsTerminalRoute(r.Route) {
		return fmt.Errorf("classification result has non-terminal route: %s", r.Route)
	}
	if r.Route == models.RouteExtract && r.ExtractedData == nil {
		return fmt.Errorf("extract route missing ExtractedData")
	}
	if r.Route == models.RouteAuto && r.MatchedRuleID == nil {
		return fmt.Errorf("auto route missing MatchedRuleID")
	}
	return nil
}
