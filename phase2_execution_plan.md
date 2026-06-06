# Phase 2: Execution Plan — Decision Stack Implementation

## Plan Structure
- 8 Development Phases (0-7), sequenced by data dependency
- Tasks within each phase ordered by dependency (no parallel tasks with data deps)
- Every task: Interface-locked, Test-gated, Rollback-defined
- Total estimated timeline: 20-24 weeks

---

## Phase 0: Foundation (Weeks 1–2)
**Theme**: Infrastructure, data stores, CI/CD, local dev environment

### P0-T1: Terraform AWS Infrastructure Base
| Attribute | Detail |
|---|---|
| **Agent** | Infrastructure Agent |
| **Task** | Terraform modules for VPC (public/private subnets, NAT Gateways), RDS PostgreSQL 16 Multi-AZ, ElastiCache Redis 7.x, S3 buckets with KMS encryption, IAM roles/policies |
| **Input** | `infra/terraform/` directory structure; env vars: AWS_REGION, VPC_CIDR |
| **Output** | `infra/terraform/modules/{vpc,rds,redis,s3,iam}/` with `.tf` files; `terraform plan` output validated |
| **Schema Output** | RDS: `users`, `email_accounts`, `threads`, `raw_emails` tables (initial CREATE TABLE only) |
| **Verification** | `terraform plan` shows 0 errors; `terraform validate` passes; cost estimate <$800/mo for dev |
| **Rollback** | `terraform destroy` on dev workspace; all resources tagged with `Project=decision-stack` |
| **Invariant Check** | S3 uses SSE-KMS (not SSE-S3); RDS has encryption at rest enabled; private subnets for compute |

### P0-T2: NATS JetStream + Qdrant + Neo4j Infrastructure
| Attribute | Detail |
|---|---|
| **Agent** | Infrastructure Agent |
| **Task** | EC2 instances for NATS JetStream (c6i.large), Qdrant 1.8+ (r6g.xlarge encrypted EBS), Neo4j 5.x (EC2 or AuraDS trial); security groups; ECR repositories |
| **Input** | VPC/ subnet IDs from P0-T1; AMI IDs for Amazon Linux 2023 |
| **Output** | `infra/terraform/modules/{nats,qdrant,neo4j,ecr}/`; docker-compose.yml for local dev; `Makefile` with `make dev` |
| **Verification** | `docker-compose up` brings up all 7 services (PG, Redis, Qdrant, Neo4j, NATS); health checks pass; NATS `nats stream info` shows JetStream ready |
| **Rollback** | `docker-compose down -v` for local; `terraform destroy -target=module.nats` etc. for AWS |
| **Invariant Check** | Qdrant on encrypted EBS; Neo4j native encryption at rest; NATS JetStream persistence enabled |

### P0-T3: CI/CD Pipeline + ECR + ECS Fargate
| Attribute | Detail |
|---|---|
| **Agent** | Infrastructure Agent |
| **Task** | GitHub Actions workflow (`.github/workflows/ci.yml`); ECR repos per bounded context; ECS Fargate cluster with task definitions; IAM roles for ECS execution |
| **Input** | ECR repo names: `ingestion`, `classification`, `intelligence`, `sync`; Dockerfile templates from each service |
| **Output** | `.github/workflows/ci.yml` (build → test → push to ECR → ECS deploy); `infra/terraform/modules/ecs/`; `infra/terraform/modules/ecr/` |
| **Verification** | Push to `main` triggers workflow; Docker image builds and pushes to ECR; ECS task starts and health check passes |
| **Rollback** | ECS rolling deployment auto-rollback on health check failure; previous task definition version retained |
| **Invariant Check** | Secrets (DB URLs, API keys) injected via AWS Secrets Manager, never in env vars or code |

### P0-T4: Database Schema Migration Setup
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Ingestion Mesh) |
| **Task** | Migration tooling: golang-migrate for Ingestion/Sync; alembic for Intelligence; initial schema migrations for all PostgreSQL tables |
| **Input** | Schema definitions from Document A Section 3.1 (users, email_accounts, threads, raw_emails, decision_cards, auto_handle_rules, drafts, calendar_events, billing_records, decision_logs) |
| **Output** | `ingestion/migrations/001_initial_schema.up.sql`; `sync/migrations/001_initial_schema.up.sql`; `intelligence/alembic/versions/001_initial_schema.py` |
| **Verification** | `make migrate-up` applies all migrations cleanly; `pg_dump --schema-only` matches spec; foreign key constraints verified |
| **Rollback** | `make migrate-down` reverts to baseline; migration files are immutable (never edit, only add new) |
| **Invariant Check** | UUIDv7 primary keys; pgcrypto enabled; field-level encryption for refresh_token_enc; CHECK constraints on enums |

### P0-T5: Neo4j Schema + Qdrant Collections Setup
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Neo4j constraints (Contact.canonical_email unique, Contact.id unique); Qdrant collections (email_chunks, voice_examples, consultation_index) with correct vector sizes and payload schemas |
| **Input** | Neo4j schema from Section 3.2; Qdrant schema from Section 3.3 |
| **Output** | `intelligence/core/neo4j_schema.cypher`; `intelligence/core/qdrant_setup.py` |
| **Verification** | Cypher `CREATE CONSTRAINT` statements execute without error; Qdrant collection creation returns 200; vector size=1024, distance=Cosine, payload indexes exist |
| **Rollback** | `DROP CONSTRAINT` scripts; Qdrant collection delete + recreate |
| **Invariant Check** | user_id indexed on all Qdrant payloads for multi-tenancy; Neo4j constraints prevent duplicate contacts |

---

## Phase 1: Ingestion Mesh (Weeks 3–5)
**Theme**: Email fetch, parse, thread, dedup, publish — never lose an email

