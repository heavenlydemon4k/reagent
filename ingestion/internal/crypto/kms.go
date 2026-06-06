// Package crypto provides AWS KMS-backed encryption for OAuth tokens.
// All token encryption uses AES-256-GCM with DEKs managed by AWS KMS.
package crypto

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	appconfig "github.com/decisionstack/ingestion/internal/config"
)

const (
	// DEKSize is the size of the AES-256 data encryption key in bytes.
	DEKSize = 32
)

// KMSClient wraps the AWS KMS SDK for DEK lifecycle management.
type KMSClient struct {
	client *kms.Client
	keyID  string
	mu     sync.RWMutex
}

// NewKMSClient creates a new KMSClient using the application configuration.
// It loads the default AWS SDK configuration and validates the KMS key ID.
func NewKMSClient(cfg *appconfig.Config) (*KMSClient, error) {
	if cfg.KMSKeyID == "" {
		return nil, fmt.Errorf("KMS key ID is required")
	}

	awsCfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS SDK config: %w", err)
	}

	client := kms.NewFromConfig(awsCfg)

	// Validate key exists and is accessible by attempting a describe key call
	_, err = client.DescribeKey(context.Background(), &kms.DescribeKeyInput{
		KeyId: aws.String(cfg.KMSKeyID),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to validate KMS key %s: %w", cfg.KMSKeyID, err)
	}

	return &KMSClient{
		client: client,
		keyID:  cfg.KMSKeyID,
	}, nil
}

// GenerateDEK creates a cryptographically secure random 256-bit AES key.
// This key is used as a data encryption key (DEK) for token encryption.
func (k *KMSClient) GenerateDEK(_ context.Context) ([]byte, error) {
	dek := make([]byte, DEKSize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, fmt.Errorf("failed to generate DEK: %w", err)
	}
	return dek, nil
}

// EncryptDEK encrypts a plaintext DEK using the AWS KMS CMK.
// The returned encrypted DEK can be safely stored alongside encrypted data.
func (k *KMSClient) EncryptDEK(ctx context.Context, plaintextDEK []byte) ([]byte, error) {
	if len(plaintextDEK) != DEKSize {
		return nil, fmt.Errorf("invalid DEK size: expected %d bytes, got %d", DEKSize, len(plaintextDEK))
	}

	result, err := k.client.Encrypt(ctx, &kms.EncryptInput{
		KeyId:             aws.String(k.keyID),
		Plaintext:         plaintextDEK,
		EncryptionContext: k.defaultEncryptionContext(),
		EncryptionAlgorithm: types.EncryptionAlgorithmSpecSymmetricDefault,
	})
	if err != nil {
		return nil, fmt.Errorf("KMS EncryptDEK failed: %w", err)
	}

	return result.CiphertextBlob, nil
}

// DecryptDEK decrypts an encrypted DEK using the AWS KMS CMK.
// The returned plaintext DEK must be handled securely and never logged.
func (k *KMSClient) DecryptDEK(ctx context.Context, encryptedDEK []byte) ([]byte, error) {
	if len(encryptedDEK) == 0 {
		return nil, fmt.Errorf("encrypted DEK is empty")
	}

	result, err := k.client.Decrypt(ctx, &kms.DecryptInput{
		CiphertextBlob:    encryptedDEK,
		KeyId:             aws.String(k.keyID), // specify expected key ID for additional security
		EncryptionContext: k.defaultEncryptionContext(),
		EncryptionAlgorithm: types.EncryptionAlgorithmSpecSymmetricDefault,
	})
	if err != nil {
		return nil, fmt.Errorf("KMS DecryptDEK failed: %w", err)
	}

	if len(result.Plaintext) != DEKSize {
		return nil, fmt.Errorf("decrypted DEK has unexpected size: expected %d bytes, got %d", DEKSize, len(result.Plaintext))
	}

	return result.Plaintext, nil
}

// Close releases resources held by the KMS client.
// The underlying AWS SDK client does not require explicit cleanup,
// but this method exists for interface compatibility.
func (k *KMSClient) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.client = nil
	return nil
}

// KeyID returns the configured KMS CMK key ID.
func (k *KMSClient) KeyID() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.keyID
}

// defaultEncryptionContext returns the encryption context used for all KMS operations.
// Encryption context provides additional authenticated data (AAD) for KMS operations
// and appears in CloudTrail logs for audit purposes.
func (k *KMSClient) defaultEncryptionContext() map[string]string {
	return map[string]string{
		"purpose":    "oauth-token-encryption",
		"service":    "ingestion-mesh",
		"key_origin": k.keyID,
	}
}

// Ensure KMSClient implements the interface at compile time.
var _ io.Closer = (*KMSClient)(nil)
