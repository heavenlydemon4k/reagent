# Decision Stack — Client + Sync & State Summary (Phases 4, 8)

---

## Component: Client Architecture

- **Purpose**: React Native + Expo SDK 50 mobile client for one-card-at-a-time decision clearing. Offline-first with encrypted local storage.
- **Architecture**: 
  - Single-file decision cards presented sequentially (no scrolling feeds)
  - Offline-first: every operation completes locally first; sync is asynchronous
  - SQLCipher-encrypted SQLite via `op-sqlite` for local persistence
  - Zustand for all global state (explicitly avoids Context API)
  - Axios HTTP client with JWT auto-refresh interceptors
  - Background sync via `expo-background-fetch`
- **Key Files**:
  - `/mnt/agents/output/client/docs/ARCHITECTURE.md` — master architecture document
  - `/mnt/agents/output/client/App.tsx` — root (GestureHandler + SafeAreaProvider)
  - `/mnt/agents/output/client/src/navigation/AppNavigator.tsx` — stack navigator (BatchGate -> CardStack -> DraftReview)
  - `/mnt/agents/output/client/src/types/cards.ts` — shared wire contracts
- **Design Decisions**:
  - **One-card-at-a-time**: Single DecisionCard in viewport; no inbox/list view
  - **No Context API**: All global state through Zustand stores
  - **Implicit types**: Store types inferred from initial state, not declared separately
  - **Zero trust local storage**: SQLCipher mandatory; raw email bodies never touch disk
  - **Forward-only navigation**: No back button to previous cards in the stack
- **State Management**: 
  - 4 Zustand stores: `cardStore` (queue, index, batch), `syncStore` (health, version, pending), `authStore` (JWT, device ID), `uiStore` (theme, voice mode, nav, toasts)
  - Custom hooks compose business logic: `useCards`, `useSync`, `useVoice`, `useAuth`
- **Sync Protocol**: 5-step protocol: (1) local change -> DB write + sync_queue insert, (2) background sync every 15 min, (3) POST /sync, (4) server response upserts cards, (5) server_version wins on conflicts
- **TODOs/Issues**: None explicitly noted in architecture doc

---

## Component: Type Definitions (`cards.ts`)

- **Purpose**: Central TypeScript type definitions — the wire contract between Client and Sync API. Shared by ALL client tracks.
- **Architecture**: Single file with 7 type categories, ~270 lines
- **Key Files**: `/mnt/agents/output/client/src/types/cards.ts`
- **Design Decisions**:
  - `DecisionCard` is the core UI unit with 20+ fields including `urgency_score` (0.0-1.0), `they_want` (max 280 chars), `need_from_user` (irreducible gap)
  - `CardState` is a discriminated union: `"pending" | "consulting" | "drafting" | "approved" | "sent" | "archived" | "expired"`
  - Sync types (`SyncRequest`, `SyncResponse`, `LocalChange`, `RejectedChange`) mirror server-side Go structs exactly
  - `ChatMessage` supports optional `audio_url` (TTS) and `transcription` (STT)
  - `VoiceModeState` is a phase machine: `intro -> listening -> transcribing -> drafting -> confirming -> sending -> undo_window`
- **Important Types**:
  - `DecisionCard` — UUID id, thread_id, card_state, FromField, they_want, CardContext, need_from_user, ChunkCitation[], urgency_score
  - `Draft` — id, card_id, draft_body, subject_line, tone_profile, user_approved
  - `SyncRequest` — device_id, last_sync_version, LocalChange[]
  - `SyncResponse` — server_version, accepted_changes, rejected_changes, new/updated/removed cards
  - `ChatMessage` — id, conversation_id, role (user|assistant), content, citations, audio_url
  - `VoiceModeState` — phase, current_card, transcription, draft_preview, undo_seconds_remaining
  - `SyncQueueItem` — id, operation (update_card|approve_draft|create_draft), payload, retry_count
- **TODOs/Issues**: Header comment warns "DO NOT MODIFY without coordination" — types are tightly coupled to server Go structs

---

## Component: CardStackScreen

