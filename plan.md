# Decision Stack — Critical Gap Analysis + Replan

> Date: 2026-06-06
> Status: 11/11 invariants PASS, but CRITICAL FUNCTIONAL GAPS found in calendar + sending pipeline

---

## What I Found: Calendar + Sending Audit

### Calendar Service — 80% Complete

**Calendar microservice** (`services/calendar/`) has FULL read/write API:

| Endpoint | Method | Capability |
|----------|--------|------------|
| `GET /calendar/events` | Read | List events (cached or live from Google/Outlook) |
| `POST /calendar/events` | **Write** | Create events on provider |
| `GET /calendar/freebusy` | Read | Check free/busy slots |
| `POST /calendar/conflicts` | Read | Detect scheduling conflicts (hard/soft) |
| `GET /calendar/sync` | Read | Trigger on-demand sync |
| `POST /calendar/sync/full` | Read | Full sync all accounts |

**Calendar context** (`intelligence/app/calendar_context/`) has:
- `get_events_next_7_days()` — fetch events
- `check_conflicts()` — hard/soft conflict detection with 15-min buffer
- `get_free_slots()` — find free slots within working hours
- `detect_scheduling_intent()` — NER-based intent detection
- `get_calendar_context_for_card()` — builds calendar context for LLM prompts

**BUT:** The calendar_context router is NOT registered in `intelligence/intelligence/app/router.py` — it's wrapped in a `try/except` with fallback to `None`. The chat service context retriever may not be calling it.

### Drafting + Sending Pipeline — 70% Complete (CRITICAL GAP)

**Drafting** (`intelligence/intelligence/app/drafting/router.py`) has:
- `POST /drafts/create` — generate voice-calibrated draft from decision
- `POST /drafts/{id}/approve` — approve and **publish `draft.send` to NATS**
- 30-day scheduling window
- Threading headers (In-Reply-To, References)

**Sync approval** (`sync/internal/decision/approval.go`) has:
- `Approve()` — atomic approval + publishes to NATS subject `email.send`
- `OnSendComplete()` — callback after send finishes
- `ExecuteSend()` — direct gRPC call to ingestion mesh for urgent sends

### CRITICAL GAP: No Email Send Consumer

The approval flow publishes to NATS subject `email.send`. But **nobody is listening**:

```
Publishing: sync → NATS:email.send
Consuming:  ??? (NO CONSUMER FOUND)
```

**Searched:** `ingestion/internal/nats/consumer.go` — only listens to `email.ingested`
**Searched:** All Go files for `"email.send"` handler — NONE FOUND
**Searched:** gRPC methods in ingestion — `SendEmail` interface defined but no implementation found

**The system can draft emails but CANNOT send them.** This is the #1 functional gap.

### Chat Command Handling — 60% Complete

**Chat service** (`chat/service.py`) has:
- Query complexity classifier (simple→Haiku/streaming, complex→Sonnet/full)
- Action detection: `[ACTION: action_name]` pattern
- Supported actions: `clear_batch`, `view_card`, `schedule`, `send_draft`, `add_contact`, `create_reminder`

**BUT:** No explicit calendar command handlers in chat routes. The LLM must infer calendar intent and suggest actions. No direct "create event" or "check my calendar" commands.

**Chat routes** (`chat/router.py`) has:
- Text messaging with complexity routing
- Voice messaging (STT → process → TTS)
- Per-card consultation
- NO explicit `/calendar` or `/send` command endpoints

---

## New Priority Ranking

| Priority | Gap | Impact | Effort |
|----------|-----|--------|--------|
| **P0** | **No email.send consumer** — system can't send emails | System is unusable for replies | Medium (new handler + Gmail/Outlook send) |
| **P1** | Calendar context not wired into chat retriever | Chat can't answer "what's my schedule" | Small (add calendar_context call to retriever) |
| **P1** | No explicit calendar commands in chat router | Can't "create event" or "check free slots" via chat | Medium (new endpoints) |
| **P2** | Chat action detection relies on LLM output parsing | Brittle — LLM may not format correctly | Small (add structured tool calling) |
| **P2** | No `email.send` stream consumer in ingestion | Dead letter — approved drafts never send | Medium (mirror of P0 fix in ingestion) |

---

## Revised Plan

### Turn 4: Close Critical Sending Gap (2 parallel agents)

| Agent | Task | Files | Deliverable |
|-------|------|-------|-------------|
| **Backend_Agent** | Build `email.send` NATS consumer in ingestion mesh | `ingestion/internal/nats/send_consumer.go` + `ingestion/internal/send/` | Consumer that listens to `email.send`, calls Gmail/Outlook API to send |
| **Backend_Agent** | Add gRPC `SendEmail` method to ingestion server | `ingestion/internal/grpc/` or `ingestion/internal/api/` | gRPC handler for direct sync→ingestion send calls |
| **ML_Agent** | Wire calendar_context into chat retriever | `intelligence/app/chat/retriever.py` | Calendar context fetched for scheduling-intent queries |
| **ML_Agent** | Add calendar command endpoints to chat router | `intelligence/intelligence/app/chat/router.py` | `/chat/calendar/events`, `/chat/calendar/freebusy`, `/chat/calendar/create` |

### Turn 5: Integration + Final Verification (all agents)

| Agent | Task | Deliverable |
|-------|------|-------------|
| **Integration_Agent** | Verify send pipeline end-to-end: draft → approve → NATS → send → confirm | Pipeline trace |
| **Integration_Agent** | Verify calendar commands: chat → calendar context → events | Command trace |
| **Test_Agent** | Add send pipeline test to integration scripts | Updated `full_loop_test.sh` |
| **All agents** | Final invariant check | 11/11 confirmation |

---

## The Bottom Line

The system has **excellent calendar infrastructure** (full CRUD API, conflict detection, free slot finding) and **excellent drafting infrastructure** (voice-calibrated, threading-aware). But the **sending pipeline has a dead end** — approved drafts are published to NATS but never consumed.

This is fixable. The calendar service proves the pattern works (OAuth token refresh, provider API calls, circuit breakers). The send consumer follows the same pattern.

**Without the send fix, Decision Stack can read emails and draft replies but can never send them. This is the highest-priority fix remaining.**
