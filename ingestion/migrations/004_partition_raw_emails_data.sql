-- Migration 004: Data migration — copy data from raw_emails to raw_emails_partitioned
-- Service: ingestion
--
-- Run this script during a low-traffic maintenance window.
-- Steps:
--   1. Run the INSERT below to migrate all existing data.
--   2. Verify row counts match.
--   3. Run the atomic rename (Step 6) to swap tables.
--   4. The old table is preserved as raw_emails_old for safety.

-- Step 5: Migrate data from old table to new partitioned table.
-- PostgreSQL automatically routes each row to the correct hash partition
-- based on the user_id value. ON CONFLICT handles any rows that may have
-- been inserted during a previous partial migration attempt.
INSERT INTO raw_emails_partitioned (
    id, thread_id, user_id, source_account_id, message_id,
    in_reply_to, references, sender_email, sender_name, recipient_emails,
    subject, body_text, body_html, has_attachments, attachment_s3_uris,
    extracted_codes, received_at, parsed_at, retention_until,
    classification, deleted, is_backfill, created_at, updated_at
)
SELECT
    id, thread_id, user_id, source_account_id, message_id,
    in_reply_to, references, sender_email, sender_name, recipient_emails,
    subject, body_text, body_html, has_attachments, attachment_s3_uris,
    extracted_codes, received_at, parsed_at, retention_until,
    classification, deleted, is_backfill, created_at, updated_at
FROM raw_emails
ON CONFLICT (id, user_id) DO NOTHING;

-- Verification query (run manually before proceeding):
-- SELECT
--     (SELECT COUNT(*) FROM raw_emails) AS old_count,
--     (SELECT COUNT(*) FROM raw_emails_partitioned) AS new_count;

-- Step 6: Atomic swap — rename tables.
-- Run these commands in a single transaction after verifying counts match.
--
-- BEGIN;
--     -- Acquire access exclusive lock to prevent writes during swap
--     LOCK TABLE raw_emails IN ACCESS EXCLUSIVE MODE;
--     LOCK TABLE raw_emails_partitioned IN ACCESS EXCLUSIVE MODE;
--
--     -- Swap names
--     ALTER TABLE raw_emails RENAME TO raw_emails_old;
--     ALTER TABLE raw_emails_partitioned RENAME TO raw_emails;
--
--     -- Update comments
--     COMMENT ON TABLE raw_emails IS 'Partitioned raw_emails (16 HASH partitions on user_id). Swapped from raw_emails_partitioned by migration 004.';
--     COMMENT ON TABLE raw_emails_old IS 'Backup of pre-partitioned raw_emails table. Drop after verification period.';
-- COMMIT;
