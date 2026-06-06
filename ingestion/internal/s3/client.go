// Package s3 provides an S3 upload client with SSE-KMS encryption for the
// Ingestion Mesh. All objects are stored under per-user prefixes with
// server-side encryption using AWS KMS customer-managed keys.
package s3

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awstypes "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"

	"github.com/decisionstack/ingestion/internal/config"
)

// Client wraps the AWS SDK v2 S3 client with Ingestion-Mesh-specific
// upload helpers, KMS encryption, and per-user prefix conventions.
type Client struct {
	client   *s3.Client
	bucket   string
	kmsKeyID string
	log      *slog.Logger
}

// NewClient creates a new S3 client from configuration.
// It supports both real AWS S3 and local MinIO (development) via S3Endpoint.
func NewClient(cfg *config.Config) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var optFns []func(*awsconfig.LoadOptions) error

	// If a custom endpoint is provided, assume MinIO / local dev mode.
	if cfg.S3Endpoint != "" {
		staticResolver := aws.EndpointResolverWithOptionsFunc(
			func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					URL:               cfg.S3Endpoint,
					HostnameImmutable: true,
					Source:            aws.EndpointSourceCustom,
				}, nil
			},
		)
		optFns = append(optFns,
			awsconfig.WithEndpointResolverWithOptions(staticResolver),
			awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider("minioadmin", "minioadmin", ""),
			),
		)
	}

	optFns = append(optFns, awsconfig.WithRegion(cfg.S3Region))

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg)

	return &Client{
		client:   s3Client,
		bucket:   cfg.S3Bucket,
		kmsKeyID: cfg.KMSKeyID,
		log:      slog.Default().WithGroup("s3"),
	}, nil
}

// Bucket returns the configured S3 bucket name.
func (c *Client) Bucket() string {
	return c.bucket
}

// upload performs the core PutObject call with SSE-KMS encryption.
// All uploads in the Ingestion Mesh MUST go through this path.
func (c *Client) upload(ctx context.Context, key string, data []byte, contentType string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	putInput := &s3.PutObjectInput{
		Bucket:               aws.String(c.bucket),
		Key:                  aws.String(key),
		Body:                 bytes.NewReader(data),
		ServerSideEncryption: awstypes.ServerSideEncryptionAwsKms,
		SSEKMSKeyId:          aws.String(c.kmsKeyID),
		ContentType:          aws.String(contentType),
	}

	_, err := c.client.PutObject(ctx, putInput)
	if err != nil {
		return "", fmt.Errorf("s3 PutObject failed for key %s: %w", key, err)
	}

	s3URI := fmt.Sprintf("s3://%s/%s", c.bucket, key)
	c.log.Info("uploaded object", "key", key, "bucket", c.bucket)
	return s3URI, nil
}

// UploadRawEmail stores the original MIME blob to S3 under the per-user prefix.
// The raw email body is preserved as the immutable source of truth; all parsed
// text is derivative.
//
// Path: s3://{bucket}/users/{user_id}/emails/{email_id}/raw.eml
func (c *Client) UploadRawEmail(ctx context.Context, userID, emailID uuid.UUID, data []byte) (string, error) {
	key := fmt.Sprintf("users/%s/emails/%s/raw.eml", userID.String(), emailID.String())
	s3URI, err := c.upload(ctx, key, data, "message/rfc822")
	if err != nil {
		return "", fmt.Errorf("UploadRawEmail failed: %w", err)
	}
	return s3URI, nil
}

// UploadAttachment stores a single attachment to S3 under the per-user,
// per-email prefix with SSE-KMS encryption.
//
// Path: s3://{bucket}/users/{user_id}/emails/{email_id}/attachments/{filename}
func (c *Client) UploadAttachment(ctx context.Context, userID, emailID uuid.UUID, filename string, data []byte, contentType string) (string, error) {
	key := fmt.Sprintf("users/%s/emails/%s/attachments/%s", userID.String(), emailID.String(), filename)
	s3URI, err := c.upload(ctx, key, data, contentType)
	if err != nil {
		return "", fmt.Errorf("UploadAttachment failed for %s: %w", filename, err)
	}
	return s3URI, nil
}