### P1-T1: OAuth Flow Implementation (Gmail + Outlook)
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Ingestion Mesh) |
| **Task** | Google OAuth 2.0 (offline access, refresh tokens); Microsoft MSAL (authorization_code + client_credentials); token encryption with AES-256-GCM + per-user DEK from KMS; web-based flow (no mobile yet) |
| **Input** | `ingestion/internal/oauth/` package structure; Google + Microsoft OAuth client credentials; AWS KMS key ID |
| **Output** | `ingestion/internal/oauth/google.go`; `ingestion/internal/oauth/microsoft.go`; `ingestion/internal/oauth/handler.go`; `ingestion/internal/oauth/provider.go`; `ingestion/internal/crypto/token.go`; `ingestion/internal/crypto/kms.go` |
| **Interface Output** | OAuth callback returns `{account_id, email_address}` to caller; refresh tokens stored encrypted in `email_accounts.refresh_token_enc` |
| **Verification** | End-to-end OAuth flow with test Gmail account: token received, encrypted, stored, decrypted, used to fetch email; token rotation works; `invalid_grant` handled with `is_active=false` |
| **Rollback** | Revoke OAuth token via Google/Outlook API; delete `email_accounts` row; delete cached tokens |
| **Invariant Check** | Refresh tokens NEVER stored plaintext; access tokens 15min in-memory ONLY; automatic rotation on use |

### P1-T2: Webhook Listeners (Gmail Pub/Sub + Outlook Graph)
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Ingestion Mesh) |
| **Task** | `POST /webhooks/gmail` (JWT-verified Pub/Sub push); `POST /webhooks/outlook` (validation token response + notification processing); enqueue fetch job (do NOT fetch full message from webhook) |
| **Input** | Webhook endpoint specs from Section 4.4; JWT verification library; NATS publisher from P0-T2 |
| **Output** | `ingestion/cmd/server/main.go` HTTP server; webhook handlers in `ingestion/internal/webhook/handler.go` |
| **Interface Output** | Enqueues fetch job with `{user_id, history_id, account_id}` to internal Redis queue; publishes `email.ingested` after full fetch+parse |
| **Verification** | Send test Pub/Sub message → webhook receives → history_id extracted → fetch job enqueued; Outlook validation token responded within 10s |
| **Rollback** | Disable Gmail watch / Outlook subscription; return 503 to stop receiving pushes |
| **Invariant Check** | Extract historyId only, not full message; JWT verification mandatory; 10s validation token response |

### P1-T3: Polling Workers (Gmail + Outlook)
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Ingestion Mesh) |
| **Task** | Gmail polling: `users.history.list` with stored historyId, process messageAdded/messageDeleted; Outlook polling: Delta Query with delta_link; adaptive backoff (5min→15min→1hr→6hr); deactivate when webhooks resume |
| **Input** | Polling worker package structure; Gmail API client (250 units/sec rate limit); Redis rate limit counter schema |
| **Output** | `ingestion/cmd/worker/main.go`; `ingestion/internal/poll/gmail.go`; `ingestion/internal/poll/outlook.go`; `ingestion/internal/fetch/enqueuer.go`; `ingestion/internal/fetch/job.go`; Redis rate limit logic |
| **Interface Output** | Per-user history_id/delta_link updated via `poll.StateStore` after each successful batch; fetch jobs enqueued via `fetch.Enqueuer` for new messages |
| **Verification** | Run worker against test account: fetch 1000 emails end-to-end without data loss; zero 429 errors under normal load; history_id continuity verified |
| **Rollback** | Stop worker; reset history_id to last known good; re-fetch from checkpoint |
| **Invariant Check** | Rate limit compliance mandatory; polling activates ONLY when webhooks fail; history_id updated atomically with batch commit |

### P1-T4: Parsing Pipeline (HTML→Text, Signature Strip, Attachment OCR)
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Ingestion Mesh) + ML/Intelligence Agent |
| **Task** | HTML→text (jaytaylor/html2text); Signature strip (ONNX DistilBERT classifier, P>0.85); Attachment extraction to S3 SSE-KMS; OCR microservice (Python/FastAPI with pytesseract, pdfplumber) |
| **Input** | Raw MIME payloads from P1-T3; S3 bucket from P0-T1; ONNX model for signature detection |
| **Output** | `ingestion/internal/parse/html.go`; `ingestion/internal/parse/signature.go`; `ingestion/internal/parse/mime.go`; `ingestion/internal/parse/attachment.go`; `ingestion/internal/parse/codes.go`; `services/ocr/` (Python/FastAPI microservice) |
| **Interface Output** | Parsed email: `{body_text, body_html, has_attachments, attachment_s3_uris[], extracted_codes[], is_signature_flags[]}`; OCR: `{"text": "...", "confidence": 0.92}` |
| **Verification** | Parse 1000 test emails: HTML stripped correctly, signatures detected at >85% accuracy; attachments uploaded to S3 with correct paths; OCR confidence >0.7 for 95% of images |
| **Rollback** | Re-parse from raw_emails.body_html (original preserved); re-run OCR from S3 source |
| **Invariant Check** | Raw email body preserved in raw_emails; parsed text is derivative, not replacement; OCR flagged for review if confidence <0.7 |

### P1-T5: Threading Reconstruction + Cross-Source Contact Deduplication
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Ingestion Mesh) |
| **Task** | Thread graph: In-Reply-To + References headers, fuzzy subject fallback (Levenshtein<3, sender overlap, 7d window); deterministic thread_key (SHA-256); Contact dedup: normalize email, Neo4j query, SIMILAR_TO edge for fuzzy matches |
| **Input** | Parsed emails from P1-T4; Neo4j client from P0-T5; `threads` table schema |
| **Output** | `ingestion/internal/thread/engine.go`; `ingestion/internal/contact/dedup.go`; `ingestion/internal/contact/neo4j.go`; UPSERT logic for threads + Neo4j Contact nodes |
| **Interface Output** | `threads` row with `{id, user_id, thread_key, participant_emails[], message_count, last_message_at}`; Neo4j `(:Contact)` node or `(:Contact)-[:SIMILAR_TO]->(:Contact)` edge |
| **Verification** | Manual sampling: >95% of emails correctly threaded; contact dedup catches `user+tag@gmail.com` → `user@gmail.com`; SIMILAR_TO flagged for user review |
| **Rollback** | Recompute thread_key for affected messages; delete SIMILAR_TO edges and re-derive |
| **Invariant Check** | thread_key is deterministic (same input → same hash); contact normalization strips +aliases; fuzzy matches NEVER auto-merged without user review |

