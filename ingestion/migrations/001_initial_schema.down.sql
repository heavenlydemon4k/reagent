-- Decision Stack: Rollback Initial Schema (001)
-- Drops all tables in reverse dependency order

DROP TABLE IF EXISTS decision_logs;
DROP TABLE IF EXISTS billing_records;
DROP TABLE IF EXISTS calendar_events;
DROP TABLE IF EXISTS drafts;
-- auto_handle_rules is owned by classification service — do NOT drop here
DROP TABLE IF EXISTS decision_cards;
DROP TABLE IF EXISTS raw_emails;
DROP TABLE IF EXISTS threads;
DROP TABLE IF EXISTS email_accounts;
DROP TABLE IF EXISTS users;
