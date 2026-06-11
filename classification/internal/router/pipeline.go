package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/decisionstack/classification/internal/models"
)

const (
	// NATS subjects
	SubjectEmailIngested = "email.ingested"
	SubjectClassified    = "email.classified"
	SubjectDLQ           = "email.classified.dlq"
	SubjectSyncNotify    = "sync.notify.CardCreated"

	// Consumer configuration
	consumerName   = "classification-router"
	maxDeliveries  = 5
	batchSize      = 64
	ackWaitSeconds = 30
)

// ---------------------------------------------------------------------------
// NATS abstractions (minimal interfaces to avoid tight coupling)
// ---------------------------------------------------------------------------

// JetStreamContext wraps the NATS JetStream context methods we need.
type JetStreamContext interface {
	Subscribe(subj string, cb nats.MsgHandler, opts ...nats.SubOpt) (*nats.Subscription, error)
	Publish(subj string, data []byte) (*nats.PubAck, error)
	PublishMsg(msg *nats.Msg) (*nats.PubAck, error)
}

// Publisher is the subset of NATS JetStream needed for publishing.
type Publisher interface {
	Publish(subj string, data []byte) error
}

// ---------------------------------------------------------------------------
// Pipeline
// ---------------------------------------------------------------------------

// Pipeline consumes email.ingested events, routes them, and publishes results.
type Pipeline struct {
	router    *Router
	consumer  JetStreamContext
	publisher Publisher
	log       *slog.Logger
	metrics   *Metrics

	mu       sync.RWMutex
	running  bool
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewPipeline creates a Pipeline. All dependencies are required.
func NewPipeline(router *Router, consumer JetStreamContext, publisher Publisher, log *slog.Logger, metrics *Metrics) *Pipeline {
	return &Pipeline{
		router:    router,
		consumer:  consumer,
		publisher: publisher,
		log:       log.With("component", "pipeline"),
		metrics:   metrics,
		stopCh:    make(chan struct{}),
	}
}

// Start begins consuming from NATS and processing emails.
// Blocks until the context is cancelled or Stop() is called.
func (p *Pipeline) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return fmt.Errorf("pipeline already running")
	}
	p.running = true
	p.mu.Unlock()

	p.log.Info("starting pipeline", "subject", SubjectEmailIngested)

	// Create a durable JetStream consumer subscription.
	sub, err := p.consumer.Subscribe(SubjectEmailIngested, p.handleMessage,
		nats.Durable(consumerName),
		nats.ManualAck(),
		nats.MaxDeliver(maxDeliveries),
		nats.AckWait(ackWaitSeconds*time.Second),
		nats.MaxAckPending(batchSize),
	)
	if err != nil {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		return fmt.Errorf("create consumer: %w", err)
	}
	defer sub.Unsubscribe()

	p.log.Info("consumer created", "durable", consumerName, "max_deliver", maxDeliveries)

	// Wait for shutdown signal.
	select {
	case <-ctx.Done():
		p.log.Info("shutdown signal received, stopping pipeline")
	case <-p.stopCh:
		p.log.Info("stop called, shutting down pipeline")
	}

	// Graceful shutdown: wait for in-flight messages to complete.
	p.log.Info("waiting for in-flight messages to complete")
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.log.Info("all in-flight messages completed")
	case <-time.After(30 * time.Second):
		p.log.Warn("shutdown timeout: some messages may be in-flight")
	}

	p.mu.Lock()
	p.running = false
	p.mu.Unlock()

	return nil
}

// Stop signals the pipeline to shut down gracefully.
func (p *Pipeline) Stop() {
	p.mu.RLock()
	if !p.running {
		p.mu.RUnlock()
		return
	}
	p.mu.RUnlock()

	close(p.stopCh)
}

// IsRunning reports whether the pipeline is currently active.
func (p *Pipeline) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// ---------------------------------------------------------------------------
// Message handler
// ---------------------------------------------------------------------------