### P1-T6: Event Publishing to NATS
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Ingestion Mesh) |
| **Task** | Publish `email.ingested` event to NATS JetStream after parsing+threading+dedup complete; configure subject `email.ingested` with WorkQueue retention, max-deliver=5, DLQ=`email.ingested.dlq` |
| **Input** | NATS connection from P0-T2; event envelope schema from Section 4.7 |
| **Output** | `ingestion/internal/nats/publisher.go`; NATS stream configuration |
| **Interface Output** | `email.ingested` event: `{event_id, user_id, source, account_id, thread_id, raw_email_id, s3_uri, has_attachments, sender_email, received_at, classification_hint="pending"}` |
| **Verification** | Publish 1000 events, consume from DLQ: zero messages (all acked); pull consumer processes with exactly-once semantics; dead-letter after 5 failures |
| **Rollback** | Replay from NATS retention; re-publish events from raw_emails table |
| **Invariant Check** | Event published ONLY after full persistence (raw_emails INSERT committed); NATS retention=WorkQueue (delete after ack); DLQ monitored with alerting |

---

## Phase 2: Classification Core (Weeks 6–7)
**Theme**: Tri-state routing (Extract / Auto / Decision) with conservative bias

### P2-T1: Extract-Only Pipeline (Regex + Lightweight Classifier)
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Classification Core) |
| **Task** | Regex bank: 2FA/OTP, tracking numbers, calendar MIME, receipts/orders; ONNX DistilBERT classifier (is_receipt, is_newsletter, is_notification); threshold 0.95; extract datum and publish `ExtractCompleted` |
| **Input** | `email.ingested` events from NATS; regex patterns from Section 5.3; ONNX model |
| **Output** | `classification/internal/extract/regex_bank.go`; `classification/internal/extract/classifier.go`; `classification/internal/extract/extractor.go`; `ExtractCompleted` event publisher |
| **Interface Output** | `ExtractCompleted` event: `{user_id, raw_email_id, extract_type, extracted_datum, notification_text}`; raw_emails marked for 24h deletion |
| **Verification** | Test on 500 labeled emails: >98% accuracy, <2s processing time; extracted datums correct (2FA codes, tracking numbers); zero false negatives on critical extractions |
| **Rollback** | Re-classify extracted email → route to Decision Stack; restore from raw_emails backup |
| **Invariant Check** | Threshold 0.95 is HARD floor — anything below routes to next stage; 24h deletion timer set correctly; extracted datums sent via push notification |

### P2-T2: Auto-Handle Predicate Engine + LLM Pattern Matching
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Classification Core) + ML/Intelligence Agent |
| **Task** | JSON predicate evaluator (Go, recursive, regex caching); fields: sender_email/domain, subject, body, recipient, has_attachment, thread_participant_count, time_of_day, day_of_week; ops: eq, ne, contains, regex, gt, lt, in, not_in; LLM fallback (Claude 3 Haiku) for unstructured pattern detection |
| **Input** | `auto_handle_rules` table schema; predicate JSON schema from Section 5.4; `email.ingested` events |
| **Output** | `classification/internal/rules/engine.go`; `classification/internal/rules/predicates.go`; LLM pattern matching prompt template; `classification/internal/classifier/llm_fallback.go` |
| **Interface Output** | Routing decision: `{raw_email_id, route: "auto\|decision", matched_rule_id?, confidence, llm_match?}`; NATS publish to `intelligence.compress` or execute auto-action |
| **Verification** | 1000 classification test: zero false positives to Auto-Handle (conservative bias acceptable); predicate evaluation <100ms per rule; LLM fallback <5s |
| **Rollback** | Revoke rule → all future matching emails route to Decision Stack; log correction to decision_logs |
| **Invariant Check** | Confidence floor 0.92 is HARD; default route is ALWAYS Decision Stack; false negative acceptable, false positive to Auto-Handle is NOT |

### P2-T3: Decision Stack Default Routing + 48-Hour Rule Staging
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Classification Core) |
| **Task** | Default route all non-Extract, non-Auto emails to `intelligence.compress` NATS subject; implement 48-hour rule staging: `auto_handle_rules.status='staged'` → cron job promotes to `'active'` after 48h; user can revoke during staging |
| **Input** | Routing logic from P2-T2; cron scheduler; `auto_handle_rules` table with status field |
| **Output** | `classification/internal/router/tristate.go`; `classification/internal/rules/staging.go`; cron job for rule promotion; `intelligence.compress` event publisher |
| **Interface Output** | `intelligence.compress` event: `{event_id, user_id, thread_id, raw_email_ids[], priority_score, source}`; staging notification to user when rule created |
| **Verification** | Test: staged rule does NOT activate for 48h; cron promotes exactly at 48h; user revocation prevents activation; all non-matching emails route to intelligence.compress |
| **Rollback** | `UPDATE auto_handle_rules SET status='revoked'`; re-route affected emails back to Decision Stack |
| **Invariant Check** | 48-hour staging is MANDATORY — no immediate activation; user notified of staged rule; revocation is one-tap |

---

## Phase 3: Intelligence Layer MVP (Weeks 8–12)
**Theme**: Compression, citation, consultation, drafting — the cognitive engine

### P3-T1: LLM Client Abstraction + Token Metering
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Unified LLMClient class (Anthropic + OpenAI fallback); async generate with token tracking; Redis daily rate limit check; fallback chain (Anthropic 5xx → retry → OpenAI → queue in pending_llm); cost metering to Redis + PostgreSQL |
| **Input** | API keys for Anthropic, OpenAI; Redis connection; `intelligence/core/llm_client.py` structure |
| **Output** | `intelligence/core/llm_client.py`; `intelligence/core/metering.py`; fallback logic; token usage schema in PostgreSQL |
| **Interface Output** | `GenerationResult: {text, model_used, tokens_input, tokens_output, latency_ms, cost_estimate, warning_flags?}` |
| **Verification** | Call each provider successfully; simulate Anthropic outage → fallback to OpenAI within 10s; simulate both down → queue in pending_llm with user notification; token metering accurate to ±5% |
| **Rollback** | Switch model per-call; drain pending_llm queue when service restored |
| **Invariant Check** | NEVER generate cards with degraded models without user consent; cost >2x average → switch to cheaper model + warning flag |

