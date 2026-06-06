# Track 8: Client Architecture Review

## Decision Stack — React Native Client Audit Report

**Date:** 2025-01-28  
**Scope:** Full client architecture audit — navigation, state management, offline handling, voice/chat integration, security  
**Reviewer:** Architecture Review Bot  

---

## Executive Summary

| Criterion | Status | Notes |
|---|---|---|
| 1. Navigation flow: BatchGate -> CardStack -> DecisionInput -> DraftReview | **PARTIAL** | DecisionInputScreen is not registered in AppNavigator; exists as standalone but not wired into nav flow |
| 2. Chat accessible from multiple entry points | **PASS** | ChatList -> Chat -> ChatVoice + ChatAboutButton from cards + suggested actions |
| 3. SQLCipher encryption on local database | **PASS** | op-sqlite with AES-256 key from SecureStore |
| 4. No raw email stored locally | **PASS** | Only metadata + chunk citations; no email_body column |
| 5. Offline queue persists across app restarts | **PASS** | sync_queue table in SQLCipher database |
| 6. Voice mode has undo window support | **PASS** | 30s undo window with countdown timer in useApproval |
| 7. Background sync registered | **PASS** | expo-background-fetch with 15-min interval |
| 8. JWT refresh on 401 | **PASS** | Token rotation queue pattern, retries original request |

**Overall: 7/8 PASS, 1 PARTIAL**

---

## 1. Navigation Flow Analysis

### Screen Flow Diagram

```
+------------------+     +------------------+     +------------------+     +------------------+
|   BatchGate      | --> |   CardStack      | --> | DecisionInput    | --> |  DraftReview     |
|  (entry gate)    |     | (one-card view)  |     | (not in nav)     |     |  (modal)         |
+------------------+     +------------------+     +------------------+     +------------------+
        |                        |                                               |
        |                        | (onConsult)                                   | (onApproved)
        |                        v                                               v
        |                 +------------------+                          +------------------+
        |                 |   ChatScreen     |                          |   (next card)    |
        |                 | (linked card)    |                          |   or complete    |
        |                 +------------------+                          +------------------+
        |                        |
        |                        v
        |                 +------------------+
        |                 |  ChatVoiceScreen |  (full-screen modal, fade)
        |                 |  (voice mode)    |
        |                 +------------------+
        |
        +------------------> ChatList (side entry)
                               |
                               v
                          ChatScreen
                               |
                               v
                          ChatVoiceScreen
```

### Navigator Configuration (AppNavigator.tsx)

| Screen | Type | Gesture | Entry Points |
|---|---|---|---|
| `BatchGate` | Stack | Disabled | Initial route, Chat suggested action |
| `CardStack` | Stack | Disabled | BatchGate "Start Clearing" |
| `DraftReview` | Modal (slide_from_bottom) | — | CardStack -> DecisionInput -> DraftReview |
| `ChatList` | Stack | Enabled | Side navigation (FAB pattern) |
| `Chat` | Stack | Enabled | ChatList, CardStack (onConsult) |
| `ChatVoice` | FullScreenModal (fade) | — | Chat (voice toggle) |

### Issue: DecisionInputScreen Not Registered in Navigator

**File:** `AppNavigator.tsx`  
**Severity:** MEDIUM

The `DecisionInputScreen` component exists as a fully implemented screen (`src/screens/DecisionInputScreen.tsx`) with:
- KeyboardAvoidingView for iOS/android input handling
- Card summary display with context
- Quick replies + suggestion bar
- 200-character input limit validation
- Loading spinner for draft generation

**However**, it is **not imported or registered** in `AppNavigator.tsx`. The navigation flow only defines:
- BatchGate -> CardStack -> DraftReview

The DecisionInput step is missing from the `RootStackParamList`:
```typescript
// Current param list — DecisionInput is missing
type RootStackParamList = {
  BatchGate: undefined;
  CardStack: undefined;
  DraftReview: { draftId?: string; cardId?: string };
  ChatList: undefined;
  Chat: { conversationId?: string; linkedCardId?: string };
  ChatVoice: { conversationId: string };
};
```

**Recommendation:** Add `DecisionInput` to the stack navigator with params `{ cardId: string }` and connect CardStack's `onDecide` callback to navigate to it.

