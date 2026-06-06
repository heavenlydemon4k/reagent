-- Sync & State — Reminder Jobs
-- Adds reminder_jobs table for scheduled notification delivery.

-- ============================================================================
-- REMINDER JOBS
-- Tracks scheduled reminders (pre-event, daily digest, conflict alerts).
-- Processed by the reminder worker; rows updated when sent.
-- ============================================================================
CREATE TABLE IF NOT EXISTS reminder_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    event_id        UUID NOT NULL,
    reminder_type   VARCHAR(32) NOT NULL CHECK (reminder_type IN ('pre_event', 'daily_digest', 'conflict_alert')),
    scheduled_for   TIMESTAMPTZ NOT NULL,
    processed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE reminder_jobs IS 'Scheduled reminders for calendar events and digests';

CREATE INDEX IF NOT EXISTS idx_reminder_jobs_user ON reminder_jobs (user_id, scheduled_for);
CREATE INDEX IF NOT EXISTS idx_reminder_jobs_pending ON reminder_jobs (scheduled_for) WHERE processed_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_reminder_jobs_type ON reminder_jobs (user_id, reminder_type);
