package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/decisionstack/classification/internal/logger"
	"github.com/decisionstack/classification/internal/models"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
)

// ---------------------------------------------------------------------------
// Retry and circuit-breaker constants
// ---------------------------------------------------------------------------

const (
	// defaultMaxRetries is the default number of publish retry attempts.
	defaultMaxRetries = 5

	// retryBaseDelayMs is the initial retry backoff in milliseconds.
	// Exponential backoff: 100ms, 200ms, 400ms, 800ms, 1.6s.
	retryBaseDelayMs = 100

	// retryMaxDelayMs caps the exponential backoff.
	retryMaxDelayMs = 5000 // 5 seconds

	// circuitBreakerThreshold is the number of consecutive failures before
	// the circuit breaker opens.
	circuitBreakerThreshold = 10

	// circuitBreakerTimeout is how long the circuit stays open before
	// transitioning to half-open.
	circuitBreakerTimeout = 30 * time.Second

	// circuitBreakerHalfOpenMaxCalls is the number of trial calls allowed
	// in half-open state before closing or re-opening.
	circuitBreakerHalfOpenMaxCalls = 3
)

// Publisher publishes classification results to downstream NATS subjects.
type Publisher struct {
	js                  nats.JetStreamContext
	subjectIntelligence string
	subjectExtracted    string
	subjectAuto         string
	log                 *logger.Logger
	cb                  *CircuitBreaker
	maxRetries          int
}

// ---------------------------------------------------------------------------
// Circuit Breaker
// ---------------------------------------------------------------------------

// CircuitBreaker implements a simple three-state circuit breaker (closed,
// open, half-open) to prevent cascading failures when NATS is unavailable.
type CircuitBreaker struct {
	failures      int
	trialCalls    int // successful calls in half-open state
	threshold     int
	timeout       time.Duration
	halfOpenMax   int
	lastFailure   time.Time
	state         string // "closed", "open", "half-open"
	mu            sync.RWMutex
}

// NewCircuitBreaker creates a new CircuitBreaker with the given settings.
func NewCircuitBreaker(threshold int, timeout time.Duration, halfOpenMax int) *CircuitBreaker {
	return &CircuitBreaker{
		threshold:   threshold,
		timeout:     timeout,
		halfOpenMax: halfOpenMax,
		state:       "closed",
	}
}

// Call executes fn if the circuit allows it. Returns circuit breaker error
// if the circuit is open, otherwise returns fn's result and updates state.
func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mu.Lock()

	// If open, check if timeout has elapsed -> transition to half-open.
	if cb.state == "open" {
		if time.Since(cb.lastFailure) < cb.timeout {
			cb.mu.Unlock()
			return fmt.Errorf("circuit breaker open (retry after %v)", cb.timeout-time.Since(cb.lastFailure))
		}
		// Timeout elapsed — transition to half-open.
		cb.state = "half-open"
		cb.trialCalls = 0
		cb.failures = 0
	}

	cb.mu.Unlock()

	// Execute the call.
	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.lastFailure = time.Now()

		switch cb.state {
		case "half-open":
			// Any failure in half-open immediately re-opens the circuit.
			cb.state = "open"
		case "closed":
			if cb.failures >= cb.threshold {
				cb.state = "open"
			}
		}
		return err
	}

	// Success path.
	switch cb.state {
	case "half-open":
		cb.trialCalls++
		if cb.trialCalls >= cb.halfOpenMax {
			// Enough successful trials — close the circuit.
			cb.state = "closed"
			cb.failures = 0
			cb.trialCalls = 0
		}
	case "closed":
		cb.failures = 0
	}
	return nil
}

// State returns the current circuit breaker state (for observability).
func (cb *CircuitBreaker) State() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Stats returns current failure count and state for metrics/health.
func (cb *CircuitBreaker) Stats() (state string, failures int) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state, cb.failures
}

// NewPublisher creates a publisher using an existing JetStream context
// with default retry and circuit-breaker settings.
func NewPublisher(js nats.JetStreamContext, subjectIntelligence, subjectExtracted, subjectAuto string, log *logger.Logger) *Publisher {
	return NewPublisherWithConfig(js, subjectIntelligence, subjectExtracted, subjectAuto, log, defaultMaxRetries,
		NewCircuitBreaker(circuitBreakerThreshold, circuitBreakerTimeout, circuitBreakerHalfOpenMaxCalls),
	)
}

