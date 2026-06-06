# Phase 1: Mental Model — Decision Stack Functional Causal Graph

## 1. Data Flow Topology
### 1.1 Lifecycle of a Single Incoming Email (End-to-End Causal Chain)

```
[External Gmail/Outlook API]
    │
    ▼ (push or poll)
┌─────────────────────────────────────────────────────────────────────┐
│ BOUNDED CONTEXT: INGESTION MESH (Go)                               │
│ Runtime: High-throughput, I/O-bound, latency-tolerant, stateless   │
├─────────────────────────────────────────────────────────────────────┤
│ Step 1.0: OAuth token refresh (if needed)                          │
│   Input:  user_id, email_account_id                                │
│   Action: Fetch DEK from KMS → decrypt refresh_token → get access  │
│   Output: valid access_token (15min in-memory TTL)                 │
│   Failure: token_expired → enqueue retry, alert user               │
│                                                                    │
│ Step 1.1: Raw email fetch (Gmail users.history.list / messages.get │
│           or Outlook Delta Query)                                  │
│   Input:  history_id / delta_link                                  │
│   Output: raw MIME payload, message_id, RFC 2822 headers           │
│   Guard:  Redis dedup:msg:{message_id} 24h TTL — DROP if exists    │
│   Guard:  Redis ratelimit:gmail:{user_id} 250 units/sec            │
│                                                                    │
│ Step 1.2: Parsing Pipeline                                         │
│   1.2a: HTML→text (jaytaylor/html2text)                            │
│   1.2b: Signature strip (ONNX DistilBERT classifier, P>0.85)       │
│   1.2c: Attachment extraction → S3 SSE-KMS                         │
│   1.2d: OCR trigger for images/scanned PDFs (async microservice)   │
│   Output: body_text, body_html, attachment_s3_uris, extracted_text │
│                                                                    │
│ Step 1.3: Threading Reconstruction (3-tier matching + new)         │
│   Tier 1: In-Reply-To header -> raw_emails.message_id lookup       │
│   Tier 2: References header -> raw_emails.message_id lookup        │
│   Tier 3: Fuzzy subject (Levenshtein<3) + participant overlap + 7d │
│   Fallback: CREATE new thread with SHA-256 thread_key              │
│   Output: thread_key (SHA-256 of sorted participants + subject)    │
│   Side-effect: UPSERT threads table (increment message_count)      │
│                                                                    │
│ Step 1.4: Cross-Source Contact Deduplication                       │
│   Action: Normalize email → query Neo4j Contact node               │
│   Output: Contact node (existing or new), SIMILAR_TO edge if fuzzy │
│                                                                    │
│ Step 1.5: Persistence                                              │
│   Output: INSERT raw_emails (classification='pending')             │
│                                                                    │
│ Step 1.6: Event Publish                                            │
│   Output: NATS JetStream email.ingested                            │
│   Payload: {user_id, source, account_id, thread_id, raw_email_id,  │
│             s3_uri, has_attachments, sender_email, received_at}    │
│   Policy: WorkQueue, max-deliver=5, DLQ=email.ingested.dlq         │
└─────────────────────────────────────────────────────────────────────┘
    │
    ▼ (NATS JetStream — async, exactly-once)
┌─────────────────────────────────────────────────────────────────────┐
│ BOUNDED CONTEXT: CLASSIFICATION CORE (Go + Python/FastAPI)         │
│ Runtime: Fast-path Go, ML-path Python, <5s Extract / <30s others   │
├─────────────────────────────────────────────────────────────────────┤
│ Step 2.0: Consume email.ingested event                             │
│                                                                    │
│ Step 2.1: EXTRACT-ONLY PIPELINE (Stage 1) — Go, <2s              │
│   2.1a: Regex bank scan (2FA/OTP, tracking, calendar MIME,        │
│         receipt/order #)                                           │
│   2.1b: ONNX lightweight classifier (DistilBERT) — is_receipt,     │
│         is_newsletter, is_notification — threshold 0.95            │
│   If match: extract datum → publish ExtractCompleted event         │
│             → mark raw_emails for 24h deletion                     │
│   Output: ROUTE=extract, extracted datum → push notification       │
│   If no match: proceed to Step 2.2                                 │
│                                                                    │
│ Step 2.2: AUTO-HANDLE PIPELINE (Stage 2)                           │
│   2.2a: Structured rule matching (JSON predicates in PostgreSQL)   │
│         Fields: sender_email/domain, subject, body, recipient,     │
│                 has_attachment, thread_participant_count, time     │
│         Ops: eq, ne, contains, regex, gt, lt, in, not_in          │
│         Order: usage_count DESC (cache compiled regex patterns)    │
│   2.2b: LLM pattern matching (Claude 3 Haiku) if no rule match    │
│         Prompt: "Known patterns: [rule_names], Email: [subject+    │
│                  body 500 chars], Respond: JSON {match, confidence}│
│   2.2c: Confidence gate: >=0.92 AND rule is 'active' → Auto       │
│         Otherwise → Decision Stack                                 │
│   If Auto-Handle match:                                            │
│     - execute action (reply_template, forward, calendar_accept,    │
│       delete, extract_notify)                                      │
│     - publish AutoHandled event                                    │
│     - LOG decision_logs (action='auto_handled')                    │
│   Output: ROUTE=auto OR ROUTE=decision                             │
│                                                                    │
│ Step 2.3: DECISION STACK ROUTING (Default)                         │
│   Output: NATS JetStream intelligence.compress                     │
│   Payload: {user_id, thread_id, raw_email_ids[], priority_score,   │
│             source}                                                │
└─────────────────────────────────────────────────────────────────────┘
    │
    ▼ (NATS JetStream)
┌─────────────────────────────────────────────────────────────────────┐
│ BOUNDED CONTEXT: INTELLIGENCE LAYER (Python/FastAPI)               │
│ Runtime: Low-throughput, GPU/LLM-bound, expensive, stateful        │
│ Note: No inbound internet access. NAT Gateway for LLM API calls.   │
├─────────────────────────────────────────────────────────────────────┤
│ Step 3.0: Consume intelligence.compress event                      │
│                                                                    │
│ Step 3.1: SEMANTIC CHUNKING                                        │
│   Input:  all body_text from raw_emails for thread                 │
│   Action: Split at paragraph boundaries, 800 token max (cl100k),   │
│           100 token overlap, 50 token min, flag is_signature       │
│   Output: chunks with chunk_id, embedded via text-embedding-3-l    │
│   Side-effect: UPSERT Qdrant email_chunks collection               │
│                                                                    │
│ Step 3.2: CONTEXT INJECTION                                        │
│   3.2a: Relationship Graph → query Neo4j Contact                   │
│           Inject: interaction_count, avg_response_hours, tone_     │
│                   history, last_project, last_monetary_value        │
│   3.2b: Calendar Context → query PostgreSQL calendar_events        │
│           Inject: next 7 days, conflict detection (if scheduling)  │
│   3.2c: Prior Thread Summary → if >50 emails, hierarchical         │
│           summarization (map-reduce: Haiku batch→Sonnet synthesize)│
│           Cache in Qdrant consultation_index with thread_summary   │
│                                                                    │
│ Step 3.3: CARD GENERATION (Compression)                            │
│   Input:  thread subject, all chunks (id+text), relationship ctx,  │
│           calendar context                                         │
│   Model:  Claude 3.5 Sonnet ( Claude 3 Haiku fallback for cost)   │
│   Prompt rules:                                                    │
│     1. Every claim MUST cite chunk_id from provided chunks         │
│     2. If unverifiable, omit it                                    │
│     3. they_want: single sentence, max 280 chars                   │
│     4. need_from_user: explicit irreducible gap only user can fill │
│     5. Do NOT infer tacit knowledge (margins, risk, relationship)  │
│   Output: {from, they_want, context, need_from_user, citations[]}  │
│                                                                    │
│ Step 3.4: CITATION VERIFICATION                                    │
│   Action: For every chunk_id in citations, verify exists in Qdrant │
│           payload for this thread                                  │
│   If hallucination detected: REJECT output → retry (max 3)         │
│   On 3 failures: route to "manual review" queue (human operator)   │
│                                                                    │
│ Step 3.5: URGENCY SCORING                                          │
│   deadline<24h: +0.4 | 24-72h: +0.2 | high-interaction contact:+0.1│
│   urgent keywords: +0.2 per word, max +0.3                         │
│   user-initiated thread: +0.1 | near-auto-handle: +0.05            │
│   Cap at 1.0. Score>0.8 → flag as potential Interrupt              │
│                                                                    │
│ Step 3.6: PERSISTENCE                                              │
│   Output: INSERT decision_cards (card_state='pending')             │
│   Output: Publish sync.notify.CardCreated to NATS                  │
│   Side-effect: IF scheduling intent → INSERT calendar_events       │
└─────────────────────────────────────────────────────────────────────┘
    │
    ▼ (gRPC over HTTP/2)
┌─────────────────────────────────────────────────────────────────────┐
│ BOUNDED CONTEXT: SYNC & STATE (Go, gRPC/HTTP)                      │
│ Runtime: Medium-throughput, consistency-critical, latency-sensitive│
├─────────────────────────────────────────────────────────────────────┤
│ Step 4.0: Receive gRPC CreateCard from Intelligence                │
│                                                                    │
│ Step 4.1: QUEUE MANAGEMENT                                         │
│   Action: INSERT into per-user queue table (PostgreSQL)            │
│   Compute: batch_size, estimated_clear_time (from running avg)     │
│   If batch_size >= threshold OR urgency>0.8 Interrupt:             │
│     → trigger notification dispatch                                │
│                                                                    │
│ Step 4.2: NOTIFICATION DISPATCH                                    │
│   Normal: "You have N decisions. Estimated M minutes. Ready?"      │
│   Interrupt (urgency>0.8): Immediate push via FCM/APNS             │
│   Temporal: Daily digest, pre-event briefings (15min before)       │
│   Output: Push notification + WebSocket availability flag          │
│                                                                    │
│ Step 4.3: CLIENT BATCH SYNC (on client connect/reconnect)          │
│   Input:  device_state (last_sync_timestamp, device_id)            │
│   Action: CRDT merge: server_queue ∩ client_pending                │
│   Output: HTTPS response with batch of cards (ordered by urgency)  │
│   Conflict resolution: server_version wins on draft_body,          │
│                        user_decision wins on card_state            │
└─────────────────────────────────────────────────────────────────────┘
    │
    ▼ (HTTPS REST + WebSocket for Sending Sessions)
┌─────────────────────────────────────────────────────────────────────┐
│ BOUNDED CONTEXT: CLIENT (React Native / Expo)                      │
│ Runtime: Offline-first, local SQLite (SQLCipher), in-memory cache  │
├─────────────────────────────────────────────────────────────────────┤
│ Step 5.0: OFFLINE BATCH OPERATIONS                                 │
│   User clears entire batch without network connectivity            │
│   Local state transitions: pending → local_decision stored         │
│   Sync queue: INSERT sync_queue (operation='update', payload)      │
│                                                                    │
│ Step 5.1: CARD CLEARING FLOW (Single Card Interaction)             │
│   Display: One card at a time. No list. No inbox view.             │
│   Card shows: from, they_want, context, need_from_user             │
│   User actions:                                                    │
│     [Decide] → type/speak one-line instruction (e.g., "9500, 2wk") │
│     [Consult] → Q&A against thread chunks (max 10 consultation     │
│                 turns, Redis consultation:turns:{card_id})         │
│     [Source] → view verbatim citation with chunk_id                │
│     [Skip]   → card remains pending, moves to next                 │
│                                                                    │
│ Step 5.2: SENDING SESSION (WebSocket)                              │
│   Trigger: User provides decision fact → drafts need nuance        │
│   Input: user fact, card_id, thread context                        │
│   Flow: Client opens WebSocket → Intelligence drafts via gRPC      │
│   Spawn Response: predictive co-authorship (type "Look" → para)    │
│   Display: draft_body rendered with approval/editing UI            │
│   Actions: [Edit] → modify draft, [Approve] → queue send, [Back]   │
│                                                                    │
│ Step 5.3: VOICE PIPELINE (WebRTC + Deepgram Nova-2 streaming)      │
│   Structure: "Next: [from]. [they_want]. What do you want?"        │
│   User speaks fact → client sends audio → Deepgram STT             │
│   Display: transcription + spawned draft preview                   │
│   Approve: voice "Yes" → 30-second undo window ("Wait, no" halts)  │
│   Next: moves to next card in batch                                │
│                                                                    │
│ Step 5.4: BACKGROUND SYNC (expo-background-fetch)                  │
│   Trigger: network reconnect                                       │
│   Action: Upload sync_queue → server reconciles → download updates │
│   Security: mutual-TLS pinning, Bearer JWT (24h, refreshable)      │
│                                                                    │
│ Step 5.5: SEND EXECUTION (approved draft)                          │
│   Action: HTTPS POST /send with card_id, draft_id                  │
│   Server: Sync layer → gRPC Ingestion Mesh (send via Gmail API)    │
│   Guard: user_approved MUST be TRUE (human-in-the-loop invariant)  │
│   Side-effect: UPDATE drafts (sent_at), UPDATE decision_cards      │
│   Side-effect: UPSERT Neo4j INTERACTION edge (tone, monetary, etc) │
│   Side-effect: IF mid-session delegation → extract Auto-Handle     │
│                rule → INSERT auto_handle_rules (status='staged')   │
└─────────────────────────────────────────────────────────────────────┘
    │
    ▼ (gRPC + background processing)
[Auto-Handle Rule Activation — 48h after staging]
    │
    ▼ (NATS + cron)
[Batch Notification Cycle repeats]
```