### P3-T2: Semantic Chunking + Embedding Pipeline
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Paragraph-level splitting; 800 token max (cl100k), 100 token overlap, 50 token min merge; signature flagging (reuse ONNX classifier); embedding via text-embedding-3-large (1024-dim); Qdrant upsert with full payload |
| **Input** | `intelligence.compress` events; body_text from raw_emails; Qdrant client from P0-T5 |
| **Output** | `intelligence/app/compression/chunker.py`; `intelligence/core/embedding.py`; Qdrant upsert logic |
| **Interface Output** | Qdrant `email_chunks` points: `{chunk_id, thread_id, email_id, sender_email, timestamp, paragraph_index, is_signature, content_snippet, vector[1024]}` |
| **Verification** | Chunk 100 threads: all paragraphs covered, overlap correct, no chunk <50 tokens; embeddings are 1024-dim cosine; Qdrant retrieval by thread_id returns correct chunks |
| **Rollback** | Delete chunks by thread_id from Qdrant; re-chunk and re-embed |
| **Invariant Check** | chunk_id is UUID; user_id indexed on every point; is_signature flagged for filtering; content_snippet is first 200 chars for debugging |

### P3-T3: Compression Service (Card Generation)
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Card generation prompt (Jinja2 template); context injection (Neo4j relationship graph, calendar_events); JSON schema constraint; Claude 3.5 Sonnet primary, Haiku fallback |
| **Input** | Prompt template from Section 13.1; chunks + context; `intelligence/app/compression/` structure |
| **Output** | `intelligence/app/compression/service.py`; `intelligence/core/prompt_templates/compression.jinja2`; context injection queries |
| **Interface Output** | Decision Card JSON: `{from: {name, relationship_context, last_contact, interaction_count}, they_want, context: {history_summary, prior_commitments, quoted_numbers[], deadlines[], sentiment}, need_from_user, citations: [{chunk_id, verbatim_snippet, email_id, paragraph_index}]}` |
| **Verification** | Generate 100 cards: <10s for threads <20 emails; every card has ≥1 citation; they_want ≤280 chars; need_from_user is explicit irreducible gap |
| **Rollback** | Regenerate card with different temperature; route to manual review on 3 failures |
| **Invariant Check** | Every claim MUST cite chunk_id; do NOT infer tacit knowledge; they_want is single sentence max 280 chars |

### P3-T4: Citation Verification
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Post-generation verification: every chunk_id in citations exists in Qdrant payload for this thread; verbatim snippet fuzzy-matches actual chunk text; reject + retry on hallucination (max 3); manual review queue on persistent failure |
| **Input** | Card JSON from P3-T3; Qdrant chunks by thread_id |
| **Output** | `intelligence/app/compression/verifier.py`; retry logic; manual review queue integration |
| **Interface Output** | Verified card (citation_check: passed) OR rejected card (citation_check: failed, retry_count) OR manual review flag |
| **Verification** | 100% of generated cards pass citation check (zero hallucination tolerance); simulated hallucination correctly caught and retried; after 3 failures → manual review queue |
| **Rollback** | Reject card, regenerate from scratch; alert human operator for manual review |
| **Invariant Check** | Hallucinated citations are SYSTEM FAILURE — zero tolerance; manual review queue monitored with SLA |

### P3-T5: Context Injection (Relationship Graph + Calendar)
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Neo4j Contact query: interaction_count, avg_response_hours, tone_history, last_project, last_monetary_value; Calendar query: calendar_events for next 7 days, conflict detection, buffer rules; inject into compression prompt |
| **Input** | Neo4j client from P0-T5; calendar_events table; `intelligence/core/graph_client.py`; `intelligence/core/calendar_context.py` |
| **Output** | `intelligence/app/compression/context_builder.py`; Neo4j traversal queries; calendar free/busy integration |
| **Interface Output** | Context block: `{relationship: {...}, calendar: {events_next_7d[], conflicts[], travel_time?}}` |
| **Verification** | Contact context retrieved in <100ms; calendar context accurate (test with known events); travel time calculation works for known locations |
| **Rollback** | Generate card without context (degraded but functional); cache context for 5min to reduce API calls |
| **Invariant Check** | Calendar context ONLY injected for scheduling-intent emails; relationship context from graph is discovered, not declared |

### P3-T6: Consultation Service (Q&A against Thread)
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Embed user question; Qdrant similarity search (top 10 by cosine, filtered by user_id + thread_id); cross-encoder re-rank (ms-marco-MiniLM-L-6-v2, ONNX local); LLM answer with chunk constraints; turn limiting (Redis, max 10) |
| **Input** | `intelligence/app/consultation/` structure; Qdrant client; cross-encoder model |
| **Output** | `intelligence/app/consultation/service.py`; `intelligence/core/cross_encoder.py`; consultation prompt template |
| **Interface Output** | `ConsultResponse: {answer, citations: [{chunk_id, verbatim}], turns_remaining}` |
| **Verification** | Answer uses ONLY provided chunks; "I don't see that in the thread" when answer not found; turn counter decrements correctly; 10-turn limit enforced |
| **Rollback** | Clear consultation turns in Redis; restart consultation session |
| **Invariant Check** | Answer constrained to chunks — no external knowledge; 10-turn limit is HARD; counter resets on state change to drafting/approved |

### P3-T7: Drafting Service (Intent Parsing + Few-Shot Retrieval + Generation)
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Intent parsing (Claude 3 Haiku → structured JSON: price, timeline, condition, deadline, tone_modifier); few-shot retrieval from Qdrant voice_examples (top 3 by similarity + recency boost); draft generation (Claude 3.5 Sonnet, temp=0.4); threading engine (In-Reply-To, References, subject pivot detection) |
| **Input** | `intelligence/app/drafting/` structure; voice_examples collection; prompt template from Section 13.2 |
| **Output** | `intelligence/app/drafting/service.py`; `intelligence/app/drafting/intent_parser.py`; `intelligence/app/drafting/threading.py`; drafting prompt template |
| **Interface Output** | `Draft: {draft_id, draft_body, subject_line, in_reply_to, references[], tone_profile, model_used, tokens_used}` |
| **Verification** | Draft matches user voice (blind test: user identifies as their own); threading headers correct; subject pivot detected accurately; >70% approval rate without edit in user testing |
| **Rollback** | Regenerate draft with different voice examples; fall back to generic prompt |
| **Invariant Check** | Every draft cites voice_examples used; threading headers are EXACT (Message-ID match); user edit always allowed before approve |

