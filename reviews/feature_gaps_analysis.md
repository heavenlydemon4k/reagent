# Decision Stack — Feature Gap Analysis & Product Strategy

> **Date:** 2026-01-28
> **Analyst:** Product Strategy Team
> **Scope:** Full product feature set vs. competitive landscape
> **Goal:** Identify features that would make users switch from Gmail/Outlook and never look back

---

## Executive Summary

Decision Stack has built a **remarkably differentiated core experience** — card-based decision clearing, voice-first interaction, zero-hallucination citation verification, and offline-first architecture. These are genuine moats that no competitor has replicated.

However, the product has **significant gaps in table-stakes features** that users expect from any email client, and **missed differentiation opportunities** that could cement its position as the definitive email productivity tool.

**The verdict:** Decision Stack is a paradigm shift for email processing, but it currently functions more like a *decision-processing appliance* than a *complete email replacement*. Users will still need to keep Gmail/Outlook open for everyday tasks, which undermines the "never look back" goal.

### Top-Line Numbers

| Category | Features Assessed | Missing (P0+P1) | Differentiation Opportunities |
|----------|------------------|-----------------|------------------------------|
| Core Email UX | 12 | 8 | 2 |
| Competitive Parity | 10 | 7 | 3 |
| Differentiation | 8 | — | 6 |
| Onboarding | 6 | 5 | 4 |
| Enterprise | 8 | 8 | 2 |
| Engagement | 7 | 5 | 5 |
| Platform/API | 6 | 6 | 4 |
| **TOTAL** | **57** | **39** | **26** |

---

## Part 1: What's Missing That Users Expect?

These are table-stakes features that every modern email client provides. Their absence creates friction that forces users to context-switch back to Gmail/Outlook.

### 1.1 Search Across All Emails

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Full-text search across email threads | **Critical** — Users cannot find past emails without leaving DS | Medium | **P0** | Parity (Gmail, Outlook, Superhuman, Spark all have this) | Qdrant already has all chunks indexed. Add a search endpoint that queries Qdrant with user_id filter, returns thread matches. Cross-encoder re-rank for relevance. ~2 weeks backend, ~1 week client. |
| Advanced search (sender, date, attachment, label) | High — Power users need filtered search | Low | **P1** | Parity | Extend Qdrant payload filters (sender, date range, has_attachment). Neo4j for sender-based search. ~1 week. |
| Search within chat | Medium — Find past chat conversations | Low | **P2** | Parity | PostgreSQL conversation table already exists. Full-text index on messages. ~3 days. |

**Assessment:** Qdrant is already the search infrastructure — chunks are embedded, indexed, and filtered by user_id. This is a **surprisingly easy win** that should be P0. The intelligence layer's ChunkStore + cross-encoder retriever can be exposed as a search API with minimal work.

---

### 1.2 Attachments Viewer & Manager

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Attachment list across all emails | **High** — Users can't see/download attachments from cards | Medium | **P1** | Parity | Ingestion Mesh already extracts attachments → S3. Need: (1) API to list attachments by user/thread, (2) client viewer for common formats (images, PDFs), (3) download manager. S3 presigned URLs already supported. ~2 weeks. |
| Attachment preview (image, PDF) | High — Avoid downloading to view | Medium | **P1** | Parity | React Native: react-native-pdf + react-native-fast-image. S3 presigned URL → viewer. ~1 week client. |
| OCR for attachment text extraction | Medium — Search inside attachments | Medium | **P2** | **Differentiator** | OCR service already exists (Tesseract, 24/24 tests passing). Wire ingestion → OCR → Qdrant for searchable attachment content. ~2 weeks. |

**Assessment:** Attachments are extracted and stored in S3 during ingestion, but there's **zero client-side support**. This is a significant gap — many decisions require reviewing attachments (contracts, invoices, proposals).

---

### 1.3 Contact Profile & Relationship Timeline

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Contact detail view (profile, history) | **High** — Neo4j has rich data that's invisible to users | Medium | **P1** | **Differentiator** | Neo4j already stores: interaction stats, tone history, commitments, prior interactions. Build a ContactScreen that queries Neo4j via API. Show: contact info, email history timeline, avg response time, open commitments. ~2 weeks. |
| Relationship timeline (all emails with contact) | High — Context for decisions | Medium | **P1** | **Differentiator** | Use Qdrant thread chunks + Neo4j interaction graph. Timeline view of all communications. ~2 weeks. |
| Relationship health score | Medium — Gamify/alert on neglected contacts | Low | **P2** | **Differentiator** | Compute from Neo4j: days since last reply, response rate, tone trend. Simple formula → score 0-100. ~3 days. |

**Assessment:** The Neo4j relationship graph is one of Decision Stack's **greatest untapped assets**. Other products have contact lists; none have relationship intelligence. This should be surfaced to users immediately.

---

