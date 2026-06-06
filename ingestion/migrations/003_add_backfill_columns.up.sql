-- Migration 003: Add is_backfill columns for historical email backfill tracking
-- Service: ingestion
--
-- When emails are ingested via the historical backfill pipeline (post-OAuth),
-- they are marked with is_backfill=true so downstream systems (classification,
-- decision cards) can distinguish them from real-time webhook-driven emails.

-- 1. Add is_backfill to raw_emails — set by ingestion during backfill
ALTER TABLE raw_emails ADD COLUMN IF NOT EXISTS is_backfill BOOLEAN DEFAULT FALSE;

-- Index for fast filtering of backfill emails
CREATE INDEX IF NOT EXISTS idx_raw_emails_is_backfill ON raw_emails(user_id, is_backfill, received_at DESC);

-- 2. Add is_backfill to decision_cards — populated by classification core
-- based on the corresponding raw_emails.is_backfill value.
ALTER TABLE decision_cards ADD COLUMN IF NOT EXISTS is_backfill BOOLEAN DEFAULT FALSE;

-- Index for onboarding queries (filter out backfill cards if needed)
CREATE INDEX IF NOT EXISTS idx_decision_cards_is_backfill ON decision_cards(user_id, is_backfill, created_at DESC);

-- 3. Add backfill tracking to email_accounts
ALTER TABLE email_accounts ADD COLUMN IF NOT EXISTS backfill_status VARCHAR(20) DEFAULT 'pending'
    CHECK (backfill_status IN ('pending', 'running', 'complete', 'failed', 'not_needed'));
ALTER TABLE email_accounts ADD COLUMN IF NOT EXISTS backfill_completed_at TIMESTAMPTZ;
