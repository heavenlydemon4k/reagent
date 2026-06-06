// Package router implements the tri-state routing pipeline and its observability.
package router

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for the classification router.
type Metrics struct {
	// classification_total counts routed emails by route type.
	ClassificationTotal *prometheus.CounterVec
	// ClassificationDuration records end-to-end pipeline latency.
	ClassificationDuration prometheus.Histogram
	// StagingRulesPending tracks rules awaiting 48h activation.
	StagingRulesPending prometheus.Gauge
	// AutoHandleActionsTotal counts actions executed by auto-handle rules.
	AutoHandleActionsTotal *prometheus.CounterVec
}

// NewMetrics creates and registers a Metrics instance.
// Pass a non-nil register in tests to use a local registry.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	return &Metrics{
		ClassificationTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "classification_total",
			Help: "Total number of emails classified by route type.",
		}, []string{"route"}),
		ClassificationDuration: factory.NewHistogram(prometheus.HistogramOpts{
			Name:    "classification_duration_seconds",
			Help:    "End-to-end classification pipeline latency in seconds.",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms ~ 16s
		}),
		StagingRulesPending: factory.NewGauge(prometheus.GaugeOpts{
			Name: "staging_rules_pending_total",
			Help: "Number of auto-handle rules currently in the 48-hour staging window.",
		}),
		AutoHandleActionsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "auto_handle_actions_total",
			Help: "Total number of actions executed by auto-handle rules.",
		}, []string{"action_type"}),
	}
}

// RecordClassification increments the classification counter for the given route.
func (m *Metrics) RecordClassification(route string) {
	m.ClassificationTotal.WithLabelValues(route).Inc()
}

// ObserveClassification records the pipeline duration.
func (m *Metrics) ObserveClassification(seconds float64) {
	m.ClassificationDuration.Observe(seconds)
}

// SetStagingPending updates the gauge of rules in staging.
func (m *Metrics) SetStagingPending(count float64) {
	m.StagingRulesPending.Set(count)
}

// RecordAutoHandleAction increments the action counter for the given action type.
func (m *Metrics) RecordAutoHandleAction(actionType string) {
	m.AutoHandleActionsTotal.WithLabelValues(actionType).Inc()
}
