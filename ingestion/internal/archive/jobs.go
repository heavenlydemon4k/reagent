// Package archive provides background jobs for long-term email archival.
//
// ArchiveJob moves raw_emails older than 90 days to S3 in Parquet format
// and deletes them from the database. It runs nightly via cron during
// low-traffic hours (default: 02:00 UTC).
//
// The archived data is organized in Hive-style partitions on S3:
//   s3://{bucket}/archive/raw_emails/year=YYYY/month=MM/{uuid}.parquet
//
// This allows efficient querying via Athena, Spark, or other tools.
package archive

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awstypes "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/decisionstack/ingestion/internal/config"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

// ArchiveRow represents a single raw_email row for Parquet serialization.
// Parquet tags map Go fields to Parquet column types.
type ArchiveRow struct {
	ID               string   `parquet:"name=id, type=BYTE_ARRAY, convertedtype=UTF8"`
	ThreadID         string   `parquet:"name=thread_id, type=BYTE_ARRAY, convertedtype=UTF8"`
	UserID           string   `parquet:"name=user_id, type=BYTE_ARRAY, convertedtype=UTF8"`
	SourceAccountID  string   `parquet:"name=source_account_id, type=BYTE_ARRAY, convertedtype=UTF8"`
	MessageID        string   `parquet:"name=message_id, type=BYTE_ARRAY, convertedtype=UTF8"`
	InReplyTo        *string  `parquet:"name=in_reply_to, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	References       []string `parquet:"name=references, type=MAP, convertedtype=LIST, valuetype=BYTE_ARRAY, valueconvertedtype=UTF8"`
	SenderEmail      string   `parquet:"name=sender_email, type=BYTE_ARRAY, convertedtype=UTF8"`
	SenderName       *string  `parquet:"name=sender_name, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	RecipientEmails  []string `parquet:"name=recipient_emails, type=MAP, convertedtype=LIST, valuetype=BYTE_ARRAY, valueconvertedtype=UTF8"`
	Subject          *string  `parquet:"name=subject, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	BodyText         *string  `parquet:"name=body_text, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	BodyHTML         *string  `parquet:"name=body_html, type=BYTE_ARRAY, convertedtype=UTF8, repetitiontype=OPTIONAL"`
	HasAttachments   bool     `parquet:"name=has_attachments, type=BOOLEAN"`
	AttachmentS3URIs []string `parquet:"name=attachment_s3_uris, type=MAP, convertedtype=LIST, valuetype=BYTE_ARRAY, valueconvertedtype=UTF8"`
	ExtractedCodes   []string `parquet:"name=extracted_codes, type=MAP, convertedtype=LIST, valuetype=BYTE_ARRAY, valueconvertedtype=UTF8"`
	ReceivedAt       int64    `parquet:"name=received_at, type=INT64, convertedtype=TIMESTAMP_MILLIS"`
	ParsedAt         int64    `parquet:"name=parsed_at, type=INT64, convertedtype=TIMESTAMP_MILLIS"`
	RetentionUntil   int64    `parquet:"name=retention_until, type=INT64, convertedtype=TIMESTAMP_MILLIS"`
	Classification   string   `parquet:"name=classification, type=BYTE_ARRAY, convertedtype=UTF8"`
	Deleted          bool     `parquet:"name=deleted, type=BOOLEAN"`
	IsBackfill       bool     `parquet:"name=is_backfill, type=BOOLEAN"`
	CreatedAt        int64    `parquet:"name=created_at, type=INT64, convertedtype=TIMESTAMP_MILLIS"`
	UpdatedAt        int64    `parquet:"name=updated_at, type=INT64, convertedtype=TIMESTAMP_MILLIS"`
	ArchiveBatchID   string   `parquet:"name=archive_batch_id, type=BYTE_ARRAY, convertedtype=UTF8"`
}

// S3Uploader abstracts S3 upload operations for testability.
type S3Uploader interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// ArchiveJob archives old raw_emails to S3 and deletes them from the database.
type ArchiveJob struct {
	db                 *sql.DB
	s3                 S3Uploader
	bucket             string
	log                *slog.Logger

	// Configurable thresholds
	ArchiveAgeDays     int    // Archive emails older than this (default: 90)
	BatchSize          int    // Rows to process per batch (default: 5000)
	S3Prefix           string // S3 key prefix (default: "archive/raw_emails")
	DeleteAfterArchive bool   // Whether to delete rows after successful upload (default: true)
}

// ArchiveStats holds summary statistics for a single archive run.
type ArchiveStats struct {
	UsersProcessed  int           // Number of distinct users archived
	RowsArchived    int64         // Total rows written to Parquet
	RowsDeleted     int64         // Total rows deleted from DB
	BatchesUploaded int           // Number of Parquet files uploaded to S3
	BytesUploaded   int64         // Total bytes uploaded to S3
	Errors          int           // Number of errors (non-fatal)
	Duration        time.Duration // Total job duration
	StartTime       time.Time     // Job start time
}

