// Package parse transforms raw MIME email into structured ParsedEmail.
// This file contains the main parsing orchestrator that coordinates
// MIME parsing, HTML-to-text conversion, signature stripping,
// attachment extraction, code extraction, and raw-blob S3 upload.
package parse

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/models"
	s3client "github.com/decisionstack/ingestion/internal/s3"
)

// Parser is the main email parsing orchestrator. It coordinates all
// sub-parsers (MIME, HTML, signature, attachment, codes) and manages
// S3 upload of the raw email blob.
type Parser struct {
	s3            *s3client.Client
	ocrEndpoint   string
	sigClassifier *SignatureClassifier
	log           *slog.Logger
}

// NewParser creates a new Parser from configuration and an S3 client.
// It loads the ONNX signature classifier if available; otherwise it
// falls back to regex-based signature detection.
func NewParser(cfg *config.Config, s3Client *s3client.Client) *Parser {
	log := slog.Default().WithGroup("parser")

	// Attempt to load the signature classifier; fallback is automatic.
	sigClassifier, err := NewSignatureClassifier(defaultModelPath)
	if err != nil {
		log.Warn("signature classifier init failed; using regex fallback", "error", err)
		sigClassifier, _ = NewSignatureClassifier("") // forces fallback mode
	}

	return &Parser{
		s3:            s3Client,
		ocrEndpoint:   cfg.OCREndpoint,
		sigClassifier: sigClassifier,
		log:           log,
	}
}

// Close releases resources held by the parser (e.g., ONNX session).
func (p *Parser) Close() error {
	if p.sigClassifier != nil {
		return p.sigClassifier.Close()
	}
	return nil
}

// Parse transforms a raw MIME email into a structured ParsedEmail.
//
// Pipeline:
//  1. Parse MIME headers and body parts (parse/mime.go)
//  2. Extract threading headers (Message-ID, InReplyTo, References)
//  3. Convert HTML → plain text (parse/html.go)
//  4. Strip signature blocks (parse/signature.go)
//  5. Extract attachments + upload to S3 (parse/attachment.go)
//  6. Extract 2FA codes and tracking numbers (parse/codes.go)
//  7. Upload raw MIME blob to S3 (immutable source of truth)
//  8. Assemble and return ParsedEmail
//
// INVARIANT: The raw email body in S3 is the immutable source of truth.
// All parsed fields (BodyText, BodyHTML, stripped signatures) are derivative.
func (p *Parser) Parse(
	ctx context.Context,
	rawMIME []byte,
	userID uuid.UUID,
	accountID uuid.UUID,
	receivedAt time.Time,
) (*models.ParsedEmail, error) {
	if len(rawMIME) == 0 {
		return nil, &models.IngestionError{
			Code:    models.ErrCodeParseFailed,
			Message: "empty MIME data",
			UserID:  userID.String(),
			Retry:   false,
		}
	}

	// Generate deterministic ID for this parsed email.
	emailID := uuid.New()

	// Step 1: Parse MIME.
	mimeParser := NewMIMEParser()
	mimeResult, err := mimeParser.Parse(rawMIME)
	if err != nil {
		p.log.Error("MIME parsing failed", "error", err, "user_id", userID)
		return nil, &models.IngestionError{
			Code:    models.ErrCodeParseFailed,
			Message: fmt.Sprintf("MIME parsing failed: %v", err),
			UserID:  userID.String(),
			Retry:   true,
		}
	}

	// Step 2: Threading headers already extracted in MIME parsing.
	// Validate Message-ID presence.
	if mimeResult.MessageID == "" {
		p.log.Warn("email has no Message-ID; generating synthetic one",
			"user_id", userID,
		)
		// Generate a synthetic Message-ID for threading continuity.
		mimeResult.MessageID = fmt.Sprintf("<%s@generated>", emailID.String())
	}

	// Step 3: Convert HTML → text.
	htmlConverter := NewHTMLConverter()
	bodyText, bodyHTML, err := htmlConverter.ConvertAndJoin(
		mimeResult.BodyHTML,
		mimeResult.BodyText,
	)
	if err != nil {
		p.log.Warn("HTML conversion failed; using raw text parts",
			"error", err,
			"user_id", userID,
		)
		// Fallback: use whatever body parts we have.
		bodyText = mimeResult.BodyText
		bodyHTML = mimeResult.BodyHTML
	}

	// Step 4: Strip signatures from the text body.
	cleanedText, strippedSigs, err := p.sigClassifier.StripSignatures(bodyText)
	if err != nil {
		p.log.Warn("signature stripping failed; using uncleaned text",
			"error", err,
		)
		cleanedText = bodyText
	} else if len(strippedSigs) > 0 {
		p.log.Debug("stripped signatures",
			"count", len(strippedSigs),
			"user_id", userID,
		)
	}

	// Step 5: Extract attachments + upload to S3.
	// Combine file attachments and inline parts.
	allParts := append(mimeResult.Attachments, mimeResult.Inlines...)

	attachmentExtractor := NewAttachmentExtractor(
		p.s3, p.ocrEndpoint,
	)
	attachments, err := attachmentExtractor.Extract(ctx, userID, emailID, allParts)
	if err != nil {
		p.log.Error("attachment extraction failed",
			"error", err,
			"user_id", userID,
		)
		// Attachment extraction failure is non-fatal; continue parsing.
		attachments = nil
	}

	// Step 6: Extract 2FA codes and tracking numbers.
	// Extract from BOTH cleaned text and original text (codes may be in signatures).
	codeExtractor := NewCodeExtractor()
	codes := codeExtractor.ExtractStrings(cleanedText)
	// Also check original body text in case codes were in stripped signatures.
	codesFromOrig := codeExtractor.ExtractStrings(bodyText)
	// Deduplicate while preserving order.
	codes = deduplicateStrings(append(codes, codesFromOrig...))

	// INVARIANT: 2FA codes are NEVER logged.
	if len(codes) > 0 {
		p.log.Debug("extracted codes",
			"count", len(codes),
			"user_id", userID,
		)
	}

	// Step 7: Upload raw MIME blob to S3 (immutable source of truth).
	rawS3URI, err := p.s3.UploadRawEmail(ctx, userID, emailID, rawMIME)
	if err != nil {
		p.log.Error("failed to upload raw email to S3",
			"error", err,
			"user_id", userID,
		)
		// S3 upload failure is retryable.
		return nil, &models.IngestionError{
			Code:    models.ErrCodeParseFailed,
			Message: fmt.Sprintf("raw email S3 upload failed: %v", err),
			UserID:  userID.String(),
			Retry:   true,
		}
	}

	// Step 8: Assemble ParsedEmail.
	var inReplyToPtr *string
	if mimeResult.InReplyTo != "" {
		inReplyToPtr = &mimeResult.InReplyTo
	}

	parsed := &models.ParsedEmail{
		ID:              emailID,
		UserID:          userID,
		AccountID:       accountID,
		Source:          detectSource(mimeResult.Headers),
		MessageID:       mimeResult.MessageID,
		InReplyTo:       inReplyToPtr,
		References:      mimeResult.References,
		SenderEmail:     mimeResult.FromEmail,
		SenderName:      mimeResult.FromName,
		RecipientEmails: mimeResult.ToEmails,
		Subject:         mimeResult.Subject,
		BodyText:        cleanedText,
		BodyHTML:        bodyHTML,
		HasAttachments:  len(attachments) > 0,
		Attachments:     attachments,
		ExtractedCodes:  codes,
		ReceivedAt:      receivedAt,
		S3URI:           rawS3URI,
		ThreadHint:      buildThreadHint(mimeResult),
	}

	p.log.Info("email parsed successfully",
		"email_id", emailID,
		"user_id", userID,
		"message_id", parsed.MessageID,
		"has_attachments", parsed.HasAttachments,
		"codes_extracted", len(codes),
	)

	return parsed, nil
}

