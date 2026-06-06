-- Rollback 001_initial_schema

BEGIN;

DROP INDEX IF EXISTS idx_auto_handle_rules_lookup;
DROP INDEX IF EXISTS idx_auto_handle_rules_active;
DROP INDEX IF EXISTS idx_auto_handle_rules_created_at;
DROP INDEX IF EXISTS idx_auto_handle_rules_status;
DROP INDEX IF EXISTS idx_auto_handle_rules_user_status;
DROP INDEX IF EXISTS idx_auto_handle_rules_user_id;

DROP TABLE IF EXISTS auto_handle_rules;

DROP TYPE IF EXISTS rule_status;
DROP TYPE IF EXISTS action_type;

COMMIT;
