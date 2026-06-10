// Package parse transforms raw MIME email into structured ParsedEmail.
// This file handles HTML-to-text conversion using jaytaylor/html2text.
package parse

import (
	"fmt"
	"strings"

	"github.com/jaytaylor/html2text"
)

// HTMLConverter transforms HTML email bodies into clean, readable plain text.
// It handles email-specific HTML quirks: inline styles, broken tags, nested
// tables for layout, and various newline representations.
type HTMLConverter struct{}

// NewHTMLConverter creates a new HTMLConverter with default settings.
func NewHTMLConverter() *HTMLConverter {
	return &HTMLConverter{}
}

// ToText converts an HTML string to plain UTF-8 text with paragraph boundaries.
//
// Processing pipeline:
//  1. Pre-process: strip <script>, <style>, <noscript>, <template> blocks entirely
//  2. Convert via html2text with email-friendly options
//  3. Post-process: normalize whitespace, fix paragraph boundaries, deduplicate newlines
//  4. Preserve list indentation and image alt text
func (c *HTMLConverter) ToText(html string) (string, error) {
	if strings.TrimSpace(html) == "" {
		return "", nil
	}

	// Pre-process: completely remove script, style, and other non-content blocks.
	html = stripBlocks(html, "script")
	html = stripBlocks(html, "style")
	html = stripBlocks(html, "noscript")
	html = stripBlocks(html, "template")
	html = stripBlocks(html, "head")

	// Use html2text with options tuned for email content.
	text, err := html2text.FromString(html, html2text.Options{
		OmitLinks: false,
		TextOnly:  true,
	})
	if err != nil {
		// If html2text fails, fall back to a minimal regex-based strip.
		text = fallbackStripHTML(html)
	}

	// Post-process: normalize whitespace and fix boundaries.
	text = postProcess(text)

	return text, nil
}

// stripBlocks removes all content between <tag>...</tag> (case-insensitive),
// including the tags themselves. Handles multi-line content.
func stripBlocks(html, tag string) string {
	openTag := "<" + tag
	closeTag := "</" + tag + ">"

	var result strings.Builder
	result.Grow(len(html))

	lower := strings.ToLower(html)
	i := 0
	for i < len(html) {
		idx := strings.Index(lower[i:], openTag)
		if idx == -1 {
			result.WriteString(html[i:])
			break
		}
		idx += i

		// Write everything before this tag
		result.WriteString(html[i:idx])

		// Find the matching closing tag (case-insensitive)
		closeIdx := strings.Index(lower[idx:], closeTag)
		if closeIdx == -1 {
			i = len(html)
			break
		}
		closeIdx += idx + len(closeTag)
		i = closeIdx
	}

	return result.String()
}

// fallbackStripHTML is a minimal HTML stripper used when html2text fails.
// It removes all tags and decodes common entities.
func fallbackStripHTML(html string) string {
	var result strings.Builder
	result.Grow(len(html))
	inTag := false

	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			result.WriteRune(r)
		}
	}

	text := result.String()

	// Decode common HTML entities.
	replacements := map[string]string{
		"&nbsp;":   " ",
		"&lt;":     "<",
		"&gt;":     ">",
		"&amp;":    "&",
		"&quot;":   "\"",
		"&apos;":   "'",
		"&#39;":    "'",
		"&#x27;":   "'",
		"&mdash;":  "—",
		"&ndash;":  "–",
		"&hellip;": "…",
		"&copy;":   "©",
		"&trade;":  "™",
		"&reg;":    "®",
	}
	for entity, char := range replacements {
		text = strings.ReplaceAll(text, entity, char)
	}

	return text
}

// postProcess normalizes whitespace, fixes paragraph boundaries, and
// deduplicates newlines to produce clean output.
func postProcess(text string) string {
	// Replace carriage returns.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	// Collapse horizontal whitespace (tabs, multiple spaces) to single space.
	// But preserve leading spaces for list-like indentation.
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, " \t")
		line = collapseSpaces(line)
		lines[i] = line
	}
	text = strings.Join(lines, "\n")

	// Deduplicate blank lines: max 2 consecutive newlines (paragraph boundary).
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}

	// Trim leading/trailing whitespace.
	text = strings.TrimSpace(text)

	return text
}

// collapseSpaces collapses multiple consecutive spaces into a single space,
// while preserving leading indentation (up to 8 spaces for list nesting).
func collapseSpaces(s string) string {
	var result strings.Builder
	result.Grow(len(s))
	inSpaces := false
	spaceCount := 0
	const maxIndentSpaces = 8

	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !inSpaces {
				inSpaces = true
				spaceCount = 1
			} else {
				spaceCount++
			}
			if spaceCount <= maxIndentSpaces && result.Len() == spaceCount-1 {
				result.WriteRune(' ')
			}
		} else {
			inSpaces = false
			spaceCount = 0
			result.WriteRune(r)
		}
	}

	return result.String()
}

// ConvertAndJoin merges HTML and plain-text parts intelligently.
// If both are present, HTML is converted to text and preferred.
// If only plain text exists, it is returned as-is.
// If only HTML exists, it is converted.
func (c *HTMLConverter) ConvertAndJoin(htmlPart, textPart string) (string, string, error) {
	var bodyText, bodyHTML string

	if htmlPart != "" && textPart != "" {
		// Both parts: prefer HTML-derived text; keep original HTML.
		bodyHTML = htmlPart
		converted, err := c.ToText(htmlPart)
		if err != nil {
			// Fallback to plain text if HTML conversion fails.
			bodyText = textPart
		} else {
			bodyText = converted
		}
	} else if htmlPart != "" {
		bodyHTML = htmlPart
		converted, err := c.ToText(htmlPart)
		if err != nil {
			return "", "", fmt.Errorf("HTML conversion failed: %w", err)
		}
		bodyText = converted
	} else if textPart != "" {
		bodyText = textPart
		bodyHTML = ""
	} else {
		bodyText = ""
		bodyHTML = ""
	}

	return bodyText, bodyHTML, nil
}