### Issue: Placeholder Screens in Navigator

**File:** `AppNavigator.tsx` lines 26-86  
**Severity:** LOW

The actual `BatchGateScreen.tsx`, `CardStackScreen.tsx`, and `DraftReviewScreen.tsx` files exist as fully-implemented components with props interfaces, but `AppNavigator.tsx` redefines them as inline placeholder components. The navigator imports Chat screens from `@screens/*` but uses local inline definitions for the core flow screens.

**Recommendation:** Replace inline placeholders with proper screen imports:
```typescript
import { BatchGateScreen } from '@screens/BatchGateScreen';
import { CardStackScreen } from '@screens/CardStackScreen';
import { DraftReviewScreen } from '@screens/DraftReviewScreen';
import { DecisionInputScreen } from '@screens/DecisionInputScreen';
```

---

## 2. Chat Entry Points Analysis

### Verified Entry Points

| Entry Point | Navigation Target | Context |
|---|---|---|
| ChatListScreen FAB | `Chat` (new conversation) | Standalone chat entry |
| ChatList conversation row | `Chat` (existing) | Conversation history |
| ChatScreen voice toggle | `ChatVoice` | Full-screen voice mode |
| CardStackScreen [Consult] | `Chat` (linked card) | Card-specific consultation |
| Chat suggested action "clear_batch" | `BatchGate` | Cross-flow navigation |
| Chat suggested action "view_card" | `Chat` (with linkedCardId) | Card context in chat |

**Verdict:** Chat is accessible from **3 distinct entry points** (ChatList, CardStack consult, voice toggle). PASS.

---

## 3. SQLCipher Encryption Verification

### Encryption Architecture

```
App Layer
    |
    v
+----------------------------+
|  op-sqlite (SQLCipher)     |  <-- DB file encrypted with AES-256
|  encryptionKey: string     |
+----------------------------+
    |
    v
+----------------------------+
|  getOrCreateEncryptionKey()|  <-- Key stored in Expo SecureStore
|  Key: 64 hex chars         |      (iOS Keychain / Android Keystore)
+----------------------------+
    |
    v
+----------------------------+
|  SecureStore.WHEN_UNLOCKED |  <-- Device-lock protected
+----------------------------+
```

### Verification Checklist

| Check | Status | Evidence |
|---|---|---|
| SQLCipher enabled via op-sqlite | **PASS** | `db.ts:39-42` — `open({ name, encryptionKey })` |
| 256-bit encryption key | **PASS** | `crypto.ts:16` — `Aes.randomKey(32)` = 64 hex chars |
| Key stored in SecureStore | **PASS** | `crypto.ts:30-33` — `SecureStore.setItemAsync` with `WHEN_UNLOCKED` |
| Key auto-generated on first run | **PASS** | `crypto.ts:23-35` — getOrCreate pattern |
| Key deletion on logout | **PASS** | `crypto.ts:40-42` — `deleteEncryptionKey()` |
| AES-256-GCM for value encryption | **PASS** | `crypto.ts:53` — `aes-256-gcm` |
| Schema migrations supported | **PASS** | `db.ts:72-136` — versioned migration system |

### Schema Security

The `local_cards` table stores only card **metadata**, never raw email content:

```sql
-- Columns present: card metadata only
from_json        -- Sender info (name, email, relationship)
they_want        -- One-line summary (AI-generated)
context_json     -- Structured context (deadline, etc.)
need_from_user   -- "Reply needed / Calendar check / etc."
citations_json   -- Chunk references (chunk_id, snippet, email_id)
urgency_score    -- Numeric priority

-- Columns NOT present (confirmed absent):
-- email_body, raw_content, full_thread, attachments
```

**Verdict:** Full PASS. Encryption is properly implemented at multiple layers.

---

## 4. Raw Email Storage Verification

### No Raw Email Invariant

| Check | Status | Evidence |
|---|---|---|
| No `email_body` column in DB | **PASS** | `db.ts:75-88` — schema inspection confirms absent |
| Source email fetched via streaming | **PASS** | `api.ts:232-248` — `fetchCardSource()` streams chunks; no caching |
| SourceViewer only shows citations | **PASS** | `SourceViewerScreen.tsx` — only `verbatim_snippet` from chunk, not full email |
| Card data only has summaries | **PASS** | `types/cards.ts` — `DecisionCard` has `they_want`, `need_from_user`, not email body |
| Comment in code | **PASS** | `db.ts:3` — "Raw email bodies are NEVER stored locally" |

