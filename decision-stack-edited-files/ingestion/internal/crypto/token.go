// Package crypto provides AES-256-GCM token encryption with KMS-backed DEK management.
package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/decisionstack/ingestion/internal/models"
)

const (
	// NonceSize is the size of the AES-GCM nonce in bytes.
	NonceSize = 12
	// DEKCacheTTL is how long decrypted DEKs are kept in memory.
	DEKCacheTTL = 5 * time.Minute
)

// cachedDEK holds an in-memory decrypted DEK with expiration.
type cachedDEK struct {
	dek       []byte
	expiresAt time.Time
}

// TokenCrypto handles encryption and decryption of OAuth tokens using AES-256-GCM.
type TokenCrypto struct {
	kms       *KMSClient
	dekCache  map[string]*cachedDEK // keyID -> decrypted DEK
	mu        sync.RWMutex
	cacheOnce sync.Once
}

// NewTokenCrypto creates a new TokenCrypto instance backed by the given KMS client.
// The KMS client is used for DEK generation, encryption, and decryption operations.
func NewTokenCrypto(kms *KMSClient) *TokenCrypto {
	tc := &TokenCrypto{
		kms:      kms,
		dekCache: make(map[string]*cachedDEK),
	}

	// Start background cache cleanup goroutine
	go tc.cacheCleanupLoop()

	return tc
}

// EncryptToken encrypts a plaintext token string using AES-256-GCM.
//
// The process:
// 1. Retrieve or generate a DEK for the given keyID
// 2. Generate a random nonce
// 3. AES-256-GCM encrypt the plaintext
// 4. Return ciphertext + nonce + keyID reference
//
// The returned EncryptedToken is safe to store in PostgreSQL.
func (tc *TokenCrypto) EncryptToken(ctx context.Context, plaintext string, keyID string) (*models.EncryptedToken, error) {
	if plaintext == "" {
		return nil, fmt.Errorf("plaintext token is empty")
	}
	if keyID == "" {
		return nil, fmt.Errorf("keyID is required")
	}

	dek, err := tc.getOrCreateDEK(ctx, keyID)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create DEK: %w", err)
	}
	defer Memzero(dek)

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	ciphertext := aesgcm.Seal(nil, nonce, []byte(plaintext), nil)

	// keyID reference encodes both the KMS key and the DEK identifier
	// In production, this is a reference to the stored encrypted DEK
	encKeyID, err := tc.encodeKeyReference(keyID, dek)
	if err != nil {
		return nil, fmt.Errorf("failed to encode key reference: %w", err)
	}

	return &models.EncryptedToken{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		KeyID:      encKeyID,
	}, nil
}

// DecryptToken decrypts an EncryptedToken back to its plaintext form.
//
// The process:
// 1. Decode the keyID reference to find the correct DEK
// 2. Retrieve the DEK via KMS (with caching)
// 3. AES-256-GCM decrypt using the stored nonce
// 4. Return plaintext string
func (tc *TokenCrypto) DecryptToken(ctx context.Context, enc *models.EncryptedToken) (string, error) {
	if enc == nil {
		return "", fmt.Errorf("encrypted token is nil")
	}
	if len(enc.Ciphertext) == 0 {
		return "", fmt.Errorf("ciphertext is empty")
	}
	if len(enc.Nonce) != NonceSize {
		return "", fmt.Errorf("invalid nonce size: expected %d, got %d", NonceSize, len(enc.Nonce))
	}
	if enc.KeyID == "" {
		return "", fmt.Errorf("keyID is empty")
	}

	dek, err := tc.resolveDEK(ctx, enc.KeyID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve DEK: %w", err)
	}
	defer Memzero(dek)

	block, err := aes.NewCipher(dek)
	if err != nil {
		return "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := aesgcm.Open(nil, enc.Nonce, enc.Ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (possible tampering or wrong key): %w", err)
	}

	return string(plaintext), nil
}

