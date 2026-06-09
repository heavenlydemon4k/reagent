// Package nats defines the NATS JetStream event types and publisher interface
// for the Ingestion Mesh. These are the wire contracts with downstream
// bounded contexts (Classification Core, Intelligence Layer, Sync).
package nats

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

// Publisher is the interface for publishing events to NATS JetStream.
// Production: JetStreamPublisher. Testing: MockPublisher.
type Publisher interface {
	PublishEmailIngested(ctx context.Context, event EmailIngestedEvent) error
	HealthCheck() error
	Close() error
}

// EmailIngestedEvent is published to subject "email.ingested" after a raw
// email has been parsed, threaded, deduped, and persisted.
// Consumer: Classification Core.
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
	ContactIDs         []uuid.UUID `json:"contact_ids"`         // from dedup
}

// ExtractCompletedEvent is published when an email is classified as
// Extract-Only and the datum has been extracted.
type ExtractCompletedEvent struct {
	EventID      uuid.UUID `json:"event_id"`
	UserID       uuid.UUID `json:"user_id"`
	RawEmailID   uuid.UUID `json:"raw_email_id"`
	ExtractType  string    `json:"extract_type"`  // "2fa" | "tracking" | "calendar" | "receipt"
	ExtractedData string   `json:"extracted_data"` // the extracted datum (code, number, etc.)
	NotificationText string `json:"notification_text"`
	ProcessedAt  time.Time `json:"processed_at"`
}

// Subject constants — shared with all bounded contexts.
const (
	SubjectEmailIngested        = "email.ingested"
	SubjectEmailIngestedDLQ     = "email.ingested.dlq"
	SubjectEmailSend            = "email.send"               // Consumer: ingestion send_consumer
	SubjectEmailSent            = "email.sent"               // Consumer: sync service (handleEmailSent)
	SubjectIntelligenceCompress = "intelligence.compress"
	SubjectExtractCompleted     = "ExtractCompleted"
	SubjectAutoHandled          = "AutoHandled"
	SubjectCardCreated          = "sync.notify.CardCreated"
	SubjectEmailClassified      = "email.classified" // ORPHANED — published by classification/router, no consumer yet
	// ORPHANED STREAMS — documented below in StreamConfigs
)

