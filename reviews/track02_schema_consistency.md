# Track 2: Schema Consistency Review Report

**Date:** 2025-01-15
**Reviewer:** Automated Schema Consistency Tool
**Scope:** PostgreSQL, Neo4j, Qdrant schemas vs. Go/Python struct definitions

---

## Executive Summary

| Category | Status | Issue Count |
|----------|--------|-------------|
| PostgreSQL -- Ingestion Migrations vs. Go Models | PASS | 2 minor |
| PostgreSQL -- Classification Migrations vs. Go Models | PASS with warnings | 4 warnings |
| PostgreSQL -- Sync Migrations vs. Go Models | FAIL | 4 issues |
| PostgreSQL -- Intelligence (Alembic) vs. Python Models | PASS | 1 minor |
| Cross-Service Schema Consistency | FAIL | 3 critical |
| Neo4j -- Constraints vs. App Queries | PASS | 1 minor |
| Qdrant -- Collection Config vs. App Operations | PASS with warnings | 2 warnings |

**Critical Issues Found:** 3
**Medium Issues:** 4
**Warnings:** 9

---

## 1. PostgreSQL -- Ingestion Migrations vs. Go Models

**Files Reviewed:**
- `ingestion/migrations/001_initial_schema.up.sql`
- `ingestion/internal/models/models.go`

### 1.1 raw_emails Table

| Column | Go Field (RawEmail) | Type Match | Notes |
|--------|---------------------|------------|-------|
| `id` | `ID uuid.UUID` | MATCH | `db:"id"` tag present |
| `thread_id` | `ThreadID uuid.UUID` | MATCH | `db:"thread_id"` tag present |
| `user_id` | `UserID uuid.UUID` | MATCH | `db:"user_id"` tag present |
| `source_account_id` | `SourceAccountID uuid.UUID` | MATCH | `db:"source_account_id"` tag present |
| `message_id` | `MessageID string` | MATCH | `db:"message_id"` tag present |
| `in_reply_to` | `InReplyTo *string` | MATCH | Nullable pointer, `db:"in_reply_to"` |
| `references` | `References []string` | MATCH | `db:"references"` tag present |
| `sender_email` | `SenderEmail string` | MATCH | `db:"sender_email"` tag present |
| `sender_name` | `SenderName *string` | MATCH | Nullable pointer, `db:"sender_name"` |
| `recipient_emails` | `RecipientEmails []string` | MATCH | `db:"recipient_emails"` tag present |
| `subject` | `Subject *string` | MATCH | Nullable pointer, `db:"subject"` |
| `body_text` | `BodyText *string` | MATCH | Nullable pointer, `db:"body_text"` |
| `body_html` | `BodyHTML *string` | MATCH | Nullable pointer, `db:"body_html"` |
| `has_attachments` | `HasAttachments bool` | MATCH | `db:"has_attachments"` tag present |
| `attachment_s3_uris` | `AttachmentS3URIs []string` | MATCH | `db:"attachment_s3_uris"` tag present |
| `extracted_codes` | `ExtractedCodes []string` | MATCH | `db:"extracted_codes"` tag present |
| `received_at` | `ReceivedAt time.Time` | MATCH | `db:"received_at"` tag present |
| `parsed_at` | `ParsedAt time.Time` | MATCH | `db:"parsed_at"` tag present |
| `retention_until` | `RetentionUntil time.Time` | MATCH | `db:"retention_until"` tag present |
| `classification` | `Classification *string` | MATCH | Nullable pointer, `db:"classification"` |

**Status:** All 20 columns match their Go struct fields.

### 1.2 threads Table

