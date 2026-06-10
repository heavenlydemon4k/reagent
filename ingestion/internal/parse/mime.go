// Package parse transforms raw MIME email into structured ParsedEmail.
// This file handles MIME parsing: headers, multipart body extraction,
// and recursive MIME part traversal using net/mail, mime/multipart,
// and mime/quotedprintable from the standard library.
package parse

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"unicode"
)

// MIMEAttachment holds raw attachment data extracted from a MIME part.
// This is an intermediate representation before S3 upload and model persistence.
type MIMEAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
	Size        int64
	IsInline    bool
	ContentID   string
}

// MIMEResult is the output of parsing a raw MIME email. It contains all
// headers, body parts (text and HTML), and attachments extracted from
// the message.
type MIMEResult struct {
	// Headers contains all raw MIME headers.
	Headers map[string][]string

	// BodyText is the plain-text body (from text/plain or converted HTML).
	BodyText string

	// BodyHTML is the original HTML body (empty if none).
	BodyHTML string

	// Attachments are all non-inline file attachments.
	Attachments []MIMEAttachment

	// Inlines are inline image/content parts (e.g., embedded images).
	Inlines []MIMEAttachment

	// Threading-related headers.
	MessageID  string
	InReplyTo  string
	References []string

	// Sender / recipient headers.
	FromEmail string
	FromName  string
	ToEmails  []string
	CcEmails  []string
	Subject   string
}

// MIMEParser parses raw MIME email into a structured MIMEResult.
type MIMEParser struct{}

// NewMIMEParser creates a new MIMEParser.
func NewMIMEParser() *MIMEParser {
	return &MIMEParser{}
}

// Parse parses a raw MIME email and extracts headers, body parts,
// and attachments.
//
// It handles:
//   - multipart/alternative: text + HTML variants
//   - multipart/mixed: body + attachments
//   - multipart/related: HTML + inline resources (images, CSS)
//   - text/plain: plain text body
//   - text/html: HTML body
//   - Nested multipart structures (recursively)
//   - Content-Transfer-Encoding: base64, quoted-printable, 7bit, 8bit, binary
func (p *MIMEParser) Parse(rawMIME []byte) (*MIMEResult, error) {
	result := &MIMEResult{
		Headers:    make(map[string][]string),
		References: make([]string, 0),
		ToEmails:   make([]string, 0),
		CcEmails:   make([]string, 0),
	}

	msg, err := mail.ReadMessage(bytes.NewReader(rawMIME))
	if err != nil {
		return nil, fmt.Errorf("failed to parse MIME message: %w", err)
	}

	// === Extract all headers ===
	for key, vals := range msg.Header {
		result.Headers[key] = vals
	}

	// === Threading headers ===
	result.MessageID = strings.TrimSpace(msg.Header.Get("Message-Id"))
	if result.MessageID == "" {
		result.MessageID = strings.TrimSpace(msg.Header.Get("Message-ID"))
	}
	result.InReplyTo = strings.TrimSpace(msg.Header.Get("In-Reply-To"))

	refsHeader := msg.Header.Get("References")
	if refsHeader != "" {
		result.References = splitMessageIDs(refsHeader)
	}

	// === Sender / Recipients ===
	if fromHdr := msg.Header.Get("From"); fromHdr != "" {
		fromAddr, err := mail.ParseAddress(decodeHeader(fromHdr))
		if err == nil && fromAddr != nil {
			result.FromEmail = fromAddr.Address
			result.FromName = fromAddr.Name
		} else {
			result.FromEmail = extractEmail(fromHdr)
		}
	}

	if toHdr := msg.Header.Get("To"); toHdr != "" {
		toAddrs, err := mail.ParseAddressList(decodeHeader(toHdr))
		if err == nil {
			for _, addr := range toAddrs {
				if addr.Address != "" {
					result.ToEmails = append(result.ToEmails, addr.Address)
				}
			}
		} else {
			result.ToEmails = extractEmails(toHdr)
		}
	}

	if ccHdr := msg.Header.Get("Cc"); ccHdr != "" {
		ccAddrs, err := mail.ParseAddressList(decodeHeader(ccHdr))
		if err == nil {
			for _, addr := range ccAddrs {
				if addr.Address != "" {
					result.CcEmails = append(result.CcEmails, addr.Address)
				}
			}
		} else {
			result.CcEmails = extractEmails(ccHdr)
		}
	}

	// Subject (decode RFC 2047).
	result.Subject = decodeHeader(msg.Header.Get("Subject"))

	// === Body extraction ===
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	// Read the raw body.
	bodyRaw, err := io.ReadAll(msg.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read message body: %w", err)
	}

	// Handle content transfer decoding.
	transferEncoding := msg.Header.Get("Content-Transfer-Encoding")
	bodyDecoded, err := decodeTransferEncoding(bodyRaw, transferEncoding)
	if err != nil {
		// If decoding fails, use raw body as fallback.
		bodyDecoded = bodyRaw
	}

	// Parse media type.
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Fallback: treat as plain text.
		mediaType = "text/plain"
		params = nil
	}

	// Route to appropriate handler based on content type.
	if strings.HasPrefix(mediaType, "multipart/") {
		p.parseMultipart(bodyDecoded, mediaType, params, msg.Header, result)
	} else {
		p.parseSinglePart(bodyDecoded, mediaType, params, result)
	}

	return result, nil
}

