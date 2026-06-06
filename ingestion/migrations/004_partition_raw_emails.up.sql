-- Migration 004: Partition raw_emails by HASH(user_id) into 16 partitions
-- Service: ingestion
--
-- Context: The raw_emails table grows unbounded. At 500 users x 100 emails/day
-- x 365 days = 18.25M rows. Without partitioning, queries by user_id scan the
-- entire table, causing query timeouts at scale.
--
-- Strategy: Online migration that creates a new partitioned table alongside
-- the existing one. Data is migrated during a low-traffic window, then tables
-- are swapped via atomic rename. The old table is preserved as backup.
--
-- Constraints:
--   - PostgreSQL 16 supports HASH partitioning natively
--   - The partition key (user_id) must be part of the primary key
--   - All queries must include user_id for partition pruning

-- Step 1: Create new partitioned table with full column set
CREATE TABLE IF NOT EXISTS raw_emails_partitioned (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    thread_id UUID NOT NULL,
    user_id UUID NOT NULL,
    source_account_id UUID NOT NULL,
    message_id VARCHAR(255) NOT NULL,
    in_reply_to VARCHAR(255),
    references TEXT[],
    sender_email VARCHAR(255) NOT NULL,
    sender_name VARCHAR(255),
    recipient_emails TEXT[] NOT NULL,
    subject TEXT,
    body_text TEXT,
    body_html TEXT,
    has_attachments BOOLEAN DEFAULT FALSE,
    attachment_s3_uris TEXT[],
    extracted_codes TEXT[],
    received_at TIMESTAMPTZ NOT NULL,
    parsed_at TIMESTAMPTZ DEFAULT NOW(),
    retention_until TIMESTAMPTZ NOT NULL,
    classification VARCHAR(20) CHECK (classification IN ('extract', 'auto', 'decision', 'pending')),
    deleted BOOLEAN DEFAULT FALSE,
    is_backfill BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),

    -- Partition key must be part of primary key
    PRIMARY KEY (id, user_id)
) PARTITION BY HASH (user_id);

-- Step 2: Create 16 hash partitions
DO $$
DECLARE
    i INT;
BEGIN
    FOR i IN 0..15 LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS raw_emails_p%s PARTITION OF raw_emails_partitioned
             FOR VALUES WITH (MODULUS 16, REMAINDER %s)',
            i, i
        );
    END LOOP;
END $$;

-- Step 3: Create indexes on partitioned table
-- User + received_at for time-sorted email listings (most common query pattern)
CREATE INDEX IF NOT EXISTS idx_raw_emails_partitioned_user_received
    ON raw_emails_partitioned (user_id, received_at DESC);

-- Account + message_id for deduplication checks during ingestion
CREATE INDEX IF NOT EXISTS idx_raw_emails_partitioned_account_message
    ON raw_emails_partitioned (source_account_id, message_id);

-- Partial index for pending classification work queue
CREATE INDEX IF NOT EXISTS idx_raw_emails_partitioned_classification
    ON raw_emails_partitioned (classification, user_id)
    WHERE classification = 'pending';

-- Thread lookup for conversation grouping
CREATE INDEX IF NOT EXISTS idx_raw_emails_partitioned_thread
    ON raw_emails_partitioned (thread_id, user_id);

-- Partial index for retention policy cleanup
CREATE INDEX IF NOT EXISTS idx_raw_emails_partitioned_retention
    ON raw_emails_partitioned (retention_until)
    WHERE retention_until < NOW();

-- Backfill filtering index (added in migration 003)
CREATE INDEX IF NOT EXISTS idx_raw_emails_partitioned_is_backfill
    ON raw_emails_partitioned (user_id, is_backfill, received_at DESC);

-- Step 4: Unique constraint workaround for partitioned tables
-- PostgreSQL requires partition key in unique indexes on partitioned tables.
-- The original table had UNIQUE(message_id) which is relaxed here to
-- (source_account_id, message_id, user_id) to support partition pruning.
CREATE UNIQUE INDEX IF NOT EXISTS idx_raw_emails_partitioned_unique_message
    ON raw_emails_partitioned (source_account_id, message_id, user_id);

-- Step 5: Attach existing indexes from migration 002 if applicable
-- (indexes are created above; this step ensures idempotency)

-- Step 6: Grant permissions (idempotent)
-- Default: inherit from parent table. No explicit grants needed.

-- Step 7: Add table comment for documentation
COMMENT ON TABLE raw_emails_partitioned IS
    'Partitioned raw_emails table (16 HASH partitions on user_id). Created by migration 004. Swap via rename after data migration.';