| Column | Go Field (Thread) | Type Match | Notes |
|--------|-------------------|------------|-------|
| `id` | `ID uuid.UUID` | MATCH | `db:"id"` tag present |
| `user_id` | `UserID uuid.UUID` | MATCH | `db:"user_id"` tag present |
| `thread_key` | `ThreadKey string` | MATCH | `db:"thread_key"` tag present |
| `source_account_id` | `SourceAccountID uuid.UUID` | MATCH | `db:"source_account_id"` tag present |
| `subject` | `Subject *string` | MATCH | Nullable pointer, `db:"subject"` |
| `participant_emails` | `ParticipantEmails []string` | MATCH | `db:"participant_emails"` tag present |
| `message_count` | `MessageCount int` | MATCH | `db:"message_count"` tag present |
| `last_message_at` | `LastMessageAt *time.Time` | MATCH | Nullable pointer, `db:"last_message_at"` |
| `status` | `Status string` | MATCH | `db:"status"` tag present |
| `created_at` | `CreatedAt time.Time` | MATCH | `db:"created_at"` tag present |

**Status:** All 10 columns match.

### 1.3 Users, email_accounts, and other tables

**Finding:** The ingestion `models.go` does NOT define structs for `users`, `email_accounts`, `decision_cards`, `auto_handle_rules`, `drafts`, `calendar_events`, `billing_records`, or `decision_logs`. These tables are created by the ingestion migration but their corresponding Go models live in the **sync** service (`sync/internal/models/models.go`).

**Assessment:** This is an **architectural boundary** issue, not a schema mismatch. The ingestion service owns the migration but not all models. In a microservices architecture, this creates tight coupling -- the ingestion migration creates tables that other services depend on.

**Recommendation:** Consider centralizing shared table migrations or using a shared models library.

---

## 2. PostgreSQL -- Classification Migrations vs. Go Models

**Files Reviewed:**
- `classification/migrations/001_initial_schema.up.sql`
- `classification/internal/models/models.go`

### 2.1 auto_handle_rules Table

| Column | Go Field (AutoHandleRule) | Type Match | Notes |
|--------|--------------------------|------------|-------|
| `id` | `ID uuid.UUID` | MATCH | `db:"id"` + `json:"id"` tags |
| `user_id` | `UserID uuid.UUID` | MATCH | `db:"user_id"` tag |
| `name` | `Name string` | MATCH | `db:"name"` tag |
| `predicate` | `Predicate RulePredicate` | WARNING | No Scan/Value methods for JSONB |
| `action_type` | `ActionType string` | MATCH | `db:"action_type"` tag |
| `action_config` | `ActionConfig json.RawMessage` | WARNING | No Scan/Value methods |
| `confidence_threshold` | `ConfidenceThreshold float64` | MATCH | `db:"confidence_threshold"` |
| `status` | `Status string` | MATCH | `db:"status"` tag |
| `staged_at` | `StagedAt *time.Time` | MATCH | Nullable pointer |
| `activated_at` | `ActivatedAt *time.Time` | MATCH | Nullable pointer |
| `revoked_at` | `RevokedAt *time.Time` | MATCH | Nullable pointer |
| `usage_count` | `UsageCount int` | MATCH | `db:"usage_count"` |
| `created_at` | `CreatedAt time.Time` | MATCH | `db:"created_at"` |

**Warnings:**
1. `Predicate` field uses `RulePredicate` struct type without `sql.Scanner`/`driver.Valuer` interfaces. JSONB serialization may fail depending on the PostgreSQL driver used.
2. `ActionConfig` uses `json.RawMessage` which does NOT implement `sql.Scanner`/`driver.Valuer`. This will fail with standard `database/sql` unless a driver-specific extension (like `pgx`) handles it automatically.
3. PostgreSQL ENUM types (`rule_status`, `action_type`) are used in the migration, but Go model uses plain `string`. This is a type mismatch -- while drivers usually handle the conversion, it is not strictly consistent.
4. The migration uses `uuid_generate_v4()` (from uuid-ossp extension) while ingestion uses `gen_random_uuid()` (from pgcrypto). Minor inconsistency in UUID generation approach.

### 2.2 Constraint Comparison