**Verdict:** Full PASS. The invariant is upheld at the database, API, and UI layers.

---

## 5. Offline Queue Persistence Verification

### Sync Queue Architecture

```
User Action
    |
    v
+----------------------------+
|  syncQueue.enqueue()       |  <-- Immediate SQLite write
|  INSERT INTO sync_queue    |
+----------------------------+
    |
    v
+----------------------------+
|  SQLCipher Database        |  <-- Survives app restart
|  (encrypted at rest)       |
+----------------------------+
    |
    v  (on network reconnect)
+----------------------------+
|  SyncEngine.uploadChanges()|  <-- Drains queue
|  DELETE on server ack      |
+----------------------------+
```

### Verification Checklist

| Check | Status | Evidence |
|---|---|---|
| Queue stored in SQLite | **PASS** | `syncQueue.ts:77-81` — INSERT into `sync_queue` table |
| Queue survives app restart | **PASS** | `db.ts:100-106` — `sync_queue` is a persistent table |
| Items removed only on server ack | **PASS** | `syncQueue.ts:118-125` — `complete()` sets `completed_at` only |
| Retry count tracked | **PASS** | `syncQueue.ts:140-147` — `incrementRetry()` |
| Failed items retained for inspection | **PASS** | `syncQueue.ts:169-192` — `getFailed()` with maxRetries cap |
| Cleanup of old items | **PASS** | `syncQueue.ts:155-162` — `cleanup()` removes 7+ day old items |
| Background sync drains queue | **PASS** | `backgroundSync.ts:35-58` — runs every 15 min, calls `syncEngine.sync()` |
| Auto-sync on reconnect | **PASS** | `useSync.ts:31-46` — NetInfo listener triggers sync |
| Auto-sync on foreground | **PASS** | `useSync.ts:49-65` — AppState listener triggers sync |

### Conflict Resolution

The sync engine uses **CRDT merge** for conflict resolution:
- Server wins on drafts (`sync.ts:220` — `crdtMerge.mergeCards()`)
- User wins on decisions
- Conflicts logged in `SyncReport.conflicts[]` for analytics

**Verdict:** Full PASS. Offline queue is robust and persists across restarts.

---

## 6. Voice Mode Undo Window Verification

### Undo Architecture

| Mode | Undo Window | Mechanism |
|---|---|---|
| **Text** | 0s (immediate) | Confirmation dialog serves as implicit guard |
| **Voice** | 30 seconds | Timer-based countdown with explicit undo capability |

### useApproval Hook (useApproval.ts)

```typescript
const VOICE_UNDO_WINDOW_MS = 30_000;  // 30 seconds

// Approval record tracks:
interface ApprovalRecord {
  draftId: string;
  cardId: string;
  approvedAt: number;        // timestamp
  undoWindowMs: number;      // 30s for voice
  undoDeadline: number;      // approvedAt + 30s
  status: "pending_undo_window" | "queued" | "confirmed";
}
```

### Undo Flow

```
Voice Approve
    |
    v
+----------------------------+     +------------------+
| Status: pending_undo_window| --> | canUndo()? true  |
| Timer starts (30s)         |     | undo() available |
+----------------------------+     +------------------+
    |                                        |
    | 30s expires                            | user taps undo
    v                                        v
+----------------------------+     +------------------+
| Status: confirmed          |     | Remove approval  |
| Queue for send             |     | Remove from queue|
+----------------------------+     +------------------+
```

### Verification Checklist

| Check | Status | Evidence |
|---|---|---|
| Voice undo window defined (30s) | **PASS** | `useApproval.ts:26` — `VOICE_UNDO_WINDOW_MS = 30_000` |
| Undo timer with smooth countdown | **PASS** | `useApproval.ts:49-83` — 250ms interval, 4x/sec update |
| `canUndo()` guard check | **PASS** | `useApproval.ts:196-204` — checks deadline |
| `undo()` removes approval | **PASS** | `useApproval.ts:220-251` — clears timer, removes from queue |
| `getUndoSecondsRemaining()` for UI | **PASS** | `useApproval.ts:209-214` — countdown map |
| Approval queued in sync_queue | **PASS** | `useApproval.ts:132-137` — `syncQueue.enqueue("approve_draft", ...)` |
| Cleanup on unmount | **PASS** | `useApproval.ts:256-261` — clears all timers |