### 1.2 Inter-Context Data Flow Summary

| From → To | Protocol | Payload Schema | Persistence |
|---|---|---|---|
| Ingestion → Classification | NATS JetStream | `email.ingested` event envelope | WorkQueue, 5 retries, DLQ |
| Classification → Intelligence | NATS JetStream | `intelligence.compress` routing decision | WorkQueue |
| Classification → Sync | NATS JetStream | `ExtractCompleted` / `AutoHandled` | Logged to decision_logs |
| Intelligence → Sync | gRPC HTTP/2 | `CreateCard`, `UpdateCard`, `NotifyUser` | PostgreSQL queues |
| Sync → Client | HTTPS REST + WebSocket + FCM/APNS | Batch operations, Sending Sessions, pushes | SQLite local |
| Client → Sync | HTTPS mTLS + Bearer JWT | Sync queue uploads, send requests | Server reconciliation |
| Intelligence → Neo4j | Bolt | Contact/INTERACTION CRUD | Graph persistence |
| Intelligence → Qdrant | HTTP | Chunk embeddings, voice examples, consultation | Vector persistence |
| Ingestion → S3 | AWS SDK | Raw email blobs, attachments | SSE-KMS encrypted |
| All → PostgreSQL | SQL | Relational state (various tables) | RDS with pgcrypto |