| Constraint | In Migration | In Go Model | Match |
|------------|-------------|-------------|-------|
| `chk_confidence_threshold` | `confidence_threshold >= 0.0 AND <= 1.0` | `float64` (no bounds) | MISMATCH |
| `chk_staged_requires_staged_at` | `status <> 'staged' OR staged_at IS NOT NULL` | No validation | MISMATCH |
| `chk_active_requires_activated_at` | `status <> 'active' OR activated_at IS NOT NULL` | No validation | MISMATCH |
| `chk_revoked_requires_revoked_at` | `status <> 'revoked' OR revoked_at IS NOT NULL` | No validation | MISMATCH |

**Finding:** The Go model does not implement the status/timestamp cross-field validation that exists in the database. Status transitions require corresponding timestamp fields, but this is not enforced in code.

**Severity:** Medium -- The database will enforce these constraints, but code should validate before attempting invalid transitions.

---

## 3. PostgreSQL -- Sync Migrations vs. Go Models

**Files Reviewed:**
- `sync/migrations/001_initial_schema.up.sql`
- `sync/migrations/002_auth_refresh_tokens.up.sql`
- `sync/internal/models/models.go`

### 3.1 device_sessions Table

| Column | Go Field (DeviceSession) | Type Match | Notes |
|--------|--------------------------|------------|-------|
| `id` | `ID uuid.UUID` | MATCH | `db:"id"` tag |
| `user_id` | `UserID uuid.UUID` | MATCH | `db:"user_id"` tag |
| `device_id` | `DeviceID string` | MATCH | `db:"device_id"` tag |
| `device_type` | `DeviceType string` | MATCH | `db:"device_type"` tag |
| `device_name` | `DeviceName string` | MATCH | `db:"device_name"` tag |
| `fcm_token` | `FCMToken *string` | MATCH | Nullable pointer |
| `apns_token` | `APNSToken *string` | MATCH | Nullable pointer |
| `last_active_at` | `LastActiveAt time.Time` | MATCH | `db:"last_active_at"` |
| `created_at` | `CreatedAt time.Time` | MATCH | `db:"created_at"` |

**Status:** All columns match.

### 3.2 notifications Table

| Column | Go Field (Notification) | Type Match | Notes |
|--------|-------------------------|------------|-------|
| `id` | `ID uuid.UUID` | MATCH | `db:"id"` tag |
| `user_id` | `UserID uuid.UUID` | MATCH | `db:"user_id"` tag |
| `type` | `Type string` | MATCH | `db:"type"` tag |
| `title` | `Title string` | MATCH | `db:"title"` tag |
| `body` | `Body string` | MATCH | `db:"body"` tag |
| `data` | `Data json.RawMessage` | MATCH | `db:"data"` tag |
| `sent_at` | `SentAt *time.Time` | MATCH | Nullable pointer |
| `read_at` | `ReadAt *time.Time` | MATCH | Nullable pointer |
| `error_message` | **MISSING** | **FAIL** | Column exists in DB but NOT in struct |
| `created_at` | `CreatedAt time.Time` | MATCH | `db:"created_at"` tag |

**ISSUE-3.1:** The `notifications` table has `error_message TEXT` but the Go `Notification` struct does NOT have this field. Any error messages written to the database cannot be read through the Go model.

**Severity:** Medium -- Error tracking data is inaccessible via the typed model.

### 3.3 user_queues Table

| Column | Go Field (UserQueue) | Type Match | Notes |
|--------|----------------------|------------|-------|
| `user_id` | `UserID uuid.UUID` | MATCH | `db:"user_id"` tag |
| `pending_count` | `PendingCount int` | MATCH | `db:"pending_count"` |
| `server_version` | `ServerVersion int` | MATCH | `db:"server_version"` |
| `last_notification_at` | `LastNotificationAt *time.Time` | MATCH | Nullable pointer |
| `created_at` | `CreatedAt time.Time` | MATCH | `db:"created_at"` |
| `updated_at` | `UpdatedAt time.Time` | MATCH | `db:"updated_at"` |