### 1.4 Undo Send

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Undo send (text mode) | **High** — Currently only voice has 5s undo | Low | **P0** | Parity | DraftReviewScreen already has approval flow. Add a "Sent! Undo?" toast with 5-10 second timer. On undo: queue a cancel-send to NATS. Sync service adds cancel logic. ~3 days. |
| Configurable undo window (5s / 10s / 30s) | Low — User preference | Low | **P2** | Parity | Add to notification preferences. ~1 day. |

**Assessment:** Only voice mode has an undo window. Text-based draft approval has **no undo mechanism** — the draft is queued and sent. This is a P0 usability issue.

---

### 1.5 Scheduled Send

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| "Send later" — schedule draft delivery | **High** — Users want to time sends (morning, Monday) | Medium | **P1** | Parity (Superhuman, Spark, Notion Mail, Gmail all have this) | Add `scheduled_at` to draft model. Sync service: instead of immediate NATS publish on approve, store in `scheduled_sends` table. Cron worker (every 5 min) polls due sends → NATS publish. ~1 week backend. |
| "Send at optimal time" — AI-suggested timing | Medium — Differentiator for DS | Low | **P2** | **Differentiator** | Use calendar context + recipient timezone. Simple heuristic: send at 9am recipient time on next business day. ~3 days backend. |

---

### 1.6 Email Templates / Snippets

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Saved reply templates | **High** — Repetitive emails ("Thanks, I'll review", "Meeting confirmed") | Low | **P1** | Parity (Notion Mail snippets, Superhuman snippets) | PostgreSQL `templates` table: name, subject, body, user_id. Client: template picker in DecisionInputScreen. ~1 week. |
| AI-generated template suggestions | Medium — Learn from user's most common replies | Medium | **P2** | **Differentiator** | Analyze sent drafts via voice_examples Qdrant collection. Cluster common patterns → suggest as templates. ~2 weeks. |
| Dynamic snippets (variables) | Medium — Personalized templates | Low | **P2** | Parity | Jinja2 templates in snippet body: `{{first_name}}`, `{{company}}`. Populate from Neo4j contact data. ~3 days. |

---

### 1.7 Multi-Account Support UI

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Multiple email accounts (Gmail + Outlook + custom) | **High** — Power users have 2-5 accounts | High | **P1** | Parity (Spark, Superhuman, Hey support multi) | Backend: OAuth already supports Google + Microsoft. Need: account switcher UI, per-account card queues, unified vs. per-account batch gate. PostgreSQL `email_accounts` already supports multiple. ~3 weeks. |
| Unified inbox across accounts | High — See all decisions in one queue | High | **P1** | Parity | Redis queue needs `account_id` prefix. Batch API aggregates across accounts. ~1 week backend. |
| Per-account rules and preferences | Medium — Different auto-handle per account | Medium | **P2** | Parity | `auto_handle_rules` already has `user_id`; add `account_id` column. ~3 days. |

**Assessment:** The backend (OAuth, ingestion, classification) already supports multiple accounts. The **client UI does not**. This is a significant gap for professionals who manage personal + work + side-project email.

---

### 1.8 Dark Mode

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Full dark mode implementation | Medium — User preference, battery savings on OLED | Low | **P1** | Parity | uiStore already has `themeMode: 'light' | 'dark' | 'system'`. Colors.ts has palette.ink scale used in ChatScreen. Need to extend dark palette to CardStackScreen and all components. ~1 week client. |
| System theme auto-detection | Low — Follows OS preference | Low | **P1** | Parity | Already partially implemented via `useColorScheme`. ~1 day. |

**Assessment:** Partially implemented — ChatScreen uses palette.ink (dark-friendly), but CardStackScreen uses custom warm colors with no dark variant. This is a quick win.

---

### 1.9 Keyboard Shortcuts

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Keyboard shortcuts for all actions | **High** — Power user essential | Low | **P1** | Parity (Superhuman is built on this) | React Native: react-native-keyevent or Keyboard module. Shortcuts: j/k (next/prev), d (decide), s (skip), c (consult), a (approve), e (edit), / (search), ? (help). ~1 week client. |
| Shortcut help overlay | Low — Discoverability | Low | **P2** | Parity | Modal showing all shortcuts. ~2 days. |

**Assessment:** Superhuman's entire UX is keyboard-driven. Decision Stack's card-based model is actually **ideal for keyboard shortcuts** — each action is discrete. This is low effort, high impact for power users.

---

### 1.10 Snooze / Remind Later

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Snooze a decision card (remind in 1h, tomorrow, custom) | **High** — Core email triage pattern | Medium | **P1** | Parity (all email clients have this) | Add `snooze_until` to card state. Card hidden from queue until time. Cron job reactivates. Sync protocol supports state changes. ~1 week backend + client. |
| Smart snooze ("remind me when they reply") | Medium — Context-aware | Medium | **P2** | **Differentiator** | Monitor thread for new emails via ingestion webhook. Auto-unsnooze on reply. ~2 weeks. |

---