---

## 2. Trust Gradient

### 2.1 User Journey: Suspicion → Delegation

```
[Hour 0: OAuth Connect]
    │
    ▼
[Hours 0-24: First Sync + Concierge Monitoring]
    ├── System: Conservative routing — most items → Decision Stack
    ├── System: First batch notification (small, ~3-5 cards)
    ├── User: Checks sources frequently (every citation)
    └── Trust signal: Citation accuracy, card quality
    │
    ▼
[Hours 24-48: Pattern Recognition Proving]
    ├── System: Identifies Sarah-invoice as routine → still routes to Stack
    ├── System: Flags legal threat as urgent → correct prioritization
    ├── User: Begins checking sources only on high-stakes cards
    └── Trust signal: Correct urgency scoring, honest queue
    │
    ▼
[Hours 48-72: Delegation Initiation]
    ├── User: During Sending Session says "Handle Sarah's invoices going forward"
    ├── System: Extracts pattern → INSERT auto_handle_rules (status='staged')
    ├── System: NOTIFIES user: "I'll handle these after a 48-hour review"
    └── Trust signal: Staging window demonstrates epistemic humility
    │
    ▼
[Hours 72-120: First Auto-Handle Activation]
    ├── System: Rule activates (activated_at set)
    ├── System: Continues routing similar items to Stack WITH label
    │            "This would be auto-handled. Review?"
    ├── User: Sees correct executions, builds confidence
    └── Trust signal: Predictable accuracy, visible rule executions
    │
    ▼
[Week 2+: Delegation Expansion]
    ├── User: Creates 3-5 active rules
    ├── System: 60-70% of emails now Extract/Auto
    ├── User: Checks Gmail "just in case" impulse fades
    └── Trust signal: Time savings realized, habit formed
    │
    ▼
[Month 2+: Full Delegation]
    ├── User: Rarely opens source email
    ├── System: Voice corpus accumulated → drafts sound like user
    ├── System: Relationship graph rich → context injection precise
    └── Trust signal: Switching cost becomes prohibitive
```