**Status:** All columns match.

### 3.4 Tables Without Go Structs

**Finding:** The following sync service tables do NOT have corresponding Go structs:

| Table | Missing Struct | Severity |
|-------|---------------|----------|
| `refresh_tokens` | No struct defined | Medium |
| `sync_log` | No struct defined | Low (log table) |
| `notification_preferences` | No struct defined | Medium |

**ISSUE-3.2:** The `refresh_tokens` table is managed in migration 002 but has no Go struct. Token rotation logic must use raw SQL or ad-hoc queries.

**ISSUE-3.3:** The `notification_preferences` table has no Go struct. Quiet hours and notification settings cannot be read/written through typed models.

### 3.5 Go Structs Without Tables

**Finding:** The following Go structs do NOT have corresponding sync service tables:

| Struct | Missing Table | Severity |
|--------|--------------|----------|
| `ReminderJob` | No `reminder_jobs` table in sync migrations | High |

**ISSUE-3.4:** The `ReminderJob` struct (with fields: id, user_id, event_id, reminder_type, scheduled_for, processed_at, created_at) is defined in sync models but there is NO corresponding `reminder_jobs` table in the sync migrations.

---

## 4. PostgreSQL -- Intelligence (Alembic) vs. Python Models

**Files Reviewed:**
- `intelligence/alembic/versions/001_initial_schema.py`
- Various Pydantic models in `intelligence/app/*/models.py`

### 4.1 Architecture Assessment

**Finding:** The intelligence service uses **Pydantic models** (not SQLAlchemy ORM models) for application code. The Alembic migration is a **hand-written SQL migration** using `op.create_table()` / `op.create_index()` operations. The `env.py` attempts to import `intelligence.models.base.Base` but falls back to `target_metadata = None` since no SQLAlchemy models exist.

**Assessment:** This is a valid architecture -- migrations are managed independently from application models. However, there is NO automatic validation that Pydantic models match the database schema. Changes to the database require manual synchronization with Pydantic models.

### 4.2 Schema Comparison: Intelligence vs. Ingestion Migrations

The intelligence Alembic migration creates the same 10 tables as the ingestion SQL migration. Cross-comparison shows:

| Table | Ingestion SQL | Intelligence Alembic | Match |
|-------|--------------|---------------------|-------|
| `users` | 10 columns | 10 columns | MATCH |
| `email_accounts` | 13 columns + 2 constraints | 13 columns + 2 constraints | MATCH |
| `threads` | 10 columns + 3 constraints | 10 columns + 3 constraints | MATCH |
| `raw_emails` | 21 columns + 3 constraints | 21 columns + 3 constraints | MATCH |
| `decision_cards` | 18 columns + 3 constraints | 18 columns + 3 constraints | MATCH |
| `auto_handle_rules` | 13 columns | 13 columns | **MISMATCH** (see Section 5) |
| `drafts` | 15 columns + 3 FK | 15 columns + 3 FK | MATCH |
| `calendar_events` | 16 columns + 4 FK | 16 columns + 4 FK | MATCH |
| `billing_records` | 9 columns + 1 FK | 9 columns + 1 FK | MATCH |
| `decision_logs` | 8 columns + 2 FK | 8 columns + 2 FK | MATCH |

---

## 5. Cross-Service Schema Consistency (CRITICAL)

**Finding:** The `auto_handle_rules` table is defined **three different ways** across three services:

### 5.1 Comparison of auto_handle_rules Across Services

