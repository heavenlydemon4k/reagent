package auto

import (
	"testing"

	"github.com/decisionstack/classification/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// PredicateEvaluator — basic construction
// ---------------------------------------------------------------------------

func TestNewPredicateEvaluator(t *testing.T) {
	pe := NewPredicateEvaluator()
	require.NotNil(t, pe)
	assert.Equal(t, 100, pe.maxEntries)
}

// ---------------------------------------------------------------------------
// Evaluate — eq (case-insensitive equality)
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_Eq(t *testing.T) {
	pe := NewPredicateEvaluator()

	tests := []struct {
		name   string
		pred   models.RulePredicate
		attrs  models.EmailAttributes
		want   bool
	}{
		{
			name: "sender equals case insensitive",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "sender_email", Operator: "eq", Value: "Sarah@Vendor.com"},
				},
			},
			attrs: models.EmailAttributes{SenderEmail: "sarah@vendor.com"},
			want:  true,
		},
		{
			name: "sender not equal",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "sender_email", Operator: "eq", Value: "sarah@vendor.com"},
				},
			},
			attrs: models.EmailAttributes{SenderEmail: "bob@other.com"},
			want:  false,
		},
		{
			name: "exact numeric match",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "thread_participant_count", Operator: "eq", Value: 3},
				},
			},
			attrs: models.EmailAttributes{ThreadParticipantCount: 3},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pe.Evaluate(tt.pred, tt.attrs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// Evaluate — ne (not equal)
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_Ne(t *testing.T) {
	pe := NewPredicateEvaluator()

	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "sender_email", Operator: "ne", Value: "spam@evil.com"},
		},
	}
	attrs := models.EmailAttributes{SenderEmail: "alice@example.com"}
	got, err := pe.Evaluate(pred, attrs)
	require.NoError(t, err)
	assert.True(t, got)

	attrs2 := models.EmailAttributes{SenderEmail: "spam@evil.com"}
	got2, err := pe.Evaluate(pred, attrs2)
	require.NoError(t, err)
	assert.False(t, got2)
}

// ---------------------------------------------------------------------------
// Evaluate — contains
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_Contains(t *testing.T) {
	pe := NewPredicateEvaluator()

	tests := []struct {
		name  string
		pred  models.RulePredicate
		attrs models.EmailAttributes
		want  bool
	}{
		{
			name: "subject contains keyword (case insensitive)",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "subject", Operator: "contains", Value: "INVOICE"},
				},
			},
			attrs: models.EmailAttributes{Subject: "Your monthly invoice is ready"},
			want:  true,
		},
		{
			name: "body contains keyword",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "body", Operator: "contains", Value: "payment"},
				},
			},
			attrs: models.EmailAttributes{Body: "Please complete your payment by Friday."},
			want:  true,
		},
		{
			name: "does not contain",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "subject", Operator: "contains", Value: "urgent"},
				},
			},
			attrs: models.EmailAttributes{Subject: "Regular update"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pe.Evaluate(tt.pred, tt.attrs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// Evaluate — contains error cases
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_Contains_Errors(t *testing.T) {
	pe := NewPredicateEvaluator()

	// Non-string field.
	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "has_attachment", Operator: "contains", Value: "true"},
		},
	}
	attrs := models.EmailAttributes{HasAttachment: true}
	_, err := pe.Evaluate(pred, attrs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contains requires string field")
}

// ---------------------------------------------------------------------------
// Evaluate — regex
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_Regex(t *testing.T) {
	pe := NewPredicateEvaluator()

	tests := []struct {
		name    string
		pred    models.RulePredicate
		attrs   models.EmailAttributes
		want    bool
		wantErr bool
	}{
		{
			name: "sender matches domain pattern",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "sender_email", Operator: "regex", Value: `.*@github\.com$`},
				},
			},
			attrs: models.EmailAttributes{SenderEmail: "notifications@github.com"},
			want:  true,
		},
		{
			name: "sender does not match pattern",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "sender_email", Operator: "regex", Value: `.*@github\.com$`},
				},
			},
			attrs: models.EmailAttributes{SenderEmail: "alice@gmail.com"},
			want:  false,
		},
		{
			name: "subject matches wildcard",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "subject", Operator: "regex", Value: `(?i)invoice|receipt|order`},
				},
			},
			attrs: models.EmailAttributes{Subject: "Your INVOICE for March"},
			want:  true,
		},
		{
			name: "invalid regex pattern",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "sender_email", Operator: "regex", Value: `[invalid(`},
				},
			},
			attrs:   models.EmailAttributes{SenderEmail: "test@example.com"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pe.Evaluate(tt.pred, tt.attrs)
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
// Evaluate — regex LRU cache
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_Regex_Cache(t *testing.T) {
	pe := NewPredicateEvaluator()
	pattern := `.*@github\.com$`

	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "sender_email", Operator: "regex", Value: pattern},
		},
	}
	attrs := models.EmailAttributes{SenderEmail: "notifications@github.com"}

	// First evaluation should be a cache miss.
	got, err := pe.Evaluate(pred, attrs)
	require.NoError(t, err)
	assert.True(t, got)

	hits, misses := pe.CacheStats()
	assert.Equal(t, uint64(0), hits)
	assert.Equal(t, uint64(1), misses)

	// Second evaluation with same pattern should be a cache hit.
	got, err = pe.Evaluate(pred, attrs)
	require.NoError(t, err)
	assert.True(t, got)

	hits, misses = pe.CacheStats()
	assert.Equal(t, uint64(1), hits)
	assert.Equal(t, uint64(1), misses)
}

