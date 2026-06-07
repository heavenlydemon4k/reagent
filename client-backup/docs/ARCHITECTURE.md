# Decision Stack — Client Architecture

## Overview

React Native + Expo SDK 50 client for one-card-at-a-time decision clearing. Offline-first with SQLCipher-encrypted local storage.

## Principles

1. **Offline-first**: Every operation completes locally first; sync is asynchronous
2. **Zero trust local storage**: SQLCipher encryption is mandatory; raw email bodies never touch disk
3. **One-card-at-a-time**: Single decision card in viewport; no scrolling feeds
4. **No Context API**: Zustand for all global state
5. **Implicit types**: Store types are inferred from initial state, not declared separately

## Project Structure

```
client/
  App.tsx                        # Root component — GestureHandler + SafeAreaProvider
  app.json                       # Expo config with BGTaskScheduler, notifications
  package.json                   # Dependencies locked to Expo SDK 50
  tsconfig.json                  # Path aliases: @/*, @services/*, @stores/*, etc.
  babel.config.js                # Module resolver + reanimated plugin
  metro.config.js                # op-sqlite native module, Hermes optimizations
  src/
    types/
      cards.ts                   # SHARED — DecisionCard, Draft, Sync*, Voice* types
    stores/                      # Zustand — all global state
      cardStore.ts               # Card queue, current index, batch management
      syncStore.ts               # Sync health, pending uploads, connection status
      authStore.ts               # JWT tokens, device ID, hydration
      uiStore.ts                 # Voice mode, theme, navigation, toasts
    services/                    # Core infrastructure
      crypto.ts                  # AES-256-GCM, SQLCipher key management (SecureStore)
      db.ts                      # op-sqlite + SQLCipher schema, CRUD, transactions
      api.ts                     # Axios with JWT refresh interceptors
      sync.ts                    # Background sync engine (expo-background-fetch)
      websocket.ts               # WS client for real-time sending sessions
      notifications.ts           # FCM/APNS push notification handling
    hooks/                       # Business logic composition
      useCards.ts                # Card loading, decision flow, DB bridging
      useSync.ts                 # Sync control, network monitoring
      useVoice.ts                # Voice mode state machine
      useAuth.ts                 # Auth lifecycle, token refresh scheduling
    navigation/
      AppNavigator.tsx           # Stack navigator: BatchGate → CardStack → DraftReview
    theme/
      colors.ts                  # Low-saturation warm palette (sand/sage/rose/steel)
      typography.ts              # 4-pt grid scale, font weights
      spacing.ts                 # Shadows, layout constants, touch targets
```

## Dependency Tree

```
App.tsx
  └─ AppNavigator.tsx
       └─ useAuth (hook) ── authStore (zustand)
       └─ useSync (hook) ── syncStore (zustand)
  └─ useAuthStore.hydrate
  └─ useUIStore.hydrate

useCards (hook)
  ├─ cardStore (zustand)
  ├─ syncStore (zustand)
  ├─ db.ts ── crypto.ts (SQLCipher key)
  └─ api.ts ── authStore (JWT)

useSync (hook)
  ├─ syncStore (zustand)
  ├─ sync.ts ── db.ts ── crypto.ts
  ├─ sync.ts ── api.ts ── authStore
  └─ NetInfo

useVoice (hook)
  ├─ uiStore (zustand)
  ├─ cardStore (zustand)
  └─ websocket.ts

useAuth (hook)
  ├─ authStore (zustand)
  ├─ crypto.ts (device ID)
  ├─ db.ts (clearAllData)
  ├─ sync.ts (background sync registration)
  └─ notifications.ts (push token registration)

api.ts (Axios)
  ├─ authStore (read tokens)
  ├─ syncStore (server health)
  └─ Zustand getState (outside React)

sync.ts (Background task)
  ├─ db.ts (CRUD)
  ├─ api.ts (POST /sync)
  ├─ syncStore (version tracking)
  ├─ authStore (device_id)
  └─ expo-background-fetch / expo-task-manager

db.ts (op-sqlite)
  └─ crypto.ts (getOrCreateEncryptionKey)
```

## Store Structure

### cardStore
```typescript
{
  cards: DecisionCard[];           // Current queue, urgency-sorted
  currentIndex: number;            // Position in queue
  batchSize: number;               // Last loaded batch size
  isLoading: boolean;

  // Selectors (getCurrentCard, getPendingCount, getProgress)
  // Actions: loadBatch, nextCard, skipCard, decideCurrent, updateCardState, removeCard, resetQueue
}
```

### syncStore
```typescript
{
  isSyncing: boolean;
  lastSuccessfulSync: number | null;   // Unix timestamp
  lastSyncAttempt: number | null;
  lastSyncVersion: number;             // Server version watermark
  pendingUploads: number;              // sync_queue row count
  serverHealthy: boolean;
  networkAvailable: boolean;
  realtimeConnected: boolean;          // WebSocket status
  syncError: string | null;

  // Computed: isOnline, hasPendingWork, timeSinceLastSync
}
```

### authStore
```typescript
{
  tokens: AuthTokens | null;
  deviceId: string | null;
  isAuthenticated: boolean;
  isHydrated: boolean;                 // AsyncStorage restore complete
  securityStatus: SecurityStatus | null;

  // Persisted to AsyncStorage: tokens, deviceId
  // Cleared on logout: tokens, deviceId, wipes encryption key
}
```

### uiStore
```typescript
{
  themeMode: 'light' | 'dark' | 'system';
  colorScheme: 'light' | 'dark';
  voiceState: VoiceModeState;          // Phase machine state
  isVoiceModeActive: boolean;
  currentScreen: string;
  toast: { message, type, visible } | null;
  hasCompletedOnboarding: boolean;

  // Effective theme computed from system Appearance
}
```

## Sync Protocol

1. **Local change** → DB write + sync_queue insert → immediate if online
2. **Background sync** (every 15 min) → gather sync_queue → POST /sync
3. **Server response** → upsert new/updated cards, remove deleted, clear accepted
4. **Rejected changes** → accept server state, dequeue
5. **Conflict resolution** → server_version wins

## Voice Mode State Machine

```
intro → listening → transcribing → drafting → confirming → sending → undo_window → (stop)
                                                              ↑                           |
                                                              └────── undo ───────────────┘
```

- **undo_window**: 5-second grace period to revert a sent draft
- **WebSocket**: Real-time draft updates during sending sessions

## Security Invariants

| Invariant | Implementation |
|---|---|
| SQLCipher encryption | `getOrCreateEncryptionKey()` → `open({ encryptionKey })` |
| No raw email bodies | `db.ts` schema has no email body column; source fetched transiently via `GET /cards/:id/source` |
| JWT auto-refresh | Axios response interceptor: 401 → refresh → retry |
| Secure key storage | `expo-secure-store` with `WHEN_UNLOCKED` accessibility |
| Local data wipe on logout | `clearAllData()` + `deleteEncryptionKey()` |
