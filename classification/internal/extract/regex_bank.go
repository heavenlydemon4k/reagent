package extract

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/decisionstack/classification/internal/models"
)

// Pattern defines a single extract-only identification rule.
// Higher Priority wins when multiple patterns match.
type Pattern struct {
	Name     string      `json:"name"`
	Regex    string      `json:"regex"`
	Type     ExtractType `json:"type"`
	Priority int         `json:"priority"`
}

// CompiledPattern holds a compiled regexp together with its metadata.
type CompiledPattern struct {
	Name     string
	Re       *regexp.Regexp
	Type     ExtractType
	Priority int
}

// Patterns is the source-of-truth regex bank for extract-only identification.
// Each entry maps a named pattern to an extract type and priority.
// Priority scale: 10 (highest, deterministic 2FA) → 7 (receipt) → 5 (calendar MIME).
var Patterns = []Pattern{
	// ── 2FA / OTP (Priority 10 — deterministic, user-blocking) ──────────────
	{Name: "2fa_6digit", Regex: `(?i)(?:code|verify|verification|otp|token|pin)[^\d]{0,20}(\d{6})`, Type: Type2FA, Priority: 10},
	{Name: "2fa_5digit", Regex: `(?i)(?:code|verify|verification|otp|token|pin)[^\d]{0,20}(\d{5})`, Type: Type2FA, Priority: 10},
	{Name: "2fa_4digit", Regex: `(?i)(?:code|verify|verification|otp|token|pin)[^\d]{0,20}(\d{4})`, Type: Type2FA, Priority: 10},
	{Name: "2fa_8digit", Regex: `(?i)(?:code|verify|verification|otp|token|pin)[^\d]{0,20}(\d{8})`, Type: Type2FA, Priority: 10},
	{Name: "2fa_alpha_numeric", Regex: `(?i)(?:code|verify|verification|otp|token|pin)[^\w]{0,20}([A-Z0-9]{4,8})`, Type: Type2FA, Priority: 10},

	// ── Tracking numbers (Priority 8 — carrier-specific) ────────────────────
	{Name: "ups_tracking", Regex: `\b(1Z[0-9A-Z]{16})\b`, Type: TypeTracking, Priority: 8},
	{Name: "fedex_tracking", Regex: `\b(\d{12,14})\b`, Type: TypeTracking, Priority: 8},
	{Name: "usps_tracking_22", Regex: `\b(\d{20,22})\b`, Type: TypeTracking, Priority: 8},
	{Name: "usps_tracking_13", Regex: `\b([A-Z]{2}\d{9}[A-Z]{2})\b`, Type: TypeTracking, Priority: 8},
	{Name: "dhl_tracking", Regex: `\b(\d{10,11})\b`, Type: TypeTracking, Priority: 8},

	// ── Receipt / Order (Priority 7 — commercial) ───────────────────────────
	{Name: "order_number_hash", Regex: `(?i)(?:order\s*#|order\s*number|order\s*id)[^\w]{0,10}([A-Z0-9\-]{4,25})`, Type: TypeReceipt, Priority: 7},
	{Name: "order_number_label", Regex: `(?i)(?:order|invoice|receipt)[^\w]{0,5}(?:#|no\.?|number)[^\w]{0,10}([A-Z0-9\-]{4,25})`, Type: TypeReceipt, Priority: 7},
	{Name: "receipt_total_dollar", Regex: `(?i)(?:total|amount|paid|charge)[^\d]{0,15}\$?([\d,]+\.\d{2})`, Type: TypeReceipt, Priority: 7},
	{Name: "receipt_total_eur", Regex: `(?i)(?:total|amount|paid|charge)[^\d]{0,15}(€[\d,]+\.\d{2})`, Type: TypeReceipt, Priority: 7},
	{Name: "receipt_subtotal", Regex: `(?i)subtotal[^\d]{0,15}\$?([\d,]+\.\d{2})`, Type: TypeReceipt, Priority: 7},

	// ── Calendar MIME types (Priority 5 — content-type detection) ───────────
	// These are detected via email headers, not body regex; see Extract().
}

// ---------------------------------------------------------------------------
// Pre-compiled pattern cache (populated on first use via sync.Once)
// ---------------------------------------------------------------------------

var compiledPatterns []CompiledPattern

func init() {
	compiledPatterns = make([]CompiledPattern, 0, len(Patterns))
	for _, p := range Patterns {
		re, err := regexp.Compile(p.Regex)
		if err != nil {
			// Skip malformed patterns rather than panic at init.
			continue
		}
		compiledPatterns = append(compiledPatterns, CompiledPattern{
			Name:     p.Name,
			Re:       re,
			Type:     p.Type,
			Priority: p.Priority,
		})
	}
}

// ---------------------------------------------------------------------------
// Extract — fast-path regex scanner
// ---------------------------------------------------------------------------