**Verdict:** Full PASS. Voice mode has comprehensive undo support.

---

## 7. Background Sync Verification

### Background Sync Configuration (backgroundSync.ts)

| Property | Value |
|---|---|
| Task name | `DECISION_STACK_SYNC` |
| Interval | 15 minutes (900s) |
| `stopOnTerminate` | `false` (best-effort on iOS) |
| `startOnBoot` | `true` (Android) |
| Network check | Yes — skips if offline |

### Registration Flow

```
AppNavigator mount
    |
    v
useEffect (isHydrated)      <-- Only after auth store hydrated
    |
    v
useSync.registerBackground()
    |
    v
registerBackgroundSync()    <-- idempotent check
    |
    v
BackgroundFetch.registerTaskAsync(BACKGROUND_SYNC_TASK, {...})
```

### Verification Checklist

| Check | Status | Evidence |
|---|---|---|
| Background task defined | **PASS** | `backgroundSync.ts:35-59` — `TaskManager.defineTask()` |
| Registration on app mount | **PASS** | `AppNavigator.tsx:139-143` — useEffect calls `registerBackground()` |
| Idempotent registration | **PASS** | `backgroundSync.ts:73` — checks `isTaskRegisteredAsync()` first |
| Unregister on logout | **PASS** | `backgroundSync.ts:90-98` — `unregisterBackgroundSync()` |
| Network-aware execution | **PASS** | `backgroundSync.ts:38-40` — checks `NetInfo.isConnected` |
| Proper result codes | **PASS** | `backgroundSync.ts:48-52` — returns `NewData` or `NoData` |
| Error handling | **PASS** | `backgroundSync.ts:53-58` — catches errors, returns `Failed` |
| Status query available | **PASS** | `backgroundSync.ts:104-114` — `getBackgroundSyncStatus()` |

**Verdict:** Full PASS. Background sync is properly configured and registered.

---

## 8. JWT Refresh on 401 Verification

### Token Refresh Architecture (api.ts)

```
Request with expired token
    |
    v
401 Response
    |
    v
+----------------------------+
| isRefreshing check         |
| If true: queue request     |
| If false: start refresh    |
+----------------------------+
    |
    v
+----------------------------+
| POST /auth/refresh         |
| with refresh_token         |
+----------------------------+
    |
    v
+----------------------------+
| Update tokens in store     |
| Persist to AsyncStorage    |
+----------------------------+
    |
    v
+----------------------------+
| Process queued requests    |
| Retry original request     |
+----------------------------+
    |
    v
+----------------------------+
| If refresh fails:          |
| clearAuth() → logout       |
+----------------------------+
```

### Verification Checklist

| Check | Status | Evidence |
|---|---|---|
| 401 interceptor | **PASS** | `api.ts:107` — `error.response?.status === 401` |
| Request queueing during refresh | **PASS** | `api.ts:108-119` — `refreshQueue` with Promise-based queue |
| `_retry` flag to prevent loops | **PASS** | `api.ts:99, 121` — checks and sets `_retry` |
| Token refresh API call | **PASS** | `api.ts:130-136` — `POST /auth/refresh` |
| Token persistence | **PASS** | `api.ts:139` — `setTokens(newTokens)` → AsyncStorage |
| Queue processing after refresh | **PASS** | `api.ts:141` — `processQueue(null, newToken)` |
| Original request retry | **PASS** | `api.ts:143-145` — retries with new token |
| Logout on refresh failure | **PASS** | `api.ts:152` — `clearAuth()` |
| Device ID header attached | **PASS** | `api.ts:81-84` — `X-Device-Id` header |
| Token expiry check on hydrate | **PASS** | `authStore.ts:93` — `Date.now()/1000 > expires_at` |

### Token Storage Security