### P3-T8: Hierarchical Summarization (>50 email threads)
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Map-reduce: batch 10 emails → Claude 3 Haiku summarizes (3 bullets); combine all summaries → Claude 3.5 Sonnet synthesizes narrative; cache in Qdrant consultation_index with thread_summary=true |
| **Input** | Threads with >50 raw_emails; map-reduce prompt templates |
| **Output** | `intelligence/app/compression/hierarchical.py`; consultation_index upsert logic |
| **Interface Output** | Thread summary stored in Qdrant with `thread_summary: true`; narrative format for card generation |
| **Verification** | Summary captures all key decisions/asks/facts from 50+ email thread; <30s total processing time; cached result retrieved on subsequent card generation |
| **Rollback** | Re-run map-reduce; fall back to first/last 10 emails if timing constraint violated |
| **Invariant Check** | No individual email lost (all chunks embedded and retrievable); narrative is abstraction, chunks are ground truth |

---

## Phase 4: Client MVP (Weeks 10–14, parallel with Phase 3)
**Theme**: Offline-first, one-card-at-a-time, never show raw email

### P4-T1: React Native Scaffold + SQLite Local Cache
| Attribute | Detail |
|---|---|
| **Agent** | Client Agent |
| **Task** | React Native 0.73+ with Expo SDK 50; op-sqlite with SQLCipher; Zustand state management; React Query for server sync; local schema mirroring server decision_cards + drafts |
| **Input** | Client SQLite schema from Section 3.5; React Native project scaffold |
| **Output** | `client/App.js`; `client/src/stores/`; `client/src/services/db.js`; SQLite CREATE TABLE for local_cards, local_drafts, sync_queue |
| **Interface Output** | Local SQLite with: `local_cards` (mirror of decision_cards + local_decision, server_version), `local_drafts` (mirror of drafts + is_approved), `sync_queue` (operation, payload, created_at) |
| **Verification** | App builds and runs in Expo Go; SQLite encrypted (verify no plaintext in .db file); CRUD on local_cards works; offline mode functions |
| **Rollback** | Delete SQLite file → re-sync from server; app reinstall |
| **Invariant Check** | SQLCipher encryption mandatory; raw email bodies NEVER in SQLite; server_version for CRDT merge |

### P4-T2: BatchGate + CardStack UI
| Attribute | Detail |
|---|---|
| **Agent** | Client Agent |
| **Task** | BatchGate screen: "N decisions · M min · Start?"; CardStack: one card at a time, swipe up to clear/decide, tap Source for citation viewer; no list view, no inbox, no unread counter |
| **Input** | UI designs from Section 9.3; decision_cards schema; `client/src/screens/BatchGate.js`; `client/src/screens/CardStack.js` |
| **Output** | BatchGate + CardStack implementations; card component (from, they_want, context, need_from_user); source citation viewer (verbatim highlight with chunk_id) |
| **Interface Output** | Card UI renders: `{from_field, they_want, context, need_from_user, chunk_citations[]}`; Source tap → modal with verbatim snippet |
| **Verification** | User test: card processed in <60s; one-card-at-a-time feels faster than inbox triage; source viewer shows correct verbatim text |
| **Rollback** | Skip card → remains pending; back to batch gate |
| **Invariant Check** | ONE card at a time, never a list; NO inbox view, NO unread counter, NO folder list; Source viewer shows verbatim + chunk_id |

### P4-T3: Offline Queue + Sync Protocol
| Attribute | Detail |
|---|---|
| **Agent** | Client Agent |
| **Task** | Background sync (expo-background-fetch, 15min interval); POST /sync with device_id, last_sync_version, local_changes[]; CRDT merge response (server_version, accepted_changes, rejected_changes, new_cards, updated_cards); sync_queue table for pending operations |
| **Input** | Sync API spec from Section 12.2; CRDT merge rules from Section 7.4 |
| **Output** | `client/src/services/sync.js`; `client/src/stores/syncStore.js`; background task registration |
| **Interface Output** | Sync request: `{device_id, last_sync_version, local_changes: [{card_id, version, state, decision}]}`; Sync response: `{server_version, accepted_changes[], rejected_changes[], new_cards[], updated_cards[]}` |
| **Verification** | User clears 10 cards offline → sync on reconnect → zero conflicts; server_version incremented correctly; rejected changes handled with server state override |
| **Rollback** | Local wins for pending items (user decision overrides); server wins for sent items; log conflict for analytics |
| **Invariant Check** | User can clear entire batch without network; sync happens on reconnect; server_version for conflict detection; idempotent sync operations |

### P4-T4: Text-Based Decision Clearing
| Attribute | Detail |
|---|---|
| **Agent** | Client Agent |
| **Task** | Decision input: text field on card for one-line instruction; submit → POST /cards/{id}/decide → receive draft → DraftReview screen; Edit/Approve/Shorten/Formalize actions |
| **Input** | `/cards/{id}/decide` API spec; `/cards/{id}/draft` API spec |
| **Output** | `client/src/screens/DraftReview.js`; decision input component; draft display with action buttons |
| **Interface Output** | POST `/cards/{id}/decide` with `{decision: "approve|edit|consult", input?: string}`; response: `{draft_id, draft_body}` |
| **Verification** | End-to-end: type "9500, two weeks" → draft generated → review → approve → sent; <10s draft generation; editing works and preserves threading |
| **Rollback** | Reject draft → return to card; edit draft → new version; back → discard draft |
| **Invariant Check** | Human-in-the-loop: user_approved MUST be TRUE before send; NO autonomous sending; every send logged to decision_logs |

### P4-T5: Security Panel
| Attribute | Detail |
|---|---|
| **Agent** | Client Agent |
| **Task** | Settings screen: last token rotation date, active sessions (device, location, last active), data residency region, one-tap "Purge and Disconnect" (POST /security/purge → wipes all, revokes OAuth, deletes SQLite within 60s) |
| **Input** | Security specs from Section 10.6; `/security/purge` API |
| **Output** | `client/src/screens/SecurityPanel.js`; `client/src/screens/Settings.js`; purge confirmation flow |
| **Interface Output** | Security status: `{last_token_rotation, sessions: [{device, location, last_active}], residency_region}`; Purge: synchronous 60s completion |
| **Verification** | Purge completes within 60s; all local data wiped; OAuth revoked; re-auth required; session list accurate |
| **Rollback** | Purge is IRREVERSIBLE — confirmation dialog with typed confirmation required |
| **Invariant Check** | Purge is synchronous and complete; no residual data; OAuth revocation confirmed before success |

