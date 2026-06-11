// Decision Stack — Auth State and Token Refresh Hook
// Manages authentication lifecycle, token refresh, and device identity

import { useCallback, useEffect, useRef } from 'react';
import { AppState } from 'react-native';
import { useAuthStore } from '@stores/authStore';
import { openDatabase, clearAllData } from '@services/db';
import { deleteEncryptionKey } from '@services/crypto';
import { generateDeviceId } from '@services/crypto';
import { registerBackgroundSync, unregisterBackgroundSync } from '@services/sync';
import { setupNotifications, registerPushTokenWithServer } from '@services/notifications';
import { api } from '@services/api';
import type { AuthTokens } from '../types/cards';

// Token refresh buffer — refresh when within 5 min of expiry
const TOKEN_REFRESH_BUFFER_MS = 5 * 60 * 1000;

export interface UseAuthReturn {
  // State
  isAuthenticated: boolean;
  isHydrated: boolean;
  deviceId: string | null;

  // Actions
  login: (tokens: AuthTokens) => Promise<void>;
  logout: () => Promise<void>;
  refreshIfNeeded: () => Promise<boolean>;
}

export function useAuth(): UseAuthReturn {
  const store = useAuthStore();
  const refreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Hydrate auth state on mount
  useEffect(() => {
    store.hydrate();
  }, []);

  // Schedule token refresh based on expiry
  useEffect(() => {
    if (!store.tokens || !store.isAuthenticated) {
      if (refreshTimerRef.current) {
        clearTimeout(refreshTimerRef.current);
        refreshTimerRef.current = null;
      }
      return;
    }

    const expiresAt = store.tokens.expires_at * 1000;
    const refreshAt = expiresAt - TOKEN_REFRESH_BUFFER_MS;
    const delay = refreshAt - Date.now();

    if (delay <= 0) {
      // Token is near expiry — refresh now
      refreshIfNeeded();
      return;
    }

    refreshTimerRef.current = setTimeout(() => {
      refreshIfNeeded();
    }, delay);

    return () => {
      if (refreshTimerRef.current) {
        clearTimeout(refreshTimerRef.current);
      }
    };
  }, [store.tokens, store.isAuthenticated]);

  // Refresh on app foreground (in case timer missed while backgrounded)
  useEffect(() => {
    const subscription = AppState.addEventListener('change', (nextState) => {
      if (nextState === 'active' && store.isAuthenticated) {
        refreshIfNeeded();
      }
    });

    return () => {
      subscription.remove();
    };
  }, [store.isAuthenticated]);

  /**
   * Complete login flow: store tokens, init device, setup services.
   */
  const login = useCallback(
    async (tokens: AuthTokens) => {
      // 1. Store tokens
      store.setTokens(tokens);

      // 2. Generate or retrieve device ID
      let deviceId = store.deviceId;
      if (!deviceId) {
        deviceId = await generateDeviceId();
        store.setDeviceId(deviceId);
      }

      // 3. Open encrypted database
      await openDatabase();

      // 4. Setup background sync
      await registerBackgroundSync();

      // 5. Setup push notifications
      await setupNotifications();
      await registerPushTokenWithServer();
    },
    [store]
  );

  /**
   * Complete logout flow: clear data, unregister services, wipe secure storage.
   */
  const logout = useCallback(async () => {
    // 1. Unregister background sync
    await unregisterBackgroundSync();

    // 2. Clear local database
    try {
      await openDatabase();
      clearAllData();
    } catch {
      // DB may not be open
    }

    // 3. Delete encryption key (irreversible — data is lost)
    await deleteEncryptionKey();

    // 4. Clear auth state
    store.clearAuth();

    // 5. Cancel pending token refresh
    if (refreshTimerRef.current) {
      clearTimeout(refreshTimerRef.current);
      refreshTimerRef.current = null;
    }
  }, [store]);

  /**
   * Refresh access token if within buffer window of expiry.
   */
  const refreshIfNeeded = useCallback(async (): Promise<boolean> => {
    const tokens = useAuthStore.getState().tokens;
    if (!tokens) return false;

    const expiresAt = tokens.expires_at * 1000;
    const shouldRefresh = Date.now() >= expiresAt - TOKEN_REFRESH_BUFFER_MS;

    if (!shouldRefresh) return true; // Token still valid

    try {
      const response = await api.post<{
        access_token: string;
        refresh_token: string;
        expires_at: number;
      }>('/auth/refresh', {
        refresh_token: tokens.refresh_token,
      });

      store.setTokens(response.data);
      return true;
    } catch {
      // Refresh failed — clear auth
      store.clearAuth();
      return false;
    }
  }, [store]);

  return {
    isAuthenticated: store.isAuthenticated,
    isHydrated: store.isHydrated,
    deviceId: store.deviceId,

    login,
    logout,
    refreshIfNeeded,
  };
}
