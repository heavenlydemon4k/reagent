// Decision Stack — Sync Status and Trigger Hook
// Provides sync control and status monitoring for UI components

import { useCallback, useEffect, useRef } from 'react';
import { AppState } from 'react-native';
import { useSyncStore } from '@stores/syncStore';
import { performSync, registerBackgroundSync, unregisterBackgroundSync } from '@services/sync';
import NetInfo from '@react-native-community/netinfo';

export interface UseSyncReturn {
  // State
  isSyncing: boolean;
  isOnline: boolean;
  hasPendingWork: boolean;
  pendingUploads: number;
  serverHealthy: boolean;
  lastSyncTimestamp: number | null;
  syncError: string | null;

  // Actions
  triggerSync: () => Promise<void>;
  registerBackground: () => Promise<void>;
  unregisterBackground: () => Promise<void>;
}

export function useSync(): UseSyncReturn {
  const store = useSyncStore();
  const appStateRef = useRef(AppState.currentState);

  // Monitor network connectivity
  useEffect(() => {
    const unsubscribe = NetInfo.addEventListener((state) => {
      const isConnected =
        state.isConnected != null &&
        state.isInternetReachable !== false;

      store.setNetworkAvailable(isConnected);

      // Auto-sync when coming back online
      if (isConnected && store.hasPendingWork()) {
        triggerSync().catch(() => {});
      }
    });

    return unsubscribe;
  }, []);

  // Sync on app foreground
  useEffect(() => {
    const subscription = AppState.addEventListener('change', (nextState) => {
      const prevState = appStateRef.current;
      appStateRef.current = nextState;

      if (prevState === 'background' && nextState === 'active') {
        // App came to foreground — trigger sync if we have pending work
        if (store.hasPendingWork()) {
          triggerSync().catch(() => {});
        }
      }
    });

    return () => {
      subscription.remove();
    };
  }, []);

  /**
   * Manually trigger a sync cycle.
   */
  const triggerSync = useCallback(async () => {
    if (store.isSyncing) return;
    await performSync();
  }, [store.isSyncing]);

  /**
   * Register background sync task.
   */
  const registerBackground = useCallback(async () => {
    await registerBackgroundSync();
  }, []);

  /**
   * Unregister background sync task.
   */
  const unregisterBackground = useCallback(async () => {
    await unregisterBackgroundSync();
  }, []);

  return {
    isSyncing: store.isSyncing,
    isOnline: store.networkAvailable && store.serverHealthy,
    hasPendingWork: store.hasPendingWork(),
    pendingUploads: store.pendingUploads,
    serverHealthy: store.serverHealthy,
    lastSyncTimestamp: store.lastSuccessfulSync,
    syncError: store.syncError,

    triggerSync,
    registerBackground,
    unregisterBackground,
  };
}