### 1.11 Email Thread View (Raw)

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Full thread view (chronological emails) | **High** — Sometimes user needs full context | Medium | **P1** | Parity | SourceViewerScreen shows citations only. Need: fetch full thread from API (S3 raw email → parse). Show chronological email list with expandable bodies. ~2 weeks. |
| Thread action: reply, reply-all, forward | High — Standard email actions | Medium | **P1** | Parity | Drafting service already supports reply threading (RFC-2822 Message-ID matching). Add UI actions in thread view. ~1 week. |

**Assessment:** Decision Stack's philosophy is "no inbox view" — but users occasionally need to see the full thread for context. The current SourceViewer only shows cited chunks, which can be fragmented. A "View Full Thread" option would solve this without compromising the core philosophy.

---

### 1.12 Drafts Folder / Saved Drafts

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Saved drafts list | Medium — Draft started but not sent | Low | **P2** | Parity | Local SQLite already stores drafts. Add a Drafts screen listing unsent drafts. ~3 days client. |
| Draft auto-save | Medium — Prevent loss of work | Low | **P2** | Parity | Auto-save to local DB every 5 seconds while editing. ~2 days. |

---

## Part 2: What Would Differentiate Decision Stack?

These features would create moats that competitors cannot easily replicate. They leverage DS's existing architecture (Neo4j graph, Qdrant vectors, voice calibration, decision cards) in unique ways.

### 2.1 Decision Trends — Analytics Dashboard

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Personal decision analytics | **High** — "You clear 85% of decisions before noon. Tuesdays are your heaviest day." | Medium | **P1** | **Differentiator** | PostgreSQL `decision_logs` + `decision_cards` tables. Aggregate: decisions/day, avg time per decision, approval rate, top decision types, peak hours. Simple chart library (react-native-chart-kit). ~2 weeks. |
| Decision velocity trend | Medium — Track improvement over time | Low | **P2** | **Differentiator** | Weekly rolling average of decisions cleared. Show trend line. ~3 days. |
| Decision type breakdown | Medium — "40% scheduling, 30% approvals, 20% questions" | Low | **P2** | **Differentiator** | Classify decisions from card content (simple keyword matching or LLM). Pie chart. ~3 days. |

**Why this matters:** No email client gives you analytics on your *decision-making patterns*. This turns Decision Stack from a tool into a *personal productivity coach*.

---

### 2.2 Relationship Health — AI Relationship Scoring

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Relationship health alerts | **High** — "You haven't replied to Sarah Chen in 14 days. She usually hears back in 2." | Medium | **P1** | **Differentiator** | Neo4j already has all interaction data. Compute: avg_response_time, last_reply_date, reply_rate, thread_count. Alert when deviation > 2x from personal baseline. ~2 weeks. |
| Relationship priority list | Medium — Ranked list of most important contacts | Low | **P2** | **Differentiator** | Sort contacts by: interaction frequency + commercial signals (lifetime_value from Neo4j). ~1 week. |
| "Nurture this relationship" suggestions | Medium — Proactive outreach prompts | Medium | **P2** | **Differentiator** | ML model or heuristics: "It's been 10 days since your last touch with [VIP contact]. Want to send a quick check-in?" ~2 weeks. |

**Why this matters:** The Neo4j relationship graph is a unique asset. No competitor has this level of relationship intelligence. Surfacing it creates genuine stickiness.

---

### 2.3 Deal Pipeline — Negotiation Tracking

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Deal pipeline view | **High** — Track negotiations through email | Medium | **P2** | **Differentiator** | Extend decision cards with "deal" metadata: value, stage (inquiry → negotiation → agreement → closed), counterparty. Kanban-style board. Neo4j for deal relationships. ~3 weeks. |
| Deal stage transitions | Medium — "This thread moved from inquiry to negotiation" | Medium | **P2** | **Differentiator** | LLM prompt to classify email intent → stage. Track transitions. ~1 week. |
| Deal value extraction | Medium — Auto-extract dollar amounts from threads | Low | **P2** | **Differentiator** | Regex/NER on chunk text. Store in card metadata. ~3 days. |

**Why this matters:** Sales professionals and founders negotiate via email constantly. A built-in deal pipeline (like Streak + Superhuman combined) would be a killer feature.

---

### 2.4 Team Decisions — Shared Decision Cards

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Shared decision cards (team approval) | **High** — "This $50K contract needs manager approval" | High | **P2** | **Differentiator** | Multi-user card queue. Approval chains: user → manager → final. Role-based permissions. PostgreSQL schema changes. ~4 weeks. |
| Team decision audit log | Medium — Who approved what and when | Low | **P2** | **Differentiator** | Already tracked in `decision_logs`. Add team filter. ~3 days. |
| @mentions in decisions | Medium — Escalate to teammate | Medium | **P2** | **Differentiator** | Mention parser → notification to teammate. Add to decision flow. ~1 week. |

---

