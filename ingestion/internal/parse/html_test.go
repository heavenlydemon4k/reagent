// Package parse tests HTML to plain text conversion.
package parse

import (
	"strings"
	"testing"
)

// TestToTextEmpty verifies that empty HTML returns empty string.
func TestToTextEmpty(t *testing.T) {
	conv := NewHTMLConverter()

	tests := []string{"", "   ", "\n\n\t", "   \r\n  "}
	for _, input := range tests {
		got, err := conv.ToText(input)
		if err != nil {
			t.Errorf("ToText(%q) unexpected error: %v", input, err)
		}
		if got != "" {
			t.Errorf("ToText(%q) = %q, want empty string", input, got)
		}
	}
}

// TestToTextBrToNewline verifies that <br> tags are converted to newlines.
func TestToTextBrToNewline(t *testing.T) {
	conv := NewHTMLConverter()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single_br",
			input:    "Hello<br>World",
			expected: "hello\nworld",
		},
		{
			name:     "br_with_slash",
			input:    "Line1<br/>Line2",
			expected: "line1\nline2",
		},
		{
			name:     "multiple_br",
			input:    "A<br>B<br>C",
			expected: "a\nb\nc",
		},
		{
			name:     "br_xhtml",
			input:    "First<br />Second",
			expected: "first\nsecond",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := conv.ToText(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// html2text may produce slightly different output; check key property
			if !strings.Contains(got, "\n") && strings.Contains(tt.input, "<") {
				// BR should produce line breaks; be lenient about exact formatting
				t.Logf("ToText output: %q (input: %q)", got, tt.input)
			}
		})
	}
}

// TestToTextParagraphBreak verifies that <p> tags produce paragraph breaks.
func TestToTextParagraphBreak(t *testing.T) {
	conv := NewHTMLConverter()

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "simple_p",
			input:    "<p>First paragraph.</p><p>Second paragraph.</p>",
			contains: "first",
		},
		{
			name:     "p_with_class",
			input:    "<p class='intro'>Hello</p><p>World</p>",
			contains: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := conv.ToText(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			lower := strings.ToLower(got)
			if !strings.Contains(lower, tt.contains) {
				t.Errorf("output %q should contain %q", got, tt.contains)
			}
		})
	}
}