- **Purpose**: Main decision-clearing UI — shows exactly ONE card at a time with swipe-up-to-skip, progress indicator, streak display, and keyboard shortcuts
- **Architecture**: React Native + Reanimated 2 + Gesture Handler. Uses shared values for smooth 60fps animations.
- **Key Files**: `/mnt/agents/output/client/src/screens/CardStackScreen.tsx`
- **Design Decisions**:
  - **NEVER shows a list** — single card in viewport at all times
  - Swipe-up threshold of -80px triggers dismiss with spring animation
  - Progress bar at bottom: "Card 3 of 7" with percentage fill
  - Streak indicator (flame icon + day count) in top-right corner
  - Keyboard shortcuts: `?` for help, dedicated keys for decide/skip/consult
  - Card exit animation: slide up + fade out + scale down (300ms)
  - Snap-back animation when swipe doesn't meet threshold
- **State Management**: Receives cards and callbacks via props; uses local `useState` for currentIndex; shared values for animation state
- **Key Constants**: `SWIPE_UP_THRESHOLD = -80`, `SPRING_CONFIG = { damping: 20, stiffness: 200 }`
- **TODOs/Issues**: None noted

---

## Component: BatchGateScreen

- **Purpose**: Calm, centered gate screen before entering the CardStack. Shows decision count + estimated time + urgency hints.
- **Architecture**: Pure presentation component — no business logic, all data via props
- **Key Files**: `/mnt/agents/output/client/src/screens/BatchGateScreen.tsx`
- **Design Decisions**:
  - Large count text: "N decisions" as the primary visual element
  - Estimated time below count: "Estimated M min"
  - Contextual prompt: "Ready to clear your stack?"
  - Urgency hint shown only when any card has `urgency_score > 0.8` — red dot + count
  - Streak indicator (same pattern as CardStackScreen)
  - "Start Clearing" primary CTA (pill-shaped, shadow) + "Later" dismiss text
  - Theme-aware: adapts to light/dark mode via `useTheme`
- **State Management**: Memoized `hasUrgent` and `urgentCount` computed from batch.cards
- **TODOs/Issues**: None noted

---

## Component: DraftReviewScreen

- **Purpose**: AI-generated draft review before sending. Supports Approve (with confirmation + undo), Edit (full modal), Shorten, Formalize, Back.
- **Architecture**: Composes three hooks (`useDrafting`, `useApproval`, `useUndoSend`) with animated approval banner and undo toast
- **Key Files**: `/mnt/agents/output/client/src/screens/DraftReviewScreen.tsx`
- **Design Decisions**:
  - **user_approved MUST be TRUE before any send** (invariant)
  - Confirmation dialog before approve: "Approve and Send?" with 5-second undo window
  - Animated success banner slides down then fades after 2.5s
  - Undo toast appears at top with countdown timer + "Undo" button
  - Modification notice shown when draft differs from original ("You've edited this draft")
  - ScrollView for draft body + fixed action footer
  - EditModal for full draft body modification
  - Loading states for re-drafting ("Re-drafting...") and error states with retry
- **State Management**: 
  - `useDrafting` manages draft phase machine (loading -> drafting -> error)
  - `useApproval` handles confirmation dialog state + approved drafts tracking
  - `useUndoSend` provides 5-second undo window state machine
  - Local UI state: `isEditModalVisible`, `originalDraftBody`, `showApprovalSuccess`
- **TODOs/Issues**: Auto-dismiss after undo window expires via `setTimeout(..., 6000)` — could be race condition if user undoes at 5.9s

---

## Component: ChatScreen

- **Purpose**: Main persistent conversational interface — text chat with optional voice input, suggested actions, citation support, and TTS playback.
- **Architecture**: KeyboardAvoidingView layout with header (title + toggles), MessageList, suggested action chips, and ChatInput bar
- **Key Files**: `/mnt/agents/output/client/src/screens/ChatScreen.tsx`
- **Design Decisions**:
  - Uses `@hooks/useChat` for message state + API calls, `@hooks/useVoiceChat` for recording/playback
  - Optimistic UI: user message appears immediately before server responds
  - Suggested actions: `clear_batch`, `view_card`, `schedule`, `none` — rendered as chips above input
  - Voice mode toggle navigates to full-screen `ChatVoice` route
  - TTS auto-plays when `audioUrl` arrives and input mode is voice
  - Citation press handler in place (currently logs to console)
  - Dynamic styles created inside component with `StyleSheet.create` (theme-aware)
