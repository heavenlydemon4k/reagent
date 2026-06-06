-- Decision Stack: Initial Schema Migration (001)
-- Service: ingestion
-- Creates all tables with dependencies + indexes
-- Note: gen_random_uuid() is used as proxy for UUIDv7 pending pg_uuidv7 extension availability

-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- 1. users table
CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email VARCHAR(255) UNIQUE NOT NULL,
  name VARCHAR(255),
  timezone VARCHAR(50) DEFAULT 'America/New_York',
  billing_plan VARCHAR(20) CHECK (billing_plan IN ('weekly', 'monthly')),
  billing_status VARCHAR(20) DEFAULT 'active',
  data_residency VARCHAR(20) DEFAULT 'us-east-1',
  created_at TIMESTAMPTZ DEFAULT NOW(),
  voice_calibrated_at TIMESTAMPTZ,
  onboarded_at TIMESTAMPTZ,
  encryption_key_id VARCHAR(255) NOT NULL
);

-- 2. email_accounts table
CREATE TABLE email_accounts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider VARCHAR(20) CHECK (provider IN ('gmail', 'outlook', 'exchange')),
  email_address VARCHAR(255) NOT NULL,
  refresh_token_enc BYTEA NOT NULL,
  access_token_enc BYTEA,
  token_expires_at TIMESTAMPTZ,
  scope_granted TEXT[] NOT NULL,
  history_id VARCHAR(255),
  delta_link TEXT,
  is_active BOOLEAN DEFAULT TRUE,
  last_sync_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(user_id, email_address)
);

-- 3. threads table
CREATE TABLE threads (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  thread_key VARCHAR(255) NOT NULL,
  source_account_id UUID NOT NULL REFERENCES email_accounts(id),
  subject TEXT,
  participant_emails TEXT[] NOT NULL,
  message_count INT DEFAULT 0,
  last_message_at TIMESTAMPTZ,
  status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'resolved', 'archived')),
  created_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(user_id, thread_key)
);

-- 4. raw_emails table
CREATE TABLE raw_emails (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  thread_id UUID NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source_account_id UUID NOT NULL REFERENCES email_accounts(id),
  message_id VARCHAR(255) UNIQUE NOT NULL,
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
  classification VARCHAR(20) CHECK (classification IN ('extract', 'auto', 'decision', 'pending'))
);

-- Indexes for raw_emails
CREATE INDEX idx_raw_emails_user_received ON raw_emails(user_id, received_at DESC);
CREATE INDEX idx_raw_emails_thread ON raw_emails(thread_id, received_at DESC);

-- 5. decision_cards table
CREATE TABLE decision_cards (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  thread_id UUID NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
  source_account_id UUID NOT NULL REFERENCES email_accounts(id),
  card_state VARCHAR(20) DEFAULT 'pending' CHECK (card_state IN ('pending', 'consulting', 'drafting', 'approved', 'sent', 'archived', 'expired')),
  from_field JSONB NOT NULL,
  they_want TEXT NOT NULL,
  context JSONB NOT NULL,
  need_from_user TEXT NOT NULL,
  chunk_citations JSONB NOT NULL DEFAULT '[]',
  urgency_score FLOAT DEFAULT 0.0 CHECK (urgency_score >= 0.0 AND urgency_score <= 1.0),
  auto_handle_rule_id UUID,
  classification_confidence FLOAT,
  suggested_deadline TIMESTAMPTZ,
  user_decided_at TIMESTAMPTZ,
  sent_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Indexes for decision_cards
CREATE INDEX idx_cards_user_state ON decision_cards(user_id, card_state, created_at DESC);
CREATE INDEX idx_cards_urgency ON decision_cards(user_id, card_state, urgency_score DESC) WHERE card_state = 'pending';

-- 6. auto_handle_rules table (owned by classification service; referenced here)
-- NOTE: The canonical definition lives in classification/migrations/001_initial_schema.up.sql.
--       This migration does NOT create the table — classification is the table owner.
--       decision_cards.auto_handle_rule_id references this table without an enforced FK
--       to avoid cross-service migration ordering dependencies.

-- 7. drafts table
CREATE TABLE drafts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  card_id UUID NOT NULL REFERENCES decision_cards(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  thread_id UUID NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
  draft_body TEXT NOT NULL,
  subject_line TEXT,
  tone_profile VARCHAR(50),
  in_reply_to VARCHAR(255),
  references TEXT[],
  model_used VARCHAR(50),
  tokens_used INT,
  user_approved BOOLEAN DEFAULT FALSE,
  sent_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

-- 8. calendar_events table
CREATE TABLE calendar_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source_account_id UUID NOT NULL REFERENCES email_accounts(id),
  external_event_id VARCHAR(255) NOT NULL,
  thread_id UUID REFERENCES threads(id),
  title TEXT NOT NULL,
  start_at TIMESTAMPTZ NOT NULL,
  end_at TIMESTAMPTZ NOT NULL,
  timezone VARCHAR(50),
  location TEXT,
  attendee_emails TEXT[],
  description TEXT,
  is_confirmed BOOLEAN DEFAULT FALSE,
  reminder_sent_at TIMESTAMPTZ,
  briefing_card_id UUID REFERENCES decision_cards(id),
  created_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(source_account_id, external_event_id)
);

-- 9. billing_records table
CREATE TABLE billing_records (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  period_start DATE NOT NULL,
  period_end DATE NOT NULL,
  plan VARCHAR(20) NOT NULL,
  amount_cents INT NOT NULL,
  stripe_invoice_id VARCHAR(255),
  status VARCHAR(20) DEFAULT 'pending',
  paid_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW()
);

-- 10. decision_logs table
CREATE TABLE decision_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  card_id UUID NOT NULL REFERENCES decision_cards(id) ON DELETE CASCADE,
  action VARCHAR(50) NOT NULL,
  user_input TEXT,
  agent_draft TEXT,
  final_output TEXT,
  metadata JSONB,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