// NewArchiveJob creates an ArchiveJob from configuration.
func NewArchiveJob(db *sql.DB, s3Client S3Uploader, cfg *config.Config, log *slog.Logger) *ArchiveJob {
	if log == nil {
		log = slog.Default()
	}
	return &ArchiveJob{
		db:                 db,
		s3:                 s3Client,
		bucket:             cfg.S3Bucket,
		log:                log.With("component", "archive_job"),
		ArchiveAgeDays:     90,
		BatchSize:          5000,
		S3Prefix:           "archive/raw_emails",
		DeleteAfterArchive: true,
	}
}

// Run executes the archive job end-to-end.
//
// Steps:
//  1. Find distinct users with emails older than ArchiveAgeDays
//  2. For each user, batch-process their old emails
//  3. Write each batch to Parquet in memory
//  4. Upload to S3 with Hive-style partitioning (year=YYYY/month=MM)
//  5. Delete archived rows from the database (respecting partition pruning)
//  6. Return statistics
//
// The job is designed to be non-blocking: it processes one user at a time
// and uses small batches to avoid long-running transactions. If a single
// user fails, the job continues with the next user.
func (j *ArchiveJob) Run(ctx context.Context) (*ArchiveStats, error) {
	stats := &ArchiveStats{
		StartTime: time.Now().UTC(),
	}
	defer func() {
		stats.Duration = time.Since(stats.StartTime)
	}()

	cutoff := time.Now().UTC().AddDate(0, 0, -j.ArchiveAgeDays)
	j.log.Info("archive job starting",
		"cutoff", cutoff.Format(time.RFC3339),
		"archive_age_days", j.ArchiveAgeDays,
		"batch_size", j.BatchSize,
	)

	// Step 1: Find users with archivable data
	users, err := j.findUsersWithOldEmails(ctx, cutoff)
	if err != nil {
		return stats, fmt.Errorf("find users with old emails: %w", err)
	}

	j.log.Info("found users to archive", "count", len(users))

	// Step 2: Process each user independently
	for _, userID := range users {
		if err := ctx.Err(); err != nil {
			j.log.Warn("archive job cancelled", "error", err)
			return stats, err
		}

		userStats, err := j.archiveUser(ctx, userID, cutoff)
		if err != nil {
			j.log.Error("failed to archive user",
				"user_id", userID,
				"error", err,
			)
			stats.Errors++
			continue // Non-fatal: continue with next user
		}

		stats.UsersProcessed++
		stats.RowsArchived += userStats.RowsArchived
		stats.RowsDeleted += userStats.RowsDeleted
		stats.BatchesUploaded += userStats.BatchesUploaded
		stats.BytesUploaded += userStats.BytesUploaded
	}

	j.log.Info("archive job complete",
		"users_processed", stats.UsersProcessed,
		"rows_archived", stats.RowsArchived,
		"rows_deleted", stats.RowsDeleted,
		"batches_uploaded", stats.BatchesUploaded,
		"errors", stats.Errors,
		"duration", stats.Duration,
	)

	return stats, nil
}

// archiveUser archives all old emails for a single user.
// This respects partition pruning by always including user_id in queries.
func (j *ArchiveJob) archiveUser(ctx context.Context, userID uuid.UUID, cutoff time.Time) (*ArchiveStats, error) {
	log := j.log.With("user_id", userID)
	stats := &ArchiveStats{StartTime: time.Now().UTC()}

	// Process in batches until no more rows
	for {
		batchID := uuid.New().String()
		log := log.With("batch_id", batchID)

		// Fetch a batch of rows (batch size + 1 to detect if there's more)
		rows, hasMore, err := j.fetchBatch(ctx, userID, cutoff, j.BatchSize)
		if err != nil {
			return stats, fmt.Errorf("fetch batch: %w", err)
		}
		if len(rows) == 0 {
			break // No more rows for this user
		}

		// Tag rows with batch ID for traceability
		for _, r := range rows {
			r.ArchiveBatchID = batchID
		}

		// Write to Parquet in memory
		parquetBytes, err := j.writeParquet(rows)
		if err != nil {
			return stats, fmt.Errorf("write parquet: %w", err)
		}

		// Determine S3 key with Hive-style partitioning
		now := time.Now().UTC()
		s3Key := fmt.Sprintf("%s/year=%d/month=%02d/%s.parquet",
			j.S3Prefix, now.Year(), now.Month(), batchID)

		// Upload to S3 with SSE-KMS encryption
		if err := j.uploadToS3(ctx, s3Key, parquetBytes); err != nil {
			return stats, fmt.Errorf("upload to s3: %w", err)
		}

		log.Info("batch uploaded",
			"rows", len(rows),
			"s3_key", s3Key,
			"bytes", len(parquetBytes),
		)

		stats.RowsArchived += int64(len(rows))
		stats.BatchesUploaded++
		stats.BytesUploaded += int64(len(parquetBytes))

		// Delete archived rows from database
		if j.DeleteAfterArchive {
			deleted, err := j.deleteBatch(ctx, userID, rows)
			if err != nil {
				return stats, fmt.Errorf("delete batch: %w", err)
			}
			stats.RowsDeleted += deleted
			log.Debug("batch deleted", "rows_deleted", deleted)
		}

		// If this was the last batch, stop
		if !hasMore {
			break
		}

		// Small pause between batches to reduce DB load
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return stats, ctx.Err()
		}
	}

	return stats, nil
}