// ---------------------------------------------------------------------------
// Evaluate — gt (greater than)
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_Gt(t *testing.T) {
	pe := NewPredicateEvaluator()

	tests := []struct {
		name  string
		pred  models.RulePredicate
		attrs models.EmailAttributes
		want  bool
	}{
		{
			name: "participant count greater than threshold",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "thread_participant_count", Operator: "gt", Value: 2},
				},
			},
			attrs: models.EmailAttributes{ThreadParticipantCount: 5},
			want:  true,
		},
		{
			name: "participant count equal (not greater)",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "thread_participant_count", Operator: "gt", Value: 5},
				},
			},
			attrs: models.EmailAttributes{ThreadParticipantCount: 5},
			want:  false,
		},
		{
			name: "time of day greater than",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "time_of_day", Operator: "gt", Value: 8},
				},
			},
			attrs: models.EmailAttributes{TimeOfDay: 14},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pe.Evaluate(tt.pred, tt.attrs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// Evaluate — lt (less than)
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_Lt(t *testing.T) {
	pe := NewPredicateEvaluator()

	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "thread_participant_count", Operator: "lt", Value: 10},
		},
	}
	attrs := models.EmailAttributes{ThreadParticipantCount: 3}
	got, err := pe.Evaluate(pred, attrs)
	require.NoError(t, err)
	assert.True(t, got)

	attrs2 := models.EmailAttributes{ThreadParticipantCount: 15}
	got2, err := pe.Evaluate(pred, attrs2)
	require.NoError(t, err)
	assert.False(t, got2)
}

// ---------------------------------------------------------------------------
// Evaluate — in
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_In(t *testing.T) {
	pe := NewPredicateEvaluator()

	tests := []struct {
		name  string
		pred  models.RulePredicate
		attrs models.EmailAttributes
		want  bool
	}{
		{
			name: "sender domain in allowlist",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "sender_domain", Operator: "in", Value: []string{"example.com", "vendor.com", "client.com"}},
				},
			},
			attrs: models.EmailAttributes{SenderDomain: "vendor.com"},
			want:  true,
		},
		{
			name: "sender domain not in allowlist",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "sender_domain", Operator: "in", Value: []string{"example.com", "vendor.com"}},
				},
			},
			attrs: models.EmailAttributes{SenderDomain: "evil.com"},
			want:  false,
		},
		{
			name: "case insensitive in",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "sender_email", Operator: "in", Value: []string{"ALICE@Example.COM", "Bob@Test.COM"}},
				},
			},
			attrs: models.EmailAttributes{SenderEmail: "alice@example.com"},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pe.Evaluate(tt.pred, tt.attrs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// Evaluate — not_in
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_NotIn(t *testing.T) {
	pe := NewPredicateEvaluator()

	tests := []struct {
		name  string
		pred  models.RulePredicate
		attrs models.EmailAttributes
		want  bool
	}{
		{
			name: "sender domain not in blocklist",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "sender_domain", Operator: "not_in", Value: []string{"spam.com", "phishing.com"}},
				},
			},
			attrs: models.EmailAttributes{SenderDomain: "example.com"},
			want:  true,
		},
		{
			name: "sender domain in blocklist",
			pred: models.RulePredicate{
				AllOf: []models.Condition{
					{Field: "sender_domain", Operator: "not_in", Value: []string{"spam.com", "example.com"}},
				},
			},
			attrs: models.EmailAttributes{SenderDomain: "example.com"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pe.Evaluate(tt.pred, tt.attrs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// Evaluate — unknown operator
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_UnknownOperator(t *testing.T) {
	pe := NewPredicateEvaluator()

	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "sender_email", Operator: "magic", Value: "test"},
		},
	}
	attrs := models.EmailAttributes{SenderEmail: "test@example.com"}
	_, err := pe.Evaluate(pred, attrs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown operator")
}

// ---------------------------------------------------------------------------
// Evaluate — unknown field returns false
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_UnknownField(t *testing.T) {
	pe := NewPredicateEvaluator()

	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "nonexistent_field", Operator: "eq", Value: "test"},
		},
	}
	attrs := models.EmailAttributes{SenderEmail: "test@example.com"}
	got, err := pe.Evaluate(pred, attrs)
	require.NoError(t, err)
	assert.False(t, got)
}