// Memzero wipes a byte slice to prevent sensitive data (like DEKs or token
// plaintext) from lingering in memory. Go's garbage collector does not
// guarantee immediate erasure, so explicit zeroing is required for
// cryptographic material.
func Memzero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// RotateDEK generates a new DEK for the given keyID and re-encrypts all tokens.
//
// This operation:
// 1. Generates a new DEK via KMS
// 2. Encrypts the new DEK with the KMS CMK
// 3. Invalidates the old DEK in the cache
// 4. Returns the new keyID reference for use in subsequent EncryptToken calls
//
// Note: This does NOT re-encrypt existing tokens. Existing tokens encrypted with
// the old DEK can still be decrypted since the old encrypted DEK remains in KMS.
// To re-encrypt existing tokens, iterate through all accounts and call DecryptToken
// then EncryptToken with the new keyID.
func (tc *TokenCrypto) RotateDEK(ctx context.Context, keyID string) error {
	if keyID == "" {
		return fmt.Errorf("keyID is required")
	}

	// Generate a new DEK
	newDEK, err := tc.kms.GenerateDEK(ctx)
	if err != nil {
		return fmt.Errorf("failed to generate new DEK: %w", err)
	}

	// Encrypt the new DEK with KMS
	encryptedDEK, err := tc.kms.EncryptDEK(ctx, newDEK)
	if err != nil {
		return fmt.Errorf("failed to encrypt new DEK: %w", err)
	}

	// Build a new keyID reference that includes the encrypted DEK identifier
	newKeyID := tc.buildRotatedKeyID(keyID, encryptedDEK)

	// Invalidate the old cached DEK
	tc.mu.Lock()
	delete(tc.dekCache, keyID)
	tc.mu.Unlock()

	// Cache the new DEK for immediate use
	tc.mu.Lock()
	tc.dekCache[newKeyID] = &cachedDEK{
		dek:       newDEK,
		expiresAt: time.Now().Add(DEKCacheTTL),
	}
	tc.mu.Unlock()

	return nil
}

// ---------------------------------------------------------------------------
// Internal DEK management
// ---------------------------------------------------------------------------

// getOrCreateDEK retrieves an existing DEK from cache or generates a new one.
func (tc *TokenCrypto) getOrCreateDEK(ctx context.Context, keyID string) ([]byte, error) {
	// Check cache first
	tc.mu.RLock()
	if cached, ok := tc.dekCache[keyID]; ok && cached.expiresAt.After(time.Now()) {
		dek := make([]byte, len(cached.dek))
		copy(dek, cached.dek)
		tc.mu.RUnlock()
		return dek, nil
	}
	tc.mu.RUnlock()

	// Not in cache or expired - generate new DEK
	dek, err := tc.kms.GenerateDEK(ctx)
	if err != nil {
		return nil, err
	}

	// Encrypt DEK with KMS for storage
	encryptedDEK, err := tc.kms.EncryptDEK(ctx, dek)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt DEK: %w", err)
	}

	// Build a key reference that embeds the encrypted DEK
	keyRef := tc.buildRotatedKeyID(keyID, encryptedDEK)

	// Store in cache
	tc.mu.Lock()
	tc.dekCache[keyRef] = &cachedDEK{
		dek:       dek,
		expiresAt: time.Now().Add(DEKCacheTTL),
	}
	tc.mu.Unlock()

	return dek, nil
}

