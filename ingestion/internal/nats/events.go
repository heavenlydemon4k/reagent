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
	SubjectIntelligenceCompress = "intelligence.compress"
	SubjectExtractCompleted     = "ExtractCompleted"
	SubjectAutoHandled          = "AutoHandled"
	SubjectCardCreated          = "sync.notify.CardCreated"
)

// StreamConfig defines the JetStream stream configurations.
var StreamConfigs = map[string]nats.StreamConfig{
	"EMAIL_INGESTED": {
		Name:      "EMAIL_INGESTED",
		Subjects:  []string{SubjectEmailIngested},
		Retention: nats.WorkQueuePolicy,
		MaxMsgSize: 8 * 1024 * 1024, // 8MB
		MaxDeliver: 5,
		Discard:    nats.DiscardOld,
	},
	"EMAIL_INGESTED_DLQ": {
		Name:     "EMAIL_INGESTED_DLQ",
		Subjects: []string{SubjectEmailIngestedDLQ},
		Retention: nats.LimitsPolicy,
		MaxAge:   30 * 24 * time.Hour,
	},
	"INTELLIGENCE_COMPRESS": {
		Name:      "INTELLIGENCE_COMPRESS",
		Subjects:  []string{SubjectIntelligenceCompress},
		Retention: nats.WorkQueuePolicy,
		MaxMsgSize: 8 * 1024 * 1024,
	},
	"EXTRACT_COMPLETED": {
		Name:     "EXTRACT_COMPLETED",
		Subjects: []string{SubjectExtractCompleted},
		Retention: nats.LimitsPolicy,
		MaxAge:   7 * 24 * time.Hour,
	},
	"AUTO_HANDLED": {
		Name:     "AUTO_HANDLED",
		Subjects: []string{SubjectAutoHandled},
		Retention: nats.LimitsPolicy,
		MaxAge:   7 * 24 * time.Hour,
	},
	"SYNC_NOTIFY_CARD_CREATED": {
		Name:     "SYNC_NOTIFY_CARD_CREATED",
		Subjects: []string{SubjectCardCreated},
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
