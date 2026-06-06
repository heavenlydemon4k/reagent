// Decision Stack — Account Manager Component
// Settings screen section for managing multiple connected email accounts.
// Lists accounts with provider icons, supports swipe-to-disconnect, and
// provides an "Add Account" flow with Unified View toggle.

import React, { useCallback, useState } from "react";
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  ScrollView,
  Alert,
  ActivityIndicator,
} from "react-native";
import { Type, Space, Colors } from "../../styles/cardStyles";
import { useTheme } from "../../hooks/useTheme";
import { useAccounts } from "../../hooks/useAccounts";
import type { EmailAccount } from "../../types/cards";

// ============================================================================
// PROVIDER ICONS (inline SVG as React Native Views)
// ============================================================================

const ProviderIcon: React.FC<{ provider: EmailAccount["provider"]; size?: number }> = ({
  provider,
  size = 20,
}) => {
  if (provider === "google") {
    return (
      <View
        style={[
          styles.providerIcon,
          { width: size, height: size, backgroundColor: "#1A73E8" },
        ]}
      >
        <Text style={[styles.providerLetter, { fontSize: size * 0.55 }]}>G</Text>
      </View>
    );
  }

  return (
    <View
      style={[
        styles.providerIcon,
        { width: size, height: size, backgroundColor: "#0078D4" },
      ]}
    >
      <Text style={[styles.providerLetter, { fontSize: size * 0.55 }]}>O</Text>
    </View>
  );
};

// ============================================================================
// ACCOUNT ROW
// ============================================================================

interface AccountRowProps {
  account: EmailAccount;
  isActive: boolean;
  onDisconnect: (accountId: string) => void;
  onSetActive: (accountId: string) => void | Promise<void>;
  isDisconnecting: boolean;
  colors: {
    textPrimary: string;
    textSecondary: string;
    textTertiary: string;
    surface: string;
    border: string;
    error: string;
    primary: string;
  };
}

/**
 * AccountRow — Individual account row with swipe-to-disconnect gesture.
 *
 * Layout:
 *   [Provider Icon]  [Email address]      [Active dot]
 *   [Gmail/Outlook]  [Account subtitle]   [Disconnect btn]
 */
const AccountRow: React.FC<AccountRowProps> = ({
  account,
  isActive,
  onDisconnect,
  onSetActive,
  isDisconnecting,
  colors,
}) => {
  const [showDisconnect, setShowDisconnect] = useState(false);

  const handleDisconnect = useCallback(() => {
    Alert.alert(
      "Disconnect Account",
      `Remove ${account.email} from Decision Stack? You'll need to reconnect it to see decisions from this account.`,
      [
        { text: "Cancel", style: "cancel", onPress: () => setShowDisconnect(false) },
        {
          text: "Disconnect",
          style: "destructive",
          onPress: () => onDisconnect(account.id),
        },
      ]
    );
  }, [account, onDisconnect]);

  return (
    <View
      style={[
        styles.accountRow,
        {
          backgroundColor: colors.surface,
          borderColor: colors.border,
          borderWidth: 1,
        },
      ]}
    >
      <TouchableOpacity
        style={styles.accountRowMain}
        onPress={() => onSetActive(account.id)}
        activeOpacity={0.7}
      >
        {/* Provider icon */}
        <ProviderIcon provider={account.provider} />

        {/* Email + provider label */}
        <View style={styles.accountInfo}>
          <Text style={[styles.accountEmail, { color: colors.textPrimary }]}>
            {account.email}
          </Text>
          <Text style={[styles.accountProvider, { color: colors.textTertiary }]}>
            {account.provider === "google" ? "Gmail" : "Outlook"}
          </Text>
        </View>

        {/* Active indicator + disconnect toggle */}
        <View style={styles.accountActions}>
          {isActive && (
            <View style={[styles.activeDot, { backgroundColor: colors.primary }]} />
          )}
          <TouchableOpacity
            onPress={() => setShowDisconnect(!showDisconnect)}
            style={styles.moreButton}
            hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
          >
            <Text style={[styles.moreDots, { color: colors.textTertiary }]}>
              {showDisconnect ? "✕" : "⋯"}
            </Text>
          </TouchableOpacity>
        </View>
      </TouchableOpacity>

      {/* Disconnect row — shown when "⋯" tapped */}
      {showDisconnect && (
        <TouchableOpacity
          style={[styles.disconnectRow, { borderTopColor: colors.border }]}
          onPress={handleDisconnect}
          disabled={isDisconnecting}
          activeOpacity={0.7}
        >
          {isDisconnecting ? (
            <ActivityIndicator size="small" color={colors.error} />
          ) : (
            <Text style={[styles.disconnectText, { color: colors.error }]}>
              Disconnect this account
            </Text>
          )}
        </TouchableOpacity>
      )}
    </View>
  );
};

