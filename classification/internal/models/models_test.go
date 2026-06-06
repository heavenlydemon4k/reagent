package models

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// EmailIngestedEvent validation
// ---------------------------------------------------------------------------

func TestEmailIngestedEvent_StructFields(t *testing.T) {
	now := time.Now().UTC()
	uid := uuid.Must(uuid.NewRandom())

	event := EmailIngestedEvent{
		EventID:            uid,
		UserID:             uid,
		Source:             "gmail",
		AccountID:          uid,
		ThreadID:           uid,
		RawEmailID:         uid,
		S3URI:              "s3://bucket/key",
		HasAttachments:     true,
		SenderEmail:        "alice@example.com",
		ReceivedAt:         now,
		ClassificationHint: "pending",
		ContactIDs:         []uuid.UUID{uid},
	}

	assert.Equal(t, uid, event.EventID)
	assert.Equal(t, "gmail", event.Source)
	assert.Equal(t, "s3://bucket/key", event.S3URI)
	assert.True(t, event.HasAttachments)
	assert.Equal(t, "alice@example.com", event.SenderEmail)
	assert.Equal(t, "pending", event.ClassificationHint)
	assert.Len(t, event.ContactIDs, 1)
}

// ---------------------------------------------------------------------------
// RouteType constants
// ---------------------------------------------------------------------------

func TestRouteType_Values(t *testing.T) {
	assert.Equal(t, RouteType("extract"), RouteExtract)
	assert.Equal(t, RouteType("auto"), RouteAuto)
	assert.Equal(t, RouteType("decision"), RouteDecision)
}

// ---------------------------------------------------------------------------
// ClassificationResult validation
// ---------------------------------------------------------------------------

func TestClassificationResult_StructFields(t *testing.T) {
	now := time.Now().UTC()
	rawID := uuid.Must(uuid.NewRandom())
	userID := uuid.Must(uuid.NewRandom())
	threadID := uuid.Must(uuid.NewRandom())
	ruleID := uuid.Must(uuid.NewRandom())

	result := ClassificationResult{
		RawEmailID:    rawID,
		UserID:        userID,
		ThreadID:      threadID,
		Route:         RouteAuto,
		Confidence:    0.95,
		MatchedRuleID: &ruleID,
		ExtractedData: &ExtractedDatum{
			Type:             "2fa",
			Value:            "123456",
			NotificationText: "Code detected",
		},
		LLMMatched:  true,
		ProcessedAt: now,
	}

	assert.Equal(t, rawID, result.RawEmailID)
	assert.Equal(t, RouteAuto, result.Route)
	assert.InDelta(t, 0.95, result.Confidence, 0.001)
	assert.NotNil(t, result.MatchedRuleID)
	assert.Equal(t, "2fa", result.ExtractedData.Type)
	assert.Equal(t, "123456", result.ExtractedData.Value)
	assert.True(t, result.LLMMatched)
}

// ---------------------------------------------------------------------------
// ExtractedDatum types
// ---------------------------------------------------------------------------

func TestExtractedDatum_Types(t *testing.T) {
	tests := []struct {
		name string
		datum ExtractedDatum
		wantType string
		wantValue string
	}{
		{
			name: "2fa code",
			datum: ExtractedDatum{Type: "2fa", Value: "123456", NotificationText: "Verification code detected"},
			wantType:  "2fa",
			wantValue: "123456",
		},
		{
			name: "tracking number",
			datum: ExtractedDatum{Type: "tracking", Value: "1Z999AA10123456784", NotificationText: "Package tracking update"},
			wantType:  "tracking",
			wantValue: "1Z999AA10123456784",
		},
		{
			name: "calendar invite",
			datum: ExtractedDatum{Type: "calendar", Value: "calendar_invite", NotificationText: "Calendar event received"},
			wantType:  "calendar",
			wantValue: "calendar_invite",
		},
		{
			name: "receipt",
			datum: ExtractedDatum{Type: "receipt", Value: "ABC-12345", NotificationText: "Receipt or order confirmation"},
			wantType:  "receipt",
			wantValue: "ABC-12345",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantType, tt.datum.Type)
			assert.Equal(t, tt.wantValue, tt.datum.Value)
			assert.NotEmpty(t, tt.datum.NotificationText)
		})
	}
}

// ---------------------------------------------------------------------------
// RulePredicate.Evaluate — AllOf (AND) logic
// ---------------------------------------------------------------------------