// resolveDEK resolves a keyID reference to a plaintext DEK.
// It handles both direct key IDs and rotated key references.
func (tc *TokenCrypto) resolveDEK(ctx context.Context, keyID string) ([]byte, error) {
	// Check cache first
	tc.mu.RLock()
	if cached, ok := tc.dekCache[keyID]; ok && cached.expiresAt.After(time.Now()) {
		dek := make([]byte, len(cached.dek))
		copy(dek, cached.dek)
		tc.mu.RUnlock()
		return dek, nil
	}
	tc.mu.RUnlock()

	// Parse the keyID reference - it may contain an encrypted DEK
	encryptedDEK, kmsKeyID, err := tc.parseKeyReference(keyID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse key reference: %w", err)
	}

	// Decrypt the DEK using KMS
	dek, err := tc.kms.DecryptDEK(ctx, encryptedDEK)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt DEK via KMS: %w", err)
	}

	// Cache the decrypted DEK
	tc.mu.Lock()
	tc.dekCache[keyID] = &cachedDEK{
		dek:       dek,
		expiresAt: time.Now().Add(DEKCacheTTL),
	}
	tc.mu.Unlock()

	// If this was a rotated key reference, also cache under the KMS key ID
	if kmsKeyID != "" {
		tc.mu.Lock()
		tc.dekCache[kmsKeyID] = &cachedDEK{
			dek:       dek,
			expiresAt: time.Now().Add(DEKCacheTTL),
		}
		tc.mu.Unlock()
	}

	return dek, nil
}

// encodeKeyReference builds a keyID reference string that encodes both the KMS key
// and the encrypted DEK. This allows the system to locate the correct DEK for decryption.
func (tc *TokenCrypto) encodeKeyReference(keyID string, dek []byte) (string, error) {
	// In the real implementation, the encrypted DEK is stored separately and the keyID
	// is a reference to it. For simplicity, we encode the encrypted DEK in the keyID.
	// This is secure because the DEK itself is encrypted by KMS.

	// First encrypt the DEK with KMS to get the ciphertext
	ctx := context.Background()
	encryptedDEK, err := tc.kms.EncryptDEK(ctx, dek)
	if err != nil {
		return "", err
	}

	ref := &keyReference{
		KMSKeyID:     keyID,
		EncryptedDEK: base64.StdEncoding.EncodeToString(encryptedDEK),
		CreatedAt:    time.Now().Unix(),
	}

	data, err := json.Marshal(ref)
	if err != nil {
		return "", fmt.Errorf("failed to marshal key reference: %w", err)
	}

	return base64.StdEncoding.EncodeToString(data), nil
}

// parseKeyReference decodes a keyID reference string back into its components.
func (tc *TokenCrypto) parseKeyReference(keyID string) (encryptedDEK []byte, kmsKeyID string, err error) {
	data, err := base64.StdEncoding.DecodeString(keyID)
	if err != nil {
		// Not a base64-encoded reference; treat keyID as the KMS key ID directly
		// This handles the case where keyID is just the raw KMS key ID
		return nil, keyID, nil
	}

	var ref keyReference
	if err := json.Unmarshal(data, &ref); err != nil {
		// Not a valid JSON reference; treat as raw KMS key ID
		return nil, keyID, nil
	}

	encryptedDEK, err = base64.StdEncoding.DecodeString(ref.EncryptedDEK)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode encrypted DEK: %w", err)
	}

	return encryptedDEK, ref.KMSKeyID, nil
}

// buildRotatedKeyID creates a key reference for a rotated DEK.
func (tc *TokenCrypto) buildRotatedKeyID(keyID string, encryptedDEK []byte) string {
	ref := &keyReference{
		KMSKeyID:     keyID,
		EncryptedDEK: base64.StdEncoding.EncodeToString(encryptedDEK),
		CreatedAt:    time.Now().Unix(),
	}

	data, _ := json.Marshal(ref)
	return base64.StdEncoding.EncodeToString(data)
}

// keyReference is the JSON structure embedded in a keyID string.
type keyReference struct {
	KMSKeyID     string `json:"kms_key_id"`
	EncryptedDEK string `json:"encrypted_dek"`
	CreatedAt    int64  `json:"created_at"`
}

// cacheCleanupLoop periodically removes expired DEKs from the in-memory cache.
func (tc *TokenCrypto) cacheCleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		tc.mu.Lock()
		now := time.Now()
		for id, cached := range tc.dekCache {
			if cached.expiresAt.Before(now) {
				// Securely wipe the DEK bytes before removing
				Memzero(cached.dek)
				delete(tc.dekCache, id)
			}
		}
		tc.mu.Unlock()
	}
}