// ============================================================================
// ADD ACCOUNT BUTTON
// ============================================================================

interface AddAccountButtonProps {
  provider: "google" | "microsoft";
  onPress: (provider: "google" | "microsoft") => void;
  isLoading: boolean;
  colors: {
    textSecondary: string;
    border: string;
    surface: string;
    primary: string;
  };
}

const AddAccountButton: React.FC<AddAccountButtonProps> = ({
  provider,
  onPress,
  isLoading,
  colors,
}) => {
  const label = provider === "google" ? "Add Gmail Account" : "Add Outlook Account";

  return (
    <TouchableOpacity
      style={[
        styles.addButton,
        {
          backgroundColor: colors.surface,
          borderColor: colors.border,
          borderWidth: 1,
        },
      ]}
      onPress={() => onPress(provider)}
      disabled={isLoading}
      activeOpacity={0.7}
    >
      <ProviderIcon provider={provider} size={18} />
      <Text style={[styles.addButtonText, { color: colors.textSecondary }]}>
        {isLoading ? "Connecting…" : label}
      </Text>
      {isLoading && (
        <ActivityIndicator
          size="small"
          color={colors.primary}
          style={styles.addButtonSpinner}
        />
      )}
    </TouchableOpacity>
  );
};

// ============================================================================
// MAIN COMPONENT — AccountManager
// ============================================================================

export interface AccountManagerProps {
  /** Called when an OAuth URL is ready to be opened */
  onOpenOAuthUrl?: (url: string) => void;
  /** Called when accounts change (for parent to refresh batch) */
  onAccountsChanged?: () => void;
}

/**
 * AccountManager — Settings screen section for multi-account management
 *
 * Features:
 *   - Lists all connected accounts with provider icons
 *   - Tap account to set as active (filtered view)
 *   - "⋯" tap → reveals disconnect option
 *   - "Add Account" buttons for Google and Microsoft OAuth
 *   - Unified View toggle (show all accounts combined)
 *
 * Unified View (default): activeAccountId = null
 *   → All decisions from all accounts shown in one stack
 * Filtered View: activeAccountId = "<account-id>"
 *   → Only decisions from that account shown
 */
