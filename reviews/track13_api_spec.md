# Track 13: API Specification Review Report

## Summary

| Category | Count |
|---|---|
| Spec endpoints verified | 26 |
| Fully implemented + wired | 10 |
| Stubbed (inline placeholder) | 7 |
| Implemented but NOT wired | 7 |
| Path mismatches | 5 |
| Method mismatches | 2 |
| Extra endpoints (not in spec) | 14 |

---

## Detailed Endpoint Audit Table

### Auth Endpoints

| Method | Spec Path | Actual Path | Auth | Handler | Request | Response | Status |
|---|---|---|---|---|---|---|---|
| POST | `/auth/google/callback` | `GET /auth/google/callback` | None (public) | `ingestion/cmd/server/main.go` inline stub | Query params: `code`, `state` | `{"status":"callback_received"}` | **MISMATCH** - Method: spec=POST, actual=GET. Stub only. Full handler exists in `oauth/handler.go:handleAuthCallback` but NOT wired. |
| POST | `/auth/microsoft/callback` | `GET /auth/microsoft/callback` | None (public) | `ingestion/cmd/server/main.go` inline stub | Query params: `code`, `state` | `{"status":"callback_received"}` | **MISMATCH** - Method: spec=POST, actual=GET. Stub only. Full handler exists in `oauth/handler.go:handleAuthCallback` but NOT wired. |
| POST | `/auth/refresh` | `POST /api/v1/auth/refresh` | None (public) | `sync/cmd/server/main.go:handleRefreshToken` inline stub | `RefreshTokenRequest` | 501 Not Implemented | **MISMATCH** - Path: spec says `/auth/refresh`, actual is `/api/v1/auth/refresh`. Stub returns 501. Full implementation exists in `sync/internal/auth/handler.go:RefreshToken` but NOT wired. |

**OAuth handler notes:** The full OAuth handler (`oauth/handler.go`) has complete implementations with JWT validation, token encryption, DB persistence, and NATS publishing. It registers:
- `GET /auth/{provider}` - Initiate OAuth flow
- `GET /auth/{provider}/callback` - Handle OAuth callback
- `POST /auth/{provider}/refresh` - Refresh access token
- `POST /auth/{provider}/revoke` - Revoke tokens

None of these are wired into the ingestion server's main.go.

---

### Webhook Endpoints

| Method | Spec Path | Actual Path | Auth | Handler | Request | Response | Status |
|---|---|---|---|---|---|---|---|
| POST | `/webhooks/gmail` | `POST /webhooks/gmail` | JWT (Authorization header) | `ingestion/cmd/server/main.go` inline stub | `GmailPubSubRequest` | `{"status":"accepted"}` | **STUB** - Returns 202 Accepted with no processing. Full handler exists in `webhook/handler.go:HandleGmail` but NOT wired. |
| POST | `/webhooks/outlook` | `POST /webhooks/outlook` | None (validation token) | `ingestion/cmd/server/main.go` inline stub | `OutlookWebhookRequest` | `{"status":"accepted"}` | **STUB** - Returns 202 Accepted with no processing. Full handler exists in `webhook/handler.go:HandleOutlook` but NOT wired. |

**Webhook handler notes:** The full webhook handler (`webhook/handler.go`) has complete implementations with base64 decoding, JWT verification, dedup checking, and job enqueueing to NATS. Neither handler is wired into the ingestion server's router.

---

### Batch Endpoints

| Method | Spec Path | Actual Path | Auth | Handler | Request | Response | Status |
|---|---|---|---|---|---|---|---|
| GET | `/batch` | `GET /api/v1/batch` | Bearer JWT | `sync/internal/batch/handler.go:handleGetBatch` | Query: `limit` (int, optional) | `{size, estimated_clear_time_minutes, cards[]}` | **OK** - Full implementation. |
| GET | `/batch/next` | `GET /api/v1/batch/next` | Bearer JWT | `sync/internal/batch/handler.go:handleGetNextCard` | None | Single DecisionCard or 404 | **OK** - Full implementation. |
| GET | `/batch/count` | `GET /api/v1/batch/count` | Bearer JWT | `sync/internal/batch/handler.go:handleGetCount` | None | `{pending_count, urgent_count}` | **OK** - Full implementation. |

**Note:** Extra endpoint `POST /api/v1/batch/dismiss` exists (not in spec) - records batch notification dismissal for analytics.