| Aspect | Ingestion | Classification | Intelligence (Alembic) |
|--------|-----------|---------------|----------------------|
| **UUID gen** | `gen_random_uuid()` | `uuid_generate_v4()` | `gen_random_uuid()` |
| **name** | `VARCHAR(255) NOT NULL` | `TEXT NOT NULL` | `VARCHAR(255) NOT NULL` |
| **predicate default** | No default | `'{"allOf": [], "anyOf": []}'` | No default |
| **action_type** | `VARCHAR(50) + CHECK` | `action_type ENUM` | `VARCHAR(50) + CHECK` |
| **action_config default** | No default | `'{}'` | No default |
| **confidence_threshold** | `FLOAT DEFAULT 0.92` | `DECIMAL(3,2) DEFAULT 0.92 NOT NULL` | `FLOAT DEFAULT 0.92` |
| **status** | `VARCHAR(20) + CHECK` | `rule_status ENUM` | `VARCHAR(20) + CHECK` |
| **staged_at default** | `DEFAULT NOW()` | No default | `server_default NOW()` |
| **FK on user_id** | `REFERENCES users(id) CASCADE` | **No FK constraint** | `REFERENCES users(id) CASCADE` |
| **chk_confidence_range** | No | Yes (0.0-1.0) | No |
| **chk_staged_requires_staged_at** | No | Yes | No |
| **chk_active_requires_activated_at** | No | Yes | No |
| **chk_revoked_requires_revoked_at** | No | Yes | No |

### ISSUE-5.1: Divergent auto_handle_rules Schema (CRITICAL)

The classification migration defines `auto_handle_rules` with:
- PostgreSQL ENUM types (not used by other services)
- `DECIMAL(3,2)` for confidence_threshold (vs `FLOAT` elsewhere)
- Additional CHECK constraints for status/timestamp consistency
- **No foreign key constraint on user_id**

**Impact:** If all three migrations run on the same database, the second and third will fail because the table already exists with a different schema. In a microservices setup with separate databases, this creates data compatibility issues.

**Recommendation:** Standardize the `auto_handle_rules` schema across all services. Use a single source of truth.

### ISSUE-5.2: UUID Generation Inconsistency

- Ingestion: `gen_random_uuid()` (pgcrypto)
- Classification: `uuid_generate_v4()` (uuid-ossp)
- Intelligence: `gen_random_uuid()` (pgcrypto)

Both extensions work, but using different extensions creates an unnecessary dependency on `uuid-ossp` in the classification service.

### ISSUE-5.3: Missing Foreign Key in Classification (CRITICAL)

The classification migration's `auto_handle_rules` table has `user_id UUID NOT NULL` but does NOT define a `REFERENCES users(id)` foreign key constraint. This breaks referential integrity.

---

## 6. Neo4j -- Constraints vs. App Queries

**Files Reviewed:**
- `intelligence/core/neo4j_schema.cypher`
- `intelligence/intelligence/core/neo4j_client.py`
- `ingestion/internal/contact/neo4j.go`

### 6.1 Constraints/Indexes Defined

| Constraint/Index | Type | Property | Used By Queries? |
|-----------------|------|----------|-----------------|
| `contact_id` | UNIQUE | Contact.id | Yes -- CreateContact, all MATCH by id |
| `contact_email` | UNIQUE | Contact.canonical_email | Yes -- FindContactByEmail |
| `contact_user_id` | INDEX | Contact.user_id | Yes -- all user-scoped queries |
| `contact_canonical_email` | INDEX | Contact.canonical_email | Yes -- FindContactByEmail |
| `interaction_date` | INDEX | INTERACTION.date | Yes -- traversal templates with date filter |
| `interaction_thread_id` | INDEX | INTERACTION.thread_id | Yes -- thread reconstruction |

### 6.2 Go Neo4j Queries (ingestion)

