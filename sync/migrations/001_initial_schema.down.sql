-- Sync & State — Rollback Initial Schema

DROP TRIGGER IF EXISTS trg_user_queues_updated ON user_queues;
DROP FUNCTION IF EXISTS sync_update_updated_at();

DROP INDEX IF EXISTS idx_sync_log_user;
DROP INDEX IF EXISTS idx_sync_log_created;
DROP TABLE IF EXISTS sync_log;

DROP INDEX IF EXISTS idx_notifications_user;
DROP INDEX IF EXISTS idx_notifications_unsent;
DROP INDEX IF EXISTS idx_notifications_type;
DROP TABLE IF EXISTS notifications;

DROP TABLE IF EXISTS notification_preferences;

DROP INDEX IF EXISTS idx_device_sessions_user;
DROP INDEX IF EXISTS idx_device_sessions_fcm;
DROP INDEX IF EXISTS idx_device_sessions_apns;
DROP TABLE IF EXISTS device_sessions;

DROP INDEX IF EXISTS idx_user_queues_pending_count;
DROP INDEX IF EXISTS idx_user_queues_version;
DROP TABLE IF EXISTS user_queues;