// StreamConfig defines the JetStream stream configurations.
//
// ORPHANED STREAMS — The following streams have publishers but no consumers yet.
// They are retained with LimitsPolicy (not WorkQueue) so messages accumulate
// until a consumer is added or MaxAge expires. Do not remove these streams;
// they carry data that downstream components will consume in future tracks.
//
//   Stream              | Publisher (track)              | Future Consumer        | Status
//   --------------------|--------------------------------|------------------------|--------
//   EXTRACT_COMPLETED   | classification/extract         | audit-log / analytics  | orphaned
//   AUTO_HANDLED        | classification/auto            | audit-log / analytics  | orphaned
//   INTELLIGENCE_COMPRESS | classification/compress      | intelligence-layer     | orphaned
//   EMAIL_CLASSIFIED    | classification/router          | intelligence-layer     | orphaned
//   SYNC_NOTIFY_CARD_CREATED | classification/staging, ingestion/oauth | sync-service (mismatched subject — see below) | orphaned
//
// SUBJECT MISMATCH NOTE:
//   The sync consumer registers on "intelligence.card.created" but this stream
//   publishes to "sync.notify.CardCreated". These are different subjects.
//   When wiring the sync → classification integration, align the subjects.
var StreamConfigs = map[string]nats.StreamConfig{
	"EMAIL_INGESTED": {
		Name:      "EMAIL_INGESTED",
		Subjects:  []string{SubjectEmailIngested},
		Retention: nats.WorkQueuePolicy,
		MaxMsgSize: 8 * 1024 * 1024, // 8MB
		Discard:    nats.DiscardOld,
	},
	"EMAIL_INGESTED_DLQ": {
		Name:     "EMAIL_INGESTED_DLQ",
		Subjects: []string{SubjectEmailIngestedDLQ},
		Retention: nats.LimitsPolicy,
		MaxAge:   30 * 24 * time.Hour,
	},
	"EMAIL_SEND": {
		Name:      "EMAIL_SEND",
		Subjects:  []string{SubjectEmailSend},
		Retention: nats.WorkQueuePolicy,
		MaxMsgSize: 2 * 1024 * 1024, // 2 MB — drafts are small text
		Discard:    nats.DiscardOld,
	},
	"EMAIL_SENT": {
		Name:      "EMAIL_SENT",
		Subjects:  []string{SubjectEmailSent},
		Retention: nats.LimitsPolicy,
		MaxAge:   7 * 24 * time.Hour,
	},
	"INTELLIGENCE_COMPRESS": {
		Name:      "INTELLIGENCE_COMPRESS",
		Subjects:  []string{SubjectIntelligenceCompress},
		Retention: nats.WorkQueuePolicy,
		MaxMsgSize: 8 * 1024 * 1024,
	},
	// ORPHANED: ExtractCompleted — published by classification/extract. No consumer.
	// Retain for audit/analytics integration (future track).
	"EXTRACT_COMPLETED": {
		Name:     "EXTRACT_COMPLETED",
		Subjects: []string{SubjectExtractCompleted},
		Retention: nats.LimitsPolicy,
		MaxAge:   7 * 24 * time.Hour,
	},
	// ORPHANED: AutoHandled — published by classification/auto. No consumer.
	// Retain for audit/analytics integration (future track).
	"AUTO_HANDLED": {
		Name:     "AUTO_HANDLED",
		Subjects: []string{SubjectAutoHandled},
		Retention: nats.LimitsPolicy,
		MaxAge:   7 * 24 * time.Hour,
	},
	// ORPHANED: SYNC_NOTIFY_CARD_CREATED — published by classification/staging
	// and ingestion/oauth. Sync consumer listens on "intelligence.card.created"
	// (different subject). Align subjects when wiring integration.
	"SYNC_NOTIFY_CARD_CREATED": {
		Name:     "SYNC_NOTIFY_CARD_CREATED",
		Subjects: []string{SubjectCardCreated},
		Retention: nats.LimitsPolicy,
		MaxAge:   7 * 24 * time.Hour,
	},
	// ORPHANED: EMAIL_CLASSIFIED — published by classification/router after
	// routing decisions. No consumer yet. Will be consumed by intelligence-layer
	// when wiring classification → intelligence pipeline.
	"EMAIL_CLASSIFIED": {
		Name:     "EMAIL_CLASSIFIED",
		Subjects: []string{SubjectEmailClassified},
		Retention: nats.LimitsPolicy,
		MaxAge:   7 * 24 * time.Hour,
	},
}

// JetStreamPublisher implements Publisher using NATS JetStream.
type JetStreamPublisher struct {
	nc      *nats.Conn
	js      nats.JetStreamContext
	streams map[string]nats.JetStream
}

// NewJetStreamPublisher connects to NATS and creates/ensures all streams.
func NewJetStreamPublisher(natsURL string) (*JetStreamPublisher, error) {
	nc, err := nats.Connect(natsURL,
		nats.Timeout(10*time.Second),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(10),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, err
	}

	js, err := nc.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		nc.Close()
		return nil, err
	}

	// Create/ensure all streams (idempotent)
	for _, cfg := range StreamConfigs {
		_, err := js.AddStream(&cfg)
		if err != nil && err != nats.ErrStreamNameAlreadyInUse {
			nc.Close()
			return nil, err
		}
	}

	return &JetStreamPublisher{
		nc: nc,
		js: js,
	}, nil
}

// PublishEmailIngested publishes an email.ingested event.
func (p *JetStreamPublisher) PublishEmailIngested(ctx context.Context, event EmailIngestedEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = p.js.Publish(SubjectEmailIngested, data)
	return err
}

// HealthCheck verifies NATS connection and stream health.
func (p *JetStreamPublisher) HealthCheck() error {
	if !p.nc.IsConnected() {
		return nats.ErrDisconnected
	}
	// Verify all streams exist
	for name := range StreamConfigs {
		_, err := p.js.StreamInfo(name)
		if err != nil {
			return err
		}
	}
	return nil
}

// Close closes the NATS connection.
func (p *JetStreamPublisher) Close() error {
	p.nc.Close()
	return nil
}

// JetStream returns the underlying JetStream context for consumers that need
// to create their own subscriptions.
func (p *JetStreamPublisher) JetStream() nats.JetStreamContext {
	return p.js
}