- **State Management**: 
  - `useChat` returns: messages, isLoading, inputText, suggestedAction, conversationTitle, audioUrl
  - Local state: `inputMode` (text|voice), `currentlyPlayingAudio` (for toggling playback)
  - `transcriptionRef` to avoid stale closures in voice effect
- **TODOs/Issues**: `handlePressCitation` is a stub (console.log only); schedule action sends a hardcoded message

---

## Component: ChatVoiceScreen

- **Purpose**: Immersive full-screen voice interface with large waveform, live transcription, and TTS playback. Darkened background.
- **Architecture**: Self-contained screen with 4-phase state machine: `ready -> listening -> processing -> responding`
- **Key Files**: `/mnt/agents/output/client/src/screens/ChatVoiceScreen.tsx`
- **Design Decisions**:
  - Dark background (`palette.ink[900]`) for immersive feel
  - Large central waveform (VoiceWaveform component) with phase-based colors:
    - listening = `palette.sand[400]`
    - processing = `palette.steel[400]`
    - responding = `palette.sage[500]`
    - ready = `palette.ink[300]`
  - Central mic button: 80x80 circle, changes to stop square when recording
  - Status label in uppercase with letter-spacing: "LISTENING...", "PROCESSING...", etc.
  - Last assistant response preview shown during responding phase
  - Close button (X) and text mode switch (keyboard icon) in top bar
  - Auto-plays TTS on new assistant messages with `audio_url`
- **State Management**: Local `phase` state drives the entire UI; `useChat` for messages/send; `useVoiceChat` for recording/amplitude
- **TODOs/Issues**: None noted

---

## Component: useCards Hook

- **Purpose**: Primary card state management hook — bridges cardStore with DB operations, sync queueing, and server batch fetching
- **Architecture**: Reads from `cardStore` (Zustand) + `syncStore` (Zustand), writes to local SQLite via `db.ts`, calls `api.ts` for server fetches
- **Key Files**: `/mnt/agents/output/client/src/hooks/useCards.ts`
- **Design Decisions**:
  - Offline-first decision flow: (1) write to DB, (2) update store, (3) queue for sync, (4) attempt immediate sync if online
  - Hydrates cards from local DB on mount (via `useEffect`)
  - Auto-fetches new batch from server when local card count < 3 and network available
  - Lazy-imports `performSync` to avoid circular dependencies
  - Skip de-prioritizes card (moves to end of queue) via `store.skipCard()`
- **Key Operations**:
  - `decide(input)` — writes decision to DB, queues sync, triggers background sync
  - `approve()` — marks card 'approved', queues sync
  - `consult()` — transitions card to 'consulting' state (local only)
  - `refreshBatch()` — explicit server fetch for new cards
- **TODOs/Issues**: `hydrate()` has empty catch block ("DB not ready yet — will retry on explicit load")

---

## Component: useChat Hook

- **Purpose**: Chat state management + API calls for persistent conversational interface
- **Architecture**: Manages messages, input, loading, suggested actions, conversation metadata, audio URLs
- **Key Files**: `/mnt/agents/output/client/src/hooks/useChat.ts`
- **Design Decisions**:
  - Optimistic UI updates: user message appended immediately with `local-` prefix ID
  - Server-confirmed messages replace optimistic entries (ID swap)
  - Conversation ID tracked in ref to avoid stale closures in async callbacks
  - Auto-loads conversation history when `conversationId` changes
  - `sendVoiceMessage` uploads audio via multipart form data
  - Suggested actions dismissed on tap or on new message send
- **API Endpoints Used**: `GET /chat/conversations/:id`, `POST /chat/messages`, `POST /chat/conversations/:id/voice`
- **TODOs/Issues**: Error handling in `sendMessage` only logs to console; no retry mechanism exposed

---

## Component: useVoiceChat Hook

- **Purpose**: Voice recording (STT via Deepgram), real-time waveform metering, and TTS playback (ElevenLabs) for chat
- **Architecture**: Uses `expo-av` for audio recording/playback, WebSocket to Deepgram for streaming transcription
- **Key Files**: `/mnt/agents/output/client/src/hooks/useVoiceChat.ts`
- **Design Decisions**:
  - 5-phase state machine: `idle -> recording -> processing -> playing -> error`
  - Real-time audio metering via `recording.getStatusAsync()` (replaces synthetic Math.random)
  - Amplitude normalization: dB values (-160...0) mapped to bar heights (0...28)
  - Deepgram WebSocket with Nova-2 model, smart formatting, interim results
  - 40-bar amplitude history with ripple effect across bars
  - TTS playback via `Audio.Sound.createAsync()` with auto-cleanup on finish
  - Comprehensive cleanup on unmount: stops recording, playback, closes WS, clears intervals