// NewPublisherWithConfig creates a publisher with explicit retry and
// circuit-breaker configuration. Use this for testing or when you need
// non-default reliability settings.
func NewPublisherWithConfig(js nats.JetStreamContext, subjectIntelligence, subjectExtracted, subjectAuto string, log *logger.Logger, maxRetries int, cb *CircuitBreaker) *Publisher {
	if maxRetries < 1 {
		maxRetries = defaultMaxRetries
	}
	if cb == nil {
		cb = NewCircuitBreaker(circuitBreakerThreshold, circuitBreakerTimeout, circuitBreakerHalfOpenMaxCalls)
	}
	return &Publisher{
		js:                  js,
		subjectIntelligence: subjectIntelligence,
		subjectExtracted:    subjectExtracted,
		subjectAuto:         subjectAuto,
		log:                 log.WithComponent("nats-publisher"),
		cb:                  cb,
		maxRetries:          maxRetries,
	}
}

// PublishResult routes the classification result to exactly one downstream subject.
// It applies the circuit breaker and retries with exponential backoff.
// When routing to Intelligence (RouteDecision), it translates ClassificationResult
// into the IntelligenceCompressEvent schema that Intelligence expects.
func (p *Publisher) PublishResult(ctx context.Context, result *models.ClassificationResult) error {
	if result == nil {
		return fmt.Errorf("nil classification result")
	}

	var subject string
	switch result.Route {
	case models.RouteExtract:
		subject = p.subjectExtracted
	case models.RouteAuto:
		subject = p.subjectAuto
	case models.RouteDecision:
		subject = p.subjectIntelligence
	default:
		// Conservative default: send to Decision Stack
		p.log.Warn("unknown route, defaulting to decision", "route", result.Route)
		subject = p.subjectIntelligence
	}

	// -----------------------------------------------------------------------
	// Schema translation: ClassificationResult → downstream event type
	// -----------------------------------------------------------------------
	var payload interface{} = result
	if subject == p.subjectIntelligence {
		payload = buildIntelligenceCompressEvent(result)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	msg := &nats.Msg{
		Subject: subject,
		Data:    data,
		Header:  nats.Header{},
	}
	msg.Header.Set("Content-Type", "application/json")
	msg.Header.Set("X-Classification-Route", string(result.Route))
	msg.Header.Set("X-Raw-Email-ID", result.RawEmailID.String())

	// Publish through circuit breaker + retry.
	err = p.cb.Call(func() error {
		return p.publishWithRetry(msg)
	})
	if err != nil {
		// Distinguish circuit-breaker open from publish failure.
		if p.cb.State() == "open" {
			return fmt.Errorf("circuit breaker open for %s: %w", subject, err)
		}
		return fmt.Errorf("publish to %s failed after %d retries: %w", subject, p.maxRetries, err)
	}

	p.log.Debug("published classification result",
		"subject", subject,
		"route", result.Route,
		"circuit_state", p.cb.State(),
	)

	return nil
}

// publishWithRetry attempts to publish a NATS message with exponential
// backoff retries. It retries up to p.maxRetries times with delays of
// 100ms, 200ms, 400ms, 800ms, 1.6s (capped at 5s).
func (p *Publisher) publishWithRetry(msg *nats.Msg) error {
	var lastErr error

	for i := 0; i < p.maxRetries; i++ {
		if i > 0 {
			// Exponential backoff: 100ms * 2^(i-1).
			delayMs := retryBaseDelayMs * (1 << uint(i-1))
			if delayMs > retryMaxDelayMs {
				delayMs = retryMaxDelayMs
			}
			select {
			case <-time.After(time.Duration(delayMs) * time.Millisecond):
				// continue to retry
			}
		}

		_, lastErr = p.js.PublishMsg(msg)
		if lastErr == nil {
			return nil
		}

		p.log.Warn("publish attempt failed, retrying",
			"attempt", i+1,
			"max_retries", p.maxRetries,
			"subject", msg.Subject,
			"error", lastErr,
		)
	}

	return fmt.Errorf("publish failed after %d retries: %w", p.maxRetries, lastErr)
}

// CircuitBreakerState exposes the circuit breaker state for health checks.
func (p *Publisher) CircuitBreakerState() string {
	if p.cb == nil {
		return "disabled"
	}
	return p.cb.State()
}

// buildIntelligenceCompressEvent translates a ClassificationResult into the
// IntelligenceCompressEvent schema expected by the Intelligence Layer.
func buildIntelligenceCompressEvent(r *models.ClassificationResult) *models.IntelligenceCompressEvent {
	// Priority score: invert confidence so that low-confidence classifications
	// (more uncertain) get higher priority for intelligence review.
	priorityScore := 1.0 - r.Confidence
	if priorityScore < 0.1 {
		priorityScore = 0.1 // floor at 0.1 so everything gets some attention
	}

	return &models.IntelligenceCompressEvent{
		EventID:       uuid.New(),
		UserID:        r.UserID,
		ThreadID:      r.ThreadID,
		RawEmailIDs:   []uuid.UUID{r.RawEmailID},
		PriorityScore: priorityScore,
		Source:        "classification",
	}
}
