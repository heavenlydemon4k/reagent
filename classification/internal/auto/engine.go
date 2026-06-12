package auto

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/decisionstack/classification/internal/models"

	"github.com/google/uuid"
)

// ingestionConn is the minimal interface for the ingestion mesh connection.
// *grpc.ClientConn satisfies this interface automatically.
type ingestionConn interface{}

const (
	// hardConfidenceFloor is the minimum confidence for any auto-handle decision.
	hardConfidenceFloor = 0.92

	// stagingWindow is the 48-hour window before a staged rule becomes active.
	stagingWindow = 48 * time.Hour
)

// Engine is the main Auto-Handle orchestrator.
// It runs the structured rule predicate evaluation pipeline with LLM fallback.
type Engine struct {
	store       *CachedStore
	predEval    *PredicateEvaluator
	llmFallback *LLMFallback
	actionExec  *ActionExecutor
	staging     *stagingManager
	log         *slog.Logger
}

// NewEngine creates a new Auto-Handle engine with all dependencies.
func NewEngine(db *sql.DB, ingConn ingestionConn, anthropicAPIKey string, log *slog.Logger) *Engine {
	store := NewCachedStore(db)
	return &Engine{
		store:       store,
		predEval:    NewPredicateEvaluator(),
		llmFallback: NewLLMFallback(anthropicAPIKey, log),
		actionExec:  NewActionExecutor(db, ingConn, log),
		staging:     newStagingManager(),
		log:         log,
	}
}

// Evaluate runs the Auto-Handle pipeline on an email.
//
// The pipeline is:
//  1. Load active rules for the user (ordered by usage_count DESC, cached).
//  2. For each rule: evaluate predicate against email attributes.
//  3. First matching rule with confidence >= threshold → execute action.
//  4. If no rule match: try LLM fallback (Haiku).
//  5. If LLM match with confidence >= 0.92 → stage rule (not activate immediately).
//  6. Return ClassificationResult with Route=RouteAuto.
//  7. If no match at all: return nil, false, nil → caller routes to Decision Stack.
func (e *Engine) Evaluate(ctx context.Context, email *models.EmailIngestedEvent, attrs models.EmailAttributes) (*models.ClassificationResult, bool, error) {
	start := time.Now().UTC()

	e.log.Info("auto-handle evaluate started",
		"email_id", email.RawEmailID,
		"user_id", email.UserID,
		"sender", email.SenderEmail,
	)

	// 1. Load active rules for user (ordered by usage_count DESC, with cache).
	rules, err := e.store.GetActiveRules(ctx, email.UserID)
	if err != nil {
		return nil, false, fmt.Errorf("load active rules: %w", err)
	}

	e.log.Debug("loaded active rules",
		"user_id", email.UserID,
		"rule_count", len(rules),
	)

	// 2. Evaluate each rule's predicate against email attributes.
	for _, rule := range rules {
		match, evalErr := e.predEval.Evaluate(rule.Predicate, attrs)
		if evalErr != nil {
			e.log.Warn("predicate evaluation error",
				"rule_id", rule.ID,
				"rule_name", rule.Name,
				"error", evalErr,
			)
			continue
		}

		if !match {
			continue
		}

		// 3. First matching rule with confidence >= threshold → execute action.
		confidence := rule.ConfidenceThreshold
		if confidence == 0 {
			confidence = hardConfidenceFloor
		}

		if confidence < hardConfidenceFloor {
			e.log.Warn("rule confidence below hard floor, skipping",
				"rule_id", rule.ID,
				"confidence", confidence,
				"floor", hardConfidenceFloor,
			)
			continue
		}

		e.log.Info("rule matched",
			"rule_id", rule.ID,
			"rule_name", rule.Name,
			"confidence", confidence,
		)

		// Execute action.
		actionErr := e.actionExec.Execute(ctx, rule, email)
		if actionErr != nil {
			e.log.Error("action execution failed",
				"rule_id", rule.ID,
				"action_type", rule.ActionType,
				"error", actionErr,
			)
			// Action failure is logged but we still return auto-handled
			// since the match decision was correct.
		}

		// Increment rule usage counter.
		if incErr := e.store.IncrementUsage(ctx, rule.ID); incErr != nil {
			e.log.Error("failed to increment rule usage", "rule_id", rule.ID, "error", incErr)
		}

		result := &models.ClassificationResult{
			RawEmailID:    email.RawEmailID,
			UserID:        email.UserID,
			ThreadID:      email.ThreadID,
			Route:         models.RouteAuto,
			Confidence:    confidence,
			MatchedRuleID: &rule.ID,
			LLMMatched:    false,
			ProcessedAt:   time.Now().UTC(),
		}

		e.log.Info("auto-handle completed via rule match",
			"email_id", email.RawEmailID,
			"rule_id", rule.ID,
			"elapsed_ms", time.Since(start).Milliseconds(),
		)

		return result, true, nil
	}

	// 4. No rule match — try LLM fallback (Haiku).
	e.log.Debug("no rule match, attempting LLM fallback",
		"email_id", email.RawEmailID,
	)

	llmResult, handled, llmErr := e.tryLLMFallback(ctx, email, attrs, rules)
	if llmErr != nil {
		e.log.Error("LLM fallback failed", "error", llmErr)
		// LLM failure is non-fatal; route to Decision Stack.
		return nil, false, nil
	}
	if handled {
		return llmResult, true, nil
	}

	// 7. No match at all: route to Decision Stack.
	e.log.Info("no auto-handle match, routing to decision stack",
		"email_id", email.RawEmailID,
		"elapsed_ms", time.Since(start).Milliseconds(),
	)

	return nil, false, nil
}