### 2.5 Auto-Delegate — Smart Team Routing

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Auto-delegate rules | **High** — "Route all accounting emails to Jane" | Medium | **P2** | **Differentiator** | Extend auto-handle rules with `delegate_to_user_id`. Classification Core routes to team member's queue. ~2 weeks. |
| Round-robin assignment | Medium — Distribute incoming evenly | Low | **P2** | **Differentiator** | Redis counter per team. ~3 days. |
| Expertise-based routing | Medium — Route to best person based on history | Medium | **P2** | **Differentiator** | Neo4j: who responds fastest to this sender/topic? Route accordingly. ~2 weeks. |

---

### 2.6 Morning Briefing — Daily AI Digest

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Morning briefing notification | **High** — "Good morning. You have 8 decisions waiting, 2 urgent. Your calendar has a conflict at 2pm." | Medium | **P1** | **Differentiator** | LLM prompt with: card queue summary, calendar events, relationship alerts, overnight activity. Generate brief → TTS → push notification. Cron at user-preferred time. ~2 weeks. |
| Customizable briefing preferences | Low — Choose what goes in brief | Low | **P2** | **Differentiator** | Notification preferences UI: toggle cards/calendar/relationships/deals. ~3 days. |

**Why this matters:** Gmail Gemini and Shortwave both have daily briefings. Decision Stack's version would be *genuinely better* because it has structured decision data + calendar context + relationship graph.

---

## Part 3: Onboarding Experience Gaps

Current onboarding: OAuth → first card appears. This is too abrupt. Users need a guided first experience.

### 3.1 Historical Email Processing (Backfill)

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Process last 30 days of email on signup | **High** — Immediate value demonstration | High | **P0** | Parity | Ingestion Mesh polling worker needs to backfill on new account connection. Rate-limited historical fetch. Classification + intelligence pipeline processes all. ~3 weeks backend. |
| Backfill progress indicator | Medium — "Processing 143 of 500 emails..." | Low | **P1** | Parity | WebSocket or polling endpoint showing backfill status. ~3 days. |
| Pause/resume backfill | Low — User control | Low | **P2** | Parity | Pause button → stop polling. Resume → continue. ~2 days. |

**Assessment:** Without backfill, a new user sees an empty batch gate ("All caught up!") — which is the *worst* possible first impression. They just connected their email and have no decisions. This is a **P0 onboarding blocker**.

---

### 3.2 First-Card Tutorial

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Interactive first-card walkthrough | **High** — Teaches the decision flow | Low | **P0** | Parity | On first card display: coach marks for each element ("This is what they want", "Tap here to decide", "View source citations here"). react-native-copilot or custom overlay. ~1 week client. |
| Tutorial decision (safe practice) | High — Practice with a fake card | Low | **P1** | **Differentiator** | Generate a demo card (not from real email) that walks through approve/edit/consult/skip. Mark as "practice mode". ~3 days. |

---

### 3.3 Voice Calibration Walkthrough

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Voice calibration onboarding | **High** — First-time voice setup | Medium | **P1** | **Differentiator** | Guided flow: (1) Test recording, (2) Playback quality check, (3) Voice sample collection ("Read this paragraph"), (4) Store in Qdrant voice_examples. ~2 weeks. |
| Voice quality feedback | Medium — "Speak closer to the mic" | Low | **P2** | **Differentiator** | Audio level analysis during calibration. Visual feedback. ~3 days. |

---

### 3.4 Progressive Disclosure

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Feature gating by usage milestone | **High** — Start simple, add complexity | Low | **P1** | **Differentiator** | Milestones: 1st decision → show chat, 5th decision → show rules, 10th → show voice, 20th → show analytics. Unlock via client-side flag. ~1 week. |
| Contextual tips | Medium — "Did you know you can swipe up to skip?" | Low | **P2** | Parity | Tooltip system triggered by usage patterns. ~3 days. |

---

## Part 4: Enterprise Features

Decision Stack is currently a single-user product. Enterprise features unlock team and organization-wide adoption.

### 4.1 Admin Dashboard

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Organization admin dashboard | **High** — Team overview, user management | High | **P1** | Parity | Web-based admin UI. Features: user list, invite users, deactivate, view team decision stats, manage billing. React web app. ~4 weeks. |
| Team analytics (aggregate) | Medium — Team-wide decision metrics | Medium | **P2** | **Differentiator** | Aggregate across users in org: total decisions, avg clearance time, top senders, busiest days. ~2 weeks. |
| Role-based access control | Medium — Admin/member/viewer roles | Medium | **P2** | Parity | RBAC middleware. Roles: admin (full), member (decide), viewer (read-only). ~2 weeks. |

---

