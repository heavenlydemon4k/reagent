-- Migration 003: Rollback — remove is_backfill columns
-- Service: ingestion

DROP INDEX IF EXISTS idx_decision_cards_is_backfill;
DROP INDEX IF EXISTS idx_raw_emails_is_backfill;

ALTER TABLE decision_cards DROP COLUMN IF EXISTS is_backfill;
ALTER TABLE raw_emails DROP COLUMN IF EXISTS is_backfill;

ALTER TABLE email_accounts DROP COLUMN IF EXISTS backfill_status;
ALTER TABLE email_accounts DROP COLUMN IF EXISTS backfill_completed_at;