export const AccountManager: React.FC<AccountManagerProps> = ({
  onOpenOAuthUrl,
  onAccountsChanged,
}) => {
  const { colors } = useTheme();
  const {
    accounts,
    activeAccountId,
    isLoading,
    hasMultipleAccounts,
    refreshAccounts,
    addAccount,
    removeAccount,
    setActiveAccount,
    isUnifiedView,
  } = useAccounts();

  const [disconnectingId, setDisconnectingId] = useState<string | null>(null);
  const [addingProvider, setAddingProvider] = useState<"google" | "microsoft" | null>(null);

  // ── Handlers ──────────────────────────────────────────────────────

  const handleSetActive = useCallback(
    async (accountId: string) => {
      // Toggle: if already active, switch to unified view
      if (activeAccountId === accountId) {
        await setActiveAccount(null);
      } else {
        await setActiveAccount(accountId);
      }
      onAccountsChanged?.();
    },
    [activeAccountId, setActiveAccount, onAccountsChanged]
  );

  const handleSetUnified = useCallback(async () => {
    await setActiveAccount(null);
    onAccountsChanged?.();
  }, [setActiveAccount, onAccountsChanged]);

  const handleDisconnect = useCallback(
    async (accountId: string) => {
      setDisconnectingId(accountId);
      try {
        await removeAccount(accountId);
        onAccountsChanged?.();
      } finally {
        setDisconnectingId(null);
      }
    },
    [removeAccount, onAccountsChanged]
  );

  const handleAddAccount = useCallback(
    async (provider: "google" | "microsoft") => {
      setAddingProvider(provider);
      try {
        const authUrl = await addAccount(provider);
        onOpenOAuthUrl?.(authUrl);
      } catch {
        // Error is already set in the hook
      } finally {
        setAddingProvider(null);
      }
    },
    [addAccount, onOpenOAuthUrl]
  );

  // ── Render ────────────────────────────────────────────────────────

  return (
    <View style={styles.container}>
      {/* Header */}
      <View style={styles.header}>
        <Text style={[styles.headerTitle, { color: colors.textPrimary }]}>
          Email Accounts
        </Text>
        {isLoading && (
          <ActivityIndicator size="small" color={colors.primary} />
        )}
      </View>

      {/* Subtitle */}
      <Text style={[styles.subtitle, { color: colors.textSecondary }]}>
        {accounts.length === 0
          ? "Connect your email accounts to start clearing decisions"
          : hasMultipleAccounts
          ? "Tap an account to filter decisions, or use Unified View to see all"
          : "Connect another account to manage decisions across multiple inboxes"}
      </Text>

      {/* Unified View Toggle — shown when 2+ accounts */}
      {hasMultipleAccounts && (
        <TouchableOpacity
          style={[
            styles.unifiedRow,
            {
              backgroundColor: isUnifiedView
                ? colors.primary + "18"
                : colors.surface,
              borderColor: isUnifiedView ? colors.primary : colors.border,
              borderWidth: 2,
            },
          ]}
          onPress={handleSetUnified}
          activeOpacity={0.8}
        >
          <View style={styles.unifiedLeft}>
            <View
              style={[
                styles.unifiedCheck,
                {
                  backgroundColor: isUnifiedView ? colors.primary : colors.border,
                },
              ]}
            >
              {isUnifiedView && (
                <Text style={styles.unifiedCheckmark}>✓</Text>
              )}
            </View>
            <View>
              <Text
                style={[
                  styles.unifiedTitle,
                  { color: colors.textPrimary },
                ]}
              >
                Unified View
              </Text>
              <Text
                style={[
                  styles.unifiedDescription,
                  { color: colors.textSecondary },
                ]}
              >
                Show decisions from all accounts combined
              </Text>
            </View>
          </View>
        </TouchableOpacity>
      )}

      {/* Account list */}
      <View style={styles.accountList}>
        {accounts.map((account) => (
          <AccountRow
            key={account.id}
            account={account}
            isActive={activeAccountId === account.id}
            onDisconnect={handleDisconnect}
            onSetActive={handleSetActive}
            isDisconnecting={disconnectingId === account.id}
            colors={{
              textPrimary: colors.textPrimary,
              textSecondary: colors.textSecondary,
              textTertiary: colors.textTertiary,
              surface: colors.surface,
              border: colors.border,
              error: colors.error,
              primary: colors.primary,
            }}
          />
        ))}
      </View>

      {/* Add Account buttons */}
      <View style={styles.addButtonsContainer}>
        <Text style={[styles.addButtonsLabel, { color: colors.textTertiary }]}>
          Add Account
        </Text>
        <View style={styles.addButtonsRow}>
          <AddAccountButton
            provider="google"
            onPress={handleAddAccount}
            isLoading={addingProvider === "google"}
            colors={{
              textSecondary: colors.textSecondary,
              border: colors.border,
              surface: colors.surface,
              primary: colors.primary,
            }}
          />
          <AddAccountButton
            provider="microsoft"
            onPress={handleAddAccount}
            isLoading={addingProvider === "microsoft"}
            colors={{
              textSecondary: colors.textSecondary,
              border: colors.border,
              surface: colors.surface,
              primary: colors.primary,
            }}
          />
        </View>
      </View>

      {/* Refresh button */}
      {accounts.length > 0 && (
        <TouchableOpacity
          style={styles.refreshButton}
          onPress={refreshAccounts}
          disabled={isLoading}
          activeOpacity={0.7}
        >
          <Text style={[styles.refreshText, { color: colors.textTertiary }]}>
            Refresh account list
          </Text>
        </TouchableOpacity>
      )}
    </View>
  );
};