### 4.2 Compliance & Audit Logs

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Full audit log of all decisions | **High** — Compliance requirement for enterprises | Medium | **P1** | Parity | PostgreSQL `decision_logs` already exists. Need: export (CSV/JSON), filtering (date range, user, action), immutable storage (WORM S3). ~2 weeks. |
| Data retention policies | High — GDPR/CCPA compliance | Medium | **P1** | Parity | Configurable retention: auto-delete raw emails after N days, anonymize decision logs. Cron job. ~2 weeks. |
| SOC 2 / HIPAA compliance mode | High — Enterprise requirement | High | **P2** | Parity | Encryption at rest (already done), encryption in transit (already done), access logging, BAA for HIPAA. ~4 weeks (mostly documentation/process). |

---

### 4.3 SSO (SAML/OIDC)

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| SAML 2.0 SSO | **High** — Enterprise identity provider integration | High | **P1** | Parity | python3-saml or similar. Support: Okta, Azure AD, Google Workspace SAML. ~3 weeks backend. |
| OIDC (OpenID Connect) | High — Modern auth standard | Medium | **P1** | Parity | Auth0-style flow. PKCE. ~2 weeks. |
| SCIM provisioning | Medium — Auto user provisioning/deprovisioning | High | **P2** | Parity | SCIM 2.0 server endpoint. ~3 weeks. |

---

### 4.4 Data Residency Controls

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Region selection (US/EU/APAC) | **High** — GDPR/data sovereignty | High | **P2** | Parity | Multi-region deployment: replicate infra stack per region. User signup selects region. Data never leaves region. ~8 weeks (Terraform + deployment). |
| Data residency dashboard | Medium — Confirm data location | Low | **P2** | Parity | Show region, encryption status, retention policy. ~3 days. |

---

### 4.5 Team Billing

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Team billing (per-seat) | **High** — Enterprise pricing model | Medium | **P1** | Parity | Stripe integration (Phase 7 already planned). Team plan: $30/user/month. Admin-managed billing. ~2 weeks. |
| Usage-based billing | Medium — Pay per decision/volume | Medium | **P2** | **Differentiator** | Meter decisions + LLM tokens. Usage dashboard. ~2 weeks. |

---

## Part 5: What Would Increase Daily Active Usage?

These features create engagement loops that bring users back daily.

### 5.1 Morning Briefing Notification

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Smart morning briefing push | **High** — First thing user sees each day | Medium | **P1** | **Differentiator** | Cron job at user-preferred time. Generate brief: decision count, urgent items, calendar conflicts, relationship alerts. TTS option for audio brief. ~2 weeks. |
| Briefing customization | Low — User selects what's included | Low | **P2** | **Differentiator** | Toggle sections in settings. ~2 days. |

---

### 5.2 End-of-Day "Clear Your Batch" Reminder

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| EOD reminder push | **High** — Drives batch clearing habit | Low | **P1** | **Differentiator** | If queue > 0 at 5pm (user timezone), send: "You have 5 decisions waiting. Clear them in ~8 min?" Deep link to BatchGate. ~3 days. |
| Optimal time suggestion | Medium — "You're usually fastest at 9am" | Low | **P2** | **Differentiator** | Use decision analytics to find user's peak performance time. Suggest clearing then. ~1 week. |

---

### 5.3 Streak Tracking

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Daily decision streak | **High** — Gamification drives habit | Low | **P1** | **Differentiator** | Track consecutive days with ≥1 decision cleared. Show streak flame icon on completion screen. Share milestone notifications. ~3 days backend + client. |
| Weekly decision summary | Medium — Recap of week | Low | **P2** | **Differentiator** | Push notification on Sunday: "This week: 34 decisions cleared, 2h saved. 5-day streak!" ~1 week. |
| Personal records | Low — "Most decisions in a day: 23" | Low | **P2** | **Differentiator** | Track and display personal bests. ~2 days. |

**Why this matters:** Streak mechanics (from Duolingo, GitHub) are proven habit-formers. A "3-day decision streak" creates emotional investment in the product.

---

### 5.4 Achievement System

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Decision achievements | Medium — "First 100 decisions!" "Zero inbox!" | Low | **P2** | **Differentiator** | Achievement definitions: milestones (10, 50, 100, 500), speed (clear 10 in under 5 min), variety (all action types), consistency (7-day streak). ~1 week. |
| Achievement sharing | Low — Social proof | Low | **P2** | **Differentiator** | Share to social media. Deep link. ~3 days. |

---

### 5.5 "You're Caught Up" Celebration

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Celebration animation on zero decisions | Medium — Positive reinforcement | Low | **P2** | **Differentiator** | Empty state already exists. Add: confetti animation, "You're all caught up!" message, time saved this session. ~3 days client. |
| Time saved counter | Medium — "You saved 12 minutes today" | Low | **P2** | **Differentiator** | Track: decisions cleared × avg time per decision (from analytics). Show on empty state. ~2 days. |

---

## Part 6: API / Platform Play

Decision Stack could become the intelligence layer for other products. This requires a public API.