// tryLLMFallback attempts pattern matching via Claude 3 Haiku.
// If successful with confidence >= hardConfidenceFloor, it stages a new rule
// (48-hour window before activation) and returns the classification result.
func (e *Engine) tryLLMFallback(ctx context.Context, email *models.EmailIngestedEvent, attrs models.EmailAttributes, rules []models.AutoHandleRule) (*models.ClassificationResult, bool, error) {
	// Build rule names for the prompt.
	ruleNames := make([]string, 0, len(rules))
	for _, r := range rules {
		ruleNames = append(ruleNames, r.Name)
	}

	// Build body preview (first 500 chars).
	bodyPreview := attrs.Body
	if len(bodyPreview) > 500 {
		bodyPreview = bodyPreview[:500]
	}

	llmReq := models.LLMPatternMatchRequest{
		RuleNames:        ruleNames,
		SenderEmail:      attrs.SenderEmail,
		Subject:          attrs.Subject,
		BodyPreview:      bodyPreview,
		Recipient:        attrs.Recipient,
		HasAttachment:    attrs.HasAttachment,
		ParticipantCount: attrs.ThreadParticipantCount,
	}

	llmResp, err := e.llmFallback.Match(ctx, llmReq)
	if err != nil {
		return nil, false, err
	}

	if llmResp.Match == "none" || llmResp.Confidence < hardConfidenceFloor {
		e.log.Debug("LLM fallback produced no confident match",
			"match", llmResp.Match,
			"confidence", llmResp.Confidence,
			"reason", llmResp.Reason,
		)
		return nil, false, nil
	}

	// 5. LLM match with confidence >= 0.92 → stage rule (not activate immediately).
	e.log.Info("LLM fallback matched",
		"match", llmResp.Match,
		"confidence", llmResp.Confidence,
		"reason", llmResp.Reason,
	)

	// Find the matched rule to get its ID.
	var matchedRule *models.AutoHandleRule
	for i := range rules {
		if strings.EqualFold(rules[i].Name, llmResp.Match) {
			matchedRule = &rules[i]
			break
		}
	}

	if matchedRule == nil {
		e.log.Warn("LLM matched unknown rule name, creating staged rule",
			"match", llmResp.Match,
		)

		// Create a new staged rule from the LLM match.
		stagedRule, createErr := e.createStagedRuleFromLLM(ctx, email.UserID, llmResp, attrs)
		if createErr != nil {
			e.log.Error("failed to create staged rule from LLM match", "error", createErr)
			return nil, false, nil
		}
		matchedRule = stagedRule
	} else {
		// Stage the existing matched rule for 48 hours if not already staged/active.
		if matchedRule.Status != "active" && matchedRule.Status != "staged" {
			stageErr := e.stageRule(ctx, matchedRule.ID)
			if stageErr != nil {
				e.log.Error("failed to stage matched rule", "rule_id", matchedRule.ID, "error", stageErr)
			}
		}
	}

	// Execute the action for the matched rule.
	actionErr := e.actionExec.Execute(ctx, *matchedRule, email)
	if actionErr != nil {
		e.log.Error("LLM-matched action execution failed",
			"rule_id", matchedRule.ID,
			"error", actionErr,
		)
	}

	// Increment usage.
	if incErr := e.store.IncrementUsage(ctx, matchedRule.ID); incErr != nil {
		e.log.Error("failed to increment rule usage", "rule_id", matchedRule.ID, "error", incErr)
	}

	// 6. Return ClassificationResult with Route=RouteAuto.
	result := &models.ClassificationResult{
		RawEmailID:    email.RawEmailID,
		UserID:        email.UserID,
		ThreadID:      email.ThreadID,
		Route:         models.RouteAuto,
		Confidence:    llmResp.Confidence,
		MatchedRuleID: &matchedRule.ID,
		LLMMatched:    true,
		ProcessedAt:   time.Now().UTC(),
	}

	e.log.Info("auto-handle completed via LLM fallback",
		"email_id", email.RawEmailID,
		"rule_id", matchedRule.ID,
		"confidence", llmResp.Confidence,
	)

	return result, true, nil
}

