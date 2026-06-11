// Decision Stack — Auth Store (Zustand)
// Manages JWT tokens, device identity, authentication state,
// and multi-account switching for users with multiple email addresses.

import { create } from 'zustand';
import AsyncStorage from '@react-native-async-storage/async-storage';
import type { AuthTokens, SecurityStatus, EmailAccount } from '../types/cards';

const STORAGE_KEYS = {
  tokens: 'ds_auth_tokens',
  deviceId: 'ds_device_id',
  accounts: 'ds_email_accounts',
  activeAccountId: 'ds_active_account_id',
};

export interface AuthStore {
  // ── State ──────────────────────────────────────────────────────
  tokens: AuthTokens | null;
  deviceId: string | null;
  isAuthenticated: boolean;
  isHydrated: boolean;
  securityStatus: SecurityStatus | null;

  // Multi-account state
  accounts: EmailAccount[];
  activeAccountId: string | null; // null = unified view (all accounts)

  // ── Actions ────────────────────────────────────────────────────
  setTokens: (tokens: AuthTokens) => void;
  setDeviceId: (deviceId: string) => void;
  setSecurityStatus: (status: SecurityStatus) => void;
  refreshAccessToken: (accessToken: string, expiresAt: number) => void;
  clearAuth: () => void;
  hydrate: () => Promise<void>;

  // Multi-account actions
  setAccounts: (accounts: EmailAccount[]) => void;
  addAccount: (account: EmailAccount) => void;
  removeAccount: (accountId: string) => void;
  setActiveAccount: (accountId: string | null) => void;
  getActiveAccount: () => EmailAccount | null;
  getAccountById: (accountId: string) => EmailAccount | undefined;
}

export const useAuthStore = create<AuthStore>((set, get) => ({
  // ── Initial State ──────────────────────────────────────────────
  tokens: null,
  deviceId: null,
  isAuthenticated: false,
  isHydrated: false,
  securityStatus: null,
  accounts: [],
  activeAccountId: null,

  // ── Actions ────────────────────────────────────────────────────

  setTokens: (tokens) => {
    AsyncStorage.setItem(STORAGE_KEYS.tokens, JSON.stringify(tokens)).catch(
      () => {}
    );
    set({
      tokens,
      isAuthenticated: true,
    });
  },

  setDeviceId: (deviceId) => {
    AsyncStorage.setItem(STORAGE_KEYS.deviceId, deviceId).catch(() => {});
    set({ deviceId });
  },

  setSecurityStatus: (status) => set({ securityStatus: status }),

  refreshAccessToken: (accessToken, expiresAt) => {
    const current = get().tokens;
    if (!current) return;

    const updated: AuthTokens = {
      ...current,
      access_token: accessToken,
      expires_at: expiresAt,
    };

    AsyncStorage.setItem(STORAGE_KEYS.tokens, JSON.stringify(updated)).catch(
      () => {}
    );
    set({ tokens: updated });
  },

  clearAuth: () => {
    AsyncStorage.multiRemove([
      STORAGE_KEYS.tokens,
      STORAGE_KEYS.deviceId,
      STORAGE_KEYS.accounts,
      STORAGE_KEYS.activeAccountId,
    ]).catch(() => {});
    set({
      tokens: null,
      deviceId: null,
      isAuthenticated: false,
      securityStatus: null,
      accounts: [],
      activeAccountId: null,
    });
  },

  hydrate: async () => {
    try {
      const [tokensJson, deviceId, accountsJson, activeAccountIdJson] =
        await AsyncStorage.multiGet([
          STORAGE_KEYS.tokens,
          STORAGE_KEYS.deviceId,
          STORAGE_KEYS.accounts,
          STORAGE_KEYS.activeAccountId,
        ]);

      const parsedTokens = tokensJson[1]
        ? (JSON.parse(tokensJson[1]) as AuthTokens)
        : null;
      const isExpired = parsedTokens
        ? Date.now() / 1000 > parsedTokens.expires_at
        : true;

      const parsedAccounts = accountsJson[1]
        ? (JSON.parse(accountsJson[1]) as EmailAccount[])
        : [];

      const activeAccountId = activeAccountIdJson[1] ?? null;

      set({
        tokens: isExpired ? null : parsedTokens,
        isAuthenticated: !isExpired && parsedTokens !== null,
        deviceId: deviceId[1],
        accounts: parsedAccounts,
        activeAccountId,
        isHydrated: true,
      });
    } catch {
      set({ isHydrated: true });
    }
  },

  // ── Multi-Account Actions ──────────────────────────────────────

  setAccounts: (accounts) => {
    AsyncStorage.setItem(STORAGE_KEYS.accounts, JSON.stringify(accounts)).catch(
      () => {}
    );
    set({ accounts });
  },

  addAccount: (account) => {
    const current = get().accounts;
    // Prevent duplicates by email
    const filtered = current.filter((a) => a.email !== account.email);
    const updated = [...filtered, account];
    AsyncStorage.setItem(STORAGE_KEYS.accounts, JSON.stringify(updated)).catch(
      () => {}
    );
    set({ accounts: updated });
  },

  removeAccount: (accountId) => {
    const current = get().accounts;
    const updated = current.filter((a) => a.id !== accountId);

    // If removing the active account, reset to unified view
    const wasActive = get().activeAccountId === accountId;
    const newActiveId = wasActive ? null : get().activeAccountId;

    // Edge case: if this was the last account, clear auth entirely
    if (updated.length === 0) {
      get().clearAuth();
      return;
    }

    AsyncStorage.setItem(STORAGE_KEYS.accounts, JSON.stringify(updated)).catch(
      () => {}
    );
    if (wasActive) {
      AsyncStorage.removeItem(STORAGE_KEYS.activeAccountId).catch(() => {});
    }
    set({
      accounts: updated,
      activeAccountId: newActiveId,
    });
  },

  setActiveAccount: (accountId) => {
    if (accountId === null) {
      AsyncStorage.removeItem(STORAGE_KEYS.activeAccountId).catch(() => {});
    } else {
      AsyncStorage.setItem(STORAGE_KEYS.activeAccountId, accountId).catch(
        () => {}
      );
    }
    set({ activeAccountId: accountId });
  },

  getActiveAccount: () => {
    const { activeAccountId, accounts } = get();
    if (!activeAccountId) return null;
    return accounts.find((a) => a.id === activeAccountId) ?? null;
  },

  getAccountById: (accountId) => {
    return get().accounts.find((a) => a.id === accountId);
  },
}));
