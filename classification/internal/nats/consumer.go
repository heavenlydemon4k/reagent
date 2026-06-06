// Package nats provides JetStream consumer and publisher for the Classification Core.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/classification/internal/config"
	"github.com/decisionstack/classification/internal/logger"
	"github.com/decisionstack/classification/internal/models"
	"github.com/nats-io/nats.go"
)

// Consumer wraps a durable JetStream pull consumer for email.ingested.
type Consumer struct {
	conn           *nats.Conn
	js             nats.JetStreamContext
	consumerName   string
	streamName     string
	subject        string
	batchSize      int
	fetchTimeout   time.Duration
	maxDeliver     int
	dlqSubject     string
	log            *logger.Logger
	classifier     Classifier
	publisher      *Publisher

	// retryConfig controls internal retry behaviour for classification
	// and publish operations within a single message handler.
	retryConfig RetryConfig
}

// RetryConfig controls how many times the consumer retries classification
// and publishing for a single NATS message before giving up and NAKing.
type RetryConfig struct {
	// MaxAttempts is the maximum number of times we attempt classification
	// + publish for a single message within the handler. Each attempt may
	// itself be retried by the Publisher's circuit-breaker + backoff logic.
	// Default: 3.
	MaxAttempts int

	// Backoff is the delay between retry attempts in the message handler.
	// Exponential backoff is applied: Backoff, 2*Backoff, 4*Backoff, ...
	// Default: 1s.
	Backoff time.Duration
}

// Classifier is the abstraction for email classification logic.
type Classifier interface {
	Classify(ctx context.Context, event models.EmailIngestedEvent) (*models.ClassificationResult, error)
}

// NewConsumer creates a durable pull consumer on the "email.ingested" stream.
func NewConsumer(cfg *config.Config, log *logger.Logger, classifier Classifier, publisher *Publisher) (*Consumer, error) {
	log = log.WithComponent("nats-consumer")

	nc, err := nats.Connect(cfg.NATSURL,
		nats.Name(cfg.NATSConsumerName),
		nats.ReconnectWait(2*time.Second),
		nats.MaxReconnects(10),
		nats.DisconnectErrHandler(func(c *nats.Conn, err error) {
			log.Warn("nats disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Info("nats reconnected", "url", c.ConnectedUrl())
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream context: %w", err)
	}

	return NewConsumerFromConn(nc, js, cfg, log, classifier, publisher)
}

// NewConsumerFromConn creates a Consumer from an existing NATS connection and JetStream context.
// Use this when you need to share the connection with other components (e.g., Publisher).
func NewConsumerFromConn(nc *nats.Conn, js nats.JetStreamContext, cfg *config.Config, log *logger.Logger, classifier Classifier, publisher *Publisher) (*Consumer, error) {
	log = log.WithComponent("nats-consumer")

	// Ensure stream exists
	if _, err := js.StreamInfo(cfg.NATSStream); err != nil {
		log.Info("creating jetstream stream", "stream", cfg.NATSStream)
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     cfg.NATSStream,
			Subjects: []string{cfg.NATSSubjectEmail, cfg.NATSSubjectDLQ + ">"},
			Retention: nats.WorkQueuePolicy,
			Discard:   nats.DiscardOld,
			MaxMsgs:   -1,
			MaxBytes:  -1,
			MaxAge:    7 * 24 * time.Hour,
			Storage:   nats.FileStorage,
			Replicas:  3,
		})
		if err != nil {
			return nil, fmt.Errorf("create stream: %w", err)
		}
	}

	c := &Consumer{
		conn:         nc,
		js:           js,
		consumerName: cfg.NATSConsumerName,
		streamName:   cfg.NATSStream,
		subject:      cfg.NATSSubjectEmail,
		batchSize:    cfg.NATSBatchSize,
		fetchTimeout: cfg.NATSFetchTimeout,
		maxDeliver:   cfg.NATSMaxDeliver,
		dlqSubject:   cfg.NATSSubjectDLQ,
		log:          log,
		classifier:   classifier,
		publisher:    publisher,
		retryConfig: RetryConfig{
			MaxAttempts: cfg.NATSConsumerMaxRetries,
			Backoff:     cfg.NATSConsumerRetryBackoff,
		},
	}

	if err := c.createOrUpdateConsumer(); err != nil {
		return nil, fmt.Errorf("create consumer: %w", err)
	}

	return c, nil
}

// createOrUpdateConsumer idempotently creates the durable consumer.
func (c *Consumer) createOrUpdateConsumer() error {
	cfg := &nats.ConsumerConfig{
		Durable:       c.consumerName,
		Description:   "Classification Core worker for email.ingested",
		DeliverPolicy: nats.DeliverAllPolicy,
		AckPolicy:     nats.AckExplicitPolicy,
		MaxDeliver:    c.maxDeliver,
		MaxAckPending: c.batchSize,
		ReplayPolicy:  nats.ReplayInstantPolicy,
		AckWait:       30 * time.Second,
		// Dead-letter policy: after max deliveries, send to DLQ
		SampleFrequency: "100%",
		FilterSubject:   c.subject,
	}

	// Check if consumer exists
	if _, err := c.js.ConsumerInfo(c.streamName, c.consumerName); err != nil {
		c.log.Info("creating durable consumer", "consumer", c.consumerName, "max_deliver", c.maxDeliver)
		_, err = c.js.AddConsumer(c.streamName, cfg)
		if err != nil {
			return err
		}
	} else {
		c.log.Info("updating durable consumer", "consumer", c.consumerName)
		_, err = c.js.UpdateConsumer(c.streamName, cfg)
		if err != nil {
			return err
		}
	}

	return nil
}

// Subscribe starts the pull subscription loop. Blocks until context is cancelled.
func (c *Consumer) Subscribe(ctx context.Context) error {
	sub, err := c.js.PullSubscribe(c.subject, c.consumerName)
	if err != nil {
		return fmt.Errorf("pull subscribe: %w", err)
	}
	defer sub.Unsubscribe()

	c.log.Info("consumer started", "consumer", c.consumerName, "subject", c.subject, "batch_size", c.batchSize)

	for {
		select {
		case <-ctx.Done():
			c.log.Info("consumer shutting down")
			return nil
		default:
		}

		msgs, err := sub.Fetch(c.batchSize, nats.Context(ctx))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if err == nats.ErrTimeout {
				continue
			}
			c.log.Error("fetch error", "error", err)
			time.Sleep(1 * time.Second)
			continue
		}

		for _, msg := range msgs {
			if err := c.processMessage(ctx, msg); err != nil {
				c.log.Error("message processing failed", "error", err, "subject", msg.Subject)
				// Message will be redelivered up to maxDeliver; after that NATS discards per stream policy.
				// We explicitly send to DLQ after maxDeliver attempts.
				if md, err := msg.Metadata(); err == nil && md.NumDelivered >= uint64(c.maxDeliver) {
					c.sendToDLQ(ctx, msg)
				}
			}
		}
	}
}