---

### Decision Endpoints

| Method | Spec Path | Actual Path | Auth | Handler | Request | Response | Status |
|---|---|---|---|---|---|---|---|
| POST | `/cards/{id}/decide` | `POST /api/v1/decisions/decide` | Bearer JWT | `sync/cmd/server/main.go:handleDecide` inline stub | `{card_id, decision}` | `{draft_id, draft_body}` placeholder | **MISMATCH** - Path: spec says `/cards/{id}/decide`, actual is `/api/v1/decisions/decide` (different path pattern, uses body param not URL param). Full handler exists in `decision/handler.go:Decide` but NOT wired. |
| POST | `/cards/{id}/draft` | **NOT REGISTERED** | - | `decision/handler.go:RequestDraft` | `{instruction}` | `{draft_id, draft_body, subject_line}` | **MISSING** - Full implementation exists in `decision/handler.go` but NOT wired in main.go. |
| POST | `/drafts/{id}/approve` | **NOT REGISTERED** | - | `decision/handler.go:ApproveDraft` | `{approved: true}` | `{status: "approved", draft_id}` | **MISSING** - Full implementation exists in `decision/handler.go` but NOT wired in main.go. |
| POST | `/drafts/{id}/edit` | **NOT REGISTERED** | - | `decision/handler.go:EditDraft` | `{draft_body}` | `{draft_id, draft_body, subject_line}` | **MISSING** - Full implementation exists in `decision/handler.go` but NOT wired in main.go. |
| GET | `/cards/{id}/source` | **NOT REGISTERED** | - | `decision/handler.go:GetSource` | None | `{citations: [{chunk_id, verbatim_snippet, email_id, paragraph_index}]}` | **MISSING** - Full implementation exists in `decision/handler.go` but NOT wired in main.go. |

**Critical finding:** The `decision/handler.go` contains complete, production-quality implementations for ALL 5 decision endpoints, including proper auth validation, UUID parsing, request validation, error handling (404, 403, 409, 500), and response serialization. However, `decision/handler.go` is never imported or used in `sync/cmd/server/main.go`. Instead, the decision endpoints are stubbed as inline handlers at `/api/v1/decisions/decide` and `/api/v1/decisions/consult` with hardcoded placeholder responses.

---

### Consultation Endpoints

| Method | Spec Path | Actual Path | Auth | Handler | Request | Response | Status |
|---|---|---|---|---|---|---|---|
| POST | `/consult` | `POST /api/v1/decisions/consult` | Bearer JWT | `sync/cmd/server/main.go:handleConsult` inline stub | `{card_id, question}` | `{answer, citations, turns_remaining}` placeholder | **MISMATCH** - Path: spec says `/consult`, actual is `/api/v1/decisions/consult`. Stub only. Full handler exists in `decision/handler.go:Consult` but NOT wired. |
| POST | `/consult` | `POST /v1/chat/consult` | user_id in body | `intelligence/app/chat/router.py:consult` | `{card_id, user_id, question}` | `ConsultResponse` | **OK** - Full implementation in intelligence service. Path has `/v1` prefix. |

**Note:** The `/consult` endpoint exists in two places: (1) as a stub in the sync service at `/api/v1/decisions/consult`, and (2) as a full implementation in the intelligence service at `/v1/chat/consult`. The spec path `/consult` is not directly matched by either.

---

### Sync Endpoint

| Method | Spec Path | Actual Path | Auth | Handler | Request | Response | Status |
|---|---|---|---|---|---|---|---|
| POST | `/sync` | `POST /api/v1/sync` | Bearer JWT | `sync/internal/sync/handler.go:HandleSync` | `SyncRequest` {device_id, last_sync_version, local_changes[]} | `SyncResponse` {server_version, accepted_changes, rejected_changes, new_cards, updated_cards, removed_cards} | **OK** - Full implementation with 3-phase CRDT merge, request validation, and rate limiting. |

---

### Send Endpoint

| Method | Spec Path | Actual Path | Auth | Handler | Request | Response | Status |
|---|---|---|---|---|---|---|---|
| POST | `/send` | **NOT REGISTERED** | - | `decision/handler.go:Send` | `{draft_id}` | `{sent_at, message_id}` | **MISSING** - Full implementation exists in `decision/handler.go` but NOT wired in main.go. The Send handler validates the draft is approved, calls `ExecuteSend` via the mesh client, and returns send confirmation. |