// Extract runs all compiled patterns against the email subject + body.
// It returns the highest-priority match as an ExtractedDatum.
//
// Design invariants:
//   • Regex runs in <2ms for typical emails (bounded input: first 2KB).
//   • Highest Priority wins; ties broken by declaration order.
//   • If no pattern matches → returns nil, false → caller falls through to ONNX.
//   • Calendar MIME types are detected by inspecting Content-Type headers,
//     not by body regex (see calendar detection below).
func Extract(email *models.EmailIngestedEvent, subject, bodyText string) (*models.ExtractedDatum, bool) {
	// ── 1. Calendar MIME fast-path (header-based, not regex) ────────────────
	if datum := detectCalendarMIME(email, subject); datum != nil {
		return datum, true
	}

	// ── 2. Normalize input: subject + first 2048 chars of body ──────────────
	text := normalizeInput(subject, bodyText)
	if text == "" {
		return nil, false
	}

	// ── 3. Run compiled patterns, keep highest-priority match ───────────────
	var best *matchCandidate
	for _, cp := range compiledPatterns {
		groups := cp.Re.FindStringSubmatch(text)
		if groups == nil {
			continue
		}
		cand := &matchCandidate{
			type_:    cp.Type,
			pattern:  cp.Name,
			priority: cp.Priority,
			value:    pickCaptureGroup(groups),
		}
		if best == nil || cand.priority > best.priority {
			best = cand
		}
	}

	if best == nil {
		return nil, false
	}

	datum := buildDatum(best)
	return datum, true
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

type matchCandidate struct {
	type_    ExtractType
	pattern  string
	priority int
	value    string
}

// normalizeInput concatenates subject and body, truncating to 2048 chars
// to guarantee the <2ms latency invariant.
func normalizeInput(subject, body string) string {
	var sb strings.Builder
	if subject != "" {
		sb.WriteString(subject)
		sb.WriteByte(' ')
	}
	// Only scan first 2048 chars of body — enough for codes/tracking
	// while keeping regex execution bounded.
	if len(body) > 2048 {
		sb.WriteString(body[:2048])
	} else {
		sb.WriteString(body)
	}
	return sb.String()
}

// pickCaptureGroup returns the first non-empty capture group, or the full
// match if no captures produced text.
func pickCaptureGroup(groups []string) string {
	for i := 1; i < len(groups); i++ {
		if groups[i] != "" {
			return groups[i]
		}
	}
	return groups[0]
}

// buildDatum constructs an ExtractedDatum from a match candidate, generating
// user-facing notification text.
func buildDatum(c *matchCandidate) *models.ExtractedDatum {
	tmpl, ok := NotificationTemplates[c.type_]
	if !ok {
		tmpl = "Extracted data detected"
	}
	return &models.ExtractedDatum{
		Type:             string(c.type_),
		Value:            c.value,
		NotificationText: tmpl,
	}
}

// Calendar file extension patterns found in S3 URI or attachment hints.
// Expected MIME types for calendar detection (not used directly — detected
// via S3 URI and subject-line heuristics): text/calendar, application/ics.
var calendarIndicators = []string{
	".ics",
	".ical",
	".ifb",
	".icalendar",
	"invite.ics",
	"calendar.ics",
}

// detectCalendarMIME checks email metadata (S3 URI hint, content-type sniffing
// via the S3 key, attachment flag) to identify calendar invites without
// running body regex.
func detectCalendarMIME(email *models.EmailIngestedEvent, subject string) *models.ExtractedDatum {
	// Heuristic A: S3 URI contains calendar file extension
	uriLower := strings.ToLower(email.S3URI)
	for _, indicator := range calendarIndicators {
		if strings.Contains(uriLower, indicator) {
			return &models.ExtractedDatum{
				Type:             string(TypeCalendar),
				Value:            "calendar_invite",
				NotificationText: NotificationTemplates[TypeCalendar],
			}
		}
	}

	// Heuristic B: subject line contains calendar keywords
	subjLower := strings.ToLower(subject)
	calendarSubjectKeywords := []string{
		"invitation:",
		"invite:",
		"accepted:",
		"tentative:",
		"declined:",
		"updated invitation",
		"canceled:",
		"cancellation:",
	}
	for _, kw := range calendarSubjectKeywords {
		if strings.Contains(subjLower, kw) {
			return &models.ExtractedDatum{
				Type:             string(TypeCalendar),
				Value:            "calendar_invite",
				NotificationText: NotificationTemplates[TypeCalendar],
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Public accessors for testing / introspection
// ---------------------------------------------------------------------------

// PatternNames returns the ordered list of pattern names currently compiled.
func PatternNames() []string {
	names := make([]string, len(compiledPatterns))
	for i, cp := range compiledPatterns {
		names[i] = cp.Name
	}
	return names
}

// PatternCount returns the number of successfully compiled patterns.
func PatternCount() int {
	return len(compiledPatterns)
}

// SetRawEmailDeletionTimer is a no-op placeholder; the real implementation
// lives in the worker that calls Process().  It is declared here so
// extractor.go can reference it without import cycles.
var SetRawEmailDeletionTimer = func(rawEmailID uuid.UUID) {}