### 6.1 Public API

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Public REST API (documented, versioned) | **High** — Enables all integrations | High | **P2** | **Differentiator** | OpenAPI spec. Endpoints: /cards, /decisions, /drafts, /conversations, /contacts, /calendar, /search. API key auth (separate from user JWT). Rate limiting. ~4 weeks. |
| Webhook support | High — Real-time events to external systems | Medium | **P2** | **Differentiator** | User-configurable webhooks: card.created, decision.approved, draft.sent. HMAC signature verification. ~2 weeks. |
| API documentation | Medium — Developer adoption | Low | **P2** | **Differentiator** | Swagger UI + developer docs site. ~1 week. |

---

### 6.2 Zapier / Make.com Integration

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Zapier integration | **High** — No-code automation ("When decision approved, add to CRM") | Medium | **P2** | **Differentiator** | Zapier app: triggers (card.created, decision.approved) + actions (create draft, search cards). ~2 weeks. |
| Make.com (Integromat) integration | Medium — Visual workflow automation | Medium | **P2** | **Differentiator** | Make app module. Similar to Zapier. ~2 weeks. |

---

### 6.3 Slack / Teams Bot

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Slack bot for decision notifications | **High** — "You have 3 urgent decisions" in Slack | Medium | **P2** | **Differentiator** | Slack Bolt app. Commands: /decisions (show queue), /decide [id] [action]. Webhook notifications for urgent cards. ~2 weeks. |
| Microsoft Teams bot | Medium — Enterprise integration | Medium | **P2** | **Differentiator** | Teams Bot Framework. Similar functionality. ~2 weeks. |
| Thread-level notifications | Medium — "New decision from [sender]" | Low | **P2** | **Differentiator** | Per-card Slack thread for consultation. ~1 week. |

---

### 6.4 CRM Integration

| Feature | User Impact | Effort | Priority | Competitive Parity / Differentiator | Implementation Notes |
|---------|------------|--------|----------|------------------------------------|---------------------|
| Salesforce integration | **High** — Auto-log decisions to Salesforce | Medium | **P2** | **Differentiator** | Salesforce REST API. Log decisions as Activities/Tasks. Match contacts via email. ~2 weeks. |
| HubSpot integration | Medium — CRM sync | Medium | **P2** | **Differentiator** | HubSpot API. Similar pattern. ~2 weeks. |

---

## Part 7: Competitive Feature Matrix

| Feature | Decision Stack | Superhuman | Shortwave | Spark | Hey.com | Notion Mail | Gmail Gemini | Outlook Copilot |
|---------|---------------|------------|-----------|-------|---------|-------------|--------------|-----------------|
| **Card-based clearing** | **YES** | No | No | No | No | No | No | No |
| **Voice mode (STT+TTS)** | **YES** | No | No | No | No | No | No | No |
| **AI chat with citations** | **YES** | No | Partial | No | No | No | Partial | Partial |
| **Offline-first** | **YES** | No | No | No | No | No | No | No |
| **Zero-hallucination verify** | **YES** | No | No | No | No | No | No | No |
| **Auto-handle rules** | **YES** | No | No | No | No | No | No | No |
| **48h staging** | **YES** | No | No | No | No | No | No | No |
| **Calendar integration** | **YES** | Partial | No | No | No | **YES** | Partial | **YES** |
| **2FA/tracking extraction** | **YES** | No | No | No | No | No | No | No |
| **Voice calibration** | **YES** | No | No | No | No | No | No | No |
| **Full-text search** | **NO** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** |
| **Attachment viewer** | **NO** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** |
| **Contact profile/timeline** | **NO** | **YES** | Partial | **YES** | No | No | Partial | **YES** |
| **Undo send** | Partial | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** |
| **Scheduled send** | **NO** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** |
| **Email templates** | **NO** | **YES** | **YES** | **YES** | No | **YES** | **YES** | **YES** |
| **Multi-account UI** | **NO** | **YES** | **YES** | **YES** | No | Partial | **YES** | **YES** |
| **Dark mode** | Partial | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** |
| **Keyboard shortcuts** | **NO** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** |
| **Snooze** | **NO** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** |
| **Thread view** | Partial | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** |
| **Read receipts** | **NO** | **YES** | **YES** | **YES** | No | No | Partial | **YES** |
| **Daily briefing** | **NO** | No | **YES** | No | No | No | **YES** | Partial |
| **Team/shared inbox** | **NO** | **YES** | **YES** | **YES** | **YES** | No | **YES** | **YES** |
| **API/webhooks** | **NO** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** | **YES** |

**Score:** Decision Stack has **10 unique features** that no competitor has, but is **missing 15+ table-stakes features** that all competitors provide.

---

## Part 8: Prioritized Roadmap

### P0 — Must Have (Ship Before Public Beta)

These block basic usability and competitive viability.