| Query | Constraint/Index Used | Status |
|-------|----------------------|--------|
| `FindContactByEmail` -- MATCH on user_id + canonical_email | `contact_user_id` + `contact_email` | MATCH |
| `FindContactsByName` -- MATCH on user_id, filter name_variants | `contact_user_id` | MATCH |
| `CreateContact` -- CREATE with id, canonical_email | `contact_id` + `contact_email` (enforced) | MATCH |
| `CreateSimilarToEdge` -- MATCH on id, MERGE SIMILAR_TO | `contact_id` (for MATCH) | MATCH |
| `UpdateContactInteraction` -- MATCH on id, CREATE INTERACTION | `contact_id` (for MATCH) | MATCH |

**Warning:** The Go `UpdateContactInteraction` creates `INTERACTION` edges but does NOT populate all properties defined in the schema documentation (e.g., `email_id`, `agreed_to`, `committed_to`, `quote_amount`, `tone`, `mentions_project`). Only `thread_id`, `direction`, `subject`, `sent_at`, and `created_at` are set.

### 6.3 Python Neo4j Client (intelligence)

The `neo4j_client.py` is a thin driver wrapper with no business queries. Query templates in `neo4j_schema.cypher` reference constraints correctly.

### ISSUE-6.1: Schema Documentation vs. Code Divergence (Minor)

The Cypher schema documentation shows rich `INTERACTION` properties (`agreed_to`, `committed_to`, `quote_amount`, `tone`, `mentions_project`) but the Go `UpdateContactInteraction` function only sets a minimal subset. The full schema is aspirational -- the current code does not populate it.

---

## 7. Qdrant -- Collection Config vs. App Operations

**Files Reviewed:**
- `intelligence/core/qdrant_setup.py`
- `intelligence/intelligence/app/compression/store.py`
- `intelligence/app/drafting/voice_retriever.py`

### 7.1 Collection Configuration

| Collection | Vector Size | Distance | On-Disk | Collections Match Config |
|------------|------------|----------|---------|-------------------------|
| `email_chunks` | 1024 | COSINE | True | Yes |
| `voice_examples` | 1024 | COSINE | True | Yes |
| `consultation_index` | 1024 | COSINE | True | Yes |

**Vector Size Verification:**
- Config: `VECTOR_SIZE = 1024` (comment: "match OpenAI text-embedding-3-large")
- No hardcoded vector sizes in application code
- **Status:** PASS

### 7.2 email_chunks: Config vs. store.py Operations

**Payload Fields in Config:**
`user_id` (keyword), `chunk_id` (keyword), `thread_id` (keyword), `email_id` (keyword), `sender_email` (keyword), `is_signature` (bool), `timestamp` (integer)

**Payload Fields in store.py Upsert:**
`user_id`, `chunk_id`, `thread_id`, `email_id`, `sender_email`, `timestamp`, `paragraph_index`, `is_signature`, `content_snippet`

| Field | In Config? | In Upsert? | Indexed? | Status |
|-------|-----------|-----------|----------|--------|
| `user_id` | Yes | Yes | keyword | MATCH |
| `chunk_id` | Yes | Yes | keyword | MATCH |
| `thread_id` | Yes | Yes | keyword | MATCH |
| `email_id` | Yes | Yes | keyword | MATCH |
| `sender_email` | Yes | Yes | keyword | MATCH |
| `timestamp` | Yes | Yes | integer | MATCH |
| `is_signature` | Yes | Yes | bool | MATCH |
| `paragraph_index` | **No** | Yes | **No** | WARNING-7.1 |
| `content_snippet` | **No** | Yes | **No** | WARNING-7.1 |

**WARNING-7.1:** The `email_chunks` collection config does not include `paragraph_index` and `content_snippet` in its payload_fields. These fields are stored in Qdrant but are **not indexed**. The `get_chunks_by_thread` scroll and `search_similar` operations work because they do not filter on these fields -- they are just returned in results. This is acceptable if these fields are never used in filters.

### 7.3 voice_examples: Config vs. voice_retriever.py Operations

**Payload Fields in Config:**
`user_id` (keyword), `example_id` (keyword), `sender_email` (keyword), `sent_at` (integer), `tone_tags` (keyword)

