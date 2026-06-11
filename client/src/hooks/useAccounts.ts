// Decision Stack — Multi-Account Management Hook
// Manages connected email accounts: fetching, adding, removing,
// switching active account, and syncing with local SQLite.

import { useCallback, useState } from 'react';
import { useAuthStore } from '@stores/authStore';
import {
  getConnectedAccounts,
  initiateOAuth,
  disconnectAccount,
  setServerActiveAccount,
} from '@services/api';
import {
  getAllAccounts,
  deleteAccount,
} from '@services/accountDb';
import type { EmailAccount } from '../types/cards';

export interface UseAccountsReturn {
  // State
  accounts: EmailAccount[];
  activeAccountId: string | null;
  isLoading: boolean;
  error: string | null;

  // Computed
  hasMultipleAccounts: boolean;
  accountCount: number;
  activeAccount: EmailAccount | null;

  // Actions
  refreshAccounts: () => Promise<void>;
  addAccount: (provider: 'google' | 'microsoft') => Promise<string>;
  removeAccount: (accountId: string) => Promise<void>;
  setActiveAccount: (accountId: string | null) => Promise<void>;
  isUnifiedView: boolean;
  getAccountById: (accountId: string) => EmailAccount | undefined;

  // Hydration
  hydrateFromLocalDb: () => Promise<void>;
}

/**
 * useAccounts — Hook for managing multiple email accounts
 *
 * Unified view (default): activeAccountId = null → cards from ALL accounts shown
 * Filtered view: activeAccountId = '...' → only cards from that account shown
 *
 * Account data is persisted in:
 *   1. Server (source of truth)
 *   2. Local SQLite (offline access)
 *   3. AsyncStorage via Zustand (UI reactivity)
 */
export function useAccounts(): UseAccountsReturn {
  const store = useAuthStore();
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Local state mirrors Zustand store for reactivity
  const accounts = store.accounts;
  const activeAccountId = store.activeAccountId;
  const accountCount = accounts.length;
  const hasMultipleAccounts = accounts.length > 1;
  const isUnifiedView = activeAccountId === null;

  const activeAccount =
    accounts.find((a) => a.id === activeAccountId) ?? null;

  /**
   * Hydrate account state from local SQLite database.
   * Called on app startup to ensure accounts are available offline.
   */
  const hydrateFromLocalDb = useCallback(async () => {
    try {
      const localAccounts = getAllAccounts();
      if (localAccounts.length > 0) {
        store.setAccounts(localAccounts);
      }
    } catch {
      // Local DB may not be open yet — will be populated from server
    }
  }, [store]);

  /**
   * Fetch accounts from server and sync to local stores.
   */
  const refreshAccounts = useCallback(async () => {
    setIsLoading(true);
    setError(null);

    try {
      const serverAccounts = await getConnectedAccounts();
      store.setAccounts(serverAccounts);

      // Sync to local SQLite for offline access
      try {
        const { upsertAccounts } = await import('@services/accountDb');
        upsertAccounts(serverAccounts);
      } catch {
        // DB may not be initialized
      }
    } catch (err) {
      const message =
        err instanceof Error ? err.message : 'Failed to fetch accounts';
      setError(message);

      // Fall back to local data
      try {
        const localAccounts = getAllAccounts();
        if (localAccounts.length > 0) {
          store.setAccounts(localAccounts);
        }
      } catch {
        // No local data available
      }
    } finally {
      setIsLoading(false);
    }
  }, [store]);

  /**
   * Initiate OAuth flow to connect a new account.
   * Returns the OAuth URL to open in browser.
   */
  const addAccount = useCallback(
    async (provider: 'google' | 'microsoft'): Promise<string> => {
      setIsLoading(true);
      setError(null);

      try {
        const authUrl = await initiateOAuth(provider);
        return authUrl;
      } catch (err) {
        const message =
          err instanceof Error ? err.message : 'Failed to start OAuth';
        setError(message);
        throw err;
      } finally {
        setIsLoading(false);
      }
    },
    []
  );

  /**
   * Disconnect an account — removes from server, SQLite, and Zustand store.
   * If disconnecting the last account, full logout is triggered by the store.
   */
  const removeAccount = useCallback(
    async (accountId: string) => {
      setIsLoading(true);
      setError(null);

      try {
        // 1. Call server to disconnect
        await disconnectAccount(accountId);

        // 2. Remove from local SQLite
        deleteAccount(accountId);

        // 3. Remove from Zustand store (handles edge cases)
        store.removeAccount(accountId);
      } catch (err) {
        const message =
          err instanceof Error ? err.message : 'Failed to disconnect account';
        setError(message);
        throw err;
      } finally {
        setIsLoading(false);
      }
    },
    [store]
  );

  /**
   * Set the active account for filtered views.
   * null = unified view (all accounts).
   * Persists to server and local storage.
   */
  const setActiveAccount = useCallback(
    async (accountId: string | null) => {
      // Optimistic update
      store.setActiveAccount(accountId);

      // Sync to server
      try {
        await setServerActiveAccount(accountId);
      } catch {
        // Server sync failed — local state is still correct, will retry on next sync
      }
    },
    [store]
  );

  const getAccountById = useCallback(
    (accountId: string) => {
      return accounts.find((a) => a.id === accountId);
    },
    [accounts]
  );

  return {
    accounts,
    activeAccountId,
    isLoading,
    error,
    hasMultipleAccounts,
    accountCount,
    activeAccount,
    refreshAccounts,
    addAccount,
    removeAccount,
    setActiveAccount,
    isUnifiedView,
    getAccountById,
    hydrateFromLocalDb,
  };
}