// detectSource determines the email provider (gmail, outlook) from
// headers like X-Google-Smtp-Source, Received, X-Mailer.
func detectSource(headers map[string][]string) string {
	for key, vals := range headers {
		lowerKey := strings.ToLower(key)
		for _, val := range vals {
			lowerVal := strings.ToLower(val)

			// Gmail indicators.
			if strings.Contains(lowerKey, "google") ||
				strings.Contains(lowerVal, "google") ||
				strings.Contains(lowerVal, "gmail") ||
				strings.Contains(lowerVal, "gsmtp") {
				return "gmail"
			}

			// Outlook / Microsoft indicators.
			if strings.Contains(lowerKey, "microsoft") ||
				strings.Contains(lowerKey, "outlook") ||
				strings.Contains(lowerVal, "outlook.com") ||
				strings.Contains(lowerVal, "hotmail") ||
				strings.Contains(lowerVal, "office365") ||
				strings.Contains(lowerVal, "microsoft") {
				return "outlook"
			}
		}
	}

	// Default.
	return "unknown"
}

// buildThreadHint creates a ThreadHint from MIME result for the
// threading engine.
func buildThreadHint(mimeResult *MIMEResult) *models.ThreadHint {
	if mimeResult.InReplyTo == "" && len(mimeResult.References) == 0 {
		return nil
	}
	return &models.ThreadHint{
		InReplyTo:  mimeResult.InReplyTo,
		References: mimeResult.References,
		Subject:    mimeResult.Subject,
	}
}

// deduplicateStrings removes duplicates from a string slice while
// preserving order.
func deduplicateStrings(s []string) []string {
	seen := make(map[string]bool, len(s))
	result := make([]string, 0, len(s))
	for _, v := range s {
		if !seen[v] && v != "" {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
