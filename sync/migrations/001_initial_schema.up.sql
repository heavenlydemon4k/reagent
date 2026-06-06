-- Sync & State — Initial Schema
-- Tables: user_queues, device_sessions, notifications, sync_log, notification_preferences

-- ============================================================================
-- USER QUEUES
-- Tracks per-user queue state including pending count and server version.
-- ============================================================================
CREATE TABLE IF NOT EXISTS user_queues (
    user_id            UUID PRIMARY KEY,
    pending_count      INT NOT NULL DEFAULT 0,
    server_version     INT NOT NULL DEFAULT 0,
    last_notification_at TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE user_queues IS 'Per-user queue state for sync protocol';
COMMENT ON COLUMN user_queues.server_version IS 'Monotonically incremented on every server-side change to the queue';

CREATE INDEX IF NOT EXISTS idx_user_queues_pending_count ON user_queues (pending_count) WHERE pending_count > 0;
CREATE INDEX IF NOT EXISTS idx_user_queues_version ON user_queues (server_version);

-- ============================================================================
-- DEVICE SESSIONS
-- Tracks registered devices with push notification tokens.
-- ============================================================================
CREATE TABLE IF NOT EXISTS device_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    device_id       VARCHAR(255) NOT NULL,
    device_type     VARCHAR(20) NOT NULL CHECK (device_type IN ('ios', 'android', 'web')),
    device_name     VARCHAR(255),
    fcm_token       VARCHAR(512),
    apns_token      VARCHAR(512),
    last_active_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (user_id, device_id)
);

COMMENT ON TABLE device_sessions IS 'Registered devices with push notification tokens per user';

CREATE INDEX IF NOT EXISTS idx_device_sessions_user ON device_sessions (user_id);
CREATE INDEX IF NOT EXISTS idx_device_sessions_fcm ON device_sessions (fcm_token) WHERE fcm_token IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_device_sessions_apns ON device_sessions (apns_token) WHERE apns_token IS NOT NULL;

-- ============================================================================
-- NOTIFICATIONS
-- Push notification records with delivery tracking.
-- ============================================================================
CREATE TABLE IF NOT EXISTS notifications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    type            VARCHAR(32) NOT NULL CHECK (type IN ('batch', 'interrupt', 'temporal', 'staging', 'reminder')),
    title           VARCHAR(255) NOT NULL,
    body            TEXT NOT NULL,
    data            JSONB,
    sent_at         TIMESTAMPTZ,
    read_at         TIMESTAMPTZ,
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE notifications IS 'Push notification records with delivery tracking';

CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_unsent ON notifications (sent_at) WHERE sent_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_notifications_type ON notifications (user_id, type);

-- ============================================================================
-- NOTIFICATION PREFERENCES
-- Per-user notification preferences including quiet hours.
-- ============================================================================
CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id             UUID PRIMARY KEY,
    batch_enabled       BOOLEAN NOT NULL DEFAULT true,
    interrupt_enabled   BOOLEAN NOT NULL DEFAULT true,
    temporal_enabled    BOOLEAN NOT NULL DEFAULT true,
    staging_enabled     BOOLEAN NOT NULL DEFAULT true,
    quiet_hours_start   INT CHECK (quiet_hours_start >= 0 AND quiet_hours_start <= 23),
    quiet_hours_end     INT CHECK (quiet_hours_end >= 0 AND quiet_hours_end <= 23),
    timezone            VARCHAR(64) DEFAULT 'UTC',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE notification_preferences IS 'Per-user notification preferences and quiet hours';

-- ============================================================================
-- SYNC LOG
-- Audit log for sync operations.
-- ============================================================================
CREATE TABLE IF NOT EXISTS sync_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    device_id       VARCHAR(255),
    last_version    INT NOT NULL,
    new_version     INT NOT NULL,
    changes_applied INT NOT NULL DEFAULT 0,
    changes_rejected INT NOT NULL DEFAULT 0,
    new_cards       INT NOT NULL DEFAULT 0,
    updated_cards   INT NOT NULL DEFAULT 0,
    removed_cards   INT NOT NULL DEFAULT 0,
    duration_ms     INT,
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE sync_log IS 'Audit log for sync operations between client and server';

CREATE INDEX IF NOT EXISTS idx_sync_log_user ON sync_log (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_sync_log_created ON sync_log (created_at);

-- ============================================================================
-- TRIGGER: Auto-update updated_at timestamps
-- ============================================================================
CREATE OR REPLACE FUNCTION sync_update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_user_queues_updated ON user_queues;
CREATE TRIGGER trg_user_queues_updated
    BEFORE UPDATE ON user_queues
    FOR EACH ROW
    EXECUTE FUNCTION sync_update_updated_at();