// findUsersWithOldEmails returns distinct user_ids that have emails older than
// the cutoff date. This query benefits from the idx_raw_emails_user_received
// index when the partitioned table is active.
func (j *ArchiveJob) findUsersWithOldEmails(ctx context.Context, cutoff time.Time) ([]uuid.UUID, error) {
	query := `
		SELECT DISTINCT user_id
		FROM raw_emails
		WHERE received_at < $1
		ORDER BY user_id
	`
	rows, err := j.db.QueryContext(ctx, query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query distinct users: %w", err)
	}
	defer rows.Close()

	var users []uuid.UUID
	for rows.Next() {
		var uid uuid.UUID
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan user_id: %w", err)
		}
		users = append(users, uid)
	}
	return users, rows.Err()
}

// fetchBatch retrieves up to limit rows for a specific user older than cutoff.
// Returns the rows and a boolean indicating if more rows exist.
// This query is partition-pruned on user_id.
func (j *ArchiveJob) fetchBatch(ctx context.Context, userID uuid.UUID, cutoff time.Time, limit int) ([]*ArchiveRow, bool, error) {
	// Fetch limit+1 to detect if there are more rows
	query := `
		SELECT
			id, thread_id, user_id, source_account_id, message_id,
			in_reply_to, references, sender_email, sender_name,
			recipient_emails, subject, body_text, body_html,
			has_attachments, attachment_s3_uris, extracted_codes,
			received_at, parsed_at, retention_until,
			classification, deleted, is_backfill, created_at, updated_at
		FROM raw_emails
		WHERE user_id = $1 AND received_at < $2
		ORDER BY received_at ASC
		LIMIT $3
	`
	rows, err := j.db.QueryContext(ctx, query, userID, cutoff, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("query batch: %w", err)
	}
	defer rows.Close()

	var result []*ArchiveRow
	for rows.Next() {
		row := &ArchiveRow{}
		var id, threadID, uid, sourceAccountID uuid.UUID
		var messageID, senderEmail string
		var inReplyTo, senderName, subject, bodyText, bodyHTML sql.NullString
		var referencesArr, recipientEmails, attachmentS3URIs, extractedCodes pq.StringArray
		var hasAttachments, deleted, isBackfill sql.NullBool
		var classification sql.NullString
		var receivedAt, parsedAt, retentionUntil, createdAt, updatedAt sql.NullTime

		err := rows.Scan(
			&id, &threadID, &uid, &sourceAccountID, &messageID,
			&inReplyTo, &referencesArr, &senderEmail, &senderName,
			&recipientEmails, &subject, &bodyText, &bodyHTML,
			&hasAttachments, &attachmentS3URIs, &extractedCodes,
			&receivedAt, &parsedAt, &retentionUntil,
			&classification, &deleted, &isBackfill, &createdAt, &updatedAt,
		)
		if err != nil {
			return nil, false, fmt.Errorf("scan row: %w", err)
		}

		row.ID = id.String()
		row.ThreadID = threadID.String()
		row.UserID = uid.String()
		row.SourceAccountID = sourceAccountID.String()
		row.MessageID = messageID
		row.SenderEmail = senderEmail
		row.HasAttachments = hasAttachments.Valid && hasAttachments.Bool
		row.Deleted = deleted.Valid && deleted.Bool
		row.IsBackfill = isBackfill.Valid && isBackfill.Bool

		if inReplyTo.Valid && inReplyTo.String != "" {
			row.InReplyTo = &inReplyTo.String
		}
		if senderName.Valid && senderName.String != "" {
			row.SenderName = &senderName.String
		}
		if subject.Valid && subject.String != "" {
			row.Subject = &subject.String
		}
		if bodyText.Valid && bodyText.String != "" {
			row.BodyText = &bodyText.String
		}
		if bodyHTML.Valid && bodyHTML.String != "" {
			row.BodyHTML = &bodyHTML.String
		}
		if classification.Valid {
			row.Classification = classification.String
		} else {
			row.Classification = "pending"
		}

		// Convert pq.StringArray to []string
		row.References = stringSlice(referencesArr)
		row.RecipientEmails = stringSlice(recipientEmails)
		row.AttachmentS3URIs = stringSlice(attachmentS3URIs)
		row.ExtractedCodes = stringSlice(extractedCodes)

		// Convert timestamps to millis
		if receivedAt.Valid {
			row.ReceivedAt = receivedAt.Time.UnixMilli()
		}
		if parsedAt.Valid {
			row.ParsedAt = parsedAt.Time.UnixMilli()
		}
		if retentionUntil.Valid {
			row.RetentionUntil = retentionUntil.Time.UnixMilli()
		}
		if createdAt.Valid {
			row.CreatedAt = createdAt.Time.UnixMilli()
		}
		if updatedAt.Valid {
			row.UpdatedAt = updatedAt.Time.UnixMilli()
		}

		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("row iteration: %w", err)
	}

	// If we fetched limit+1 rows, there's more data
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit] // Return only the requested number
	}

	return result, hasMore, nil
}