---

### Chat Endpoints

| Method | Spec Path | Actual Path | Auth | Handler | Request | Response | Status |
|---|---|---|---|---|---|---|---|
| GET | `/chat/conversations` | `GET /v1/chat/conversations?user_id=` | user_id query param | `intelligence/app/chat/router.py:list_conversations` | Query: `user_id` | `{conversations: [...]}` | **OK** - Full implementation. |
| POST | `/chat/conversations` | `POST /v1/chat/conversations` | user_id in body | `intelligence/app/chat/router.py:create_conversation` | `{user_id, title?}` | `{conversation_id, title, created_at}` | **OK** - Full implementation. |
| POST | `/chat/conversations/{id}/messages` | `POST /v1/chat/conversations/{id}/messages` | user_id in body | `intelligence/app/chat/router.py:send_message` | `{user_id, message, linked_card_id?}` | `ChatResponse` | **OK** - Full implementation. |
| POST | `/chat/conversations/{id}/voice` | `POST /v1/chat/conversations/{id}/voice` | user_id (form) | `intelligence/app/chat/router.py:send_voice_message` | multipart/form: `audio` file + `user_id` + `linked_card_id?` + `voice_id?` | `ChatResponse` | **OK** - Full STT->chat->TTS pipeline. |

**Note:** The voice endpoint uses FastAPI `UploadFile` for audio upload, then pipes through Deepgram STT (Nova-2) -> ChatService -> ElevenLabs TTS (Turbo v2.5) -> S3 presigned URL.

---

### WebSocket Endpoint

| Method | Spec Path | Actual Path | Auth | Handler | Request | Response | Status |
|---|---|---|---|---|---|---|---|
| WSS | `/sessions/{user_id}` | `GET /ws?token=` | JWT via query param | `sync/internal/websocket/handler.go:ServeHTTP` | WS upgrade with `?token=` JWT | Bidirectional WS: ServerEvents (pong, error, progress, draft) + ClientEvents (ping, draft_request, draft_edit, send) | **MISMATCH** - Path: spec says `/sessions/{user_id}`, actual is `/ws`. Token is passed via query parameter `?token=` instead of being embedded in the path. The user_id is extracted from the JWT claims, not from the URL path. |

**WebSocket protocol details:**
- Authentication: JWT via `?token=` query parameter (validated by `tokenMgr.ValidateAccessToken()`)
- Upgrader: Gorilla WebSocket with configurable read/write buffer sizes
- Origin checking: enforced in production, permissive in development
- Per-client sessions map: `card_id -> *SendingSession`
- Client events handled: `ping`, `draft_request`, `draft_edit`, `send`
- Server events sent: `pong`, `error`, `progress`, `draft`, `send_confirmation`
- Ping/pong keepalive with configurable intervals
- Read/write pumps run in separate goroutines per connection

---

### Health Endpoint

| Method | Spec Path | Actual Path | Auth | Handler | Request | Response | Status |
|---|---|---|---|---|---|---|---|
| GET | `/health` | `GET /health` | None (public) | `sync/internal/health/handler.go:HealthCheck` | None | `{status, timestamp, uptime, service}` | **OK** - Full implementation. Also includes `GET /ready` readiness check (not in spec) with DB + Redis dependency checks. |
| GET | `/health` | `GET /health` | None (public) | `ingestion/internal/health/handler.go:HandleHealth` | None | `{status, checks, time}` | **OK** - Full implementation with NATS health check in ingestion service. |

---

## Critical Issues Found

### 1. Decision handler not wired (CRITICAL)
The `sync/internal/decision/handler.go` contains 6 fully implemented endpoints (`Decide`, `RequestDraft`, `ApproveDraft`, `EditDraft`, `GetSource`, `Consult`, `Send`) but is **never imported or mounted** in `sync/cmd/server/main.go`. Instead, `/decisions/decide` and `/decisions/consult` are stubbed as inline handlers that return hardcoded placeholder responses.

### 2. OAuth handlers not wired (CRITICAL)
The `ingestion/internal/oauth/handler.go` contains full OAuth implementations for Google and Microsoft (auth URL generation, callback handling, token encryption, DB persistence, re-auth card publishing) but is **never imported or mounted** in `ingestion/cmd/server/main.go`. OAuth routes are stubbed as simple JSON responders.