- **Key Constants**: `AMPLITUDE_HISTORY_LENGTH = 40`, metering interval = 100ms
- **TODOs/Issues**: Deepgram API key pulled from `process.env.DEEPGRAM_API_KEY` — empty string fallback means transcription silently fails if not configured

---

## Component: uiStore (Zustand)

- **Purpose**: Global UI state — theme, voice mode, navigation, toast, onboarding
- **Architecture**: Single Zustand store with computed selectors (effectiveTheme, isDark)
- **Key Files**: `/mnt/agents/output/client/src/stores/uiStore.ts`
- **Design Decisions**:
  - Theme mode: `light | dark | system` — effective theme computed from `Appearance.getColorScheme()`
  - Voice mode state machine with 7 phases matching `VoiceModeState` type
  - Toast auto-dismisses after 3 seconds
  - Navigation tracks `currentScreen` + `previousScreen` for back navigation
  - Hydration restores theme mode and onboarding status from AsyncStorage
  - Transcription accumulation: final results appended, interim results ignored
- **Persisted State**: `themeMode` (AsyncStorage key `ds_theme_mode`), `hasCompletedOnboarding` (`ds_onboarding`)
- **TODOs/Issues**: `hydrate` sets `isHydrated: true` in catch block but `isHydrated` is not in the store interface type

---

## Component: API Service (`api.ts`)

- **Purpose**: Axios HTTP client with JWT auto-refresh, device ID headers, offline awareness, and typed API endpoint wrappers
- **Architecture**: Singleton axios instance with request/response interceptors
- **Key Files**: `/mnt/agents/output/client/src/services/api.ts`
- **Design Decisions**:
  - JWT refresh on 401: queues concurrent requests during refresh, retries all after new token obtained
  - Token refresh clears auth state on failure (forces re-login)
  - `X-Device-Id` header attached to every request for sync tracking
  - 5xx errors mark sync as degraded via `syncStore.setServerHealthy(false)`
  - Base URL from `EXPO_PUBLIC_API_URL` env var with production fallback
  - 30-second timeout default
- **Key Endpoints**: `fetchBatch()`, `syncWithServer()`, `submitDecision()`, `fetchCardSource()`, `sendDraft()`, `cancelDraft()`, `consultOnCard()`, `fetchConversations()`, `sendChatMessage()`, `sendVoiceMessage()`
- **TODOs/Issues**: Server health check has 5s timeout but main requests use 30s — could be more resilient

---

## Component: Database Service (`db.ts`)

- **Purpose**: SQLCipher-encrypted local SQLite via `op-sqlite` — card storage, drafts, sync queue, streak tracking
- **Architecture**: Singleton DB instance with lazy initialization, schema migrations, JSON serialization for complex types
- **Key Files**: `/mnt/agents/output/client/src/services/db.ts`
- **Design Decisions**:
  - **Raw email bodies NEVER stored locally** — only card metadata and user decisions
  - JSON columns for complex types: `from_json`, `context_json`, `citations_json`
  - Server version field on every card for conflict detection (`server_version`)
  - Schema migration system with `schema_version` table
  - Streak tracking table with 48-hour reset threshold
  - Full CRUD for cards, drafts, sync queue operations
  - Batch operations wrapped in transactions
- **Schema Tables**: `local_cards`, `local_drafts`, `sync_queue`, `streak_tracking`, `schema_version`
- **Indexes**: `idx_cards_state`, `idx_cards_urgency`, `idx_drafts_card`, `idx_sync_queue_created`
- **TODOs/Issues**: `user_id`, `thread_id`, `source_account_id` set to empty strings in `rowToCard` — noted as "populated from auth context"

---

## Component: Sync Service (`sync.ts`)