func (c *Consumer) processMessage(ctx context.Context, msg *nats.Msg) error {
	defer logger.FromContext(ctx).Timer("process_message")()

	var event models.EmailIngestedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		// Non-retryable — ack to avoid redelivery
		msg.Ack()
		return fmt.Errorf("unmarshal event: %w", err)
	}

	log := c.log.With("event_id", event.EventID, "user_id", event.UserID, "raw_email_id", event.RawEmailID)
	ctx = logger.WithContext(ctx, log)

	// -------------------------------------------------------------------------
	// Retry loop: classify + publish with exponential backoff.
	// The Publisher already has its own circuit-breaker + retry logic,
	// but this outer loop catches transient classifier failures and
	// publisher circuit-breaker opens that may recover on retry.
	// -------------------------------------------------------------------------
	var result *models.ClassificationResult
	var err error
	var attempt int

	for attempt = 0; attempt < c.retryConfig.MaxAttempts; attempt++ {
		if attempt > 0 {
			backoff := c.retryConfig.Backoff * time.Duration(1<<uint(attempt-1))
			log.Warn("retrying classification after failure",
				"attempt", attempt+1,
				"max_attempts", c.retryConfig.MaxAttempts,
				"backoff", backoff,
			)
			select {
			case <-time.After(backoff):
				// continue
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
			}
		}

		result, err = c.classifier.Classify(ctx, event)
		if err != nil {
			log.Warn("classification attempt failed", "attempt", attempt+1, "error", err)
			continue // retry
		}

		// Publish classification result to downstream.
		if c.publisher != nil {
			if err = c.publisher.PublishResult(ctx, result); err != nil {
				log.Warn("publish attempt failed", "attempt", attempt+1,
					"circuit_state", c.publisher.CircuitBreakerState(),
					"error", err)
				continue // retry — circuit breaker may recover or backoff may help
			}
		}

		// Success — break out of retry loop.
		break
	}

	if err != nil {
		// All retries exhausted — nak with delay so NATS redelivers.
		msg.Nak(nats.NakDelay(5 * time.Second))
		return fmt.Errorf("classify/publish failed after %d attempts: %w", c.retryConfig.MaxAttempts, err)
	}

	// attempt equals the number of retries consumed (0 = first attempt succeeded).
	log.Info("classification complete",
		"route", result.Route,
		"confidence", result.Confidence,
		"matched_rule_id", result.MatchedRuleID,
		"retry_attempts", attempt,
	)

	return msg.Ack()
}

func (c *Consumer) sendToDLQ(ctx context.Context, msg *nats.Msg) {
	log := c.log.With("dlq_subject", c.dlqSubject)
	// Forward original message to DLQ
	if err := c.js.Publish(c.dlqSubject, msg.Data); err != nil {
		log.Error("dlq publish failed", "error", err)
		return
	}
	msg.Ack()
	log.Warn("message moved to DLQ after max deliveries")
}

// Close cleanly shuts down the NATS connection.
func (c *Consumer) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

// Conn exposes the underlying nats.Conn for health checks.
func (c *Consumer) Conn() *nats.Conn {
	return c.conn
}