| Check | Status | Evidence |
|---|---|---|
| Tokens in AsyncStorage (encrypted device) | **PASS** | `authStore.ts:41-43` — uses `AsyncStorage` |
| Expired tokens discarded on hydrate | **PASS** | `authStore.ts:96` — `isExpired ? null : parsedTokens` |
| Multi-remove on logout | **PASS** | `authStore.ts:74` — `AsyncStorage.multiRemove([tokens, deviceId])` |

**Note:** JWT tokens are stored in AsyncStorage (not SecureStore). While the device storage is encrypted at the OS level, this is a trade-off vs. storing in SecureStore (which has stricter access controls). Consider moving tokens to SecureStore for enhanced security.

**Verdict:** Full PASS. Token refresh is correctly implemented with queueing.

---

## Component Architecture Review

### State Management (Zustand)

```
App.tsx
  |-- useAuthStore      (hydrates on mount)
  |-- useUIStore        (hydrates on mount)
  |
  v
AppNavigator
  |-- useAuth           (auth gate)
  |-- useSync           (network monitoring, background sync)
  |-- useCards          (card loading)
  |
  v
Screens (stack-based, each manages local state)
  |-- useCardStore      (global card queue)
  |-- useSyncStore      (global sync status)
```

### Store Inventory

| Store | Scope | Persistence | Key State |
|---|---|---|---|
| `authStore` | Global | AsyncStorage | tokens, deviceId, isAuthenticated |
| `syncStore` | Global | Volatile (recomputed) | isSyncing, networkAvailable, pendingUploads |
| `cardStore` | Global | SQLite via DB layer | cards[], currentIndex |
| `uiStore` | Global | AsyncStorage | currentRoute, theme |

### Hook Inventory

| Hook | Purpose | Key Features |
|---|---|---|
| `useChat` | Chat state + API | Optimistic updates, suggested actions, voice message support |
| `useVoiceChat` | Audio recording/playback | expo-av integration, waveform simulation, TTS playback |
| `useDrafting` | Draft generation SM | State machine (idle/loading/success/error), modification support |
| `useApproval` | Approval + undo | 30s voice undo, confirmation dialog, sync queue integration |
| `useSync` | Sync control | NetInfo monitoring, AppState foreground sync, background registration |
| `useConversations` | Conversation list | CRUD operations, optimistic updates |

---

## Security Findings

### Critical: None

### Medium

1. **DecisionInputScreen not in navigator** — The core flow screen is orphaned. The expected flow `BatchGate -> CardStack -> DecisionInput -> DraftReview` cannot be completed through navigation. The screen component exists but has no route defined.

2. **JWT tokens in AsyncStorage** — While device storage is encrypted, SecureStore would provide better isolation. Tokens are sensitive credentials and would benefit from Keychain/Keystore-level protection.
   - `authStore.ts:41` — `AsyncStorage.setItem(STORAGE_KEYS.tokens, ...)`
   - Recommendation: Consider `SecureStore` for token storage

### Low

3. **Placeholder screens override real implementations** — `AppNavigator.tsx` redefines `BatchGateScreen`, `CardStackScreen`, and `DraftReviewScreen` as inline components rather than importing the full implementations. The standalone screen files contain richer UI (gestures, animations, proper data handling) that is not being used.

4. **Simulated transcription in useVoiceChat** — The hook contains demo code that simulates transcription with hardcoded words (`'Clear', 'my', 'batch', 'of', 'pending', 'decisions'`). This must be removed in production.
   - `useVoiceChat.ts:131-141`

5. **Console.log in ChatScreen** — Debug logging for citation presses should be removed or converted to a proper analytics call.
   - `ChatScreen.tsx:139` — `console.log('[ChatScreen] Citation pressed:', ...)`

---

## Dependency Analysis

### Key Dependencies (package.json)

