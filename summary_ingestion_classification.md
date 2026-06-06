# Architecture Summary: Ingestion Mesh + Classification Core (Phases 1-2)

## Table of Contents
1. [System Overview](#system-overview)
2. [Component: Ingestion Mesh (Entry Point)](#component-ingestion-mesh)
3. [Component: Core Data Models](#component-core-data-models)
4. [Component: NATS Event Bus](#component-nats-event-bus)
5. [Component: Token Encryption (Crypto)](#component-token-encryption)
6. [Component: Google OAuth](#component-google-oauth)
7. [Component: MIME Parser](#component-mime-parser)
8. [Component: Thread Reconstruction Engine](#component-thread-reconstruction)
9. [Component: HTTP Server Entry Point](#component-server-entry)
10. [Component: Worker Entry Point](#component-worker-entry)
11. [Component: Classification Core](#component-classification-core)
12. [Component: Classification Models](#component-classification-models)
13. [Component: Classification Engine](#component-classification-engine)
14. [Component: Tri-State Router](#component-tri-state-router)
15. [Component: 48h Staging System](#component-48h-staging)

---

## System Overview

The system is a two-phase email processing pipeline:

```
Gmail/Outlook  --(OAuth2+Webhooks+Polling)-->  Ingestion Mesh  --(NATS)-->  Classification Core
                                                                                |
                                    +------------------+------------------+------------------+
                                    v                  v                  v
                              Extract-Only      Auto-Handle        Decision Stack
                              (2FA,tracking)    (rule execution)   (Intelligence Layer)
```

**Phase 1 (Ingestion Mesh)**: Ingests raw emails via Gmail/Outlook APIs, parses MIME, reconstructs threads, deduplicates contacts, encrypts OAuth tokens, and publishes structured events to NATS JetStream.

**Phase 2 (Classification Core)**: Consumes `email.ingested` events, routes each email through a tri-state pipeline (Extract-Only -> Auto-Handle -> Decision Stack), manages auto-handle rules with a 48-hour staging window, and publishes results to downstream NATS subjects.

---

## Component: Ingestion Mesh (Entry Point)

- **Purpose**: Entry point of the Decision Stack platform. Ingests emails from Gmail and Outlook, parses MIME, threads conversations, deduplicates contacts, and publishes events to NATS.
- **Architecture**: Dual-binary Go service (server + worker) sharing the same Docker image. Server handles HTTP (webhooks, OAuth, health); worker handles background polling with a worker pool.
- **Key Files**:
  - `/mnt/agents/output/ingestion/docs/ARCHITECTURE.md`
  - `/mnt/agents/output/ingestion/cmd/server/main.go`
  - `/mnt/agents/output/ingestion/cmd/worker/main.go`
- **Design Decisions**:
  - Two separate K8s deployments (server + worker) from the same image, different CMD entries
  - Server on port 8080; worker pool size = 4 concurrent pollers
  - Rate limiting via Redis: Gmail (250 quota units/sec), Outlook (10K/10min)
  - Graceful shutdown with 30-second timeout on SIGTERM
  - All config via environment variables (12-factor app)
- **Data Flow**:
  ```
  Gmail/Outlook --> OAuth/Webhooks/Polling --> Parser --> Threader --> Deduper --> NATS Event
  ```
- **TODOs/Issues**:
  - Polling worker fetchers are stubs (return errors) — real Gmail/Outlook API clients not wired
  - Token store adapter (`GetTokens`, `RefreshIfNeeded`) returns "not yet implemented"
  - MIME parser adapter not fully aligned with `poll.MIMEParser` interface
  - `internal/server/router.go` is dead code (inline router used instead)

---

## Component: Core Data Models

- **Purpose**: Shared data structures that are the contracts between all ingestion components. Must not change without cross-track coordination.
- **Architecture**: Single package (`internal/models`) with structs for raw emails, threads, contacts, events, OAuth tokens, and rate limiting.
- **Key Files**:
  - `/mnt/agents/output/ingestion/internal/models/models.go`
- **Design Decisions**:
  - `ParsedEmail` is the intermediate representation: parser produces it, threading/dedup/event consume it
  - `ThreadKey` is a SHA-256 of sorted participants + normalized subject (deterministic)
  - `EmailIngestedEvent` is the NATS wire contract with Classification Core
  - `TokenPair` separates `AccessTokenPlaintext` (in-memory only, NEVER persisted) from encrypted tokens
  - JSONB type with custom `Value()`/`Scan()` for PostgreSQL JSONB fields
  - Structured error types with `Retry` flag for transient vs permanent failures
- **Data Flow**: `RawEmail` -> `ParsedEmail` -> `Thread` + `Contact` -> `EmailIngestedEvent`
- **Key Models**:
  | Model | Purpose |
  |-------|---------|
  | `ParsedEmail` | Structured email after MIME parsing |
  | `Thread` | Conversation thread container with thread_key |
  | `Contact` | Canonicalized contact with name variants, tone history |
  | `EmailIngestedEvent` | NATS event sent to Classification Core |
  | `TokenPair` | Encrypted OAuth tokens with ephemeral plaintext |

---

## Component: NATS Event Bus

- **Purpose**: JetStream publisher for the Ingestion Mesh with retry + DLQ support. Defines all event types shared with downstream bounded contexts.
- **Architecture**: `JetStreamPublisher` connects to NATS, ensures all streams exist (idempotent), publishes events asynchronously.
- **Key Files**:
  - `/mnt/agents/output/ingestion/internal/nats/events.go`
- **Design Decisions**:
  - 6 JetStream streams defined: `EMAIL_INGESTED`, `EMAIL_INGESTED_DLQ`, `INTELLIGENCE_COMPRESS`, `EXTRACT_COMPLETED`, `AUTO_HANDLED`, `SYNC_NOTIFY_CARD_CREATED`
  - WorkQueuePolicy for work streams (EMAIL_INGESTED, INTELLIGENCE_COMPRESS) — messages removed after ack
  - LimitsPolicy for audit streams (DLQ, EXTRACT_COMPLETED, etc.) — time-based retention
  - Max 5 delivery attempts before DLQ; 8MB max message size
  - Connection resilience: retry on failed connect, max 10 reconnects, 2s reconnect wait
  - Async publish with max 256 pending
- **Data Flow**: Ingestion completes -> `PublishEmailIngested()` -> NATS subject `email.ingested` -> Classification Core consumes
- **TODOs/Issues**: None noted

---

## Component: Token Encryption (Crypto)

- **Purpose**: AES-256-GCM encryption for OAuth tokens with KMS-backed Data Encryption Key (DEK) management.
- **Architecture**: `TokenCrypto` handles encrypt/decrypt using AES-GCM. DEKs are generated via KMS, encrypted by KMS, cached in-memory with TTL, and wiped on expiration.
- **Key Files**:
  - `/mnt/agents/output/ingestion/internal/crypto/token.go`
- **Design Decisions**:
  - Envelope encryption: KMS encrypts DEKs; DEKs encrypt tokens (avoids KMS per-token calls)
  - In-memory DEK cache with 5-minute TTL + periodic cleanup goroutine (30s ticker)
  - `memzero()` explicitly wipes DEK bytes from memory (Go GC doesn't guarantee erasure)
  - Key references are base64-encoded JSON containing KMS key ID + encrypted DEK
  - DEK rotation supported: old DEKs remain decryptable, new tokens use new DEKs
  - 12-byte nonce size (standard for GCM)
- **Data Flow**: Plaintext token -> retrieve/generate DEK -> random nonce -> AES-256-GCM encrypt -> `EncryptedToken` (ciphertext + nonce + keyID)
- **TODOs/Issues**: None noted

---

## Component: Google OAuth

- **Purpose**: Google OAuth 2.0 implementation for Gmail integration. Supports full flow: init, callback, refresh, revoke, webhook validation, sent history fetch, and email send.
- **Architecture**: `googleProvider` implements the `OAuthProvider` interface. Uses `golang.org/x/oauth2` for token exchange and `google.golang.org/api/gmail/v1` for API calls.
- **Key Files**:
  - `/mnt/agents/output/ingestion/internal/oauth/google.go`
- **Design Decisions**:
  - Scopes: GmailReadonly, GmailSend, GmailModify, Calendar, userinfo.email
  - Forces `prompt=consent` + `AccessTypeOffline` to ensure refresh tokens are issued
  - Access token default TTL: 15 minutes (conservative, Google default is 1 hour)
  - `invalid_grant` detection mapped to `ErrCodeOAuthExpired` (non-retryable)
  - Webhook validation supports both Pub/Sub push format and direct payload
  - Send email constructs RFC 2822 messages with multipart support (text + HTML)
  - Message body extraction handles recursive `multipart/alternative/mixed/related` nesting
- **Data Flow**: Auth URL -> code exchange -> `TokenPair` (encrypted refresh + access) -> refresh via token source -> revoke via Google endpoint
- **TODOs/Issues**: Token placeholders in Exchange/Refresh use raw token bytes (caller must encrypt)

---

## Component: MIME Parser

- **Purpose**: Transforms raw MIME email into structured `ParsedEmail`. Orchestrates MIME parsing, HTML-to-text, signature stripping, attachment extraction, code extraction, and S3 upload.
- **Architecture**: `Parser` struct coordinates sub-parsers. Supports ONNX signature classifier with regex fallback.
- **Key Files**:
  - `/mnt/agents/output/ingestion/internal/parse/parser.go`
- **Design Decisions**:
  - 8-step pipeline: MIME parse -> threading headers -> HTML->text -> signature strip -> attachments -> code extraction -> S3 upload -> assemble
  - Raw email in S3 is the immutable source of truth; all parsed fields are derivative
  - Signature classifier: ONNX model preferred, regex fallback automatic
  - Code extraction scans BOTH cleaned text and original text (codes may be in signatures)
  - 2FA codes are NEVER logged (invariant)
  - Attachment extraction failure is non-fatal (continues parsing)
  - Synthetic Message-ID generated if missing for threading continuity
  - Source detection from headers (gmail/outlook/unknown)
- **Data Flow**: `rawMIME []byte` -> MIME parse -> strip signatures -> extract attachments/codes -> S3 upload -> `ParsedEmail`
- **TODOs/Issues**: `parse.Parser.Parse()` signature differs from `poll.MIMEParser` interface (adapter TODO in worker)

---

## Component: Thread Reconstruction Engine

- **Purpose**: Reconstructs email conversation threads using a 3-tier matching strategy with fuzzy fallback.
- **Architecture**: `Engine` uses PostgreSQL for thread storage with optional Neo4j for graph queries. 4-tier matching: In-Reply-To -> References -> Fuzzy subject + participants -> New thread.
- **Key Files**:
  - `/mnt/agents/output/ingestion/internal/thread/engine.go`
  - `/mnt/agents/output/ingestion/internal/thread/key.go`
  - `/mnt/agents/output/ingestion/internal/thread/fuzzy.go`
- **Design Decisions**:
  - **Tier 1**: `In-Reply-To` header -> `raw_emails.message_id` lookup (exact)
  - **Tier 2**: `References` headers -> `raw_emails.message_id` lookup (exact)
  - **Tier 3**: Fuzzy subject match (Levenshtein distance < 3) + participant overlap (>=1 common) + 7-day window, limited to 50 candidates
  - **Tier 4**: New thread with `ON CONFLICT` upsert on `(user_id, thread_key)`
  - Thread key = SHA-256 of sorted, deduplicated participant emails + normalized subject
  - Subject normalization: strip re:/fwd:/fw:/[external], lowercase, collapse whitespace
  - Levenshtein distance uses space-optimized DP (O(min(m,n)) space, O(m*n) time)
  - `NormalizeSubjectForKey` is more aggressive: strips all non-alphanumeric chars
  - Thread upsert handles concurrent creation via ON CONFLICT
- **Data Flow**: `ParsedEmail` -> tiered matching -> `ThreadMatchResult` (thread_id, match_method, is_new)
- **TODOs/Issues**: Neo4j driver included but fuzzyMatch uses PostgreSQL only

---

## Component: HTTP Server Entry Point

- **Purpose**: HTTP server providing webhook endpoints, OAuth flows, health checks, and graceful shutdown.
- **Architecture**: Chi router with middleware stack. Initializes all dependencies (DB, Redis, NATS, KMS) inline.
- **Key Files**:
  - `/mnt/agents/output/ingestion/cmd/server/main.go`
- **Design Decisions**:
  - Chi router with middleware: Recovery -> RequestID -> Logging (outer to inner)
  - Routes: `/health`, `/auth/*` (OAuth handler), `/webhooks/*` (webhook handler), `/api/v1/*` (stubs)
  - All API v1 routes return 501 Not Implemented (implemented by other tracks)
  - Health check verifies DB, Redis, and NATS connectivity
  - 30-second graceful shutdown timeout
  - Structured JSON logging via slog
- **Data Flow**: HTTP request -> middleware -> handler -> dependency -> response
- **TODOs/Issues**: `internal/server/router.go` (`NewRouter` + `Dependencies`) is dead code

---

## Component: Worker Entry Point

- **Purpose**: Background polling worker that polls email accounts, parses messages, and publishes events.
- **Architecture**: Initializes composite job processor that dispatches Gmail vs Outlook polling. Worker pool of 4 + scheduler queries DB for due accounts.
- **Key Files**:
  - `/mnt/agents/output/ingestion/cmd/worker/main.go`
- **Design Decisions**:
  - Composite pattern: `compositeJobProcessor` dispatches to `GmailPoller` or `OutlookPoller` based on `job.Provider`
  - Worker pool: 4 concurrent polling workers
  - Scheduler queries DB for accounts due for polling
  - Graceful shutdown: stop scheduler first (no new jobs), then stop worker pool (finish current)
  - Adapters bridge between `oauth.TokenStore` -> `poll.TokenStore` and `parse.Parser` -> `poll.MIMEParser`
- **Data Flow**: Scheduler -> fetch job -> composite processor -> provider poller -> fetch -> parse -> publish NATS event
- **TODOs/Issues**:
  - `stubGmailFetcher` and `stubOutlookFetcher` return errors (real API clients not wired)
  - `tokenStoreAdapter.GetTokens/RefreshIfNeeded` not implemented
  - `mimeParserAdapter.Parse` not fully implemented (signature mismatch)

---

## Component: Classification Core

- **Purpose**: Go microservice that consumes `email.ingested` events and routes emails into one of three downstream pipelines: Extract-Only, Auto-Handle, or Decision Stack.
- **Architecture**: Dual-binary service (server for HTTP API/rules, worker for NATS consumer). Uses PostgreSQL for rules, NATS JetStream for event flow.
- **Key Files**:
  - `/mnt/agents/output/classification/docs/ARCHITECTURE.md`
- **Design Decisions**:
  - Tri-state routing: Extract -> Auto -> Decision (strict order, never skip)
  - Hard confidence floor of 0.92 for Auto-Handle (enforced at config, DB, and runtime levels)
  - Every email MUST be classified — no unprocessed emails
  - Exactly-one downstream: result published to exactly one NATS subject
  - Staged rules (48h window) do NOT auto-fire — only active rules
  - Pull-based NATS consumer with explicit ack, max 5 deliveries, 30s ack wait
  - DLQ for failed messages after 5 delivery attempts
- **Data Flow**: `email.ingested` (NATS) -> tryExtract -> matchRules -> tryLLM -> RouteDecision -> publish result
- **TODOs/Issues**:
  - LLM Client: AWS SDK v2 Bedrock runtime not yet wired
  - Extract-Only: S3 body fetch + regex/heuristic extraction not fully implemented
  - Staging Cron: background auto-promote job exists but needs integration
  - Rule Analytics: usage_count aggregation not yet built

---

## Component: Classification Models

- **Purpose**: Shared domain types for Classification Core. Contracts with downstream contexts (Intelligence Layer, Sync).
- **Architecture**: Single package with input events, routing decisions, rule structures, predicate engine, LLM types, and staging types.
- **Key Files**:
  - `/mnt/agents/output/classification/internal/models/models.go`
- **Design Decisions**:
  - `EmailIngestedEvent` is a copy (not import) to avoid cross-module cycles — must match ingestion exactly
  - `RouteType` is a tri-state: `extract`, `auto`, `decision`
  - `RulePredicate` uses `AllOf` (AND) + `AnyOf` (OR) conditions
  - `Condition` supports: eq, ne, contains, regex, gt, lt, in, not_in
  - `EmailAttributes` exposes 9 fields for matching: sender, domain, subject, body (500 chars), recipient, attachment, participant count, time/day
  - `Evaluate()` method on `RulePredicate` performs the actual matching
  - Helper functions (`containsIgnoreCase`, `matchRegex`, `compareNumeric`) are placeholders (implementation in utils.go)
- **Data Flow**: `EmailIngestedEvent` -> `EmailAttributes` -> `RulePredicate.Evaluate()` -> `ClassificationResult`
- **TODOs/Issues**: Helper function implementations are placeholders

---

## Component: Classification Engine

- **Purpose**: Implements the Extract -> Auto -> Decision classification pipeline with LLM fallback.
- **Architecture**: `Engine` in `internal/classifier` runs the pipeline. Delegates to `auto.Engine` for rule matching and `extract.Extractor` for extract-only detection.
- **Key Files**:
  - `/mnt/agents/output/classification/internal/classifier/engine.go`
  - `/mnt/agents/output/classification/internal/auto/engine.go`
  - `/mnt/agents/output/classification/internal/extract/extractor.go`
- **Design Decisions**:
  - Conservative default: `RouteDecision` with confidence 1.0
  - `tryExtract()` is a fast-path placeholder (returns nil, falling through to rules)
  - Rule matching: loads active rules ordered by `usage_count DESC`, first match wins
  - Per-rule confidence threshold with hard floor enforcement (0.92)
  - LLM fallback: Claude 3 Haiku via `PatternMatch()` interface; if match >= 0.92 -> stage rule (not activate immediately)
  - LLM-generated rules get `extract_notify` action type (safest default)
  - Action execution failure is logged but doesn't change routing decision
  - Rule usage counter incremented on every match
- **Data Flow**: `EmailIngestedEvent` -> `Classify()` -> tryExtract -> matchRules -> tryLLM -> `ClassificationResult`
- **TODOs/Issues**:
  - `tryExtract()` is a stub (no actual extraction logic)
  - `tryLLM()` body preview is empty (S3 fetch not wired)
  - LLM client is optional (rule-only mode works)

---

## Component: Tri-State Router

- **Purpose**: Entry point for the classification pipeline. Enforces Extract -> Auto -> Decision ordering with validation and metrics.
- **Architecture**: `Router` delegates to `Extractor` and `AutoEngine` interfaces. `Pipeline` wraps the router as a NATS consumer with DLQ support.
- **Key Files**:
  - `/mnt/agents/output/classification/internal/router/router.go`
  - `/mnt/agents/output/classification/internal/router/pipeline.go`
  - `/mnt/agents/output/classification/internal/router/metrics.go`
- **Design Decisions**:
  - **Strict ordering**: Extract tried first (immediate return on match), then Auto (active rules only), then Decision (unconditional default)
  - **Stage isolation**: Auto-Handle only considers *active* rules; staged rules do NOT auto-fire
  - **Failure tolerance**: Extract failure -> continue to Auto; Auto failure -> fall through to Decision
  - **Validation**: `ValidateResult()` enforces invariants (RawEmailID present, terminal route, extract has datum, auto has rule ID)
  - **Metrics**: Prometheus counters by route type, histogram for latency, gauge for pending staged rules, counters for auto actions
  - **Pipeline**: Durable NATS consumer, explicit ack only after successful classify + publish, negative ack on failure, 5 max deliveries
  - **Graceful shutdown**: 30s timeout for in-flight messages
- **Data Flow**: NATS `email.ingested` -> parse -> `router.Route()` -> classify -> publish `email.classified` -> ack
- **TODOs/Issues**: None noted

---

## Component: 48h Staging System

- **Purpose**: Trust-building safety window for auto-handle rules. Rules created by users or LLM detection enter a 48-hour staging period before becoming active.
- **Architecture**: Four sub-components: `StagingCron` (periodic scanner), `Activator` (promotes staged->active), `Revoker` (user-initiated deactivation), `Notifier` (user notifications via NATS).
- **Key Files**:
  - `/mnt/agents/output/classification/internal/staging/cron.go`
  - `/mnt/agents/output/classification/internal/staging/activator.go`
  - `/mnt/agents/output/classification/internal/staging/revoker.go`
  - `/mnt/agents/output/classification/internal/staging/notifier.go`
  - `/mnt/agents/output/classification/internal/auto/predicate.go` (contains `stagingManager`)
- **Design Decisions**:
  - **48-hour window**: `staged_at < NOW() - INTERVAL '48 hours'` for auto-promotion
  - **Cron interval**: 15 minutes (configurable), runs immediately on start
  - **Atomic promotion**: `UPDATE ... WHERE status='staged'` — idempotent, race-safe
  - **ONE-WAY activation**: once active, stays active until explicitly revoked
  - **LLM-created rules**: enter staging immediately with `extract_notify` action (safest)
  - **User notifications**: staged, activated, and revoked events all publish `sync.notify.CardCreated`
  - **Revocation is terminal**: revoked rules cannot be re-activated; no retroactive undo
  - **In-memory staging manager**: tracks rules with 5-minute cleanup ticker
  - **DB uses `FOR UPDATE SKIP LOCKED`**: prevents cron job conflicts
- **Staging Lifecycle**:
  ```
  User creates rule / LLM detects pattern
         |
         v
    STAGED (48h review) --------auto after 48h------> ACTIVE
         |                                               |
         |----manual revoke---> REVOKED (terminal)       |
                              (no re-activation)         |
         |                                               |
         +----manual activate (bypass 48h)--------------->
  ```
- **Data Flow**: Rule created -> `status='staged'` -> cron scans -> `Activator.BulkActivate()` -> `status='active'` -> notify user
- **TODOs/Issues**:
  - Staging cron exists but integration with main worker loop needs wiring
  - Notifier uses placeholder URL pattern for action links