| # | Feature | Why P0 | Est. Effort |
|---|---------|--------|-------------|
| 1 | **Full-text email search** | Qdrant already indexed; trivial to expose. Users can't find emails without it. | 2 weeks |
| 2 | **Historical email backfill** | New users see empty batch gate = instant churn. Must process past emails on signup. | 3 weeks |
| 3 | **First-card interactive tutorial** | Users don't understand card flow without guidance. High abandonment risk. | 1 week |
| 4 | **Undo send (text mode)** | Currently no undo for text approvals. Users will accidentally send wrong drafts. | 3 days |
| 5 | **Attachment viewer** | Decisions often require reviewing attachments. Currently impossible in client. | 2 weeks |
| 6 | **Dark mode (complete)** | Partially done. Finish for all screens. | 1 week |
| 7 | **Multi-account UI** | Backend supports it; client doesn't. Power users need 2-5 accounts. | 3 weeks |

**P0 Total: ~12 weeks** (3 parallel tracks = ~4 weeks wall-clock)

---

### P1 — Should Have (Ship Within 3 Months of Launch)

These close competitive gaps and add differentiation.

| # | Feature | Why P1 | Est. Effort |
|---|---------|--------|-------------|
| 1 | **Scheduled send** | Table stakes. Users expect this. | 1 week |
| 2 | **Email templates/snippets** | Power user essential. Low effort. | 1 week |
| 3 | **Contact profile + timeline** | Unique asset (Neo4j) invisible to users. Surface it. | 2 weeks |
| 4 | **Keyboard shortcuts** | Perfect fit for card-based UX. Low effort. | 1 week |
| 5 | **Snooze decisions** | Core email triage pattern. | 1 week |
| 6 | **Full thread view** | Sometimes users need raw email context. | 2 weeks |
| 7 | **Morning briefing notification** | Differentiator. Drives daily habit. | 2 weeks |
| 8 | **Decision analytics dashboard** | Differentiator. Personal productivity insights. | 2 weeks |
| 9 | **Relationship health alerts** | Differentiator. Neo4j intelligence surfaced. | 2 weeks |
| 10 | **EOD batch reminder** | Engagement driver. Low effort. | 3 days |
| 11 | **Streak tracking** | Gamification. Proven habit formation. | 3 days |
| 12 | **Voice calibration walkthrough** | Voice is a core differentiator. Needs onboarding. | 2 weeks |
| 13 | **Progressive disclosure** | Reduce overwhelm for new users. | 1 week |
| 14 | **SSO (SAML + OIDC)** | Enterprise requirement. Phase 7 adjacent. | 3 weeks |
| 15 | **Team billing** | Revenue enablement. Phase 7 adjacent. | 2 weeks |
| 16 | **Audit logs + compliance** | Enterprise blocker without this. | 2 weeks |

**P1 Total: ~25 weeks** (4 parallel tracks = ~6 weeks wall-clock)

---

### P2 — Nice to Have (Post-Launch Iteration)

| # | Feature | Why P2 | Est. Effort |
|---|---------|--------|-------------|
| 1 | Deal pipeline board | Differentiator but niche (sales users). | 3 weeks |
| 2 | Team decision cards / shared approval | High effort, needs enterprise traction first. | 4 weeks |
| 3 | Auto-delegate / team routing | High effort, needs team features first. | 2 weeks |
| 4 | Public API + webhooks | Platform play. Needs product-market fit first. | 4 weeks |
| 5 | Zapier/Make integration | Depends on public API. | 2 weeks |
| 6 | Slack/Teams bot | Channel strategy. Depends on API. | 2 weeks |
| 7 | CRM integrations (Salesforce, HubSpot) | Depends on API + enterprise demand. | 2 weeks |
| 8 | Data residency controls | High effort. Only needed for large enterprise. | 8 weeks |
| 9 | Achievement system | Fun but not essential. | 1 week |
| 10 | OCR for attachments | Searchable attachments. Nice but not critical. | 2 weeks |
| 11 | AI-generated template suggestions | Smart feature. Can add later. | 2 weeks |
| 12 | Smart snooze (reply-triggered) | Clever but complex. | 2 weeks |
| 13 | Optimal send time | Nice polish. Can add later. | 3 days |
| 14 | Search within chat | Chat is secondary to card flow. | 3 days |

**P2 Total: ~39 weeks** (selectively picked based on feedback)

---

## Part 9: Strategic Recommendations

### 9.1 The Core Insight

Decision Stack's card-based decision model is **genuinely revolutionary**. No competitor has anything like it. But the product currently asks users to **abandon their entire email workflow** and adopt a completely new paradigm — without providing the safety net of familiar features.

**The strategy: Be the best of both worlds.**

1. **Keep the card-based clearing as the primary experience** — this is the moat.
2. **Add traditional email features as an "escape hatch"** — search, thread view, attachments — so users never need to leave DS.
3. **Surface the unique intelligence** — relationship graph, decision analytics, calendar conflicts — as premium differentiators.
4. **Drive daily engagement** — morning briefings, streaks, reminders — to build habits.

### 9.2 The Switching Cost Problem