// writeParquet serializes archive rows to a Parquet file in memory.
func (j *ArchiveJob) writeParquet(rows []*ArchiveRow) ([]byte, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	var buf bytes.Buffer
	pw, err := writer.NewParquetWriterFromWriter(&buf, new(ArchiveRow), 4)
	if err != nil {
		return nil, fmt.Errorf("create parquet writer: %w", err)
	}

	// Use SNAPPY compression for good speed/compression ratio
	pw.CompressionType = parquet.CompressionCodec_SNAPPY

	for _, row := range rows {
		if err := pw.Write(row); err != nil {
			_ = pw.WriteStop()
			return nil, fmt.Errorf("write parquet row: %w", err)
		}
	}

	if err := pw.WriteStop(); err != nil {
		return nil, fmt.Errorf("finalize parquet: %w", err)
	}

	return buf.Bytes(), nil
}

// uploadToS3 uploads Parquet bytes to S3 with server-side encryption.
func (j *ArchiveJob) uploadToS3(ctx context.Context, key string, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	putInput := &s3.PutObjectInput{
		Bucket:               aws.String(j.bucket),
		Key:                  aws.String(key),
		Body:                 bytes.NewReader(data),
		ServerSideEncryption: awstypes.ServerSideEncryptionAwsKms,
		ContentType:          aws.String("application/octet-stream"),
	}

	_, err := j.s3.PutObject(ctx, putInput)
	if err != nil {
		return fmt.Errorf("s3 put object %s: %w", key, err)
	}

	return nil
}

// deleteBatch removes archived rows from the database using the primary key.
// The DELETE includes user_id for partition pruning.
func (j *ArchiveJob) deleteBatch(ctx context.Context, userID uuid.UUID, rows []*ArchiveRow) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	// Use a single DELETE with WHERE id IN (...) AND user_id = $1
	// This respects partition pruning while being efficient for batch deletion.
	// For very large batches, we chunk the IDs.
	const chunkSize = 1000 // Safe chunk size under PostgreSQL parameter limits

	var totalDeleted int64
	for i := 0; i < len(rows); i += chunkSize {
		end := i + chunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunk := rows[i:end]

		ids := make([]uuid.UUID, len(chunk))
		for j, row := range chunk {
			id, err := uuid.Parse(row.ID)
			if err != nil {
				return totalDeleted, fmt.Errorf("parse uuid %s: %w", row.ID, err)
			}
			ids[j] = id
		}

		// Build the query with the right number of placeholders
		query, args := buildDeleteQuery(userID, ids)

		res, err := j.db.ExecContext(ctx, query, args...)
		if err != nil {
			return totalDeleted, fmt.Errorf("delete chunk: %w", err)
		}

		n, err := res.RowsAffected()
		if err != nil {
			return totalDeleted, fmt.Errorf("rows affected: %w", err)
		}
		totalDeleted += n
	}

	return totalDeleted, nil
}

// buildDeleteQuery creates a DELETE ... WHERE user_id = $1 AND id IN ($2, $3, ...)
// query with the correct number of placeholders.
func buildDeleteQuery(userID uuid.UUID, ids []uuid.UUID) (string, []interface{}) {
	var b strings.Builder
	b.WriteString("DELETE FROM raw_emails WHERE user_id = $1 AND id IN (")

	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, userID)

	for i := 0; i < len(ids); i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(fmt.Sprintf("$%d", i+2))
		args = append(args, ids[i])
	}
	b.WriteString(")")
	return b.String(), args
}

// stringSlice converts a pq.StringArray to a plain []string,
// filtering out empty strings.
func stringSlice(arr pq.StringArray) []string {
	if len(arr) == 0 {
		return nil
	}
	var result []string
	for _, s := range arr {
		if s != "" {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
