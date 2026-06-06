-- Sync & State — Auth Refresh Tokens
-- Adds refresh token storage for JWT rotation.

-- ============================================================================
-- REFRESH TOKENS
-- Stores SHA-256 hashed refresh tokens for JWT rotation.
-- One row per (user_id, device_id) — upsert on rotation.
-- ============================================================================
CREATE TABLE IF NOT EXISTS refresh_tokens (
    user_id         UUID NOT NULL,
    device_id       VARCHAR(255) NOT NULL,
    token_hash      VARCHAR(64) NOT NULL,   -- SHA-256 hex hash of composite refresh token
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days'),

    PRIMARY KEY (user_id, device_id)
);

COMMENT ON TABLE refresh_tokens IS 'SHA-256 hashed refresh tokens for JWT rotation per device';

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires ON refresh_tokens (expires_at)
    WHERE expires_at < NOW() + INTERVAL '7 days';