// parseMultipart handles multipart/* messages recursively.
func (p *MIMEParser) parseMultipart(body []byte, mediaType string, params map[string]string, header mail.Header, result *MIMEResult) {
	boundary := params["boundary"]
	if boundary == "" {
		// No boundary found; treat entire body as plain text.
		result.BodyText = string(body)
		return
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Partial parse; continue with what we have.
			continue
		}

		p.processPart(part, result)
	}
}

// processPart handles a single MIME part (could be nested multipart).
func (p *MIMEParser) processPart(part *multipart.Part, result *MIMEResult) {
	contentType := part.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = "text/plain"
		params = nil
	}

	// Read part body.
	partRaw, err := io.ReadAll(part)
	if err != nil {
		return
	}

	// Handle content transfer encoding for the part.
	transferEncoding := part.Header.Get("Content-Transfer-Encoding")
	partDecoded, err := decodeTransferEncoding(partRaw, transferEncoding)
	if err != nil {
		partDecoded = partRaw
	}

	// Check disposition.
	disposition, dispParams, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	contentID := part.Header.Get("Content-Id")
	if contentID == "" {
		contentID = part.Header.Get("Content-ID")
	}

	// Route based on content type and disposition.
	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		// Nested multipart: recurse.
		p.parseMultipart(partDecoded, mediaType, params, nil, result)

	case isAttachment(disposition, dispParams):
		filename := getFilename(disposition, dispParams, mediaType, params)
		att := MIMEAttachment{
			Filename:    filename,
			ContentType: mediaType,
			Data:        partDecoded,
			Size:        int64(len(partDecoded)),
			IsInline:    disposition == "inline",
			ContentID:   contentID,
		}
		if disposition == "inline" {
			result.Inlines = append(result.Inlines, att)
		} else {
			result.Attachments = append(result.Attachments, att)
		}

	case mediaType == "text/plain":
		// Only take the first text/plain body.
		if result.BodyText == "" {
			charset := strings.ToLower(params["charset"])
			result.BodyText = decodeCharset(string(partDecoded), charset)
		}

	case mediaType == "text/html":
		// Prefer the HTML body.
		charset := strings.ToLower(params["charset"])
		result.BodyHTML = decodeCharset(string(partDecoded), charset)

	default:
		// Unknown part: treat as attachment if it has a filename, else inline.
		filename := getFilename(disposition, dispParams, mediaType, params)
		if filename != "" {
			att := MIMEAttachment{
				Filename:    filename,
				ContentType: mediaType,
				Data:        partDecoded,
				Size:        int64(len(partDecoded)),
				IsInline:    false,
				ContentID:   contentID,
			}
			result.Attachments = append(result.Attachments, att)
		} else if len(partDecoded) > 0 {
			// Inline content (e.g., calendar invites, embedded XML).
			result.Inlines = append(result.Inlines, MIMEAttachment{
				Filename:    filename,
				ContentType: mediaType,
				Data:        partDecoded,
				Size:        int64(len(partDecoded)),
				IsInline:    true,
				ContentID:   contentID,
			})
		}
	}
}

// parseSinglePart handles non-multipart messages.
func (p *MIMEParser) parseSinglePart(body []byte, mediaType string, params map[string]string, result *MIMEResult) {
	charset := ""
	if params != nil {
		charset = strings.ToLower(params["charset"])
	}

	switch {
	case mediaType == "text/html":
		result.BodyHTML = decodeCharset(string(body), charset)
		// Also set BodyText as a fallback.
		result.BodyText = decodeCharset(string(body), charset)
	default:
		// text/plain or any other type.
		result.BodyText = decodeCharset(string(body), charset)
	}
}

// isAttachment determines if a MIME part is an attachment based on
// Content-Disposition header.
func isAttachment(disposition string, params map[string]string) bool {
	if disposition == "attachment" || disposition == "inline" {
		return true
	}
	// If there's a filename parameter, treat as attachment.
	if params != nil && params["filename"] != "" {
		return true
	}
	return false
}

// getFilename extracts the filename from Content-Disposition or Content-Type params.
func getFilename(disposition string, dispParams map[string]string, mediaType string, typeParams map[string]string) string {
	// Try Content-Disposition filename first.
	if dispParams != nil {
		if fn := dispParams["filename"]; fn != "" {
			return decodeHeader(fn)
		}
		if fn := dispParams["filename*"]; fn != "" {
			return decodeRFC5987(fn)
		}
	}

	// Try Content-Type name parameter.
	if typeParams != nil {
		if name := typeParams["name"]; name != "" {
			return decodeHeader(name)
		}
	}

	// Generate a filename from the Content-Type.
	ext := ".bin"
	if mediaType != "" {
		switch mediaType {
		case "text/plain":
			ext = ".txt"
		case "text/html":
			ext = ".html"
		case "image/png":
			ext = ".png"
		case "image/jpeg":
			ext = ".jpg"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		case "application/pdf":
			ext = ".pdf"
		case "application/msword":
			ext = ".doc"
		case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
			ext = ".docx"
		}
	}
	return "attachment" + ext
}