// handleMessage is the NATS message callback.
func (p *Pipeline) handleMessage(msg *nats.Msg) {
	p.wg.Add(1)
	defer p.wg.Done()

	logger := p.log.With("nats_subject", msg.Subject)
	if md, err := msg.Metadata(); err == nil {
		logger = logger.With("nats_seq", md.Sequence.Stream)
	}

	// Parse the ingested event.
	var event models.EmailIngestedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		logger.Error("failed to unmarshal email.ingested event", "error", err)
		p.nak(msg) // negative ack — will be redelivered
		return
	}

	logger = logger.With("event_id", event.EventID, "raw_email_id", event.RawEmailID)

	// -----------------------------------------------------------------------
	// Route the email.
	// -----------------------------------------------------------------------
	result, err := p.router.Route(context.Background(), &event)
	if err != nil {
		logger.Error("routing failed", "error", err)
		p.nak(msg)
		return
	}

	// Validate result invariants.
	if err := ValidateResult(result); err != nil {
		logger.Error("invalid classification result", "error", err)
		p.nak(msg)
		return
	}

	// -----------------------------------------------------------------------
	// Publish the classification result.
	// -----------------------------------------------------------------------
	resultJSON, err := json.Marshal(result)
	if err != nil {
		logger.Error("failed to marshal classification result", "error", err)
		p.nak(msg)
		return
	}

	if err := p.publisher.Publish(SubjectClassified, resultJSON); err != nil {
		logger.Error("failed to publish classification result", "error", err)
		p.nak(msg)
		return
	}

	logger.Info("classification published",
		"route", result.Route,
		"confidence", result.Confidence,
		"matched_rule_id", result.MatchedRuleID,
	)

	// -----------------------------------------------------------------------
	// Acknowledge NATS message ONLY after successful classification + publish.
	// -----------------------------------------------------------------------
	if err := msg.Ack(); err != nil {
		logger.Error("failed to ack message after publish", "error", err)
		// Message will be redelivered; downstream must be idempotent.
		return
	}
}

// ---------------------------------------------------------------------------
// NATS helpers
// ---------------------------------------------------------------------------

// nak sends a negative acknowledgment, allowing redelivery.
// After MaxDeliver redeliveries, NATS will auto-DLQ the message.
func (p *Pipeline) nak(msg *nats.Msg) {
	if err := msg.Nak(); err != nil {
		p.log.Warn("failed to nak message", "error", err)
	}
}

// ---------------------------------------------------------------------------
// DLQ helper (manual fallback if NATS MaxDeliver is not configured)
// ---------------------------------------------------------------------------

// sendToDLQ publishes a failed message to the DLQ subject for manual inspection.
func (p *Pipeline) sendToDLQ(msg *nats.Msg, reason string) error {
	dlqEvent := struct {
		OriginalSubject string    `json:"original_subject"`
		OriginalData    []byte    `json:"original_data"`
		Reason          string    `json:"reason"`
		FailedAt        time.Time `json:"failed_at"`
		DeliveryCount   int       `json:"delivery_count"`
	}{
		OriginalSubject: msg.Subject,
		OriginalData:    msg.Data,
		Reason:          reason,
		FailedAt:        time.Now().UTC(),
		DeliveryCount:   0, // populated from metadata if available
	}

	data, err := json.Marshal(dlqEvent)
	if err != nil {
		return fmt.Errorf("marshal dlq event: %w", err)
	}

	if err := p.publisher.Publish(SubjectDLQ, data); err != nil {
		return fmt.Errorf("publish to dlq: %w", err)
	}

	p.log.Warn("message sent to dlq", "reason", reason, "subject", msg.Subject)
	return nil
}

// ---------------------------------------------------------------------------
// Health check
// ---------------------------------------------------------------------------

// Health returns the current pipeline health status.
func (p *Pipeline) Health() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return map[string]interface{}{
		"running":       p.running,
		"consumer_name": consumerName,
		"max_deliver":   maxDeliveries,
		"batch_size":    batchSize,
		"subject_in":    SubjectEmailIngested,
		"subject_out":   SubjectClassified,
		"subject_dlq":   SubjectDLQ,
	}
}

// ---------------------------------------------------------------------------
// Event builder helper
// ---------------------------------------------------------------------------

// BuildClassificationEvent is a convenience wrapper for external callers
// that need to classify an email synchronously.
func (p *Pipeline) BuildClassificationEvent(ctx context.Context, event *models.EmailIngestedEvent) (*models.ClassificationResult, error) {
	return p.router.Route(ctx, event)
}