---

## Phase 5: Voice + Calendar (Weeks 13–16)
**Theme**: Voice clearing, calendar integration, reminder stack

### P5-T1: Deepgram Streaming STT Integration
| Attribute | Detail |
|---|---|
| **Agent** | Client Agent |
| **Task** | Audio recording (expo-av, 16kHz mono WAV); Deepgram Nova-2 WebSocket streaming (wss://api.deepgram.com/v1/listen); interim results as subtitles; final result committed to local_decision; audio fallback (save to voice_memo_path if offline, transcribe later) |
| **Input** | Deepgram API key; react-native-webrtc or Deepgram JS SDK; audio recording config |
| **Output** | `client/src/services/voice/recorder.js`; `client/src/services/voice/deepgram.js`; audio buffer management |
| **Interface Output** | Transcription: `{text, is_final, confidence}`; fallback audio path if offline |
| **Verification** | Latency <300ms from end-of-speech to final transcription; interim results display smoothly; offline fallback saves audio correctly |
| **Rollback** | Fall back to text input; offline audio uploaded on reconnect for Whisper transcription |
| **Invariant Check** | Voice memo path stored locally; uploaded + transcribed on reconnect; Deepgram streaming for real-time |

### P5-T2: ElevenLabs TTS + Voice Mode UX
| Attribute | Detail |
|---|---|
| **Agent** | Client Agent |
| **Task** | TTS caching: common phrases in SQLite tts_cache (hash → audio blob); warm-cache top 20 on launch; ElevenLabs Turbo v2.5 streaming (<300ms target); OS TTS fallback (>500ms); Voice mode structured flow: "Next: [from]. [they_want]. What do you want?" → listen → draft → "Send it?" → yes → 30s undo window |
| **Input** | ElevenLabs API key; voice mode flow spec from Section 9.5; TTS cache schema |
| **Output** | `client/src/services/voice/tts.js`; `client/src/screens/VoiceMode.js`; voice mode state machine; undo timer logic |
| **Interface Output** | TTS audio stream; voice mode states: `intro → listening → transcribing → drafting → confirming → sending → next_card`; undo window: 30s with "Wait, no" detection |
| **Verification** | TTS latency <300ms for cached phrases, <500ms for ElevenLabs stream; voice mode completes full card cycle in <45s; undo window halts send on "Wait, no" |
| **Rollback** | OS TTS fallback; text mode fallback; undo halts send if within window |
| **Invariant Check** | 30-second undo window MANDATORY for voice approvals; "Wait, no" halts send; undo window is psychological safety net |

### P5-T3: Calendar Read/Write Integration
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Sync & State) + ML/Intelligence Agent |
| **Task** | Google Calendar API (events.insert, patch, freebusy.query, delete); Outlook Calendar API (POST/PATCH/DELETE /me/events, getSchedule); share OAuth tokens with email; calendar-aware compression (NER for temporal expressions, conflict detection, travel time, buffer rules) |
| **Input** | Calendar API specs from Section 8.5; existing OAuth tokens from P1-T1; `calendar_events` table |
| **Output** | `sync/internal/calendar/google.go`; `sync/internal/calendar/outlook.go`; `intelligence/app/calendar_context/ner.py`; `intelligence/app/calendar_context/conflict.py` |
| **Interface Output** | Calendar context injection: `{events_next_7d[], proposed_time, conflicts[], travel_time?, buffer_violation?}`; Event creation: `{external_event_id, title, start_at, end_at, timezone, attendee_emails[]}` |
| **Verification** | Event created in user's calendar on approve; free/busy conflict detected and shown in card; travel time accurate for known locations; buffer rules respected |
| **Rollback** | Delete calendar event via API; notify user of rollback |
| **Invariant Check** | Calendar is downstream action surface, NOT separate app; user never opens calendar grid; scheduling is decision output |

### P5-T4: Reminder Stack (Pre-Event Briefings + Daily Digest)
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Sync & State) |
| **Task** | Calendar worker scans every 15min; pre-event briefing (15min before): "Your 2pm with [Name] starts in 15 min. Context: [Topic]. Last contact: [Date], [Value]."; daily digest (user-configured time, default 8am); conflict alerts for overlapping meeting requests |
| **Input** | `sync/cmd/calendar_worker/main.go`; `calendar_events` table; briefing card template |
| **Output** | `sync/cmd/calendar_worker/main.go`; `sync/internal/notify/briefing.go`; briefing card generator |
| **Interface Output** | Briefing card: `{type: "briefing", title, contact_name, context_summary, last_contact_date, last_monetary_value, location, travel_time}`; daily digest: `{meetings_today[], decisions_queued_count, estimated_clear_time}` |
| **Verification** | Briefing arrives exactly 15min before event; daily digest at configured time; conflict alert shows both events with options; all push via Interrupt channel (high priority) |
| **Rollback** | Skip briefing; manual calendar check (degraded) |
| **Invariant Check** | Briefings are Interrupts (high priority), not background noise; daily digest replaces calendar checking; sparse and contextual |

---

## Phase 6: Sending Session + Polish (Weeks 17–20)
**Theme**: Real-time co-authoring, pattern delegation, security audit

### P6-T1: WebSocket Co-Authoring Server
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Sync & State) |
| **Task** | WebSocket server `wss://sync.decisionstack.com/sessions/{user_id}`; JWT auth in query param; heartbeat ping every 30s; protocol: spawn → paragraph → accept/edit/delegate; pattern delegation detection (NL → structured predicate → auto_handle_rules staged) |
| **Input** | WebSocket spec from Section 9.6; `sync/cmd/server/main.go` gRPC+HTTP upgrade |
| **Output** | `sync/internal/sessions/websocket.go`; session manager; spawn response handler; pattern delegation parser |
| **Interface Output** | WebSocket messages: `{"type": "spawn", "trigger_word": "...", "card_id": "..."}` → `{"type": "paragraph", "text": "...", "cursor_position": N}` → `{"type": "accept|edit|delegate"}` |
| **Verification** | Spawn response arrives <2s after trigger word; paragraph streams incrementally; edit updates draft in real-time; delegate creates staged rule |
| **Rollback** | Close WebSocket → draft saved to PostgreSQL; resume from saved state on reconnect |
| **Invariant Check** | WebSocket auth via JWT; heartbeat detects stale connections; delegate ALWAYS creates STAGED rule (48h window) |

