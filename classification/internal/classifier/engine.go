// Package classifier implements the classification pipeline.
package classifier

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/classification/internal/config"
	"github.com/decisionstack/classification/internal/logger"
	"github.com/decisionstack/classification/internal/models"
	"github.com/decisionstack/classification/internal/rules"
	"github.com/google/uuid"
)

// Engine implements the Classifier interface for the NATS consumer.
type Engine struct {
	store            *rules.Store
	cfg              *config.Config
	log              *logger.Logger
	llmClient        LLMClient
	confidenceFloor  float64
}

// LLMClient abstracts the LLM pattern matching call.
type LLMClient interface {
	PatternMatch(ctx context.Context, req models.LLMPatternMatchRequest) (*models.LLMPatternMatchResponse, error)
}

// NewEngine creates a classification engine.
func NewEngine(store *rules.Store, cfg *config.Config, log *logger.Logger, llm LLMClient) *Engine {
	if llm == nil {
		log.Warn("LLM client not configured, running in rule-only mode")
	}
	return &Engine{
		store:           store,
		cfg:             cfg,
		log:             log.WithComponent("classifier"),
		llmClient:       llm,
		confidenceFloor: cfg.ConfidenceFloor,
	}
}

// Classify runs the Extract → Auto → Decision pipeline.
// Invariant: every email returns a ClassificationResult — no unprocessed emails.
func (e *Engine) Classify(ctx context.Context, event models.EmailIngestedEvent) (*models.ClassificationResult, error) {
	log := e.log.With("raw_email_id", event.RawEmailID, "user_id", event.UserID)
	defer log.Timer("classify")()

	result := &models.ClassificationResult{
		RawEmailID:  event.RawEmailID,
		UserID:      event.UserID,
		ThreadID:    event.ThreadID,
		ProcessedAt: time.Now().UTC(),
		// Conservative default: RouteDecision
		Route:      models.RouteDecision,
		Confidence: 1.0,
	}

	// ─── Extract-Only detection (fast path) ──────────────────────────────────
	extracted := e.tryExtract(event)
	if extracted != nil {
		result.Route = models.RouteExtract
		result.ExtractedData = extracted
		result.Confidence = 1.0
		log.Info("extract-only match", "type", extracted.Type)
		return result, nil
	}

	// Build email attributes for rule matching
	attrs := e.buildAttributes(event)

	// ─── Auto-Handle: active rule matching ───────────────────────────────────
	ruleMatches, err := e.matchRules(ctx, event.UserID, attrs)
	if err != nil {
		log.Error("rule matching failed", "error", err)
		// Conservative: fall through to RouteDecision rather than fail
		return result, nil
	}

	if ruleMatches != nil {
		// Check confidence floor for Auto-Handle
		if ruleMatches.Confidence >= e.confidenceFloor {
			result.Route = models.RouteAuto
			result.Confidence = ruleMatches.Confidence
			result.MatchedRuleID = &ruleMatches.RuleID
			log.Info("auto-handle rule match", "rule_id", ruleMatches.RuleID, "confidence", ruleMatches.Confidence)
			return result, nil
		}
		log.Warn("rule match below confidence floor, continuing to decision",
			"rule_id", ruleMatches.RuleID,
			"confidence", ruleMatches.Confidence,
			"floor", e.confidenceFloor,
		)
	}

	// ─── LLM Fallback ────────────────────────────────────────────────────────
	if e.llmClient != nil && e.cfg.LLMEnabled {
		llmResult, err := e.tryLLM(ctx, event)
		if err != nil {
			log.Error("llm fallback failed", "error", err)
			// Conservative: RouteDecision with confidence from rule attempt
			return result, nil
		}
		if llmResult.Match != "none" && llmResult.Confidence >= e.confidenceFloor {
			result.Route = models.RouteAuto
			result.Confidence = llmResult.Confidence
			result.LLMMatched = true
			log.Info("llm auto-handle match", "match", llmResult.Match, "confidence", llmResult.Confidence)
			return result, nil
		}
		log.Debug("llm did not match or below floor", "match", llmResult.Match, "confidence", llmResult.Confidence)
	}

	// ─── Default: Decision Stack ─────────────────────────────────────────────
	result.Route = models.RouteDecision
	result.Confidence = 0.95 // Standard confidence for intentional routing
	log.Info("routed to decision stack")
	return result, nil
}

// matchResult holds a successful rule match.
type matchResult struct {
	RuleID     uuid.UUID
	Confidence float64
}

// matchRules evaluates all active rules for a user against email attributes.
func (e *Engine) matchRules(ctx context.Context, userID uuid.UUID, attrs models.EmailAttributes) (*matchResult, error) {
	activeRules, err := e.store.ListActive(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list active rules: %w", err)
	}

	for _, rule := range activeRules {
		matched, err := rule.Predicate.Evaluate(attrs)
		if err != nil {
			e.log.Warn("predicate evaluation error", "rule_id", rule.ID, "error", err)
			continue
		}
		if matched {
			// Use the rule's own confidence threshold, but respect the hard floor
			conf := rule.ConfidenceThreshold
			if conf < e.confidenceFloor {
				conf = e.confidenceFloor
			}
			return &matchResult{
				RuleID:     rule.ID,
				Confidence: conf,
			}, nil
		}
	}

	return nil, nil // No match
}

// tryExtract detects Extract-Only email types without LLM or rules.
func (e *Engine) tryExtract(event models.EmailIngestedEvent) *models.ExtractedDatum {
	// Placeholder: real implementation would scan subject/body for 2FA codes,
	// tracking numbers, calendar invites, receipts, etc.
	// Returns nil here to fall through to rule matching.
	return nil
}

// tryLLM calls the LLM for unstructured pattern matching.
func (e *Engine) tryLLM(ctx context.Context, event models.EmailIngestedEvent) (*models.LLMPatternMatchResponse, error) {
	req := models.LLMPatternMatchRequest{
		SenderEmail:      event.SenderEmail,
		BodyPreview:      "", // Would be populated from S3 fetch in production
		HasAttachment:    event.HasAttachments,
		ParticipantCount: 1, // Default
	}
	return e.llmClient.PatternMatch(ctx, req)
}

// buildAttributes constructs EmailAttributes from the ingested event.
func (e *Engine) buildAttributes(event models.EmailIngestedEvent) models.EmailAttributes {
	return models.EmailAttributes{
		SenderEmail:           event.SenderEmail,
		SenderDomain:          extractDomain(event.SenderEmail),
		HasAttachment:         event.HasAttachments,
		ThreadParticipantCount: 1, // Would be fetched from thread service
		TimeOfDay:             event.ReceivedAt.Hour(),
		DayOfWeek:             int(event.ReceivedAt.Weekday()),
	}
}

func extractDomain(email string) string {
	for i := len(email) - 1; i >= 0; i-- {
		if email[i] == '@' {
			return email[i+1:]
		}
	}
	return ""
}

// Ensure Engine implements the nats.Classifier interface.
var _ interface {
	Classify(ctx context.Context, event models.EmailIngestedEvent) (*models.ClassificationResult, error)
} = (*Engine)(nil)
