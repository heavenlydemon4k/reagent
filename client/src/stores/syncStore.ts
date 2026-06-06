// Decision Stack — Sync State Store (Zustand)
// Tracks sync health, pending uploads, and connection status

import { create } from 'zustand';

export interface SyncStore {
  // ── State ──────────────────────────────────────────────────────
  isSyncing: boolean;
  lastSuccessfulSync: number | null;
  lastSyncAttempt: number | null;
  lastSyncVersion: number;
  pendingUploads: number;
  serverHealthy: boolean;
  networkAvailable: boolean;
  realtimeConnected: boolean;
  syncError: string | null;

  // ── Computed ───────────────────────────────────────────────────
  isOnline: () => boolean;
  hasPendingWork: () => boolean;
  timeSinceLastSync: () => number | null;

  // ── Actions ────────────────────────────────────────────────────
  setIsSyncing: (syncing: boolean) => void;
  setLastSuccessfulSync: (timestamp: number) => void;
  setLastSyncAttempt: (timestamp: number) => void;
  setLastSyncVersion: (version: number) => void;
  setPendingUploads: (count: number) => void;
  setServerHealthy: (healthy: boolean) => void;
  setNetworkAvailable: (available: boolean) => void;
  setRealtimeConnected: (connected: boolean) => void;
  setSyncError: (error: string | null) => void;
  reset: () => void;
}

const initialState = {
  isSyncing: false,
  lastSuccessfulSync: null as number | null,
  lastSyncAttempt: null as number | null,
  lastSyncVersion: 0,
  pendingUploads: 0,
  serverHealthy: true,
  networkAvailable: true,
  realtimeConnected: false,
  syncError: null as string | null,
};

export const useSyncStore = create<SyncStore>((set, get) => ({
  // ── Initial State ──────────────────────────────────────────────
  ...initialState,

  // ── Computed ───────────────────────────────────────────────────

  isOnline: () => {
    const { networkAvailable, serverHealthy } = get();
    return networkAvailable && serverHealthy;
  },

  hasPendingWork: () => {
    const { pendingUploads } = get();
    return pendingUploads > 0;
  },

  timeSinceLastSync: () => {
    const { lastSuccessfulSync } = get();
    if (!lastSuccessfulSync) return null;
    return Date.now() - lastSuccessfulSync;
  },

  // ── Actions ────────────────────────────────────────────────────

  setIsSyncing: (syncing) => set({ isSyncing: syncing }),

  setLastSuccessfulSync: (timestamp) =>
    set({
      lastSuccessfulSync: timestamp,
      syncError: null,
    }),

  setLastSyncAttempt: (timestamp) => set({ lastSyncAttempt: timestamp }),

  setLastSyncVersion: (version) => set({ lastSyncVersion: version }),

  setPendingUploads: (count) => set({ pendingUploads: count }),

  setServerHealthy: (healthy) => set({ serverHealthy: healthy }),

  setNetworkAvailable: (available) => set({ networkAvailable: available }),

  setRealtimeConnected: (connected) => set({ realtimeConnected: connected }),

  setSyncError: (error) => set({ syncError: error }),

  reset: () => set(initialState),
}));