| Dependency | Version | Purpose |
|---|---|---|
| `expo` | ~50.0.0 | Framework |
| `react-native` | 0.73.0 | Core RN |
| `@react-navigation/native` | ^6.1.9 | Navigation |
| `@react-navigation/native-stack` | ^6.9.17 | Stack navigator |
| `zustand` | ^4.5.0 | State management |
| `axios` | ^1.6.0 | HTTP client |
| `react-native-aes-crypto` | ^3.0.0 | AES encryption |
| `op-sqlite` | (implied) | SQLCipher database |
| `expo-background-fetch` | ~11.8.0 | Background sync |
| `expo-task-manager` | ~11.7.0 | Background tasks |
| `@react-native-community/netinfo` | ^11.0.0 | Network detection |
| `react-native-gesture-handler` | ~2.14.0 | Gestures (swipe) |
| `react-native-reanimated` | ~3.6.0 | Animations |
| `expo-secure-store` | ~12.8.1 | Keychain/Keystore |
| `expo-notifications` | ~0.27.6 | Push notifications |
| `expo-device` | ~5.9.3 | Device info |
| `@react-native-async-storage/async-storage` | 1.21.0 | Local persistence |

### Dependency Concerns

- **op-sqlite not in package.json** — The code imports from `op-sqlite` but it's not listed in `dependencies`. This must be added.
- **expo-av not in package.json** — `useVoiceChat.ts` imports from `expo-av` but it's not listed in dependencies.
- **@tanstack/react-query** listed but not used — The code uses direct axios calls and Zustand, not React Query. Either remove or integrate properly.

---

## Recommendations

### Priority 1 (Required)

1. **Register DecisionInputScreen in AppNavigator** — Add it to `RootStackParamList` and `<Stack.Screen>` with the route params it expects (`{ cardId: string }`).

2. **Add missing dependencies to package.json:**
   - `op-sqlite`
   - `expo-av`

3. **Remove simulated transcription** from `useVoiceChat.ts` before production.

### Priority 2 (Recommended)

4. **Replace placeholder screens** in `AppNavigator.tsx` with imports from the actual screen files.

5. **Consider moving JWT tokens to SecureStore** instead of AsyncStorage.

6. **Remove or utilize @tanstack/react-query** — either integrate it for server state or remove from dependencies.

### Priority 3 (Nice to Have)

7. Add deep-linking configuration for chat conversations.
8. Add navigation state persistence for recovery from app kills mid-flow.

---

## Appendix: File Inventory

### Screens (9 files)
| File | Lines | Props-based | Nav-registered | Status |
|---|---|---|---|---|
| `BatchGateScreen.tsx` | 168 | Yes | No (inline) | Orphaned |
| `CardStackScreen.tsx` | 329 | Yes | No (inline) | Orphaned |
| `DecisionInputScreen.tsx` | 350 | Yes | **No** | **Orphaned** |
| `DraftReviewScreen.tsx` | 468 | Yes | No (inline) | Orphaned |
| `SourceViewerScreen.tsx` | 258 | Yes | No | Orphaned |
| `ChatScreen.tsx` | 306 | No (uses hooks) | Yes | Active |
| `ChatListScreen.tsx` | 204 | No (uses hooks) | Yes | Active |
| `ChatVoiceScreen.tsx` | 415 | No (uses hooks) | Yes | Active |

### Services (8 files)
| File | Purpose | Status |
|---|---|---|
| `api.ts` | HTTP client + JWT refresh | Active |
| `db.ts` | SQLCipher schema + CRUD | Active |
| `crypto.ts` | AES encryption helpers | Active |
| `sync.ts` | CRDT sync engine | Active |
| `syncQueue.ts` | Persistent operation queue | Active |
| `backgroundSync.ts` | Background fetch task | Active |
| `crdt.ts` | CRDT merge logic | Referenced |
| `notifications.ts` | Push notifications | Present |

### Hooks (8 files)
| File | Purpose | Status |
|---|---|---|
| `useChat.ts` | Chat state + API | Active |
| `useVoiceChat.ts` | Voice recording/playback | Active (has demo code) |
| `useDrafting.ts` | Draft generation SM | Active |
| `useApproval.ts` | Approval + undo window | Active |
| `useSync.ts` | Sync control | Active |
| `useConversations.ts` | Conversation list | Active |
| `useAuth.ts` | Auth logic | Referenced |
| `useCards.ts` | Card operations | Referenced |

### Stores (4 files)
| File | State | Persistence |
|---|---|---|
| `authStore.ts` | Auth tokens, deviceId | AsyncStorage |
| `syncStore.ts` | Sync status | Volatile |
| `cardStore.ts` | Card queue | SQLite |
| `uiStore.ts` | UI state | AsyncStorage |

---

*End of Report*
