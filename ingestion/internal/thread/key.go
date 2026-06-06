// Package thread provides thread reconstruction for the Ingestion Mesh.
// key.go implements deterministic thread_key generation via SHA-256.
package thread

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// GenerateThreadKey produces a deterministic, hex-encoded SHA-256 hash
// from the sorted participant emails and the normalized subject.
//
// Algorithm:
//  1. Sort participant emails (case-insensitive ascending).
//  2. Normalize subject: lowercase, strip re:/fwd:/fw:/[external], collapse whitespace.
//  3. Concatenate: "email1,email2,email3|normalized_subject"
//  4. SHA-256 hash the concatenated string, hex-encode.
//
// The key is deterministic: the same set of participants and subject
// root will always produce the same output.
func GenerateThreadKey(participantEmails []string, subject string) string {
	// 1. Sort participant emails (case-insensitive, in-place copy)
	emails := make([]string, len(participantEmails))
	copy(emails, participantEmails)
	sort.Slice(emails, func(i, j int) bool {
		return strings.ToLower(emails[i]) < strings.ToLower(emails[j])
	})

	// Deduplicate while preserving order
	deduped := make([]string, 0, len(emails))
	seen := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		le := strings.ToLower(strings.TrimSpace(e))
		if le == "" {
			continue
		}
		if _, ok := seen[le]; !ok {
			seen[le] = struct{}{}
			deduped = append(deduped, le)
		}
	}

	// 2. Normalize subject
	normSubject := NormalizeSubjectForKey(subject)

	// 3. Concatenate: "email1,email2|normalized_subject"
	var sb strings.Builder
	for i, e := range deduped {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(e)
	}
	sb.WriteByte('|')
	sb.WriteString(normSubject)

	input := sb.String()

	// 4. SHA-256 hash, hex-encode
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}