### 2.2 Trust Mechanisms by Component

| Trust Transition | Technical Mechanism | Enforcing Component | Invariant |
|---|---|---|---|
| Source verification | chunk_id citation anchoring → verbatim highlight | Intelligence Layer (Step 3.4) | Every claim cites chunk_id |
| Conservative routing | Confidence floor 0.92, default=Decision Stack | Classification Core (Step 2.2) | False negative > false positive |
| Staging window | 48h delay: staged→active via cron | Sync & State (auto_handle_rules.status) | No immediate activation |
| Undo safety | 30s voice undo window | Client voice pipeline | Lower friction ≠ higher risk |
| Honest queue | Accumulative batch size + time estimate | Sync & State (batch mgmt) | No truncation, no hiding |
| Offline clearing | Full batch operation without network | Client (SQLite + CRDT) | User owns their queue |
| Predictable error | Omit nuance > invent facts | Intelligence Layer prompt engineering | Conservative bias |

---

## 3. The Inversion Boundary

### 3.1 The Line: Above (AI) vs. Below (User)

```
┌─────────────────────────────────────────────────────────────────────┐
│ ABOVE THE LINE — AI Handles (Expensive, Reducible, Comprehension)   │
├─────────────────────────────────────────────────────────────────────┤
│ Ingestion Mesh                                                      │
│   ├─ OAuth lifecycle management                                     │
│   ├─ Raw email fetching (Gmail/Outlook APIs)                        │
│   ├─ HTML→text conversion                                           │
│   ├─ Signature stripping (ML classifier)                            │
│   ├─ Attachment extraction + OCR                                    │
│   ├─ Thread reconstruction (In-Reply-To, fuzzy matching)            │
│   └─ Cross-source contact deduplication                             │
│                                                                      │
│ Classification Core                                                   │
│   ├─ Regex-based extract-only identification                        │
│   ├─ Lightweight ONNX classification (receipt/newsletter/notify)    │
│   ├─ Structured rule predicate evaluation                           │
│   ├─ LLM-based pattern matching (Haiku)                             │
│   └─ Tri-state routing decision (extract/auto/decision)             │
│                                                                      │
│ Intelligence Layer                                                    │
│   ├─ Semantic chunking + embedding (text-embedding-3-large)         │
│   ├─ Context injection (relationship graph, calendar, history)      │
│   ├─ Hierarchical summarization (>50 email threads)                 │
│   ├─ Compression: raw email → decision card (Claude 3.5 Sonnet)     │
│   ├─ Citation verification (chunk_id hallucination detection)       │
│   ├─ Urgency scoring                                                │
│   ├─ Consultation: Q&A against thread chunks (max 10 turns)         │
│   ├─ Drafting: user fact → full email (Claude 3.5 Sonnet)           │
│   ├─ Voice calibration: few-shot retrieval from voice_examples      │
│   ├─ Spawn response generation (predictive co-authorship)           │
│   └─ Calendar conflict detection + scheduling intent resolution     │
│                                                                      │
│ Sync & State                                                          │
│   ├─ Queue management + CRDT merge                                  │
│   ├─ Batch accumulation + time estimation                           │
│   ├─ Notification dispatch (batch, interrupt, temporal)             │
│   ├─ Auto-Handle rule staging + activation cron                     │
│   └─ Offline reconciliation                                         │
└─────────────────────────────────────────────────────────────────────┘
                              ═══════════════════
                           THE INVERSION BOUNDARY
                    "The AI eats the 8 minutes. The human
                     supplies the 30 seconds."
                              ═══════════════════
┌─────────────────────────────────────────────────────────────────────┐
│ BELOW THE LINE — User Handles (Irreducible, Judgment, Stance)       │
├─────────────────────────────────────────────────────────────────────┤
│ Decision Input (irreducible human judgment — NEVER inferred)        │
│   ├─ The price: "Tell her 9500"                                     │
│   ├─ The stance: "Push back on the additional deliverables"         │
│   ├─ The yes/no: "Accept the meeting" / "Decline with alt"          │
│   ├─ The timeline: "Two weeks" / "End of month"                     │
│   ├─ The bridge: "Preserve relationship" / "Stand firm"             │
│   └─ The priority: Which cards to clear first in a batch            │
│                                                                      │
│ Delegation Decisions (mid-session, earned not assumed)              │
│   ├─ "Handle Sarah's invoices going forward" → creates staged rule  │
│   ├─ "Always forward these to accounting" → creates rule            │
│   └─ "Never auto-handle legal@domain.com" → creates negative rule   │
│                                                                      │
│ Approval Actions (human-in-the-loop for ALL sends)                  │
│   ├─ [Approve] on drafted email → authorization to send             │
│   ├─ [Edit] on draft → user modifies AI-generated text              │
│   ├─ [Back] → return to decision, reject draft                      │
│   └─ [Wait, no] within 30s voice undo → halt send                   │
│                                                                      │
│ Verification Actions (trust gradient traversal)                     │
│   ├─ [Source] tap → view verbatim citation                          │
│   ├─ [Consult] → ask follow-up about thread context                 │
│   └─ Frequency decreases as trust accrues                           │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.2 Boundary Enforcement Points

| Boundary | Enforcement | Violation |
|---|---|---|
| AI does NOT infer pricing floors | Intelligence prompt: "Do not infer tacit knowledge" | System failure if card shows inferred margin |
| AI does NOT decide yes/no | card_state stays 'pending' until user_decided_at set | System failure if auto-approval occurs |
| AI does NOT send without approval | drafts.user_approved MUST be TRUE for send | Critical security violation |
| AI does NOT create active rules immediately | auto_handle_rules.status='staged' for 48h | Trust violation if staged_at skipped |
| User does NOT thread/parse/synthesize | Client shows only cards, never raw email | UX violation if raw MIME shown |
| User does NOT manage stream | Batch model only, no continuous feed | UX violation if "new email" notifications |

---

## 4. Compression & Expansion Loop

### 4.1 Forward: Raw Email → Decision Card (Compression)

```
RAW EMAIL (High Entropy, Container-Heavy)
├─ Headers: From, To, CC, BCC, Reply-To, Message-ID
├─ Subject: "Re: Re: Fwd: Website Redesign — Updated Proposal"
├─ Body HTML: <html><head><style>...</style></head><body>...
├─ Quoted thread: 
│   ├─ On Mon, Bob wrote:
│   │   └─ > Thanks Sarah, can you send the updated...
│   │      └─ > > Sure, I've attached the latest version...
│   ├─ Signature: --\nSarah Chen | Vendor Inc | 555-0123
│   └─ Disclaimer: This email is confidential...
├─ Attachments: proposal_v3.pdf, timeline.xlsx
└─ Calendar invite: invite.ics

    │
    ▼ [INGESTION MESH: Parsing Pipeline]
    │    Strip HTML → plain text (lossy: formatting discarded)
    │    Strip signature (lossy: contact info extracted to graph)
    │    Strip quoted thread (preserved as separate raw_emails rows)
    │    Extract attachments → S3 (reference preserved)
    │
    ▼ [INTELLIGENCE: Semantic Chunking]
    │    Split into paragraphs → chunks with chunk_id
    │    Embed → 1024-dim vectors in Qdrant
    │    Information preserved: semantic units, retrievable
    │    Information lost: exact formatting, paragraph ordering cues
    │
    ▼ [INTELLIGENCE: Card Generation — LLM Synthesis]
    │    Compress chunks + context into decision vector
    │