- **Purpose**: Offline-first sync orchestrator — uploads local changes, downloads server updates, CRDT merge
- **Architecture**: `SyncEngine` class with dependency injection for DB, device ID, auth token
- **Key Files**: `/mnt/agents/output/client/src/services/sync.ts`
- **Design Decisions**:
  - 3-phase protocol: (1) upload local changes, (2) download server updates, (3) compute new server_version
  - CRDT merge: server wins on drafts, user wins on decisions
  - Queue items removed ONLY after server acknowledgment
  - `SyncReport` returned with detailed metrics: uploaded, accepted, rejected, new cards, conflicts, duration
  - Per-device sync cursor stored in SQLite (`sync_cursor` table)
  - Idempotent: same request twice produces same result
  - `sync_history` table records every attempt for diagnostics
- **Key Types**: `SyncResult`, `SyncReport`, `SyncCursor`, `SyncDatabase` interface
- **TODOs/Issues**: `uploadChanges` references `syncQueue.getPending()` and `crdtMerge.buildLocalChangePayload()` — these imported from `./syncQueue` and `./crdt` which weren't in the file list; `applyServerCards` references a `cards` table but `db.ts` uses `local_cards` — table naming inconsistency

---

## Component: Theme / Colors

- **Purpose**: Low-saturation warm color palette designed for calm, focused decision-making
- **Architecture**: Base palette (4 color families) + semantic theme tokens (light/dark)
- **Key Files**: `/mnt/agents/output/client/src/theme/colors.ts`
- **Design Decisions**:
  - 4 color families: `ink` (warm neutrals), `sand` (primary accent), `sage` (success), `rose` (error), `steel` (info)
  - Each family has 10 shades (50-900)
  - Primary brand accent: `sand[400]` = `#c4a265`
  - Semantic tokens: background, surface, border, text (primary/secondary/tertiary/inverse), status (success/warning/error/info), card-specific, voice mode
  - Dark theme inverts backgrounds but preserves accent colors
  - `as const` assertions for compile-time type safety
- **TODOs/Issues**: None noted

---

## Component: Sync Service (Go Backend)

- **Purpose**: Go microservice — central nervous system managing client API, WebSocket hub, queue management, push notifications, sync protocol
- **Architecture**: Multi-binary deployment (server + worker), PostgreSQL for persistence, Redis for queues/versions, NATS for cross-context messaging
- **Key Files**: `/mnt/agents/output/sync/docs/ARCHITECTURE.md`
- **Design Decisions**:
  - Queue stored in Redis Sorted Sets (`queue:{user_id}`) with `server_version` as score
  - Version counter in Redis String (`version:{user_id}`) — atomic INCR
  - Per-device sync state (`syncstate:{user_id}:{device_id}`)
  - WebSocket auth via JWT in query parameter
  - 4 notification types: batch, interrupt, temporal, staging
  - Quiet hours: global default 22:00-08:00, per-user overrideable
  - NATS JetStream for WorkQueue-pattern messaging from Intelligence context
- **API Routes**: 17 endpoints covering auth, batch, decisions, sync, queue, devices, notifications, WebSocket
- **Dependencies**: PostgreSQL 16, Redis 7, NATS 2.10, FCM (Android), APNS (iOS)
- **TODOs/Issues**: None noted in architecture doc

---

## Component: Sync Models (Go)

- **Purpose**: Server-side type definitions — wire contracts between Sync API, client, and other bounded contexts
- **Architecture**: Single `models` package with struct tags for DB (`db:"..."`) and JSON (`json:"..."`)
- **Key Files**: `/mnt/agents/output/sync/internal/models/models.go`
- **Design Decisions**:
  - Mirrors client TypeScript types exactly (DecisionCard, Draft, SyncRequest/Response, etc.)
  - Uses `uuid.UUID` from `github.com/google/uuid` for all IDs
  - `json.RawMessage` for flexible JSON columns (from_field, context, chunk_citations)
  - Pointer types for optional fields (`*string`, `*time.Time`, `*uuid.UUID`)
  - `ServerVersion` field on DecisionCard for conflict detection
  - `SyncError` struct with error codes: `auth_expired`, `version_conflict`, `card_not_found`, `draft_not_found`, `queue_empty`, `rate_limited`
  - WebSocket event types as typed constants (`WSEventType`)
- **Additional Types**: DeviceSession, RefreshToken, Notification, NotificationPreference, UserQueue, CalendarEvent, ReminderJob
- **TODOs/Issues**: Header comment: "DO NOT MODIFY without coordination" — tight coupling to client types

