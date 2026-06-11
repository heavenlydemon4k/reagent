//go:build windows || !cgo

// Windows stub: onnxruntime-go has no Windows build support.
// This file provides the same SignatureClassifier API using regex-only detection.
package parse

import (
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

const defaultModelPath = "/models/signature_classifier.onnx"

const SignatureThreshold = 0.85

type SignatureClassifier struct {
	session      interface{} // placeholder so test literals compile; always nil on Windows
	modelPath    string
	enabled      bool
	log          *slog.Logger
	fallbackOnce sync.Once
	fallbackRe   *regexp.Regexp
}

func NewSignatureClassifier(modelPath string) (*SignatureClassifier, error) {
	if modelPath == "" {
		modelPath = defaultModelPath
	}
	return &SignatureClassifier{
		modelPath: modelPath,
		enabled:   false,
		log:       slog.Default().WithGroup("signature-classifier"),
	}, nil
}

func (sc *SignatureClassifier) Close() error { return nil }

func (sc *SignatureClassifier) IsSignature(paragraph string) (bool, float64, error) {
	if strings.TrimSpace(paragraph) == "" {
		return false, 0.0, nil
	}
	isSig := sc.regexIsSignature(paragraph)
	var prob float64
	if isSig {
		prob = 0.90
	}
	return isSig, prob, nil
}

func (sc *SignatureClassifier) StripSignatures(text string) (string, []string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil, nil
	}
	paragraphs := splitParagraphs(text)
	var kept, stripped []string
	for _, para := range paragraphs {
		isSig, _, err := sc.IsSignature(para)
		if err != nil {
			kept = append(kept, para)
			continue
		}
		if isSig {
			stripped = append(stripped, para)
		} else {
			kept = append(kept, para)
		}
	}
	return strings.Join(kept, "\n\n"), stripped, nil
}

// splitParagraphs splits text on double newlines.
// Defined as package-level to match the non-stub signature.go.
func splitParagraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n\r\n", "\n\n")
	text = strings.ReplaceAll(text, "\r\r", "\n\n")
	parts := strings.Split(text, "\n\n")
	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func (sc *SignatureClassifier) regexIsSignature(paragraph string) bool {
	sc.fallbackOnce.Do(sc.compileFallback)
	trimmed := strings.TrimSpace(paragraph)
	if trimmed == "" {
		return false
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 {
		return false
	}
	firstLine := strings.TrimSpace(lines[0])
	if strings.HasPrefix(firstLine, "--") && !strings.HasPrefix(firstLine, "---") {
		return true
	}
	mobileSigs := []string{
		"sent from my iphone", "sent from my ipad", "sent from my android",
		"sent from my blackberry", "sent from my windows phone",
		"sent from my mobile", "sent from my samsung", "sent via ",
	}
	lowerFirst := strings.ToLower(firstLine)
	for _, sig := range mobileSigs {
		if strings.HasPrefix(lowerFirst, sig) {
			return true
		}
	}
	if sc.fallbackRe != nil && sc.fallbackRe.MatchString(trimmed) {
		matches := sc.fallbackRe.FindAllString(trimmed, -1)
		if len(matches) >= 2 {
			return true
		}
	}
	if len(lines) <= 4 && sc.containsSignatureSignals(trimmed) >= 3 {
		return true
	}
	return false
}

func (sc *SignatureClassifier) compileFallback() {
	pattern := `(?i)(` +
		`\b\+?\d[\d\s\-().]{7,}\d\b|` +
		`https?://\S+|www\.\S+|` +
		`\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b|` +
		`@[A-Za-z0-9_]{3,15}|` +
		`\b(fax|tel|phone|mobile|cell):\s*\S+|` +
		`\b(linkedin\.com|twitter\.com|x\.com|github\.com|facebook\.com)/\S+` +
		`)`
	re, err := regexp.Compile(pattern)
	if err != nil {
		sc.log.Warn("failed to compile fallback regex", "error", err)
		return
	}
	sc.fallbackRe = re
}

func (sc *SignatureClassifier) containsSignatureSignals(text string) int {
	signals := 0
	lower := strings.ToLower(text)
	heuristics := []string{
		"sent from", "http", "www.", "@", "tel", "phone", "fax", "mobile",
		"linkedin", "twitter", "skype", "best regards", "kind regards",
		"sincerely", "cheers", "yours truly", "--",
	}
	for _, h := range heuristics {
		if strings.Contains(lower, h) {
			signals++
		}
	}
	return signals
}

// preview returns a short preview for logging. Defined here to match signature.go.
func preview(text string, maxLen int) string {
	if len(text) <= maxLen+3 {
		return text
	}
	return text[:maxLen] + "..."
}