### 3. Webhook handlers not wired (CRITICAL)
The `ingestion/internal/webhook/handler.go` contains full webhook implementations (Gmail Pub/Sub decoding, Outlook validation token handling, JWT verification, dedup checking, job enqueueing) but is **never imported or mounted** in `ingestion/cmd/server/main.go`. Webhook routes are stubbed as no-op acceptors.

### 4. Auth refresh path mismatch (HIGH)
Spec says `POST /auth/refresh`. The full implementation is at `POST /auth/refresh` (in `auth/handler.go`). But what's wired is a stub at `POST /api/v1/auth/refresh` with a different path prefix.

### 5. WebSocket path mismatch (HIGH)
Spec says `WSS /sessions/{user_id}`. Actual is `GET /ws` with token via query param. The URL path parameter for user_id is not used.

### 6. OAuth callback method mismatch (MEDIUM)
Spec says `POST /auth/google/callback` and `POST /auth/microsoft/callback`. Actual stubs use `GET`. The full handler also uses `GET` (standard OAuth 2.0 flow uses redirect via GET).

### 7. Decision endpoint path mismatch (MEDIUM)
Spec says `/cards/{id}/decide`, `/cards/{id}/draft`, `/drafts/{id}/approve`, `/drafts/{id}/edit`, `/cards/{id}/source`. The actual wired stubs use `/api/v1/decisions/decide` and `/api/v1/decisions/consult` with different URL patterns (body params instead of URL params).

---

## Extra Endpoints (Not in Spec)

| Method | Path | Service | Description |
|---|---|---|---|
| POST | `/api/v1/batch/dismiss` | sync | Batch notification dismissal |
| POST | `/auth/device` | sync | Device registration (returns JWT pair) |
| POST | `/auth/revoke` | sync | Revoke device session |
| GET | `/auth/sessions` | sync | List active device sessions |
| GET | `/ready` | sync | Readiness check (DB + Redis) |
| POST | `/auth/{provider}/revoke` | ingestion | Revoke OAuth tokens |
| GET | `/auth/{provider}` | ingestion | Initiate OAuth flow |
| GET | `/v1/chat/conversations/{id}/messages` | intelligence | Get messages in a conversation |
| GET | `/v1/chat/consult/{card_id}/turns` | intelligence | Get consultation turns remaining |
| POST | `/api/v1/devices/register` | sync | Device registration (alt path) |
| DELETE | `/api/v1/devices/{deviceID}` | sync | Device unregistration |
| GET | `/api/v1/devices` | sync | List devices |
| GET/POST | `/api/v1/notifications/*` | sync | Notification management |
| GET | `/api/v1/queue/*` | sync | Queue count/version |

---

## Files Reviewed

| File | Lines | Status |
|---|---|---|
| `sync/cmd/server/main.go` | 513 | Reviewed - route wiring, inline stubs |
| `sync/internal/batch/handler.go` | 195 | Reviewed - full implementation |
| `sync/internal/decision/handler.go` | 543 | Reviewed - full implementation, NOT wired |
| `sync/internal/sync/handler.go` | 209 | Reviewed - full implementation |
| `sync/internal/auth/handler.go` | 381 | Reviewed - full implementation, partially wired |
| `sync/internal/auth/middleware.go` | 133 | Reviewed - JWT middleware, route helpers |
| `sync/internal/health/handler.go` | 100 | Reviewed - full implementation |
| `sync/internal/websocket/handler.go` | 336 | Reviewed - full implementation |
| `ingestion/cmd/server/main.go` | 174 | Reviewed - route wiring, inline stubs |
| `ingestion/internal/webhook/handler.go` | 426 | Reviewed - full implementation, NOT wired |
| `ingestion/internal/oauth/handler.go` | 625 | Reviewed - full implementation, NOT wired |
| `intelligence/intelligence/app/chat/router.py` | 341 | Reviewed - full implementation |
| `intelligence/intelligence/app/chat/voice_handler.py` | 238 | Reviewed - full STT/TTS pipeline |
| `intelligence/intelligence/app/router.py` | 28 | Reviewed - router aggregation |
| `intelligence/intelligence/main.py` | 185 | Reviewed - app factory, lifespan |

---

*Report generated by Track 13 API Specification Review.*