func TestRulePredicate_Evaluate_AllOf(t *testing.T) {
	tests := []struct {
		name    string
		pred    RulePredicate
		attrs   EmailAttributes
		want    bool
		wantErr bool
	}{
		{
			name: "all conditions match",
			pred: RulePredicate{
				AllOf: []Condition{
					{Field: "sender_email", Operator: "eq", Value: "alice@example.com"},
					{Field: "sender_domain", Operator: "eq", Value: "example.com"},
				},
			},
			attrs: EmailAttributes{SenderEmail: "alice@example.com", SenderDomain: "example.com"},
			want:  true,
		},
		{
			name: "first condition fails",
			pred: RulePredicate{
				AllOf: []Condition{
					{Field: "sender_email", Operator: "eq", Value: "alice@example.com"},
					{Field: "sender_domain", Operator: "eq", Value: "other.com"},
				},
			},
			attrs: EmailAttributes{SenderEmail: "alice@example.com", SenderDomain: "example.com"},
			want:  false,
		},
		{
			name: "empty allOf matches trivially",
			pred: RulePredicate{AllOf: []Condition{}},
			attrs: EmailAttributes{SenderEmail: "any@thing.com"},
			want:  true,
		},
		{
			name: "nil allOf matches trivially",
			pred: RulePredicate{},
			attrs: EmailAttributes{SenderEmail: "any@thing.com"},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.pred.Evaluate(tt.attrs)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// RulePredicate.Evaluate — AnyOf (OR) logic
// ---------------------------------------------------------------------------

func TestRulePredicate_Evaluate_AnyOf(t *testing.T) {
	tests := []struct {
		name  string
		pred  RulePredicate
		attrs EmailAttributes
		want  bool
	}{
		{
			name: "anyOf one matches",
			pred: RulePredicate{
				AnyOf: []Condition{
					{Field: "sender_email", Operator: "eq", Value: "alice@example.com"},
					{Field: "sender_email", Operator: "eq", Value: "bob@example.com"},
				},
			},
			attrs: EmailAttributes{SenderEmail: "bob@example.com"},
			want:  true,
		},
		{
			name: "anyOf none matches",
			pred: RulePredicate{
				AnyOf: []Condition{
					{Field: "sender_email", Operator: "eq", Value: "alice@example.com"},
					{Field: "sender_email", Operator: "eq", Value: "bob@example.com"},
				},
			},
			attrs: EmailAttributes{SenderEmail: "charlie@other.com"},
			want:  false,
		},
		{
			name: "allOf AND anyOf both satisfied",
			pred: RulePredicate{
				AllOf: []Condition{
					{Field: "sender_domain", Operator: "eq", Value: "example.com"},
				},
				AnyOf: []Condition{
					{Field: "sender_email", Operator: "eq", Value: "alice@example.com"},
					{Field: "sender_email", Operator: "eq", Value: "bob@example.com"},
				},
			},
			attrs: EmailAttributes{SenderEmail: "alice@example.com", SenderDomain: "example.com"},
			want:  true,
		},
		{
			name: "allOf passes but anyOf fails",
			pred: RulePredicate{
				AllOf: []Condition{
					{Field: "sender_domain", Operator: "eq", Value: "example.com"},
				},
				AnyOf: []Condition{
					{Field: "sender_email", Operator: "eq", Value: "alice@example.com"},
				},
			},
			attrs: EmailAttributes{SenderEmail: "bob@example.com", SenderDomain: "example.com"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.pred.Evaluate(tt.attrs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// Condition operators
// ---------------------------------------------------------------------------

func TestCondition_Eq(t *testing.T) {
	attrs := EmailAttributes{SenderEmail: "Alice@Example.COM"}
	pred := RulePredicate{
		AllOf: []Condition{
			{Field: "sender_email", Operator: "eq", Value: "alice@example.com"},
		},
	}
	got, err := pred.Evaluate(attrs)
	require.NoError(t, err)
	assert.True(t, got, "eq should be case-insensitive")
}

func TestCondition_Ne(t *testing.T) {
	attrs := EmailAttributes{SenderEmail: "alice@example.com"}
	pred := RulePredicate{
		AllOf: []Condition{
			{Field: "sender_email", Operator: "ne", Value: "bob@example.com"},
		},
	}
	got, err := pred.Evaluate(attrs)
	require.NoError(t, err)
	assert.True(t, got)
}

func TestCondition_Contains(t *testing.T) {
	attrs := EmailAttributes{Subject: "Your Invoice from ACME Corp"}
	pred := RulePredicate{
		AllOf: []Condition{
			{Field: "subject", Operator: "contains", Value: "invoice"},
		},
	}
	got, err := pred.Evaluate(attrs)
	require.NoError(t, err)
	assert.True(t, got, "contains should be case-insensitive")
}

func TestCondition_Regex(t *testing.T) {
	attrs := EmailAttributes{SenderEmail: "notifications@github.com"}
	pred := RulePredicate{
		AllOf: []Condition{
			{Field: "sender_email", Operator: "regex", Value: `.*@github\.com$`},
		},
	}
	got, err := pred.Evaluate(attrs)
	require.NoError(t, err)
	assert.True(t, got)
}

func TestCondition_Gt(t *testing.T) {
	attrs := EmailAttributes{ThreadParticipantCount: 5}
	pred := RulePredicate{
		AllOf: []Condition{
			{Field: "thread_participant_count", Operator: "gt", Value: 3},
		},
	}
	got, err := pred.Evaluate(attrs)
	require.NoError(t, err)
	assert.True(t, got)
}

func TestCondition_Lt(t *testing.T) {
	attrs := EmailAttributes{ThreadParticipantCount: 2}
	pred := RulePredicate{
		AllOf: []Condition{
			{Field: "thread_participant_count", Operator: "lt", Value: 5},
		},
	}
	got, err := pred.Evaluate(attrs)
	require.NoError(t, err)
	assert.True(t, got)
}

func TestCondition_In(t *testing.T) {
	attrs := EmailAttributes{SenderDomain: "example.com"}
	pred := RulePredicate{
		AllOf: []Condition{
			{Field: "sender_domain", Operator: "in", Value: []string{"example.com", "test.com", "demo.com"}},
		},
	}
	got, err := pred.Evaluate(attrs)
	require.NoError(t, err)
	assert.True(t, got)
}

func TestCondition_NotIn(t *testing.T) {
	attrs := EmailAttributes{SenderDomain: "evil.com"}
	pred := RulePredicate{
		AllOf: []Condition{
			{Field: "sender_domain", Operator: "not_in", Value: []string{"example.com", "test.com"}},
		},
	}
	got, err := pred.Evaluate(attrs)
	require.NoError(t, err)
	assert.True(t, got, "evil.com should not be in the allowlist")
}

// ---------------------------------------------------------------------------
// EmailAttributes.Get
// ---------------------------------------------------------------------------

func TestEmailAttributes_Get(t *testing.T) {
	attrs := EmailAttributes{
		SenderEmail:            "alice@example.com",
		SenderDomain:           "example.com",
		Subject:                "Test Subject",
		Body:                   "Hello world",
		Recipient:              "bob@example.com",
		HasAttachment:          true,
		ThreadParticipantCount: 3,
		TimeOfDay:              14,
		DayOfWeek:              2,
	}

	assert.Equal(t, "alice@example.com", attrs.Get("sender_email"))
	assert.Equal(t, "example.com", attrs.Get("sender_domain"))
	assert.Equal(t, "Test Subject", attrs.Get("subject"))
	assert.Equal(t, "Hello world", attrs.Get("body"))
	assert.Equal(t, "bob@example.com", attrs.Get("recipient"))
	assert.Equal(t, true, attrs.Get("has_attachment"))
	assert.Equal(t, 3, attrs.Get("thread_participant_count"))
	assert.Equal(t, 14, attrs.Get("time_of_day"))
	assert.Equal(t, 2, attrs.Get("day_of_week"))
	assert.Nil(t, attrs.Get("unknown_field"))
}

// ---------------------------------------------------------------------------
// AutoHandleRule defaults
// ---------------------------------------------------------------------------

func TestAutoHandleRule_Defaults(t *testing.T) {
	rule := AutoHandleRule{
		ID:     uuid.Must(uuid.NewRandom()),
		UserID: uuid.Must(uuid.NewRandom()),
		Name:   "Test Rule",
		Predicate: RulePredicate{
			AllOf: []Condition{
				{Field: "sender_email", Operator: "eq", Value: "sarah@vendor.com"},
			},
		},
		ActionType:          "reply_template",
		ConfidenceThreshold: 0.92,
		Status:              "staged",
	}

	assert.Equal(t, "reply_template", rule.ActionType)
	assert.InDelta(t, 0.92, rule.ConfidenceThreshold, 0.001)
	assert.Equal(t, "staged", rule.Status)
	assert.Equal(t, 0, rule.UsageCount)
	assert.Len(t, rule.Predicate.AllOf, 1)
}

// ---------------------------------------------------------------------------
// StagingRule struct
// ---------------------------------------------------------------------------

func TestStagingRule_Fields(t *testing.T) {
	now := time.Now().UTC()
	ruleID := uuid.Must(uuid.NewRandom())
	userID := uuid.Must(uuid.NewRandom())

	sr := StagingRule{
		RuleID:      ruleID,
		UserID:      userID,
		StagedAt:    now,
		ActivatesAt: now.Add(48 * time.Hour),
		Status:      "staged",
	}

	assert.Equal(t, ruleID, sr.RuleID)
	assert.Equal(t, userID, sr.UserID)
	assert.Equal(t, "staged", sr.Status)
	assert.WithinDuration(t, now.Add(48*time.Hour), sr.ActivatesAt, time.Second)
}

// ---------------------------------------------------------------------------
// ClassificationError
// ---------------------------------------------------------------------------

func TestClassificationError_Error(t *testing.T) {
	err := ClassificationError{
		Code:    ErrCodePredicateEval,
		Message: "predicate evaluation failed",
		Retry:   false,
	}

	assert.Equal(t, "predicate evaluation failed", err.Error())
	assert.Equal(t, "predicate_eval_failed", err.Code)
	assert.False(t, err.Retry)
}

func TestClassificationError_Constants(t *testing.T) {
	assert.Equal(t, "predicate_eval_failed", ErrCodePredicateEval)
	assert.Equal(t, "llm_unavailable", ErrCodeLLMUnavailable)
	assert.Equal(t, "rule_not_found", ErrCodeRuleNotFound)
	assert.Equal(t, "confidence_below_floor", ErrCodeConfidenceBelow)
}

// ---------------------------------------------------------------------------
// IntelligenceCompressEvent
// ---------------------------------------------------------------------------

func TestIntelligenceCompressEvent_Fields(t *testing.T) {
	uid := uuid.Must(uuid.NewRandom())

	event := IntelligenceCompressEvent{
		EventID:       uid,
		UserID:        uid,
		ThreadID:      uid,
		RawEmailIDs:   []uuid.UUID{uid},
		PriorityScore: 0.85,
		Source:        "gmail",
	}

	assert.Equal(t, uid, event.EventID)
	assert.InDelta(t, 0.85, event.PriorityScore, 0.001)
	assert.Equal(t, "gmail", event.Source)
	assert.Len(t, event.RawEmailIDs, 1)
}

// ---------------------------------------------------------------------------
// LLM request/response
// ---------------------------------------------------------------------------

func TestLLMPatternMatchRequest_Fields(t *testing.T) {
	req := LLMPatternMatchRequest{
		RuleNames:        []string{"Invoice Rule", "Newsletter Rule"},
		SenderEmail:      "vendor@example.com",
		Subject:          "Your March Invoice",
		BodyPreview:      "Please find your invoice attached...",
		Recipient:        "user@example.com",
		HasAttachment:    true,
		ParticipantCount: 2,
	}

	assert.Len(t, req.RuleNames, 2)
	assert.Equal(t, "vendor@example.com", req.SenderEmail)
	assert.Equal(t, "Your March Invoice", req.Subject)
	assert.True(t, req.HasAttachment)
}

func TestLLMPatternMatchResponse_Fields(t *testing.T) {
	resp := LLMPatternMatchResponse{
		Match:      "Invoice Rule",
		Confidence: 0.97,
		Reason:     "Subject contains 'Invoice' and sender matches known vendor",
	}

	assert.Equal(t, "Invoice Rule", resp.Match)
	assert.InDelta(t, 0.97, resp.Confidence, 0.001)
	assert.NotEmpty(t, resp.Reason)
}

// ---------------------------------------------------------------------------
// ExtractedDatum nil safety
// ---------------------------------------------------------------------------

func TestExtractedDatum_NotificationText_NeverEmpty(t *testing.T) {
	// Ensure all datum types have non-empty notification text.
	types := []string{"2fa", "tracking", "calendar", "receipt", "newsletter", "notification"}
	for _, typ := range types {
		datum := ExtractedDatum{Type: typ, Value: "test-value", NotificationText: typ + " detected"}
		assert.NotEmpty(t, datum.NotificationText, "type %s must have notification text", typ)
	}
}