### P6-T2: Spawn Response Generation (Predictive Co-Authorship)
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Trigger word detection (type "Look" → LLM generates contextual paragraph); full thread history + relationship model + voice corpus; streaming response via WebSocket; topic pivot detection for subject line changes |
| **Input** | `intelligence/app/drafting/spawn.py`; WebSocket integration; card + thread context |
| **Output** | `intelligence/app/drafting/spawn.py`; streaming LLM response handler; topic pivot detector |
| **Interface Output** | Spawned paragraph: `{text, cursor_position, completion_confidence}`; topic pivot: `{pivoted: true/false, new_subject?}` |
| **Verification** | Spawn response relevant to thread context and user intent; user steers with single words; paragraph quality matches voice corpus; <2s from trigger to first paragraph |
| **Rollback** | Reject spawn → type own text; regenerate with different context |
| **Invariant Check** | Spawn is contextual expansion (paragraph), NOT autocomplete (next 3 words); user authorship preserved |

### P6-T3: Interrupt System (Urgency + Push Dispatch)
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Sync & State) |
| **Task** | Interrupt triggers: urgency_score>0.85 + deadline<24h, legal keywords, whitelist sender, internal contradiction; FCM (Android) + APNS (iOS) dispatch; apns-priority 10 for Interrupts, 5 for batch; quiet hours respected; queue hygiene (48h pending + deadline<24h → escalate, 7d → expire) |
| **Input** | `sync/internal/notify/` package; FCM/APNS credentials; interrupt rules from Section 7.3 |
| **Output** | `sync/cmd/notifier/main.go`; `sync/internal/notify/interrupt.go`; `sync/internal/notify/batch.go`; queue hygiene worker |
| **Interface Output** | Interrupt push: `"Urgent: [Sender] — [Atomic ask]. Tap to enter sending session or approve."`; Batch push: `"N decisions · M min · Ready?"` |
| **Verification** | Interrupt arrives within 5s of trigger; quiet hours defer correctly; 7-day expiry marks card expired; queue hygiene runs every 15min |
| **Rollback** | Disable interrupts per-user; return to batch-only notifications |
| **Invariant Check** | Interrupts are sparse and high-signal; batch notifications accumulate (not per-card); quiet hours respected; 7-day expiry is honest queue management |

### P6-T4: Security Audit + SOC 2 Readiness
| Attribute | Detail |
|---|---|
| **Agent** | Infrastructure Agent + Backend Agent (all contexts) |
| **Task** | Penetration test (OWASP ZAP or Burp Suite); dependency vulnerability scan (Snyk or Dependabot); encryption audit (at rest + in transit); access control review; SOC 2 readiness assessment; documentation |
| **Input** | Security specs from Section 10; running system from all prior phases |
| **Output** | Security audit report; vulnerability remediation plan; SOC 2 readiness checklist; updated security documentation |
| **Verification** | Zero critical vulnerabilities; all dependencies up-to-date; encryption verified (AES-256-GCM, TLS 1.3, mTLS); no plaintext secrets in code; access logs complete |
| **Rollback** | Patch vulnerabilities; rotate compromised credentials; revert to last known secure version |
| **Invariant Check** | AES-256-GCM with quarterly key rotation; TLS 1.3 all inter-service; mTLS for gRPC; certificate pinning on mobile; zero human access to plaintext email |

---

## Phase 7: Onboarding + Billing (Weeks 19–22)
**Theme**: Concierge onboarding, voice calibration, billing, app store submission

### P7-T1: Stripe Integration (Weekly + Monthly Plans)
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Sync & State) |
| **Task** | Stripe Subscription API (weekly interval + standard monthly); Products: $15-20/week, $60-80/month; webhook handlers (invoice.paid, invoice.payment_failed, customer.subscription.deleted); grace period 72h; pause ingestion on non-payment |
| **Input** | Stripe API docs; `sync/internal/billing/stripe.go`; billing_records table |
| **Output** | `sync/internal/billing/stripe.go`; `POST /webhooks/stripe` handler; `POST /billing/subscribe` endpoint; billing dashboard queries |
| **Interface Output** | Subscribe response: `{subscription_id, status, payment_method_required?}`; Stripe webhook updates billing_records status |
| **Verification** | Test payments succeed; failed payment triggers grace period; subscription cancellation pauses ingestion; billing records accurate |
| **Rollback** | Refund via Stripe dashboard; reactivate subscription; resume ingestion |
| **Invariant Check** | Grace period 72h (data retained); pause ingestion (not deletion) on non-payment; weekly billing as primary option |

### P7-T2: Voice Calibration Automation (Onboarding)
| Attribute | Detail |
|---|---|
| **Agent** | ML/Intelligence Agent |
| **Task** | Fetch 3-5 sent emails from past 90 days (in:sent query via Gmail API); strip quoted text; embed into voice_examples; extract tone tags via LLM; ongoing calibration: every sent email auto-embedded with recency boost |
| **Input** | `intelligence/app/voice/calibration.py`; sent email fetch via Ingestion Mesh gRPC; tone tag classification prompt |
| **Output** | `intelligence/app/voice/calibration.py`; initial calibration service; ongoing calibration hook in send pipeline |
| **Interface Output** | Voice profile: `{example_count, tone_tags: [{tag, frequency}], recency_date}`; voice_examples Qdrant points |
| **Verification** | 3-5 examples embedded on onboarding; tone tags accurate (manual spot-check); every sent email adds to corpus; retrieval quality improves over time |
| **Rollback** | Clear voice_examples, re-calibrate from sent history; use generic prompts |
| **Invariant Check** | Voice corpus is private to user; improves asymptotically with use; calibration quality gate on "doesn't sound like me" feedback |

