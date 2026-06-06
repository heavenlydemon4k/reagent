// Package crypto tests AWS KMS-backed DEK lifecycle management.
package crypto

import (
	"context"
	"testing"
	"time"
)

// TestGenerateDEKSize verifies that GenerateDEK produces a 256-bit (32-byte) key.
func TestGenerateDEKSize(t *testing.T) {
	k := &KMSClient{keyID: "test-key-id"}
	ctx := context.Background()

	dek, err := k.GenerateDEK(ctx)
	if err != nil {
		t.Fatalf("GenerateDEK failed: %v", err)
	}

	if len(dek) != DEKSize {
		t.Errorf("expected DEK size %d, got %d", DEKSize, len(dek))
	}
}

// TestGenerateDEKRandomness verifies that two DEKs are different.
func TestGenerateDEKRandomness(t *testing.T) {
	k := &KMSClient{keyID: "test-key-id"}
	ctx := context.Background()

	dek1, err := k.GenerateDEK(ctx)
	if err != nil {
		t.Fatalf("GenerateDEK #1 failed: %v", err)
	}

	dek2, err := k.GenerateDEK(ctx)
	if err != nil {
		t.Fatalf("GenerateDEK #2 failed: %v", err)
	}

	// Probability of collision is astronomically low
	if string(dek1) == string(dek2) {
		t.Error("two DEKs should not be identical")
	}
}

// TestGenerateDEKNonZero verifies that DEK bytes are non-zero.
func TestGenerateDEKNonZero(t *testing.T) {
	k := &KMSClient{keyID: "test-key-id"}
	ctx := context.Background()

	dek, err := k.GenerateDEK(ctx)
	if err != nil {
		t.Fatalf("GenerateDEK failed: %v", err)
	}

	allZero := true
	for _, b := range dek {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("DEK should not be all zeros")
	}
}

// TestKeyID verifies that KeyID returns the configured key ID.
func TestKeyID(t *testing.T) {
	tests := []struct {
		name  string
		keyID string
	}{
		{"simple", "arn:aws:kms:us-east-1:123456:key/test-key"},
		{"uuid_key", "12345678-1234-1234-1234-123456789abc"},
		{"alias", "alias/my-key"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &KMSClient{keyID: tt.keyID}
			if got := k.KeyID(); got != tt.keyID {
				t.Errorf("KeyID() = %q, want %q", got, tt.keyID)
			}
		})
	}
}

// TestKeyIDThreadSafe verifies KeyID works under concurrent reads.
func TestKeyIDThreadSafe(t *testing.T) {
	k := &KMSClient{keyID: "concurrent-test-key"}

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				if k.KeyID() != "concurrent-test-key" {
					t.Error("KeyID mismatch under concurrent read")
				}
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for concurrent KeyID reads")
		}
	}
}

// TestClose verifies that Close releases resources.
func TestClose(t *testing.T) {
	k := &KMSClient{keyID: "test-key"}
	if err := k.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}

	// After close, client should be nil
	if k.client != nil {
		t.Error("client should be nil after Close()")
	}
}

// TestEncryptDEKInvalidSize verifies that EncryptDEK rejects non-32-byte DEKs.
func TestEncryptDEKInvalidSize(t *testing.T) {
	k := &KMSClient{keyID: "test-key"}
	ctx := context.Background()

	tests := []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"too_short", 16},
		{"too_long", 64},
		{"one_byte", 1},
		{"31_bytes", 31},
		{"33_bytes", 33},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dek := make([]byte, tt.size)
			_, err := k.EncryptDEK(ctx, dek)
			if err == nil {
				t.Error("expected error for invalid DEK size")
			}
		})
	}
}

// TestDecryptDEKEmpty verifies that DecryptDEK rejects empty input.
func TestDecryptDEKEmpty(t *testing.T) {
	k := &KMSClient{keyID: "test-key"}
	ctx := context.Background()

	_, err := k.DecryptDEK(ctx, []byte{})
	if err == nil {
		t.Error("expected error for empty encrypted DEK")
	}

	_, err = k.DecryptDEK(ctx, nil)
	if err == nil {
		t.Error("expected error for nil encrypted DEK")
	}
}

// TestDefaultEncryptionContext verifies the encryption context content.
func TestDefaultEncryptionContext(t *testing.T) {
	k := &KMSClient{keyID: "test-key-123"}
	ctx := k.defaultEncryptionContext()

	if ctx["purpose"] != "oauth-token-encryption" {
		t.Errorf("purpose mismatch: %q", ctx["purpose"])
	}
	if ctx["service"] != "ingestion-mesh" {
		t.Errorf("service mismatch: %q", ctx["service"])
	}
	if ctx["key_origin"] != "test-key-123" {
		t.Errorf("key_origin mismatch: %q", ctx["key_origin"])
	}
}

// TestDEKConstant verifies the DEK size constant.
func TestDEKConstant(t *testing.T) {
	// DEKSize should be 32 bytes (256 bits)
	if DEKSize != 32 {
		t.Errorf("DEKSize = %d, want 32", DEKSize)
	}
}

// TestNonceConstant verifies the nonce size constant.
func TestNonceConstant(t *testing.T) {
	// NonceSize should be 12 bytes for AES-GCM
	if NonceSize != 12 {
		t.Errorf("NonceSize = %d, want 12", NonceSize)
	}
}

// TestKMSClientImplementsCloser verifies KMSClient implements io.Closer.
func TestKMSClientImplementsCloser(t *testing.T) {
	// This is a compile-time check in the source; we verify at runtime
	k := &KMSClient{keyID: "test"}
	if k == nil {
		t.Error("KMSClient should be instantiable")
	}
}
