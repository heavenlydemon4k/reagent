-- Initial schema for Classification Core
-- Creates the auto_handle_rules table matching models.AutoHandleRule

BEGIN;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TYPE rule_status AS ENUM ('staged', 'active', 'revoked');
CREATE TYPE action_type AS ENUM ('reply_template', 'forward', 'calendar_accept', 'delete', 'extract_notify');

CREATE TABLE auto_handle_rules (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id             UUID NOT NULL,
    name                TEXT NOT NULL,
    predicate           JSONB NOT NULL DEFAULT '{"allOf": [], "anyOf": []}',
    action_type         action_type NOT NULL,
    action_config       JSONB NOT NULL DEFAULT '{}',
    confidence_threshold DECIMAL(3,2) NOT NULL DEFAULT 0.92,
    status              rule_status NOT NULL DEFAULT 'staged',
    staged_at           TIMESTAMPTZ,
    activated_at        TIMESTAMPTZ,
    revoked_at          TIMESTAMPTZ,
    usage_count         INT NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_confidence_threshold CHECK (confidence_threshold >= 0.0 AND confidence_threshold <= 1.0)
);

-- Indexes
CREATE INDEX idx_auto_handle_rules_user_id ON auto_handle_rules(user_id);
CREATE INDEX idx_auto_handle_rules_user_status ON auto_handle_rules(user_id, status);
CREATE INDEX idx_auto_handle_rules_status ON auto_handle_rules(status);
CREATE INDEX idx_auto_handle_rules_created_at ON auto_handle_rules(created_at);

-- Partial index for active rules (hot path for matching)
CREATE INDEX idx_auto_handle_rules_active ON auto_handle_rules(user_id, activated_at)
    WHERE status = 'active';

-- Composite index for rule lookup during classification
CREATE INDEX idx_auto_handle_rules_lookup ON auto_handle_rules(user_id, status, confidence_threshold);

-- Constraint: staged rules must have staged_at set
ALTER TABLE auto_handle_rules
    ADD CONSTRAINT chk_staged_requires_staged_at
    CHECK (status <> 'staged' OR staged_at IS NOT NULL);

-- Constraint: active rules must have activated_at set
ALTER TABLE auto_handle_rules
    ADD CONSTRAINT chk_active_requires_activated_at
    CHECK (status <> 'active' OR activated_at IS NOT NULL);

-- Constraint: revoked rules must have revoked_at set
ALTER TABLE auto_handle_rules
    ADD CONSTRAINT chk_revoked_requires_revoked_at
    CHECK (status <> 'revoked' OR revoked_at IS NOT NULL);

-- Foreign key: user_id references users table (managed by ingestion service)
ALTER TABLE auto_handle_rules
    ADD CONSTRAINT fk_auto_handle_rules_user_id
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- Comments
COMMENT ON TABLE auto_handle_rules IS 'User-created delegation rules for the Auto-Handle pipeline';
COMMENT ON COLUMN auto_handle_rules.predicate IS 'JSON RulePredicate: {allOf: [...], anyOf: [...]}';
COMMENT ON COLUMN auto_handle_rules.confidence_threshold IS 'Hard floor at 0.92; reject match if below';

COMMIT;