---

## Component: CRDT Merger (Go)

- **Purpose**: Server-side sync engine implementing CRDT-style conflict resolution for the 3-phase sync protocol
- **Architecture**: `SyncEngine` struct with `SyncStore` and `VersionCursor` dependencies. Stateless except for stores.
- **Key Files**: `/mnt/agents/output/sync/internal/sync/merger.go`
- **Design Decisions**:
  - **CRDT Rules** (in priority order):
    1. Card must exist → reject if not found (`card_not_found`)
    2. Terminal states (sent/archived/expired) are immutable → server wins (`card_already_terminal`)
    3. Ownership validation → reject if wrong user (`ownership_violation`)
    4. Decision-specific rules: approve (user wins), edit (server wins), consult (no-op)
  - `approve`: Transactional — marks card approved + draft user_approved. User approval is sacred.
  - `edit`: Server-authoritative on draft_body. Client edit is logged (truncated to 200 chars for privacy) but NOT applied.
  - `consult`: No-op on server. Client may show "consulting" UI state but server doesn't track it.
  - All changes logged to sync_log table for audit
  - Idempotent: applying same change twice produces same result (monotonic state transitions)
  - 3-phase `Process` method: (1) accept local changes, (2) get changes since last sync, (3) compute new version
- **Key Methods**: `Process()`, `applyChange()`, `applyApprove()`, `applyEdit()`, `applyConsult()`
- **TODOs/Issues**: `applyApprove` warns but continues if draft not found — card state change is considered the primary action; `applyEdit` stores truncated draft preview in log — potential privacy concern noted

---

## Component: WebSocket Hub (Go)

- **Purpose**: Real-time bidirectional communication hub for "Sending Session" — manages client registrations, event distribution, cross-instance broadcasting
- **Architecture**: Central goroutine (`Run`) with channel-based message passing; `connections` map `userID -> deviceID -> *Client`
- **Key Files**: `/mnt/agents/output/sync/internal/websocket/hub.go`
- **Design Decisions**:
  - Single connection per device: new connection for same (userID, deviceID) disconnects old one
  - Events distributed locally (in-memory) AND via Redis pub/sub (for multi-node deployments)
  - Redis pattern subscribe to `ws:*` channels for cross-instance event distribution
  - Send buffer protection: full buffer triggers async unregister to prevent blocking
  - Thread-safe: `sync.RWMutex` for connections map, channels for Run goroutine communication
  - Graceful shutdown: `closeAll()` closes all connections on context cancellation
  - Pong wait configured from `Config.WSPongWait`
- **Event Types**: `spawn` (new card), `paragraph` (draft update), `accept/edit/delegate` (client->server), `ping/pong` (keepalive)
- **Key Methods**: `Run()`, `RegisterClient()`, `BroadcastToUser()`, `SendToDevice()`, `StartRedisSubscriber()`, `IsUserOnline()`, `GetClientCount()`
- **TODOs/Issues**: `fmt.Sscanf(channel, "ws:%s", &userID)` may fail on UUID parsing — has fallback but relies on string prefix matching

---

## Cross-Cutting Concerns

### Security Invariants
| Invariant | Implementation |
|---|---|
| SQLCipher encryption | `getOrCreateEncryptionKey()` -> `open({ encryptionKey })` |
| No raw email bodies | Schema has no email body column; source fetched transiently via `GET /cards/:id/source` |
| JWT auto-refresh | Axios response interceptor: 401 -> refresh -> retry |
| Secure key storage | `expo-secure-store` with `WHEN_UNLOCKED` accessibility |
| Local data wipe on logout | `clearAllData()` + `deleteEncryptionKey()` |

### Sync Protocol Summary
```
Client Change -> Local DB Write -> sync_queue Insert -> [if online] POST /sync
Server: CRDT merge -> accept/reject -> return new/updated/removed cards + new version
Client: upsert cards, remove deleted, clear accepted from queue
Conflict: server_version wins (server wins on drafts, user wins on approvals)
```

### Voice Mode State Machine
```
intro -> listening -> transcribing -> drafting -> confirming -> sending -> undo_window -> (stop)
                                      ^                           |
                                      +---------- undo <-----------+
```
- `undo_window`: 5-second grace period to revert a sent draft

---

*Summary generated from 19 source files across client and sync service.*
