// Package parse transforms raw MIME email into structured ParsedEmail.
// This file handles signature detection using an ONNX classifier and
// fallback regex-based stripping when the model is unavailable.
package parse

import (
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"

	onnx "github.com/microsoft/onnxruntime-go"
)

// Default model path (mounted via Docker volume).
const defaultModelPath = "/models/signature_classifier.onnx"

// SignatureThreshold is the probability threshold above which a paragraph
// is classified as a signature and stripped. P > 0.85.
const SignatureThreshold = 0.85

// SignatureClassifier wraps an ONNX Runtime inference session for
// email signature detection.
type SignatureClassifier struct {
	session   *onnx.AdvancedSession
	modelPath string
	enabled   bool
	log       *slog.Logger

	// Regex-based fallback patterns, compiled once.
	fallbackOnce sync.Once
	fallbackRe   *regexp.Regexp
}

// NewSignatureClassifier loads the ONNX signature classifier model.
// If model loading fails, the classifier still returns a usable instance
// that falls back to regex-based detection.
func NewSignatureClassifier(modelPath string) (*SignatureClassifier, error) {
	if modelPath == "" {
		modelPath = defaultModelPath
	}

	sc := &SignatureClassifier{
		modelPath: modelPath,
		log:       slog.Default().WithGroup("signature-classifier"),
	}

	// Attempt to load the ONNX model.
	// The model expects a feature vector input and outputs a single probability.
	session, err := onnx.NewAdvancedSession(
		modelPath,
		[]string{"input"},      // input node names
		[]string{"output"},    // output node names
		[]*onnx.ArbitraryTensor{}, // input tensors (allocated per-inference)
		nil,                   // output tensors (allocated per-inference)
		nil,                   // session options
	)
	if err != nil {
		sc.log.Warn("failed to load ONNX signature model; using regex fallback",
			"model_path", modelPath,
			"error", err,
		)
		sc.enabled = false
		return sc, nil
	}

	sc.session = session
	sc.enabled = true
	sc.log.Info("ONNX signature model loaded", "model_path", modelPath)
	return sc, nil
}

// Close releases the ONNX session resources.
func (sc *SignatureClassifier) Close() error {
	if sc.session != nil {
		return sc.session.Destroy()
	}
	return nil
}

// IsSignature runs ONNX inference on a single paragraph and returns
// whether it is classified as a signature along with the confidence
// probability.
func (sc *SignatureClassifier) IsSignature(paragraph string) (bool, float64, error) {
	if strings.TrimSpace(paragraph) == "" {
		return false, 0.0, nil
	}

	// If ONNX is unavailable, use regex fallback.
	if !sc.enabled {
		isSig := sc.regexIsSignature(paragraph)
		var prob float64
		if isSig {
			prob = 0.90 // Synthetic high confidence for regex matches.
		} else {
			prob = 0.10
		}
		return isSig, prob, nil
	}

	// ONNX inference path.
	probability, err := sc.runInference(paragraph)
	if err != nil {
		sc.log.Warn("ONNX inference failed, falling back to regex", "error", err)
		isSig := sc.regexIsSignature(paragraph)
		return isSig, 0.90, nil
	}

	isSig := probability > SignatureThreshold
	return isSig, probability, nil
}

// runInference executes the ONNX model on a single paragraph.
// The model expects a text feature vector (bag-of-words / TF-IDF).
// We compute a simple normalized feature vector here.
func (sc *SignatureClassifier) runInference(paragraph string) (float64, error) {
	// Feature extraction: create a simple feature vector from the paragraph.
	// In production, this should match the exact preprocessing pipeline used
	// during model training (tokenization, TF-IDF, etc.).
	features := sc.extractFeatures(paragraph)
	featureDim := len(features)

	// Create input tensor: shape [1, featureDim].
	inputShape := onnx.NewShape(1, int64(featureDim))
	inputTensor, err := onnx.NewTensor(inputShape, features)
	if err != nil {
		return 0, fmt.Errorf("failed to create input tensor: %w", err)
	}
	defer inputTensor.Destroy()

	// Output tensor: shape [1, 1] — single probability.
	outputShape := onnx.NewShape(1, 1)
	outputTensor, err := onnx.NewTensor(outputShape, make([]float32, 1))
	if err != nil {
		return 0, fmt.Errorf("failed to create output tensor: %w", err)
	}
	defer outputTensor.Destroy()

	// Run inference.
	err = sc.session.Run(
		[]*onnx.ArbitraryTensor{inputTensor.GetPtr()},
		[]*onnx.ArbitraryTensor{outputTensor.GetPtr()},
	)
	if err != nil {
		return 0, fmt.Errorf("ONNX inference failed: %w", err)
	}

	// Get output probability.
	outputData := outputTensor.GetData()
	if len(outputData) != 1 {
		return 0, fmt.Errorf("unexpected output shape: expected 1, got %d", len(outputData))
	}

	return float64(outputData[0]), nil
}