// ---------------------------------------------------------------------------
// Evaluate — anyOf satisfied
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_AnyOf(t *testing.T) {
	pe := NewPredicateEvaluator()

	pred := models.RulePredicate{
		AnyOf: []models.Condition{
			{Field: "sender_email", Operator: "eq", Value: "alice@example.com"},
			{Field: "sender_email", Operator: "eq", Value: "bob@example.com"},
			{Field: "sender_email", Operator: "eq", Value: "charlie@example.com"},
		},
	}
	attrs := models.EmailAttributes{SenderEmail: "bob@example.com"}
	got, err := pe.Evaluate(pred, attrs)
	require.NoError(t, err)
	assert.True(t, got)
}

// ---------------------------------------------------------------------------
// Evaluate — combined allOf AND anyOf
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_Combined_AllOf_And_AnyOf(t *testing.T) {
	pe := NewPredicateEvaluator()

	// Match: from example.com domain AND (subject has "invoice" OR "receipt")
	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "sender_domain", Operator: "eq", Value: "example.com"},
		},
		AnyOf: []models.Condition{
			{Field: "subject", Operator: "contains", Value: "invoice"},
			{Field: "subject", Operator: "contains", Value: "receipt"},
		},
	}

	// Both allOf and anyOf satisfied.
	attrs1 := models.EmailAttributes{
		SenderDomain: "example.com",
		Subject:      "Your receipt for March",
	}
	got, err := pe.Evaluate(pred, attrs1)
	require.NoError(t, err)
	assert.True(t, got)

	// allOf fails.
	attrs2 := models.EmailAttributes{
		SenderDomain: "other.com",
		Subject:      "Your receipt for March",
	}
	got, err = pe.Evaluate(pred, attrs2)
	require.NoError(t, err)
	assert.False(t, got)

	// allOf passes but anyOf fails.
	attrs3 := models.EmailAttributes{
		SenderDomain: "example.com",
		Subject:      "Regular newsletter",
	}
	got, err = pe.Evaluate(pred, attrs3)
	require.NoError(t, err)
	assert.False(t, got)
}

// ---------------------------------------------------------------------------
// Cache management
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_Cache_ResetCache(t *testing.T) {
	pe := NewPredicateEvaluator()

	// Populate cache.
	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "sender_email", Operator: "regex", Value: `.*@test\.com$`},
		},
	}
	attrs := models.EmailAttributes{SenderEmail: "user@test.com"}
	_, err := pe.Evaluate(pred, attrs)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, pe.CacheSize(), 1)

	// Reset cache.
	pe.ResetCache()
	assert.Equal(t, 0, pe.CacheSize())
	hits, misses := pe.CacheStats()
	assert.Equal(t, uint64(0), hits)
	assert.Equal(t, uint64(0), misses)
}

// ---------------------------------------------------------------------------
// evalCondition — type comparison edge cases
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_CompareValues(t *testing.T) {
	pe := NewPredicateEvaluator()

	// int vs float64
	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "thread_participant_count", Operator: "gt", Value: 2.0},
		},
	}
	attrs := models.EmailAttributes{ThreadParticipantCount: 5}
	got, err := pe.Evaluate(pred, attrs)
	require.NoError(t, err)
	assert.True(t, got)
}

// ---------------------------------------------------------------------------
// Full end-to-end predicate scenarios
// ---------------------------------------------------------------------------

func TestPredicateEvaluator_EndToEnd_InvoiceRule(t *testing.T) {
	pe := NewPredicateEvaluator()

	// A realistic invoice matching rule.
	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "sender_domain", Operator: "eq", Value: "quickbooks.com"},
			{Field: "subject", Operator: "contains", Value: "invoice"},
		},
	}

	// Should match.
	attrs1 := models.EmailAttributes{
		SenderDomain: "quickbooks.com",
		Subject:      "Your monthly invoice is ready",
	}
	got, err := pe.Evaluate(pred, attrs1)
	require.NoError(t, err)
	assert.True(t, got)

	// Wrong domain.
	attrs2 := models.EmailAttributes{
		SenderDomain: "other.com",
		Subject:      "Your monthly invoice is ready",
	}
	got, err = pe.Evaluate(pred, attrs2)
	require.NoError(t, err)
	assert.False(t, got)
}

func TestPredicateEvaluator_EndToEnd_2FARule(t *testing.T) {
	pe := NewPredicateEvaluator()

	// A realistic 2FA code detection rule.
	pred := models.RulePredicate{
		AllOf: []models.Condition{
			{Field: "sender_email", Operator: "regex", Value: `(?i)noreply@.*`},
			{Field: "body", Operator: "contains", Value: "code"},
		},
	}

	attrs := models.EmailAttributes{
		SenderEmail: "noreply@google.com",
		Body:        "Your verification code is 123456.",
	}
	got, err := pe.Evaluate(pred, attrs)
	require.NoError(t, err)
	assert.True(t, got)
}