**Payload Fields Used in voice_retriever.py:**
`user_id`, `sender_email`, `reply_text`, `topic_keywords`, `tone_tags`, `sent_at`/`timestamp`, `content_snippet`

| Field | In Config? | In Query? | Indexed? | Status |
|-------|-----------|----------|----------|--------|
| `user_id` | Yes | Yes (filter) | keyword | MATCH |
| `sender_email` | Yes | Yes (filter) | keyword | MATCH |
| `example_id` | Yes | No | keyword | OK (stored not queried) |
| `sent_at` | Yes | Yes (read) | integer | MATCH |
| `tone_tags` | Yes | Yes (read) | keyword | MATCH |
| `reply_text` | **No** | Yes (read) | **No** | WARNING-7.2 |
| `topic_keywords` | **No** | Yes (read) | **No** | WARNING-7.2 |
| `content_snippet` | **No** | Yes (fallback read) | **No** | WARNING-7.2 |

**WARNING-7.2:** The `voice_examples` collection stores and reads `reply_text`, `topic_keywords`, and `content_snippet` payload fields, but these are not declared in the collection config. Like WARNING-7.1, these are not used in filters, so the lack of indexing is acceptable for retrieval. However, the config should document all stored payload fields for maintainability.

### 7.4 Filter Usage Verification

All Qdrant filter operations use payload fields that are properly indexed:

| Operation | Collection | Filter Fields | Indexed? |
|-----------|-----------|--------------|----------|
| `get_chunks_by_thread` | email_chunks | `thread_id`, `user_id` | Yes |
| `search_similar` | email_chunks | `user_id`, `is_signature` | Yes |
| `delete_by_thread` | email_chunks | `thread_id`, `user_id` | Yes |
| `_qdrant_search` | voice_examples | `user_id`, `sender_email` | Yes |

**Status:** All query filters use indexed fields.

---

## 8. UUID Type Consistency

| Layer | UUID Type | Consistent? |
|-------|-----------|-------------|
| PostgreSQL | `UUID` (native) | Yes |
| Go | `github.com/google/uuid.UUID` | Yes |
| Python | `uuid.UUID` | Yes |
| Neo4j | String representation | Yes (stored as string) |
| Qdrant payload | `str(uuid)` | Yes |

**Status:** All UUID types are consistent across the stack.

---

## 9. JSON/JSONB Field Consistency

| Field | PostgreSQL Type | Go Type | Match? |
|-------|----------------|---------|--------|
| `from_field` | JSONB | `json.RawMessage` | Yes |
| `context` | JSONB | `json.RawMessage` | Yes |
| `chunk_citations` | JSONB | `json.RawMessage` | Yes |
| `predicate` | JSONB | `RulePredicate` (struct) | Partial -- lacks Scan/Value |
| `action_config` | JSONB | `json.RawMessage` | Yes |
| `data` (notifications) | JSONB | `json.RawMessage` | Yes |

---

## 10. Foreign Key Consistency

| Table Column | FK Definition (Ingestion) | FK Definition (Intelligence) | FK Definition (Classification) | Status |
|-------------|--------------------------|------------------------------|--------------------------------|--------|
| `email_accounts.user_id` | `REFERENCES users(id) ON DELETE CASCADE` | Same | N/A (separate DB) | MATCH |
| `threads.user_id` | `REFERENCES users(id) ON DELETE CASCADE` | Same | N/A | MATCH |
| `threads.source_account_id` | `REFERENCES email_accounts(id)` | Same | N/A | MATCH |
| `raw_emails.thread_id` | `REFERENCES threads(id) ON DELETE CASCADE` | Same | N/A | MATCH |
| `raw_emails.user_id` | `REFERENCES users(id) ON DELETE CASCADE` | Same | N/A | MATCH |
| `decision_cards.user_id` | `REFERENCES users(id) ON DELETE CASCADE` | Same | N/A | MATCH |
| `decision_cards.thread_id` | `REFERENCES threads(id) ON DELETE CASCADE` | Same | N/A | MATCH |
| `auto_handle_rules.user_id` | `REFERENCES users(id) ON DELETE CASCADE` | Same | **No FK** | **FAIL** |