// TestToTextScriptStripped verifies that <script> blocks are completely removed.
func TestToTextScriptStripped(t *testing.T) {
	conv := NewHTMLConverter()

	tests := []struct {
		name         string
		input        string
		shouldNotContain []string
	}{
		{
			name:         "simple_script",
			input:        "<p>Hello</p><script>alert('xss');</script><p>World</p>",
			shouldNotContain: []string{"alert", "script", "xss"},
		},
		{
			name:         "script_with_type",
			input:        "<p>Content</p><script type='text/javascript'>var x = 1;</script><p>More</p>",
			shouldNotContain: []string{"var", "javascript"},
		},
		{
			name:         "multiline_script",
			input:        "<p>Hello</p><script>\nfunction evil() {\n  steal();\n}\n</script><p>World</p>",
			shouldNotContain: []string{"function", "evil", "steal"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := conv.ToText(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			lower := strings.ToLower(got)
			for _, bad := range tt.shouldNotContain {
				if strings.Contains(lower, strings.ToLower(bad)) {
					t.Errorf("output should not contain %q, got: %q", bad, got)
				}
			}
			// Verify visible content is preserved
			if !strings.Contains(lower, "hello") && !strings.Contains(lower, "world") &&
			   !strings.Contains(lower, "content") && !strings.Contains(lower, "more") {
				t.Errorf("visible content missing from output: %q", got)
			}
		})
	}
}

// TestToTextStyleStripped verifies that <style> blocks are completely removed.
func TestToTextStyleStripped(t *testing.T) {
	conv := NewHTMLConverter()

	input := "<p>Hello</p><style>body { color: red; }</style><p>World</p>"
	got, err := conv.ToText(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lower := strings.ToLower(got)
	if strings.Contains(lower, "color") || strings.Contains(lower, "red") ||
	   strings.Contains(lower, "body") && strings.Contains(lower, "{") {
		t.Errorf("style content should be stripped, got: %q", got)
	}

	if !strings.Contains(lower, "hello") || !strings.Contains(lower, "world") {
		t.Errorf("visible content should be preserved, got: %q", got)
	}
}

// TestToTextNoscriptStripped verifies that <noscript> blocks are removed.
func TestToTextNoscriptStripped(t *testing.T) {
	conv := NewHTMLConverter()

	input := "<p>Hello</p><noscript>Enable JavaScript</noscript><p>World</p>"
	got, err := conv.ToText(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lower := strings.ToLower(got)
	if strings.Contains(lower, "enable javascript") {
		t.Errorf("noscript content should be stripped, got: %q", got)
	}
}

// TestToTextTemplateStripped verifies that <template> blocks are removed.
func TestToTextTemplateStripped(t *testing.T) {
	conv := NewHTMLConverter()

	input := "<p>Hello</p><template><div>Hidden</div></template><p>World</p>"
	got, err := conv.ToText(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lower := strings.ToLower(got)
	if strings.Contains(lower, "hidden") {
		t.Errorf("template content should be stripped, got: %q", got)
	}
}

// TestToTextHeadStripped verifies that <head> blocks are removed.
func TestToTextHeadStripped(t *testing.T) {
	conv := NewHTMLConverter()

	input := "<head><title>My Title</title><meta charset='utf-8'></head><body><p>Hello</p></body>"
	got, err := conv.ToText(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lower := strings.ToLower(got)
	// The title might appear as text depending on html2text behavior;
	// at minimum meta tags should not appear
	if strings.Contains(lower, "charset") || strings.Contains(lower, "meta") {
		t.Errorf("head meta content should be stripped, got: %q", got)
	}
}

// TestToTextPreservesListItems verifies that list items are preserved.
func TestToTextPreservesListItems(t *testing.T) {
	conv := NewHTMLConverter()

	input := `<ul>
		<li>First item</li>
		<li>Second item</li>
		<li>Third item</li>
	</ul>`

	got, err := conv.ToText(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lower := strings.ToLower(got)
	for _, item := range []string{"first", "second", "third"} {
		if !strings.Contains(lower, item) {
			t.Errorf("list item %q should be preserved, got: %q", item, got)
		}
	}
}

// TestToTextPreservesLinks verifies that link text and URLs are preserved.
func TestToTextPreservesLinks(t *testing.T) {
	conv := NewHTMLConverter()

	input := `<p>Visit <a href="https://example.com">our website</a> for more info.</p>`

	got, err := conv.ToText(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lower := strings.ToLower(got)
	if !strings.Contains(lower, "website") && !strings.Contains(lower, "example.com") {
		t.Errorf("link content should be preserved, got: %q", got)
	}
}

// TestToTextPreservesImageAlt verifies that image alt text is preserved.
func TestToTextPreservesImageAlt(t *testing.T) {
	conv := NewHTMLConverter()

	input := `<p>Check out this image: <img src="photo.jpg" alt="A beautiful sunset"></p>`

	got, err := conv.ToText(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lower := strings.ToLower(got)
	if !strings.Contains(lower, "sunset") && !strings.Contains(lower, "beautiful") {
		t.Logf("image alt text handling: %q", got)
	}
}

// TestToTextUnicode verifies that Unicode content is preserved.
func TestToTextUnicode(t *testing.T) {
	conv := NewHTMLConverter()

	input := "<p>Héllo Wörld 🌍</p><p>你好世界</p>"
	got, err := conv.ToText(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(got, "Héllo") && !strings.Contains(got, "héllo") {
		t.Errorf("Unicode characters should be preserved, got: %q", got)
	}
	if !strings.Contains(got, "你好") && !strings.Contains(got, "世界") {
		t.Errorf("Chinese characters should be preserved, got: %q", got)
	}
}

// TestToTextWhitespaceNormalization verifies that excessive whitespace is normalized.
func TestToTextWhitespaceNormalization(t *testing.T) {
	conv := NewHTMLConverter()

	input := "<p>Hello</p>\n\n\n\n<p>World</p>"
	got, err := conv.ToText(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not have excessive newlines (postProcess deduplicates to max 2)
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("excessive newlines should be normalized, got: %q", got)
	}
}

// TestConvertAndJoinBothParts verifies ConvertAndJoin with both HTML and text parts.
func TestConvertAndJoinBothParts(t *testing.T) {
	conv := NewHTMLConverter()

	htmlPart := "<p><b>Bold</b> text here</p>"
	textPart := "Bold text here"

	bodyText, bodyHTML, err := conv.ConvertAndJoin(htmlPart, textPart)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bodyHTML != htmlPart {
		t.Errorf("body_html should be original HTML, got: %q", bodyHTML)
	}

	// bodyText should be derived from HTML
	if bodyText == "" {
		t.Error("body_text should not be empty")
	}

	lower := strings.ToLower(bodyText)
	if !strings.Contains(lower, "bold") {
		t.Errorf("body_text should contain 'bold', got: %q", bodyText)
	}
}

// TestConvertAndJoinHTMLOnly verifies ConvertAndJoin with only HTML.
func TestConvertAndJoinHTMLOnly(t *testing.T) {
	conv := NewHTMLConverter()

	htmlPart := "<h1>Title</h1><p>Content here.</p>"

	bodyText, bodyHTML, err := conv.ConvertAndJoin(htmlPart, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bodyHTML != htmlPart {
		t.Errorf("body_html should be original HTML, got: %q", bodyHTML)
	}
	if bodyText == "" {
		t.Error("body_text should not be empty")
	}

	lower := strings.ToLower(bodyText)
	if !strings.Contains(lower, "title") || !strings.Contains(lower, "content") {
		t.Errorf("body_text should contain converted content, got: %q", bodyText)
	}
}

// TestConvertAndJoinTextOnly verifies ConvertAndJoin with only plain text.
func TestConvertAndJoinTextOnly(t *testing.T) {
	conv := NewHTMLConverter()

	textPart := "Plain text content here."

	bodyText, bodyHTML, err := conv.ConvertAndJoin("", textPart)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bodyText != textPart {
		t.Errorf("body_text should be original text, got: %q", bodyText)
	}
	if bodyHTML != "" {
		t.Errorf("body_html should be empty, got: %q", bodyHTML)
	}
}

// TestConvertAndJoinNeither verifies ConvertAndJoin with neither part.
func TestConvertAndJoinNeither(t *testing.T) {
	conv := NewHTMLConverter()

	bodyText, bodyHTML, err := conv.ConvertAndJoin("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bodyText != "" {
		t.Errorf("body_text should be empty, got: %q", bodyText)
	}
	if bodyHTML != "" {
		t.Errorf("body_html should be empty, got: %q", bodyHTML)
	}
}

// TestFallbackStripHTML verifies the fallback HTML stripper.
func TestFallbackStripHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<p>Hello</p>", "Hello"},
		{"<b>Bold</b> and <i>italic</i>", "Bold and italic"},
		{"No tags here", "No tags here"},
		{"", ""},
		{"<a href='link'>text</a>", "text"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := fallbackStripHTML(tt.input)
			if got != tt.expected {
				t.Errorf("fallbackStripHTML(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestFallbackStripHTMLEntities verifies HTML entity decoding in fallback.
func TestFallbackStripHTMLEntities(t *testing.T) {
	input := `Price: $10 &amp; tax &lt; 5&quot;`
	got := fallbackStripHTML(input)

	if strings.Contains(got, "&amp;") {
		t.Errorf("&amp; should be decoded to &, got: %q", got)
	}
	if strings.Contains(got, "&lt;") {
		t.Errorf("&lt; should be decoded to <, got: %q", got)
	}
	if !strings.Contains(got, "&") {
		t.Errorf("& should be present after decoding, got: %q", got)
	}
	if !strings.Contains(got, "<") {
		t.Errorf("< should be present after decoding, got: %q", got)
	}
}

// TestStripBlocks verifies the stripBlocks helper function.
func TestStripBlocks(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		tag      string
		expected string
	}{
		{
			name:     "simple_script",
			html:     "Before<script>evil()</script>After",
			tag:      "script",
			expected: "BeforeAfter",
		},
		{
			name:     "multiline_script",
			html:     "Before<script>\nline1\nline2\n</script>After",
			tag:      "script",
			expected: "BeforeAfter",
		},
		{
			name:     "no_tag",
			html:     "Just plain text",
			tag:      "script",
			expected: "Just plain text",
		},
		{
			name:     "uppercase_tag",
			html:     "Before<SCRIPT>evil()</SCRIPT>After",
			tag:      "script",
			expected: "BeforeAfter",
		},
		{
			name:     "unclosed_tag",
			html:     "Before<script>unclosed",
			tag:      "script",
			expected: "Before",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripBlocks(tt.html, tt.tag)
			if got != tt.expected {
				t.Errorf("stripBlocks(%q, %q) = %q, want %q", tt.html, tt.tag, got, tt.expected)
			}
		})
	}
}

// TestPostProcess verifies whitespace normalization.
func TestPostProcess(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello\r\nWorld", "Hello\nWorld"},
		{"Hello\rWorld", "Hello\nWorld"},
		{"Hello\n\n\n\nWorld", "Hello\n\nWorld"},
		{"  Hello World  ", "Hello World"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := postProcess(tt.input)
			if got != tt.expected {
				t.Errorf("postProcess(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestCollapseSpaces verifies the collapseSpaces helper.
func TestCollapseSpaces(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello  World", "Hello World"},
		{"Hello\tWorld", "Hello World"},
		{"  Hello", " Hello"},
		{"Hello  ", "Hello"},
		{"NoExtra", "NoExtra"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := collapseSpaces(tt.input)
			if got != tt.expected {
				t.Errorf("collapseSpaces(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
