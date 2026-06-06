package extract

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/decisionstack/classification/internal/models"
)

// ---------------------------------------------------------------------------
// Extractor — Extract-Only pipeline orchestrator
// ---------------------------------------------------------------------------

// RawEmailStore abstracts the database that holds raw email bodies.
// Production implementation provided by the worker package.
type RawEmailStore interface {
	// FetchBody returns the plain-text body and subject for a given raw_email_id.
	// Returns sql.ErrNoRows if the email has already been deleted.
	FetchBody(ctx context.Context, rawEmailID uuid.UUID) (subject string, body string, err error)
}

// EventPublisher emits downstream events.
type EventPublisher interface {
	// PublishExtractCompleted sends the ExtractCompleted event to NATS.
	PublishExtractCompleted(ctx context.Context, rawEmailID uuid.UUID, userID uuid.UUID, datum *models.ExtractedDatum) error
}

// DeletionTimer schedules the 24-hour raw-email deletion.
type DeletionTimer interface {
	// ScheduleRawEmailDeletion sets a timer to delete the raw email after 24h.
	ScheduleRawEmailDeletion(ctx context.Context, rawEmailID uuid.UUID) error
}

// Extractor is the Extract-Only pipeline entry point.
//
// Architecture:
//   1. Regex bank  (fast path,  <2ms)
//   2. ONNX classifier (ML path, <50ms)
//   3. Fall through → Auto-Handle / Decision Stack
//
// Invariants:
//   • Regex and ONNX are mutually exclusive for a single email (regex hit → done).
//   • ONNX confidence < 0.95 → hard fall-through (no guess).
//   • No extracted datum produced → returns nil, nil → caller routes next stage.
type Extractor struct {
	db        RawEmailStore
	publisher EventPublisher
	timer     DeletionTimer
	onnx      *ONNXClassifier
}

// NewExtractor builds an Extractor.  Pass nil for onnx to skip ML inference
// (all emails then rely on regex or fall through).
func NewExtractor(db RawEmailStore, pub EventPublisher, timer DeletionTimer, onnx *ONNXClassifier) *Extractor {
	return &Extractor{
		db:        db,
		publisher: pub,
		timer:     timer,
		onnx:      onnx,
	}
}

// ---------------------------------------------------------------------------
// Main entry point
// ---------------------------------------------------------------------------

// Process executes the Extract-Only pipeline for a single ingested email.
//
// Steps:
//   1. Fetch body_text from raw_emails via DB (by raw_email_id).
//   2. Try regex bank first (<2ms fast path).
//   3. If regex miss → try ONNX classifier (<50ms ML path).
//   4. If ONNX confidence >= 0.95 → build ExtractedDatum.
//   5. Publish ExtractCompleted event + set 24h deletion timer.
//   6. Return ClassificationResult with Route=RouteExtract.
//   7. If no match at all → return nil, nil → caller routes to Auto-Handle.
//
// This method is safe for concurrent use.
func (e *Extractor) Process(ctx context.Context, email *models.EmailIngestedEvent) (*models.ClassificationResult, error) {
	if email == nil {
		return nil, fmt.Errorf("email is nil")
	}

	// ── 1. Fetch subject + body from raw email store ─────────────────────────
	subject, bodyText, err := e.db.FetchBody(ctx, email.RawEmailID)
	if err != nil {
		if err == sql.ErrNoRows {
			// Raw email already gone — nothing to extract.
			return nil, nil
		}
		return nil, fmt.Errorf("fetch body for raw_email_id=%s: %w", email.RawEmailID, err)
	}

	// ── 2. Regex fast path ───────────────────────────────────────────────────
	if datum, ok := Extract(email, subject, bodyText); ok {
		return e.buildResult(ctx, email, datum, 1.0, "regex")
	}

	// ── 3. ONNX ML path (only if regex missed) ───────────────────────────────
	if e.onnx != nil {
		preview := buildONNXInput(subject, bodyText)
		class, confidence, err := e.onnx.Classify(preview)
		if err != nil {
			// Non-recoverable ONNX error → conservative fall-through.
			return nil, nil
		}
		if class != classUnknown && confidence >= onnxConfidenceThreshold {
			datum := onnxClassToDatum(class, confidence)
			return e.buildResult(ctx, email, datum, confidence, "onnx")
		}
	}

	// ── 4. No match → fall through to next pipeline stage ────────────────────
	return nil, nil
}

// ---------------------------------------------------------------------------
// Result builder & side effects
// ---------------------------------------------------------------------------

// buildResult constructs the ClassificationResult, publishes the downstream
// event, and schedules raw-email deletion.
func (e *Extractor) buildResult(
	ctx context.Context,
	email *models.EmailIngestedEvent,
	datum *models.ExtractedDatum,
	confidence float64,
	matchedVia string,
) (*models.ClassificationResult, error) {
	now := time.Now().UTC()

	result := &models.ClassificationResult{
		RawEmailID: email.RawEmailID,
		UserID:     email.UserID,
		ThreadID:   email.ThreadID,
		Route:      models.RouteExtract,
		Confidence: confidence,
		ExtractedData: datum,
		ProcessedAt: now,
	}

	// ── Publish ExtractCompleted event (best-effort) ─────────────────────────
	if e.publisher != nil {
		if err := e.publisher.PublishExtractCompleted(ctx, email.RawEmailID, email.UserID, datum); err != nil {
			// Log but don't fail extraction; event can be retried by scavenger.
			// TODO: structured logging
			_ = err
		}
	}

	// ── Set 24h raw email deletion timer ─────────────────────────────────────
	if e.timer != nil {
		if err := e.timer.ScheduleRawEmailDeletion(ctx, email.RawEmailID); err != nil {
			// Log but don't fail extraction; deletion can be handled by TTL.
			_ = err
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// ONNX helpers
// ---------------------------------------------------------------------------

// buildONNXInput concatenates subject + body preview (first 200 chars).
// This matches the training-time input format for DistilBERT.
func buildONNXInput(subject, body string) string {
	var sb strings.Builder
	sb.WriteString(subject)
	sb.WriteString(" ")
	// Truncate body to 200 chars as specified.
	if len(body) > 200 {
		sb.WriteString(body[:200])
	} else {
		sb.WriteString(body)
	}
	return sb.String()
}

// onnxClassToDatum converts an ONNX class label into an ExtractedDatum with
// appropriate notification text.
func onnxClassToDatum(class string, confidence float64) *models.ExtractedDatum {
	switch class {
	case classReceipt:
		return &models.ExtractedDatum{
			Type:             string(TypeReceipt),
			Value:            fmt.Sprintf("receipt_conf_%.4f", confidence),
			NotificationText: NotificationTemplates[TypeReceipt],
		}
	case classNewsletter:
		return &models.ExtractedDatum{
			Type:             string(TypeNewsletter),
			Value:            fmt.Sprintf("newsletter_conf_%.4f", confidence),
			NotificationText: NotificationTemplates[TypeNewsletter],
		}
	case classNotification:
		return &models.ExtractedDatum{
			Type:             string(TypeNotification),
			Value:            fmt.Sprintf("notification_conf_%.4f", confidence),
			NotificationText: NotificationTemplates[TypeNotification],
		}
	default:
		return &models.ExtractedDatum{
			Type:             string(TypeNotification),
			Value:            fmt.Sprintf("unknown_conf_%.4f", confidence),
			NotificationText: NotificationTemplates[TypeNotification],
		}
	}
}
