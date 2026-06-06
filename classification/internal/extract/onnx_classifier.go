package extract

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// ONNX Classifier — DistilBERT 3-class extract classifier
// ---------------------------------------------------------------------------

// Class labels must match the order the ONNX model was trained with.
const (
	classReceipt      = "is_receipt"
	classNewsletter   = "is_newsletter"
	classNotification = "is_notification"
	classUnknown      = "unknown"
)

var labelOrder = []string{classReceipt, classNewsletter, classNotification}

// Hard confidence floor. Anything below this falls through to Decision Stack.
const onnxConfidenceThreshold = 0.95

// Default model path (override via ONNXModelPath or env var).
const defaultModelPath = "/models/extract_classifier.onnx"

// ONNXSession abstracts the underlying ONNX Runtime session so tests can
// inject a mock without importing the heavy onnxruntime library.
type ONNXSession interface {
	// Run executes inference and returns a flat float32 slice of logits
	// or softmax probabilities.  Length must equal len(labelOrder).
	Run(input []int64) ([]float32, error)
	Close() error
}

// ---------------------------------------------------------------------------
// Classifier struct
// ---------------------------------------------------------------------------

// ONNXClassifier wraps the DistilBERT ONNX model for the Extract-Only pipeline.
type ONNXClassifier struct {
	session   ONNXSession
	modelPath string
	once      sync.Once
	initErr   error
}

// NewONNXClassifier creates a classifier that lazily loads the model on first
// use (keeping startup fast).  Pass an empty path to use the default.
func NewONNXClassifier(modelPath string) *ONNXClassifier {
	if modelPath == "" {
		modelPath = defaultModelPath
	}
	if p := os.Getenv("ONNX_EXTRACT_MODEL_PATH"); p != "" {
		modelPath = p
	}
	return &ONNXClassifier{
		modelPath: modelPath,
	}
}