// createStagedRuleFromLLM creates a new staged auto-handle rule based on LLM pattern detection.
// The rule enters a 48-hour staging window before it can be activated.
func (e *Engine) createStagedRuleFromLLM(ctx context.Context, userID uuid.UUID, llmResp *models.LLMPatternMatchResponse, attrs models.EmailAttributes) (*models.AutoHandleRule, error) {
	now := time.Now().UTC()
	stagedAt := now

	// Build a simple predicate from the email attributes that triggered the match.
	predicate := models.RulePredicate{
		AllOf: []models.Condition{
			{
				Field:    "sender_email",
				Operator: "eq",
				Value:    attrs.SenderEmail,
			},
		},
	}

	// If subject seems distinctive, add a subject contains condition.
	if attrs.Subject != "" && len(attrs.Subject) > 3 {
		predicate.AllOf = append(predicate.AllOf, models.Condition{
			Field:    "subject",
			Operator: "contains",
			Value:    extractSubjectKeyword(attrs.Subject),
		})
	}

	rule := &models.AutoHandleRule{
		UserID:              userID,
		Name:                llmResp.Match,
		Predicate:           predicate,
		ActionType:          "extract_notify", // safest default for LLM-generated rules
		ActionConfig:        []byte(`{"notification_title": "Auto-handled email"}`),
		ConfidenceThreshold: hardConfidenceFloor,
		Status:              "staged",
		StagedAt:            &stagedAt,
		UsageCount:          0,
	}

	if err := e.store.Create(ctx, rule); err != nil {
		return nil, fmt.Errorf("create staged rule: %w", err)
	}

	e.log.Info("created staged rule from LLM match",
		"rule_id", rule.ID,
		"user_id", userID,
		"name", rule.Name,
		"staged_at", stagedAt,
	)

	return rule, nil
}

// stageRule transitions a rule to staged status with 48-hour window.
func (e *Engine) stageRule(ctx context.Context, ruleID uuid.UUID) error {
	return e.store.UpdateStatus(ctx, ruleID, "staged")
}

// extractSubjectKeyword extracts a meaningful keyword from a subject line.
// This is a heuristic to build predicates from LLM-detected patterns.
func extractSubjectKeyword(subject string) string {
	// Simple heuristic: take the first 2-3 significant words.
	words := strings.Fields(subject)
	if len(words) == 0 {
		return subject
	}

	// Skip common prefixes and stop words (checked after stripping trailing colon).
	skipWords := map[string]bool{
		"re": true, "fw": true, "fwd": true, "aw": true, "wg": true,
		"the": true, "a": true, "an": true, "is": true, "it": true,
		"to": true, "for": true, "of": true, "in": true, "on": true,
		"at": true, "by": true, "or": true, "and": true,
	}

	var keywords []string
	for _, w := range words {
		clean := strings.ToLower(strings.TrimSuffix(w, ":"))
		if !skipWords[clean] && len(clean) > 4 {
			keywords = append(keywords, clean)
			if len(keywords) >= 2 {
				break
			}
		}
	}

	if len(keywords) > 0 {
		return strings.Join(keywords, " ")
	}
	return subject
}

// Stop gracefully shuts down the engine's background goroutines.
func (e *Engine) Stop() {
	if e.staging != nil {
		e.staging.Stop()
	}
}