// decodeTransferEncoding decodes content based on Content-Transfer-Encoding.
func decodeTransferEncoding(data []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(encoding) {
	case "base64":
		return base64.StdEncoding.DecodeString(string(data))
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
	case "", "7bit", "8bit", "binary":
		return data, nil
	default:
		return data, nil
	}
}

// decodeHeader decodes RFC 2047 encoded header values (e.g., =?UTF-8?Q?...?=).
func decodeHeader(header string) string {
	if header == "" {
		return ""
	}
	decoded, err := (&mime.WordDecoder{}).DecodeHeader(header)
	if err != nil {
		return header
	}
	return decoded
}

// decodeRFC5987 decodes RFC 5987 encoded filename parameters (e.g., filename*=UTF-8''...).
func decodeRFC5987(s string) string {
	// Format: charset'lang'value or charset''value
	parts := strings.SplitN(s, "'", 3)
	if len(parts) < 3 {
		return s
	}
	value := parts[2]
	// URL-decode the value.
	return unescapePercent(value)
}

// unescapePercent performs percent-decoding on a string.
func unescapePercent(s string) string {
	var result strings.Builder
	result.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			b, err := decodeHexByte(s[i+1], s[i+2])
			if err == nil {
				result.WriteByte(b)
				i += 2
				continue
			}
		}
		result.WriteByte(s[i])
	}
	return result.String()
}

// decodeHexByte decodes two hex characters into a byte.
func decodeHexByte(h1, h2 byte) (byte, error) {
	b1 := hexValue(h1)
	b2 := hexValue(h2)
	if b1 < 0 || b2 < 0 {
		return 0, fmt.Errorf("invalid hex chars: %c%c", h1, h2)
	}
	return byte(b1<<4 | b2), nil
}

// hexValue converts a hex character to its numeric value.
func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'F':
		return int(c - 'A' + 10)
	case c >= 'a' && c <= 'f':
		return int(c - 'a' + 10)
	}
	return -1
}

// decodeCharset handles common charset conversions.
// For charsets beyond UTF-8 and ASCII, the raw bytes are returned
// (Go source is always UTF-8; explicit conversion would require
// golang.org/x/text/encoding which is not in go.mod).
func decodeCharset(s, charset string) string {
	switch charset {
	case "", "utf-8", "us-ascii", "ascii":
		return s
	default:
		// If we can't decode the charset, return as-is.
		// In production, add golang.org/x/text/encoding for full charset support.
		return s
	}
}

// splitMessageIDs splits a References header value into individual
// message IDs. Handles both space-separated and angle-bracket formats.
func splitMessageIDs(refs string) []string {
	var ids []string
	parts := strings.Fields(refs)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Strip angle brackets.
		part = strings.Trim(part, "<>")
		if part != "" {
			ids = append(ids, part)
		}
	}
	return ids
}

// extractEmail extracts the first email address found in a string.
func extractEmail(s string) string {
	// Strip display name, keep angle-bracketed address.
	s = decodeHeader(s)
	if idx := strings.Index(s, "<"); idx >= 0 {
		if end := strings.Index(s[idx:], ">"); end > 0 {
			candidate := strings.TrimSpace(s[idx+1 : idx+end])
			if strings.Contains(candidate, "@") {
				return candidate
			}
		}
	}
	s = strings.Trim(s, "<>")
	if strings.Contains(s, "@") {
		return s
	}
	return ""
}

// extractEmails extracts all email addresses from a comma-separated string.
func extractEmails(s string) []string {
	s = decodeHeader(s)
	addrs, err := mail.ParseAddressList(s)
	if err != nil {
		// Manual extraction as last resort.
		var emails []string
		parts := strings.Split(s, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			p = strings.Trim(p, "<>")
			if strings.Contains(p, "@") {
				// Strip any remaining display name.
				if idx := strings.LastIndex(p, " "); idx > 0 {
					after := strings.TrimSpace(p[idx:])
					if strings.HasPrefix(after, "<") {
						p = strings.Trim(after, "<>")
					}
				}
				emails = append(emails, p)
			}
		}
		return emails
	}
	var result []string
	for _, a := range addrs {
		if a.Address != "" {
			result = append(result, a.Address)
		}
	}
	return result
}

// isPrintableASCII reports whether s contains only printable ASCII characters.
func isPrintableASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII || (!unicode.IsPrint(r) && !unicode.IsSpace(r)) {
			return false
		}
	}
	return true
}