// NewONNXClassifierWithSession allows injection of a pre-built session
// (useful for tests or for callers that manage their own ort session).
func NewONNXClassifierWithSession(sess ONNXSession) *ONNXClassifier {
	return &ONNXClassifier{
		session:   sess,
		modelPath: "injected",
	}
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// Classify runs the ONNX model against the email text.
//
// Parameters:
//   • text — subject + body preview (caller should truncate body to 200 chars).
//
// Returns:
//   • class     — one of "is_receipt", "is_newsletter", "is_notification", or
//                 "unknown" when confidence is below threshold or model fails.
//   • confidence — 0.0–1.0 softmax probability for the winning class.
//   • error      — non-nil only for unexpected (non-recoverable) errors.
//
// Invariant: if ONNX fails internally, returns ("unknown", 0.0, nil) so the
// caller can fall through to the next pipeline stage conservatively.
func (c *ONNXClassifier) Classify(text string) (string, float64, error) {
	c.once.Do(func() {
		if c.session == nil {
			c.session, c.initErr = c.loadSession()
		}
	})
	if c.initErr != nil {
		// Conservative fallback: model missing or corrupt → don't block pipeline.
		return classUnknown, 0.0, nil
	}
	if c.session == nil {
		return classUnknown, 0.0, nil
	}

	// 1. Tokenize with DistilBERT tokenizer (word-piece).
	inputIDs := tokenizeDistilBERT(text)

	// 2. Run inference.
	probs, err := c.session.Run(inputIDs)
	if err != nil {
		// ONNX failure → conservative fallback, don't bubble error.
		return classUnknown, 0.0, nil
	}
	if len(probs) != len(labelOrder) {
		return classUnknown, 0.0, nil
	}

	// 3. Find winner and apply HARD threshold.
	bestIdx, confidence := argMax(probs)
	if bestIdx < 0 || confidence < onnxConfidenceThreshold {
		return classUnknown, confidence, nil
	}

	return labelOrder[bestIdx], confidence, nil
}

// Close releases the underlying ONNX session.
func (c *ONNXClassifier) Close() error {
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Model loading (placeholder for real ONNX Runtime integration)
// ---------------------------------------------------------------------------

// loadSession attempts to create a real ONNX Runtime session.
// When the onnxruntime package is available it will be used;
// otherwise this returns an error and the classifier falls back to "unknown".
func (c *ONNXClassifier) loadSession() (ONNXSession, error) {
	// Try real ONNX Runtime first.
	if sess, err := tryRealONNX(c.modelPath); err == nil {
		return sess, nil
	}

	// If model file doesn't exist, return error → classifier will always
	// return ("unknown", 0.0, nil) and the pipeline falls through.
	if _, err := os.Stat(c.modelPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("onnx model not found at %s", c.modelPath)
	}

	return nil, fmt.Errorf("onnx runtime not available for %s", c.modelPath)
}

// tryRealONNX attempts to create a real ONNX Runtime session using
// github.com/microsoft/onnxruntime-binding or similar.
// Returns error if the package is not compiled in or model loading fails.
func tryRealONNX(modelPath string) (ONNXSession, error) {
	// This is a compile-time stub.  When the real onnxruntime tag is present,
	// replace the body with ort.NewAdvancedSession(...).
	return nil, fmt.Errorf("onnx runtime not compiled in")
}

// ---------------------------------------------------------------------------
// Tokenization — DistilBERT word-piece
// ---------------------------------------------------------------------------

// tokenizerOnce ensures the tokenizer is loaded exactly once.
var tokenizerOnce sync.Once
var vocab map[string]int64 // word → token id

// tokenizeDistilBERT converts text to a fixed-length int64 slice suitable
// for DistilBERT input.  Truncates/pads to 128 tokens.
//
// This is a minimal implementation. In production, swap for a proper
// huggingface/tokenizers Go binding or call a tokenizer micro-service.
func tokenizeDistilBERT(text string) []int64 {
	tokenizerOnce.Do(func() {
		// Placeholder vocab init. Real implementation loads vocab.txt from
		// /models/distilbert-base-uncased-vocab.txt
		vocab = make(map[string]int64)
	})

	const (
		clsToken int64 = 101
		sepToken int64 = 102
		padToken int64 = 0
		maxLen   int   = 128
	)

	// Normalization: lower-case, strip extra spaces, truncate to first 200 chars.
	text = strings.ToLower(text)
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 200 {
		text = text[:200]
	}

	// Naïve whitespace tokenization — production uses WordPiece.
	words := strings.Fields(text)

	// Build input IDs: [CLS] tokens ... [SEP] [PAD]...
	inputIDs := make([]int64, 0, maxLen)
	inputIDs = append(inputIDs, clsToken)

	for _, w := range words {
		if len(inputIDs) >= maxLen-1 {
			break
		}
		// Look up in vocab; fall back to [UNK] == 100
		if tid, ok := vocab[w]; ok {
			inputIDs = append(inputIDs, tid)
		} else {
			// Sub-word naïve fallback: try stripping punctuation
			w = strings.TrimRight(w, ".:,;!?")
			if tid, ok := vocab[w]; ok {
				inputIDs = append(inputIDs, tid)
			} else {
				inputIDs = append(inputIDs, 100) // [UNK]
			}
		}
	}

	inputIDs = append(inputIDs, sepToken)

	// Pad to maxLen
	for len(inputIDs) < maxLen {
		inputIDs = append(inputIDs, padToken)
	}

	return inputIDs[:maxLen]
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

// argMax returns the index and value of the largest element.
// Returns -1, 0.0 for empty slices.
func argMax(values []float32) (int, float64) {
	if len(values) == 0 {
		return -1, 0.0
	}
	bestIdx := 0
	bestVal := values[0]
	for i, v := range values[1:] {
		if v > bestVal {
			bestVal = v
			bestIdx = i + 1
		}
	}
	return bestIdx, float64(bestVal)
}

// IsBelowThreshold reports whether a confidence value is below the hard floor.
func IsBelowThreshold(confidence float64) bool {
	return confidence < onnxConfidenceThreshold
}

// Threshold returns the hard confidence floor (0.95).
func Threshold() float64 {
	return onnxConfidenceThreshold
}
