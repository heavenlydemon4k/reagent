// Package parse transforms raw MIME email into structured ParsedEmail.
// This file handles attachment extraction, S3 upload with SSE-KMS encryption,
// and asynchronous OCR triggering for images and scanned PDFs.
package parse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/decisionstack/ingestion/internal/models"
	s3client "github.com/decisionstack/ingestion/internal/s3"
)

// ocrRequestPayload is the JSON body sent to the OCR microservice.
type ocrRequestPayload struct {
	EmailID   string `json:"email_id"`
	S3URI     string `json:"s3_uri"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"` // "image" | "pdf_scanned"
}

// AttachmentExtractor handles uploading attachments to S3 and triggering
// OCR for image/scanned-PDF content.
type AttachmentExtractor struct {
	s3          *s3client.Client
	ocrEndpoint string
	log         *slog.Logger
}

// NewAttachmentExtractor creates a new AttachmentExtractor.
func NewAttachmentExtractor(s3 *s3client.Client, ocrEndpoint string) *AttachmentExtractor {
	return &AttachmentExtractor{
		s3:          s3,
		ocrEndpoint: ocrEndpoint,
		log:         slog.Default().WithGroup("attachment-extractor"),
	}
}

// Extract uploads each attachment to S3 and returns model Attachment structs.
// OCR is triggered asynchronously for images and scanned PDFs — it does NOT
// block the extraction pipeline.
func (ae *AttachmentExtractor) Extract(
	ctx context.Context,
	userID uuid.UUID,
	emailID uuid.UUID,
	attachments []MIMEAttachment,
) ([]models.Attachment, error) {
	if len(attachments) == 0 {
		return nil, nil
	}

	result := make([]models.Attachment, 0, len(attachments))
	var wg sync.WaitGroup
	var ocrErrors []error
	var mu sync.Mutex

	for _, att := range attachments {
		att := att // capture range variable

		// Upload to S3 with SSE-KMS.
		s3URI, err := ae.s3.UploadAttachment(
			ctx,
			userID,
			emailID,
			att.Filename,
			att.Data,
			att.ContentType,
		)
		if err != nil {
			ae.log.Error("failed to upload attachment to S3",
				"filename", att.Filename,
				"error", err,
			)
			// Continue with other attachments; partial failure is acceptable.
			continue
		}

		modelAtt := models.Attachment{
			Filename:    att.Filename,
			ContentType: att.ContentType,
			Size:        att.Size,
			S3URI:       s3URI,
			IsInline:    att.IsInline,
		}
		result = append(result, modelAtt)

		// Trigger OCR asynchronously for eligible types.
		mediaType := classifyForOCR(att.ContentType, att.Filename, att.Data)
		if mediaType != "" {
			wg.Add(1)
			go func(uri, filename, mtype string) {
				defer wg.Done()
				if err := ae.triggerOCR(ctx, emailID, uri, filename, mtype); err != nil {
					mu.Lock()
					ocrErrors = append(ocrErrors, fmt.Errorf(
						"OCR trigger failed for %s: %w", filename, err,
					))
					mu.Unlock()
				}
			}(s3URI, att.Filename, mediaType)
		}
	}

	// Wait for all OCR triggers to complete, but don't block return of results.
	wg.Wait()

	if len(ocrErrors) > 0 {
		ae.log.Warn("some OCR triggers failed", "count", len(ocrErrors))
		// OCR failures are non-fatal; attachments are still uploaded.
	}

	return result, nil
}

// classifyForOCR determines whether an attachment should be sent to OCR
// and what media type label to use.
// Returns empty string if OCR should not be triggered.
func classifyForOCR(contentType, filename string, data []byte) string {
	ct := strings.ToLower(contentType)
	fn := strings.ToLower(filename)

	// Image types: PNG, JPG/JPEG, GIF, WEBP.
	if strings.HasPrefix(ct, "image/") {
		switch {
		case strings.Contains(ct, "png"):
			return "image"
		case strings.Contains(ct, "jpeg") || strings.Contains(ct, "jpg"):
			return "image"
		case strings.Contains(ct, "gif"):
			return "image"
		case strings.Contains(ct, "webp"):
			return "image"
		}
	}

	// PDF: needs text-layer check.
	if strings.HasPrefix(ct, "application/pdf") || strings.HasSuffix(fn, ".pdf") {
		if isScannedPDF(data) {
			return "pdf_scanned"
		}
		// PDF with text layer: no OCR needed.
		return ""
	}

	return ""
}

// isScannedPDF checks whether a PDF contains a text layer by looking for
// text-related PDF operators. This is a lightweight heuristic — if no
// text operators (Tj, TJ, ') are found within the first N bytes, the PDF
// is assumed to be image-based (scanned) and needs OCR.
func isScannedPDF(data []byte) bool {
	if len(data) < 8 {
		return false
	}

	// Check PDF magic number.
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		return false
	}

	// Search for text operators in the first 256KB of the PDF.
	scanLimit := minInt(len(data), 256*1024)
	scanRegion := data[:scanLimit]

	// Common text operators in PDFs with text layers.
	textOperators := [][]byte{
		[]byte("Tj"),
		[]byte("TJ"),
		[]byte("BT"), // begin text
	}

	for _, op := range textOperators {
		if bytes.Contains(scanRegion, op) {
			return false // Has text layer — not scanned.
		}
	}

	return true // No text operators found — likely scanned.
}

// triggerOCR sends an async request to the OCR microservice.
// It is non-blocking: uses a short timeout and does not retry.
func (ae *AttachmentExtractor) triggerOCR(
	ctx context.Context,
	emailID uuid.UUID,
	s3URI, filename, mediaType string,
) error {
	payload := ocrRequestPayload{
		EmailID:   emailID.String(),
		S3URI:     s3URI,
		Filename:  filename,
		MediaType: mediaType,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal OCR request: %w", err)
	}

	// Use a short timeout — OCR is async and should not block parsing.
	ocrCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ocrURL := ae.ocrEndpoint + "/extract"
	req, err := http.NewRequestWithContext(ocrCtx, http.MethodPost, ocrURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create OCR request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("OCR request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("OCR service returned status %d", resp.StatusCode)
	}

	ae.log.Debug("OCR triggered successfully",
		"email_id", emailID,
		"filename", filename,
		"media_type", mediaType,
	)
	return nil
}

// minInt returns the smaller of a and b.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