// ============================================================================
// STYLES
// ============================================================================

const styles = StyleSheet.create({
  container: {
    paddingVertical: Space.md,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: Space.lg,
    marginBottom: Space.xs,
  },
  headerTitle: {
    ...Type.title,
    fontSize: 20,
  },
  subtitle: {
    ...Type.caption,
    paddingHorizontal: Space.lg,
    marginBottom: Space.md,
  },
  // ── Unified View Toggle ───────────────────────────────────────────
  unifiedRow: {
    marginHorizontal: Space.lg,
    marginBottom: Space.md,
    paddingVertical: Space.md,
    paddingHorizontal: Space.md,
    borderRadius: 14,
    flexDirection: "row",
    alignItems: "center",
  },
  unifiedLeft: {
    flexDirection: "row",
    alignItems: "center",
    flex: 1,
  },
  unifiedCheck: {
    width: 22,
    height: 22,
    borderRadius: 11,
    alignItems: "center",
    justifyContent: "center",
    marginRight: Space.sm,
  },
  unifiedCheckmark: {
    fontSize: 13,
    fontWeight: "700",
    color: "#FFFFFF",
  },
  unifiedTitle: {
    ...Type.bodyBold,
  },
  unifiedDescription: {
    ...Type.caption,
    marginTop: 2,
  },
  // ── Account List ──────────────────────────────────────────────────
  accountList: {
    gap: Space.sm,
    paddingHorizontal: Space.lg,
  },
  accountRow: {
    borderRadius: 14,
    overflow: "hidden",
  },
  accountRowMain: {
    flexDirection: "row",
    alignItems: "center",
    paddingVertical: Space.md - 2,
    paddingHorizontal: Space.md,
  },
  accountInfo: {
    flex: 1,
    marginLeft: Space.sm,
  },
  accountEmail: {
    ...Type.bodyBold,
    fontSize: 14,
  },
  accountProvider: {
    ...Type.micro,
    marginTop: 1,
  },
  accountActions: {
    flexDirection: "row",
    alignItems: "center",
  },
  activeDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    marginRight: Space.sm,
  },
  moreButton: {
    padding: Space.xs,
  },
  moreDots: {
    ...Type.caption,
    fontSize: 16,
    lineHeight: 18,
  },
  disconnectRow: {
    paddingVertical: Space.sm + 2,
    paddingHorizontal: Space.md,
    borderTopWidth: 1,
    alignItems: "center",
  },
  disconnectText: {
    ...Type.captionBold,
  },
  // ── Provider Icon ─────────────────────────────────────────────────
  providerIcon: {
    borderRadius: 6,
    alignItems: "center",
    justifyContent: "center",
  },
  providerLetter: {
    fontWeight: "700",
    color: "#FFFFFF",
  },
  // ── Add Account ───────────────────────────────────────────────────
  addButtonsContainer: {
    marginTop: Space.lg,
    paddingHorizontal: Space.lg,
  },
  addButtonsLabel: {
    ...Type.captionBold,
    textTransform: "uppercase",
    marginBottom: Space.sm,
  },
  addButtonsRow: {
    flexDirection: "row",
    gap: Space.sm,
  },
  addButton: {
    flex: 1,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    paddingVertical: Space.sm + 4,
    paddingHorizontal: Space.sm,
    borderRadius: 12,
  },
  addButtonText: {
    ...Type.captionBold,
    marginLeft: Space.xs,
  },
  addButtonSpinner: {
    marginLeft: Space.xs,
  },
  // ── Refresh ───────────────────────────────────────────────────────
  refreshButton: {
    alignItems: "center",
    marginTop: Space.lg,
    paddingVertical: Space.sm,
  },
  refreshText: {
    ...Type.caption,
  },
});