DECISION CARD (Low Entropy, Content-Only)
├─ from: {name: "Sarah Chen", relationship_context: "Vendor — Website Redesign",
│         last_contact: "2 days ago", interaction_count: 12}
├─ they_want: "Updated proposal with additional deliverables, $12,500, 3-week timeline"
├─ context: {history_summary: "Original scope was $9,500 for 2 weeks. Sarah added SEO 
│            optimization and content migration.",
│            prior_commitments: ["$9,500 base quote agreed March 15"],
│            quoted_numbers: ["$12,500", "$9,500", "3 weeks", "2 weeks"],
│            deadlines: ["2024-06-15"],
│            sentiment: "professional, slightly pushy"}
├─ need_from_user: "Approve the expanded scope at $12,500 or counter with adjusted 
│                   deliverables and your price."
└─ citations: [{chunk_id: "abc-123", verbatim: "The updated proposal includes SEO 
                optimization and content migration, bringing the total to $12,500 
                with a 3-week timeline.", email_id: "def-456", paragraph_index: 3}]

COMPRESSION RATIO: ~5000 tokens raw → ~200 tokens card (25:1)
INFORMATION LOSS: HTML formatting, full quoted thread, signature metadata,
                  all but key quoted_numbers, exact paragraph ordering
RECOVERY MECHANISM: chunk_id → Qdrant → verbatim snippet → raw_emails
```

### 4.2 Reverse: User Fact → Drafted Email (Expansion)

```
USER FACT (Low Entropy, Irreducible Judgment)
└─ User types/speaks: "9500, two weeks. Cut the SEO stuff."

    │
    ▼ [INTELLIGENCE: Voice Calibration + Context Retrieval]
    │    Query voice_examples (Qdrant) for similar past replies to Sarah
    │    Retrieve: user's typical tone with Sarah (direct but friendly)
    │    Retrieve: standard sign-off, hedging patterns
    │    Retrieve: relationship context (12 interactions, vendor relationship)
    │
    ▼ [INTELLIGENCE: Drafting — LLM Expansion]
    │    Expand fact into full email with:
    │    - Appropriate greeting (based on relationship history)
    │    - Historical reference (March 15 agreement)
    │    - Structural justification (scope boundary explanation)
    │    - Tone calibration (match user's voice corpus)
    │    - Conditional clause ("If the timeline works for you...")
    │    - Threading headers (In-Reply-To, References)
    │    - Signature (from user's voice profile)
    │
DRAFT EMAIL (High Entropy, Fully Composed)
├─ Subject: Re: Website Redesign — Updated Proposal
├─ To: sarah@vendor.com
├─ In-Reply-To: <def-456@vendor.com>
├─ References: <orig-789@vendor.com> <def-456@vendor.com>
└─ Body:
     Hi Sarah,

     Thanks for sending over the updated proposal. After reviewing the expanded
     scope, I think we should stick with the original parameters: $9,500 for the
     two-week timeline we discussed on March 15.

     The SEO optimization and content migration are valuable, but they're outside
     what we agreed to for this phase. Let's get the core redesign locked down first
     and we can revisit the additional work as a separate engagement.

     Let me know if the two-week timeline still works on your end.

     Best,
     [User's standard sign-off]

EXPANSION RATIO: ~10 tokens fact → ~150 tokens draft (1:15)
INFORMATION SOURCES: User fact + voice corpus + relationship graph + thread
                     history + calendar context
VERIFICATION: User reviews draft → [Approve] or [Edit] before send
```

### 4.3 Information Loss & Recovery Matrix

| Transformation | Information Lost | Recovery Mechanism | Component |
|---|---|---|---|
| HTML→text | Formatting, colors, layout | None needed (container) | Ingestion |
| Signature strip | Contact details, titles | Neo4j Contact node | Ingestion + Graph |
| Threading | Temporal ordering | raw_emails.received_at sort | Ingestion |
| Chunking | Cross-paragraph context | 100-token overlap + consultation | Intelligence |
| Card compression | Full quoted text | chunk_id → Qdrant → source viewer | Intelligence |
| Draft expansion | User's exact wording | User edit before approve | Client |
| Voice→text | Prosody, emphasis | TTS playback of draft | Client |

---

## 5. Moat Accumulation

### 5.1 Accumulating Data Structures

```
┌─────────────────────────────────────────────────────────────────────┐
│ MOAT 1: VOICE CALIBRATION CORPUS                                    │
├─────────────────────────────────────────────────────────────────────┤
│ Description: Retrieval index of user's sent email history           │
│ Store:     Qdrant voice_examples collection (1024-dim)              │
│ Written by: Drafting service (every approved→sent email)            │
│ Read by:   Drafting service (few-shot retrieval for new drafts)     │
│ Content:   reply_text, topic_keywords, tone_tags, sender_email      │
│ Accumulation: +1 vector per sent email, asymptotic improvement      │
│ Switching cost: Draft quality degrades to generic AI on export      │
│ Cannot export: Tone is emergent, not declarable                     │
├─────────────────────────────────────────────────────────────────────┤
│ MOAT 2: RELATIONSHIP GRAPH                                          │
├─────────────────────────────────────────────────────────────────────┤
│ Description: Network of user's business relationships + interactions│
│ Store:     Neo4j (Contact nodes, INTERACTION edges)                 │
│ Written by: Card generation (contact queries), Send execution       │
│             (INTERACTION edge UPSERT), Contact dedup (Ingestion)    │
│ Read by:   Context injection (Compression), Consultation search     │
│ Content:   interaction_count, avg_response_hours, tone_history,     │
│            monetary_value, project associations, sentiment trajectory│
│ Accumulation: +1 INTERACTION edge per thread, discovered patterns   │
│ Switching cost: 18 months of history lost; must re-teach every      │
│                 relationship                                        │
│ Cannot export: Graph is network-of-meaning, not CSV-exportable      │
├─────────────────────────────────────────────────────────────────────┤
│ MOAT 3: DELEGATION RULE CORPUS                                      │
├─────────────────────────────────────────────────────────────────────┤
│ Description: User's encoded operational logic                       │
│ Store:     PostgreSQL auto_handle_rules table                       │
│ Written by: Mid-session delegation ("Handle these from Sarah")      │
│             → pattern extraction → INSERT status='staged'           │
│ Read by:   Classification Core (rule predicate evaluation)          │
│ Content:   JSON predicates (sender, subject, body patterns),        │
│            action_type, confidence_threshold                        │
│ Accumulation: +1 rule per delegation gesture, usage_count tracks    │
│               proven value                                          │
│ Switching cost: User must re-teach operational workflows; invested  │
│                 teaching effort lost                                │
│ Partial export: Predicates are structured but user-specific context │
│                 is lost                                             │
├─────────────────────────────────────────────────────────────────────┤
│ MOAT 4: BATCH HABIT LOCK-IN                                         │
├─────────────────────────────────────────────────────────────────────┤
│ Description: Psychological transition from ambient anxiety to       │
│              bounded clearing ritual                                │
│ Store:     Behavioral (not data)                                    │
│ Written by: Consistent batch experience + accurate time estimates   │
│ Read by:   User psychology                                          │
│ Accumulation: Each successful batch reinforces trust + habit        │
│ Switching cost: Return to inbox feels like regression, not feature  │
│                 comparison; 72h trust window is barrier to re-entry │
│ Cannot export: Habit is experiential, not portable                  │
└─────────────────────────────────────────────────────────────────────┘
```

### 5.2 Moat Write/Read Flow Diagram

```
                    ┌──────────────────┐
                    │   APPROVED SEND  │
                    └────────┬─────────┘
                             │
            ┌────────────────┼────────────────┐
            ▼                ▼                ▼
    ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
    │ Voice Corpus │ │ Relationship │ │ Delegation   │
    │ (Qdrant)     │ │ Graph (Neo4j)│ │ Rules (PG)   │
    │ INSERT voice │ │ MERGE Contact│ │ (mid-session)│
    │ _examples    │ │ + INTERACTION│ │ INSERT rules │
    └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
           │                │                │
           └────────────────┼────────────────┘
                            ▼
                    ┌──────────────────┐
                    │  FUTURE DRAFTS   │
                    │  (higher quality,│
                    │  personalized,   │
                    │  context-aware)  │
                    └──────────────────┘
```

---

## 6. Failure Cascades

### 6.1 Per-Context Trust-Damaging Failure Points

| Bounded Context | Single Point of Failure | Trust Impact | Mitigation | Detection |
|---|---|---|---|---|
| **Ingestion Mesh** | Losing an email (messageAdded not processed, dedup collision, history_id not updated) | User discovers missing email in Gmail → "I can't trust this with my business" | Redis dedup TTL 24h only; persistent history_id in PG; polling fallback when webhooks fail; DLQ with 5 retries + alerting | Monitor: messages fetched vs. history records processed; alert on gap |
| **Classification Core** | False positive to Auto-Handle (routine email misclassified as auto-able with wrong action) | Wrong email sent on user's behalf → "This damaged my relationship" | Confidence floor 0.92; conservative default=Decision Stack; 48h staging; no immediate activation; human operator review queue for edge cases | Log: all Auto-Handle decisions with full context; weekly human audit sample |
| **Intelligence Layer** | Hallucinated citation (chunk_id that doesn't exist in Qdrant) | User checks source → citation is fake → "The system makes things up" | Citation verification (Step 3.4): reject + retry (max 3); manual review queue on failure; prompt engineering: "If you cannot verify, omit it" | Monitor: citation verification pass rate; alert on >0.1% hallucination rate |
| **Sync & State** | Corrupting queue state (CRDT merge error → lost user decision) | User cleared batch but decisions not reflected → "My work was lost" | Server_version on every card; CRDT merge with server-version-wins on draft, user-wins on decision; idempotent sync operations; reconciliation on every connect | Monitor: decision_logs count vs. cards cleared; alert on mismatch |
| **Client** | Leaking plaintext email to local storage | Raw email body found in SQLite → "My data is not secure" | SQLCipher encryption; client SQLite stores ONLY cards/drafts/metadata; raw email bodies NEVER leave server unencrypted; periodic security audit | Automated penetration test: verify no raw_email content in client DB |

### 6.2 Failure Cascade Chains

```
CASCADE 1: Ingestion → Intelligence (Missing email never becomes card)
[Webhook drop] → [history_id not updated] → [polling also misses if gap>sync window]
  → [email invisible to user] → [trust death if user finds it in Gmail]
MITIGATION: Polling runs every 5min as backup; gap detection via historyId sequence
           validation; alert on history discontinuity

CASCADE 2: Classification → Auto-Handle (Wrong action on wrong email)
[LLM hallucinates pattern match] → [confidence inflated] → [rule mis-applied]
  → [wrong reply sent] → [user revokes all rules] → [delegation reset to zero]
MITIGATION: Confidence floor 0.92 is hard ceiling; staging window gives 48h to catch;
           action execution logs to decision_logs for audit trail

CASCADE 3: Intelligence → Sync → Client (Card with bad citation)
[LLM invents chunk_id] → [citation verification passes (rare bug)] → [card shown]
  → [user taps Source → 404] → ["system lies"] → [user checks every source forever]
MITIGATION: Double-verify: (a) chunk_id in Qdrant payload, (b) verbatim snippet fuzzy
           match to actual chunk text; manual review queue on any mismatch

CASCADE 4: Client → Sync (Lost decision on reconnect)
[User clears 5 cards offline] → [sync_queue uploaded] → [CRDT merge conflict]
  → [server overwrites with stale state] → [user decisions lost]
  → ["I already did these" → redo work → frustration]
MITIGATION: CRDT merge: user_decision field is user-wins; reconciliation log;
           client keeps local_decision until server ack; idempotent sync ops

CASCADE 5: Cross-context: Neo4j graph corruption
[Bad contact merge] → [wrong relationship context injected into card]
  → [card references wrong prior commitments] → [user acts on false context]
MITIGATION: SIMILAR_TO edge with confidence score + user review flag; contact merge
           requires high confidence; human review for fuzzy matches
```

### 6.3 System-Wide Failure Mode Matrix

| Failure | Scope | Recovery Time | User Impact | Severity |
|---|---|---|---|---|
| Single email lost (ingestion) | 1 user, 1 message | Hours (manual recovery) | "Check Gmail just in case" returns | Critical |
| Hallucinated citation | 1 card | Minutes (regenerate card) | Source check fails | High |
| False Auto-Handle | 1 email | Irreversible (sent) | Wrong email sent | Critical |
| Queue state corruption | 1 user batch | Hours (manual reconciliation) | Lost decisions | High |
| Client plaintext leak | 1 device | Permanent (data exposed) | Security breach | Critical |
| LLM provider outage | All users | Minutes (fallback model) | Delayed card generation | Medium |
| NATS JetStream loss | All users | Hours (replay from retention) | Delayed processing | High |
| PostgreSQL corruption | All users | Hours (restore from backup) | Service unavailable | Critical |
| Neo4j graph loss | All users | Days (rebuild from emails) | Degraded context | High |
| KMS key failure | All users | Minutes (HSM failover) | Cannot decrypt tokens | Critical |