### P7-T3: Concierge Onboarding Dashboard
| Attribute | Detail |
|---|---|
| **Agent** | Backend Agent (Sync & State) + Client Agent |
| **Task** | Support staff web dashboard: view user OAuth scopes, initial sync status, first-batch routing decisions (read-only, no plaintext email); screen-share initiation; first 24h monitoring (conservative routing verification) |
| **Input** | Onboarding spec from Section 11.8; admin API endpoints |
| **Output** | `sync/internal/onboarding/dashboard.go`; admin API: `/admin/users/{id}/status`, `/admin/users/{id}/routing-log`; web dashboard (React) |
| **Interface Output** | User status: `{oauth_status, sync_progress, first_batch_size, routing_decisions[], support_notes}`; routing log: `{raw_email_id, route, confidence, rule_matched?}` |
| **Verification** | Support staff can view status without accessing email content; first-batch routing is conservative (>80% to Decision Stack); concierge can initiate screen-share |
| **Rollback** | Restrict admin access; manual support via direct user communication |
| **Invariant Check** | ZERO human access to plaintext email; support sees metadata only; first batch is conservative by design |

### P7-T4: App Store Submission
| Attribute | Detail |
|---|---|
| **Agent** | Client Agent |
| **Task** | iOS App Store submission (screenshots, description, privacy policy, app review); Google Play submission; app signing certificates; store listing optimization |
| **Input** | App Store Connect account; Google Play Console account; built app from P4-T1 through P6-T3 |
| **Output** | Submitted apps on both stores; store listings; privacy policy document |
| **Verification** | Apps approved and live on both stores; downloads function; push notifications work; crash-free rate >99% |
| **Rollback** | Roll back to previous version via store consoles; emergency patch via hot-fix |
| **Invariant Check** | Privacy policy accurate (data handling, retention, residency); app conforms to store guidelines |

---

## Cross-Cutting Concerns (All Phases)

### Monitoring & Alerting
| Concern | Implementation | Owner |
|---|---|---|
| Email loss detection | History ID gap monitoring; fetch count vs. ingest count | Ingestion Mesh |
| Citation hallucination rate | Citation verification pass rate alert at >0.1% | Intelligence Layer |
| False Auto-Handle rate | Weekly human audit sample of 100 Auto-Handle decisions | Classification Core |
| Queue state drift | Decision logs count vs. cards cleared mismatch alert | Sync & State |
| LLM cost per user | Daily token roll-up; 2x average flag | Intelligence Layer |
| API rate limit proximity | Redis rate limit at 80% → alert; 100% → throttle | Ingestion Mesh |
| Client crash rate | Sentry or similar; >0.1% crash-free rate target | Client |

### Dependency Graph Summary

```
Phase 0 (Foundation)
    ├── P0-T1 (Terraform VPC/RDS/Redis/S3)
    ├── P0-T2 (NATS/Qdrant/Neo4j/ECR)
    ├── P0-T3 (CI/CD/ECS)
    ├── P0-T4 (DB Migrations) ──depends──► P0-T1
    └── P0-T5 (Neo4j/Qdrant Schema) ──depends──► P0-T2

Phase 1 (Ingestion Mesh)
    ├── P1-T1 (OAuth) ──depends──► P0-T1, P0-T4
    ├── P1-T2 (Webhooks) ──depends──► P0-T2, P1-T1
    ├── P1-T3 (Polling Workers) ──depends──► P1-T1, P0-T2
    ├── P1-T4 (Parsing) ──depends──► P1-T3, P0-T1 (S3)
    ├── P1-T5 (Threading + Dedup) ──depends──► P1-T4, P0-T5
    └── P1-T6 (Event Publishing) ──depends──► P1-T5, P0-T2

Phase 2 (Classification)
    ├── P2-T1 (Extract-Only) ──depends──► P1-T6
    ├── P2-T2 (Auto-Handle Engine) ──depends──► P1-T6, P0-T4
    └── P2-T3 (Routing + Staging) ──depends──► P2-T2, P0-T4

Phase 3 (Intelligence) ──parallel──► Phase 4 (Client)
    ├── P3-T1 (LLM Client) ──depends──► P0-T2 (Redis)
    ├── P3-T2 (Chunking) ──depends──► P1-T6, P0-T5, P3-T1
    ├── P3-T3 (Compression) ──depends──► P3-T2, P3-T1
    ├── P3-T4 (Citation Verify) ──depends──► P3-T3, P0-T5
    ├── P3-T5 (Context Injection) ──depends──► P3-T3, P0-T5
    ├── P3-T6 (Consultation) ──depends──► P3-T2, P0-T5
    ├── P3-T7 (Drafting) ──depends──► P3-T1, P0-T5, P3-T5
    └── P3-T8 (Hierarchical Summary) ──depends──► P3-T2, P3-T1

Phase 4 (Client) ──parallel──► Phase 3
    ├── P4-T1 (RN Scaffold + SQLite) ──depends──► P0-T4 (schema)
    ├── P4-T2 (BatchGate + CardStack) ──depends──► P4-T1
    ├── P4-T3 (Offline Sync) ──depends──► P4-T1, P4-T2
    ├── P4-T4 (Text Clearing) ──depends──► P4-T2, P4-T3
    └── P4-T5 (Security Panel) ──depends──► P4-T1

Phase 5 (Voice + Calendar)
    ├── P5-T1 (Deepgram STT) ──depends──► P4-T2
    ├── P5-T2 (TTS + Voice Mode) ──depends──► P5-T1, P4-T4
    ├── P5-T3 (Calendar R/W) ──depends──► P1-T1 (OAuth tokens), P3-T5
    └── P5-T4 (Reminder Stack) ──depends──► P5-T3

Phase 6 (Sending Session + Polish)
    ├── P6-T1 (WebSocket Server) ──depends──► P0-T1, P3-T7
    ├── P6-T2 (Spawn Response) ──depends──► P6-T1, P3-T7
    ├── P6-T3 (Interrupt System) ──depends──► P4-T2, P5-T4
    └── P6-T4 (Security Audit) ──depends──► ALL prior

Phase 7 (Onboarding + Billing)
    ├── P7-T1 (Stripe Billing) ──depends──► P0-T4, P6-T4
    ├── P7-T2 (Voice Calibration) ──depends──► P3-T7, P1-T1
    ├── P7-T3 (Concierge Dashboard) ──depends──► P7-T2, P2-T3
    └── P7-T4 (App Store) ──depends──► P4-T1..P6-T3
```