Users won't switch if they need to keep Gmail/Outlook open "just in case." The P0 features eliminate this by covering 95% of email use cases:

| "I need to..." | Current DS | With P0 Complete |
|---------------|-----------|------------------|
| Find an old email | Open Gmail | **Search in DS** |
| Check an attachment | Open Gmail | **View in DS** |
| Send a draft later | Can't | **Schedule in DS** |
| Fix a sent mistake | Can't (text) | **Undo in DS** |
| Check work email | Open Outlook | **Switch account in DS** |
| Use at night | Eye strain | **Dark mode in DS** |

### 9.3 Pricing Strategy Implications

| Tier | Features | Price | Notes |
|------|----------|-------|-------|
| **Free** | Card clearing, chat, voice (limited), 1 account, 30-day history | $0 | Growth engine. Limited to 50 decisions/month. |
| **Pro** | Everything in Free + unlimited decisions + multi-account + templates + scheduled send + analytics + dark mode | $20/month | Individual power users. |
| **Team** | Everything in Pro + shared decisions + admin dashboard + SSO + audit logs + team analytics | $30/user/month | Enterprise. 5-seat minimum. |
| **Enterprise** | Everything in Team + data residency + SCIM + custom SLA + dedicated support | Custom | Large organizations. |

### 9.4 Risk Assessment

| Risk | Severity | Mitigation |
|------|----------|------------|
| Users reject card-based paradigm (too different) | **High** | P0 tutorial + progressive disclosure + thread view escape hatch |
| Competitors copy card-based model | Medium | Voice calibration + Neo4j graph + citation verification are hard to replicate |
| Backend stubs prevent launch | **High** | Phase 10 remediation (6 sprints) addresses all 37 P0 blockers |
| No mobile app (React Native only) | Medium | React Native supports iOS + Android. Phase 7 includes app store submission. |
| Enterprise features too late | Medium | P1 SSO + billing unlocks enterprise. Full enterprise (SCIM, residency) is P2. |

---

## Part 10: Quick Wins (Implement This Week)

These require minimal effort and have disproportionate impact:

1. **Add search endpoint** — Qdrant is already indexed. Expose ChunkStore.search() via API. (~3 days)
2. **Complete dark mode** — Extend palette.ink to CardStackScreen. (~3 days)
3. **Add undo to text approval** — Toast + cancel-send NATS message. (~2 days)
4. **Keyboard shortcuts** — React Native Keyboard module. j/k/d/s/c. (~3 days)
5. **Streak tracking** — Increment counter on decision. Show flame icon. (~2 days)
6. **EOD reminder** — Cron check at 5pm. Push if queue > 0. (~2 days)
7. **Attachment presigned URLs** — API endpoint + client download button. (~2 days)

**7 quick wins = ~2 weeks of work, massive user impact.**

---

## Appendix A: Data Sources

| Source | Date Accessed | Content |
|--------|--------------|---------|
| DS_PROGRESS.md | 2026-01-28 | Full project status, phase completion, invariant checklist |
| intelligence/docs/ARCHITECTURE.md | 2026-01-28 | Intelligence layer: compression, drafting, chat, voice, consultation |
| client/docs/ARCHITECTURE.md | 2026-01-28 | Client: screens, stores, sync protocol, security |
| classification/docs/ARCHITECTURE.md | 2026-01-28 | Classification Core: routing, rules, staging |
| ingestion/docs/ARCHITECTURE.md | 2026-01-28 | Ingestion Mesh: OAuth, parsing, threading |
| sync/docs/ARCHITECTURE.md | 2026-01-28 | Sync & State: API, WebSocket, push notifications |
| Client source code (10 screens) | 2026-01-28 | CardStack, Chat, ChatVoice, DraftReview, SourceViewer, BatchGate, etc. |
| Service READMEs (STT, TTS, OCR, Calendar) | 2026-01-28 | Microservice capabilities and APIs |
| Superhuman.com product page | 2026-01-28 | Features: team share, read receipts, shortcuts, send later |
| Spark Mail reviews & docs | 2026-01-28 | Smart Inbox, team features, AI, pricing |
| Hey.com reviews & alternatives | 2026-01-28 | Screener, bundles, philosophy |
| Notion Mail reviews & features | 2026-01-28 | Database email, snippets, AI, calendar |
| Gmail Gemini feature articles | 2026-01-28 | Summarize, draft, daily briefing, catch me up |
| Outlook Copilot feature articles | 2026-01-28 | Meeting prep, scheduling, coaching, agenda |

---

## Appendix B: Feature Scoring Methodology

| Dimension | Weight | Description |
|-----------|--------|-------------|
| User Impact | 40% | How many users benefit and how much |
| Competitive Parity | 25% | Is this table stakes or differentiator? |
| Implementation Effort | 20% | Engineering time required |
| Strategic Fit | 15% | Aligns with DS's decision-centric philosophy? |

Priority = weighted score mapped to P0/P1/P2 buckets.

---

*End of Report*