---

## Issue Registry

### Critical Issues

| ID | Description | Location | Impact | Recommendation |
|----|-------------|----------|--------|----------------|
| ISSUE-5.1 | `auto_handle_rules` table has 3 incompatible definitions across services | All migrations | Migration conflicts in shared DB; data inconsistency | Create a single shared migration file; standardize on one schema |
| ISSUE-5.3 | Classification `auto_handle_rules` missing FK on `user_id` | `classification/migrations/001_initial_schema.up.sql` | Orphaned rules possible | Add `REFERENCES users(id) ON DELETE CASCADE` |
| ISSUE-3.4 | `ReminderJob` struct has no corresponding table in sync migrations | `sync/internal/models/models.go` | Cannot persist reminder jobs | Add `reminder_jobs` table to sync migrations |

### Medium Issues

| ID | Description | Location | Impact | Recommendation |
|----|-------------|----------|--------|----------------|
| ISSUE-3.1 | `notifications.error_message` column missing from Go struct | `sync/internal/models/models.go` | Error data not accessible via model | Add `ErrorMessage *string` field to Notification struct |
| ISSUE-3.2 | `refresh_tokens` table has no Go struct | `sync/migrations/002_auth_refresh_tokens.up.sql` | Token management uses raw SQL | Add RefreshToken struct to sync models |
| ISSUE-3.3 | `notification_preferences` table has no Go struct | `sync/migrations/001_initial_schema.up.sql` | Preferences managed via raw SQL | Add NotificationPreferences struct to sync models |
| ISSUE-5.2 | UUID generation uses different extensions (`pgcrypto` vs `uuid-ossp`) | Classification migration | Extra extension dependency | Standardize on `pgcrypto` + `gen_random_uuid()` |

### Warnings

| ID | Description | Location | Impact | Recommendation |
|----|-------------|----------|--------|----------------|
| WARNING-2.1 | `RulePredicate` lacks JSONB Scan/Value methods | `classification/internal/models/models.go` | May fail with some PostgreSQL drivers | Implement `sql.Scanner` and `driver.Valuer` |
| WARNING-2.2 | `json.RawMessage` lacks Scan/Value for standard sql | `classification/internal/models/models.go` | Works with pgx but not database/sql | Verify driver compatibility or add methods |
| WARNING-6.1 | Neo4j INTERACTION edges do not populate full schema | `ingestion/internal/contact/neo4j.go` | Rich interaction data not captured | Populate all documented properties |
| WARNING-7.1 | `paragraph_index` and `content_snippet` not in email_chunks config | `intelligence/core/qdrant_setup.py` | Unindexed payload fields | Add to payload_fields for documentation |
| WARNING-7.2 | `reply_text`, `topic_keywords`, `content_snippet` not in voice_examples config | `intelligence/core/qdrant_setup.py` | Unindexed payload fields | Add to payload_fields for documentation |
| WARNING-1.1 | Ingestion migration creates tables without owning all models | `ingestion/migrations/001_initial_schema.up.sql` | Tight coupling across services | Consider shared models library or schema-per-service |

---

## Summary Statistics

| Metric | Count |
|--------|-------|
| Tables Reviewed (PostgreSQL) | 16 |
| Go Structs Reviewed | 12 |
| Python Models Reviewed | 8 |
| Neo4j Constraints Reviewed | 2 constraints + 4 indexes |
| Qdrant Collections Reviewed | 3 |
| Columns/Fields Checked | 200+ |
| **Critical Issues** | **3** |
| **Medium Issues** | **4** |
| **Warnings** | **6** |
| **Total Findings** | **13** |

---

*Report generated by automated schema consistency review tool.*
