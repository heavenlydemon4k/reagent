// Package circuitbreaker provides circuit breaker pattern for external service calls.
//
// The circuit breaker prevents cascading failures by stopping calls to failing
// services. It has three states:
//
//   - Closed:   Normal operation, calls pass through.
//   - Open:     Calls fail fast without reaching the service.
//   - HalfOpen: A trial call is allowed to test if the service recovered.
//
// Usage:
//
//	cb := circuitbreaker.New("deepgram", 5, 30*time.Second)
//	err := cb.Call(func() error {
//	    return callExternalAPI()
//	})
package circuitbreaker

import (
	"fmt"
	"sync"
	"time"
)

//go:generate go test ./...

// State represents the current state of the circuit breaker.
type State int

const (
	// Closed means normal operation — calls pass through.
	Closed State = iota
	// Open means calls fail fast without reaching the service.
	Open
	// HalfOpen means a trial call is allowed to test recovery.
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// CircuitBreaker
// ---------------------------------------------------------------------------

// CircuitBreaker protects external service calls from cascading failures.
type CircuitBreaker struct {
	name        string
	mu          sync.RWMutex
	state       State
	failures    int
	threshold   int
	timeout     time.Duration
	lastFailure time.Time

	halfOpenMaxCalls int // max calls allowed in half-open state before deciding
	halfOpenCalls    int // current number of trial calls in half-open state
}

// ---------------------------------------------------------------------------
// Configuration presets for known integrations
// ---------------------------------------------------------------------------

// Config holds circuit breaker configuration for an integration.
type Config struct {
	FailureThreshold   int
	ResetTimeout       time.Duration
	HalfOpenMaxCalls   int
}

// Presets provides recommended configurations for each external integration.
var Presets = map[string]Config{
	"deepgram":    {FailureThreshold: 5, ResetTimeout: 30 * time.Second, HalfOpenMaxCalls: 3},
	"elevenlabs":  {FailureThreshold: 5, ResetTimeout: 30 * time.Second, HalfOpenMaxCalls: 3},
	"intelligence": {FailureThreshold: 5, ResetTimeout: 30 * time.Second, HalfOpenMaxCalls: 3},
	"calendar":    {FailureThreshold: 5, ResetTimeout: 30 * time.Second, HalfOpenMaxCalls: 3},
	"nats":        {FailureThreshold: 5, ResetTimeout: 30 * time.Second, HalfOpenMaxCalls: 3},
}

// ---------------------------------------------------------------------------
// Construction
// ---------------------------------------------------------------------------

// New creates a CircuitBreaker with the given name, failure threshold, and
// reset timeout. Use Presets for recommended defaults per integration.
func New(name string, threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		name:             name,
		threshold:        threshold,
		timeout:          timeout,
		state:            Closed,
		halfOpenMaxCalls: 3,
	}
}

// NewWithPreset creates a CircuitBreaker using a named preset configuration.
func NewWithPreset(name string) *CircuitBreaker {
	preset, ok := Presets[name]
	if !ok {
		preset = Config{FailureThreshold: 5, ResetTimeout: 30 * time.Second, HalfOpenMaxCalls: 3}
	}
	cb := New(name, preset.FailureThreshold, preset.ResetTimeout)
	cb.halfOpenMaxCalls = preset.HalfOpenMaxCalls
	return cb
}

// ---------------------------------------------------------------------------
// Core API
// ---------------------------------------------------------------------------

// Call executes fn if the circuit is closed (or half-open).
// Returns a circuit-open error if the breaker is open.
func (cb *CircuitBreaker) Call(fn func() error) error {
	if cb.IsOpen() {
		return fmt.Errorf("circuit breaker %q is %s (last failure: %s ago)",
			cb.name, cb.State(), time.Since(cb.lastFailure).Round(time.Second))
	}

	err := fn()
	if err != nil {
		cb.recordFailure()
		return err
	}
	cb.recordSuccess()
	return nil
}

// ---------------------------------------------------------------------------
// State queries
// ---------------------------------------------------------------------------

// State returns the current state of the circuit breaker (safe for concurrent use).
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.currentState()
}

// IsOpen returns true if the circuit breaker is currently open.
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.currentState() == Open
}

// Name returns the circuit breaker name.
func (cb *CircuitBreaker) Name() string { return cb.name }

// ---------------------------------------------------------------------------
// Internal state management
// ---------------------------------------------------------------------------

func (cb *CircuitBreaker) currentState() State {
	if cb.state == Open && time.Since(cb.lastFailure) > cb.timeout {
		cb.state = HalfOpen
		cb.halfOpenCalls = 0
	}
	return cb.state
}

func (cb *CircuitBreaker) recordFailure() {
	cb.failures++
	cb.lastFailure = time.Now()
	if cb.failures >= cb.threshold {
		cb.state = Open
	}
}

func (cb *CircuitBreaker) recordSuccess() {
	cb.failures = 0
	cb.state = Closed
	cb.halfOpenCalls = 0
}

// ---------------------------------------------------------------------------
// Registry — holds named circuit breakers for the application
// ---------------------------------------------------------------------------

// Registry is a collection of named circuit breakers.
type Registry struct {
	mu        sync.RWMutex
	breakers  map[string]*CircuitBreaker
}

// NewRegistry creates an empty circuit breaker registry.
func NewRegistry() *Registry {
	return &Registry{breakers: make(map[string]*CircuitBreaker)}
}

// GetOrCreate returns an existing breaker or creates a new one with the given config.
func (r *Registry) GetOrCreate(name string, threshold int, timeout time.Duration) *CircuitBreaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cb, ok := r.breakers[name]; ok {
		return cb
	}
	cb := New(name, threshold, timeout)
	r.breakers[name] = cb
	return cb
}

// Get returns the named breaker or nil.
func (r *Registry) Get(name string) *CircuitBreaker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.breakers[name]
}

// Names returns all registered breaker names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.breakers))
	for n := range r.breakers {
		names = append(names, n)
	}
	return names
}