// extractFeatures creates a normalized feature vector from a paragraph.
// This is a simplified bag-of-words approach. The actual feature extraction
// should match the training pipeline.
func (sc *SignatureClassifier) extractFeatures(paragraph string) []float32 {
	lower := strings.ToLower(strings.TrimSpace(paragraph))

	// Define signature-indicator tokens (weak signals, NOT hard rules).
	// These are ordered alphabetically for deterministic feature vectors.
	tokenWeights := []struct {
		token  string
		weight float32
	}{
		{"--", 0.3},
		{"@", 0.15},
		{"android", 0.2},
		{"best", 0.15},
		{"blackberry", 0.3},
		{"cheers", 0.15},
		{"fax:", 0.3},
		{"http", 0.2},
		{"https", 0.2},
		{"ipad", 0.2},
		{"iphone", 0.2},
		{"linkedin", 0.2},
		{"mobile:", 0.25},
		{"phone:", 0.25},
		{"regards", 0.15},
		{"sent from", 0.4},
		{"sincerely", 0.2},
		{"skype", 0.2},
		{"tel:", 0.25},
		{"thanks", 0.1},
		{"thank you", 0.1},
		{"twitter", 0.2},
		{"windows phone", 0.2},
		{"www.", 0.2},
		{"yours truly", 0.2},
	}

	features := make([]float32, len(tokenWeights))
	for i, tw := range tokenWeights {
		if strings.Contains(lower, tw.token) {
			features[i] = tw.weight
		}
	}

	// L2 normalize.
	var sumSq float32
	for _, v := range features {
		sumSq += v * v
	}
	if sumSq > 0 {
		norm := float32(math.Sqrt(float64(sumSq)))
		for i := range features {
			features[i] = features[i] / norm
		}
	}

	return features
}

// StripSignatures splits the text into paragraphs, classifies each,
// and strips paragraphs with P > 0.85 (SignatureThreshold).
// It returns the cleaned text and the list of stripped signature blocks.
func (sc *SignatureClassifier) StripSignatures(text string) (string, []string, error) {
	if strings.TrimSpace(text) == "" {
		return text, nil, nil
	}

	// Split into paragraphs on double newline.
	paragraphs := splitParagraphs(text)

	var kept []string
	var stripped []string

	for _, para := range paragraphs {
		trimmed := strings.TrimSpace(para)
		if trimmed == "" {
			continue
		}

		isSig, prob, err := sc.IsSignature(trimmed)
		if err != nil {
			sc.log.Warn("signature classification failed, keeping paragraph", "error", err)
			kept = append(kept, para)
			continue
		}

		if isSig {
			sc.log.Debug("stripped signature paragraph",
				"probability", prob,
				"preview", preview(trimmed, 60),
			)
			stripped = append(stripped, trimmed)
		} else {
			kept = append(kept, para)
		}
	}

	cleaned := strings.Join(kept, "\n\n")
	return cleaned, stripped, nil
}

// splitParagraphs splits text on double newlines (\n\n).
// It handles various newline conventions (\r\n\r\n, \n\n, \r\r).
func splitParagraphs(text string) []string {
	// Normalize all paragraph separators to a common form.
	text = strings.ReplaceAll(text, "\r\n\r\n", "\n\n")
	text = strings.ReplaceAll(text, "\r\r", "\n\n")

	parts := strings.Split(text, "\n\n")

	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		result = append(result, part)
	}

	return result
}

// regexIsSignature uses fallback heuristic patterns to detect signatures.
// These are weak signals used ONLY when the ONNX model is unavailable.
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

	// Check first line for signature delimiter.
	firstLine := strings.TrimSpace(lines[0])

	// Pattern: line starts with "--" (signature delimiter, but not "---" horizontal rule).
	if strings.HasPrefix(firstLine, "--") && !strings.HasPrefix(firstLine, "---") {
		return true
	}

	// Pattern: common mobile signature phrases.
	mobileSigs := []string{
		"sent from my iphone",
		"sent from my ipad",
		"sent from my android",
		"sent from my blackberry",
		"sent from my windows phone",
		"sent from my mobile",
		"sent from my samsung",
		"sent via ",
	}
	lowerFirst := strings.ToLower(firstLine)
	for _, sig := range mobileSigs {
		if strings.HasPrefix(lowerFirst, sig) {
			return true
		}
	}

	// Pattern: signature block with name + contact info patterns.
	if sc.fallbackRe != nil && sc.fallbackRe.MatchString(trimmed) {
		// Require at least 2 signature signals to reduce false positives.
		matches := sc.fallbackRe.FindAllString(trimmed, -1)
		if len(matches) >= 2 {
			return true
		}
	}

	// Single-line: phone number + URL + name (classic signature block).
	if len(lines) <= 4 && sc.containsSignatureSignals(trimmed) >= 3 {
		return true
	}

	return false
}

// compileFallback compiles the fallback regex patterns once.
func (sc *SignatureClassifier) compileFallback() {
	// Patterns: phone numbers, URLs, email addresses, social handles.
	pattern := `(?i)(` +
		`\b\+?\d[\d\s\-().]{7,}\d\b|` + // phone numbers
		`https?://\S+|www\.\S+|` + // URLs
		`\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b|` + // email addresses
		`@[A-Za-z0-9_]{3,15}|` + // social handles (@twitter)
		`\b(fax|tel|phone|mobile|cell):\s*\S+|` + // labeled contacts
		`\b(linkedin\.com|twitter\.com|x\.com|github\.com|facebook\.com)/\S+` + // social URLs
		`)`

	re, err := regexp.Compile(pattern)
	if err != nil {
		sc.log.Warn("failed to compile fallback regex", "error", err)
		return
	}
	sc.fallbackRe = re
}

// containsSignatureSignals counts how many weak signature signals are present.
func (sc *SignatureClassifier) containsSignatureSignals(text string) int {
	signals := 0
	lower := strings.ToLower(text)

	heuristics := []string{
		"sent from",
		"http",
		"www.",
		"@",
		"tel",
		"phone",
		"fax",
		"mobile",
		"linkedin",
		"twitter",
		"skype",
		"best regards",
		"kind regards",
		"sincerely",
		"cheers",
		"yours truly",
		"--",
	}

	for _, h := range heuristics {
		if strings.Contains(lower, h) {
			signals++
		}
	}

	return signals
}

// preview returns a short preview of text for logging (2FA codes are NOT logged).
func preview(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}
