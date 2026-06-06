// Package oauth tests secure token storage with mock dependencies.
package oauth

import (
	"testing"

	"github.com/decisionstack/ingestion/internal/crypto"
	"github.com/decisionstack/ingestion/internal/models"
	"github.com/google/uuid"
)

// TestNewTokenStore verifies TokenStore can be created.
func TestNewTokenStore(t *testing.T) {
	kms := &crypto.KMSClient{}
	tc := crypto.NewTokenCrypto(kms)
	defer tc.Close()

	// TokenStore requires a *sql.DB which we can't easily mock without sqlmock,
	// but we can verify the struct is well-formed.
	store := &TokenStore{
		crypto: tc,
	}

	if store == nil {
		t.Fatal("TokenStore is nil")
	}
	if store.crypto != tc {
		t.Error("TokenStore.crypto not set correctly")
	}
}

// TestIsEncrypted verifies the isEncrypted helper logic.
func TestIsEncrypted(t *testing.T) {
	kms := &crypto.KMSClient{}
	tc := crypto.NewTokenCrypto(kms)
	defer tc.Close()

	store := &TokenStore{
		crypto: tc,
	}

	tests := []struct {
		name     string
		enc      *models.EncryptedToken
		expected bool
	}{
		{
			name:     "nil_token",
			enc:      nil,
			expected: false,
		},
		{
			name: "valid_encrypted",
			enc: &models.EncryptedToken{
				Ciphertext: []byte("data"),
				Nonce:      make([]byte, crypto.NonceSize),
				KeyID:      "key-1",
			},
			expected: true,
		},
		{
			name: "wrong_nonce_size",
			enc: &models.EncryptedToken{
				Ciphertext: []byte("data"),
				Nonce:      make([]byte, 8), // wrong size
				KeyID:      "key-1",
			},
			expected: false,
		},
		{
			name: "empty_keyid",
			enc: &models.EncryptedToken{
				Ciphertext: []byte("data"),
				Nonce:      make([]byte, crypto.NonceSize),
				KeyID:      "",
			},
			expected: false,
		},
		{
			name: "empty_nonce",
			enc: &models.EncryptedToken{
				Ciphertext: []byte("data"),
				Nonce:      []byte{},
				KeyID:      "key-1",
			},
			expected: false,
		},
		{
			name: "raw_token_no_nonce",
			enc: &models.EncryptedToken{
				Ciphertext: []byte("raw-token-value"),
				Nonce:      nil,
				KeyID:      "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.isEncrypted(tt.enc)
			if got != tt.expected {
				t.Errorf("isEncrypted() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestIsEncryptedNonceSizeBoundary verifies boundary cases for nonce size.
func TestIsEncryptedNonceSizeBoundary(t *testing.T) {
	kms := &crypto.KMSClient{}
	tc := crypto.NewTokenCrypto(kms)
	defer tc.Close()

	store := &TokenStore{
		crypto: tc,
	}

	// Exactly 12 bytes (correct) + keyID = encrypted
	t.Run("exact_12_bytes", func(t *testing.T) {
		enc := &models.EncryptedToken{
			Ciphertext: []byte("data"),
			Nonce:      make([]byte, 12),
			KeyID:      "key-1",
		}
		if !store.isEncrypted(enc) {
			t.Error("12-byte nonce with keyID should be encrypted")
		}
	})

	// 11 bytes (one short) = not encrypted
	t.Run("11_bytes", func(t *testing.T) {
		enc := &models.EncryptedToken{
			Ciphertext: []byte("data"),
			Nonce:      make([]byte, 11),
			KeyID:      "key-1",
		}
		if store.isEncrypted(enc) {
			t.Error("11-byte nonce should not be considered encrypted")
		}
	})

	// 13 bytes (one over) = not encrypted
	t.Run("13_bytes", func(t *testing.T) {
		enc := &models.EncryptedToken{
			Ciphertext: []byte("data"),
			Nonce:      make([]byte, 13),
			KeyID:      "key-1",
		}
		if store.isEncrypted(enc) {
			t.Error("13-byte nonce should not be considered encrypted")
		}
	})
}

// TestTokenMetadata verifies the TokenMetadata struct.
func TestTokenMetadata(t *testing.T) {
	meta := TokenMetadata{
		ID:       uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890"),
		Provider: "gmail",
		IsActive: true,
	}

	if meta.Provider != "gmail" {
		t.Errorf("Provider = %q, want %q", meta.Provider, "gmail")
	}
	if !meta.IsActive {
		t.Error("IsActive should be true")
	}
}

// TestSaveTokensNilPair verifies that SaveTokens rejects nil pair.
func TestSaveTokensNilPair(t *testing.T) {
	kms := &crypto.KMSClient{}
	tc := crypto.NewTokenCrypto(kms)
	defer tc.Close()

	store := &TokenStore{
		crypto: tc,
	}

	err := store.SaveTokens(nil, uuid.New(), nil)
	if err == nil {
		t.Error("expected error for nil token pair")
	}
}

// TestUpdateAccessTokenNilPair verifies that UpdateAccessToken rejects nil pair.
func TestUpdateAccessTokenNilPair(t *testing.T) {
	kms := &crypto.KMSClient{}
	tc := crypto.NewTokenCrypto(kms)
	defer tc.Close()

	store := &TokenStore{
		crypto: tc,
	}

	err := store.UpdateAccessToken(nil, uuid.New(), nil)
	if err == nil {
		t.Error("expected error for nil token pair")
	}
}

// TestEncryptedTokenModel verifies EncryptedToken structure.
func TestEncryptedTokenModel(t *testing.T) {
	enc := &models.EncryptedToken{
		Ciphertext: []byte("encrypted-data"),
		Nonce:      make([]byte, crypto.NonceSize),
		KeyID:      "test-key",
	}

	if string(enc.Ciphertext) != "encrypted-data" {
		t.Error("ciphertext mismatch")
	}
	if len(enc.Nonce) != crypto.NonceSize {
		t.Errorf("nonce size = %d, want %d", len(enc.Nonce), crypto.NonceSize)
	}
}
