// Decision Stack — Account Badge Component
// Displays the source email account on a DecisionCard with provider-colored badge.
// Small, non-intrusive tag showing which account a decision originated from.

import React from "react";
import { View, Text, StyleSheet } from "react-native";
import { Type, Space, Colors } from "../../styles/cardStyles";
import type { EmailAccount } from "../../types/cards";

// ============================================================================
// PROVIDER COLORS
// ============================================================================

const PROVIDER_COLORS = {
  google: {
    bg: "#E8F0FE",      // Light blue background
    text: "#1A73E8",    // Google blue
    border: "#D2E3FC",
    label: "Gmail",
  },
  microsoft: {
    bg: "#E8F4FD",      // Light sky background
    text: "#0078D4",    // Microsoft blue
    border: "#CCE4F6",
    label: "Outlook",
  },
} as const;

// ============================================================================
// TYPES
// ============================================================================

export interface AccountBadgeProps {
  /** The email account this card originated from */
  account: Pick<EmailAccount, "id" | "email" | "provider">;
  /** Optional short display name (e.g., "work", "personal") */
  displayName?: string;
  /** Size variant */
  size?: "sm" | "md";
  /** Optional override for the provider label (e.g., show just "G" or "O") */
  showProviderIcon?: boolean;
}

/**
 * AccountBadge — Small colored tag indicating source email account
 *
 * - Google accounts: blue "G" + Gmail label
 * - Microsoft accounts: blue square + Outlook label
 * - Compact: just the colored dot + provider icon
 *
 * Used on DecisionCard header and account listing rows.
 */
export const AccountBadge: React.FC<AccountBadgeProps> = ({
  account,
  displayName,
  size = "sm",
  showProviderIcon = true,
}) => {
  const colors = PROVIDER_COLORS[account.provider];
  const label = displayName || colors.label;

  return (
    <View
      style={[
        styles.badge,
        {
          backgroundColor: colors.bg,
          borderColor: colors.border,
          paddingVertical: size === "sm" ? 2 : Space.xs,
          paddingHorizontal: size === "sm" ? Space.xs : Space.sm,
        },
      ]}
    >
      {/* Provider icon dot */}
      {showProviderIcon && (
        <View
          style={[
            styles.providerDot,
            { backgroundColor: colors.text },
          ]}
        >
          <Text style={styles.providerInitial}>
            {account.provider === "google" ? "G" : "O"}
          </Text>
        </View>
      )}

      {/* Account label */}
      <Text style={[styles.label, { color: colors.text }, size === "sm" && styles.labelSmall]}>
        {label}
      </Text>

      {/* Email address (md size only) */}
      {size === "md" && (
        <Text style={[styles.email, { color: colors.text }]}>
          {" · "}{account.email}
        </Text>
      )}
    </View>
  );
};

// ============================================================================
// ACCOUNT BREAKDOWN BADGES
// ============================================================================

export interface AccountBreakdownBadgeProps {
  /** Map of account ID to count + account info */
  breakdown: Record<
    string,
    {
      account: Pick<EmailAccount, "id" | "email" | "provider">;
      count: number;
      displayName?: string;
    }
  >;
  /** Total decision count */
  total: number;
}

/**
 * AccountBreakdownBadges — Row of colored badges showing per-account counts
 *
 * Example: "7 decisions · 3 from work (Gmail) · 4 from personal (Outlook)"
 *
 * Used on BatchGateScreen to give users visibility into account distribution.
 */
export const AccountBreakdownBadges: React.FC<AccountBreakdownBadgeProps> = ({
  breakdown,
  total,
}) => {
  const entries = Object.values(breakdown);
  if (entries.length <= 1) return null;

  return (
    <View style={styles.breakdownContainer}>
      <Text style={[styles.breakdownTotal, { color: Colors.textTertiary }]}>
        {total} decision{total !== 1 ? "s" : ""}
      </Text>
      {entries.map(({ account, count, displayName }) => {
        const colors = PROVIDER_COLORS[account.provider];
        return (
          <View key={account.id} style={styles.breakdownRow}>
            <View style={[styles.countDot, { backgroundColor: colors.text }]} />
            <Text style={[styles.breakdownText, { color: Colors.textSecondary }]}>
              {count} from {displayName || account.email.split("@")[0]} ({colors.label})
            </Text>
          </View>
        );
      })}
    </View>
  );
};

// ============================================================================
// STYLES
// ============================================================================

const styles = StyleSheet.create({
  badge: {
    flexDirection: "row",
    alignItems: "center",
    alignSelf: "flex-start",
    borderRadius: 8,
    borderWidth: 1,
    marginTop: Space.xs,
  },
  providerDot: {
    width: 14,
    height: 14,
    borderRadius: 4,
    alignItems: "center",
    justifyContent: "center",
    marginRight: 4,
  },
  providerInitial: {
    fontSize: 8,
    fontWeight: "700",
    color: "#FFFFFF",
  },
  label: {
    ...Type.micro,
    fontWeight: "600",
  },
  labelSmall: {
    fontSize: 10,
  },
  email: {
    ...Type.micro,
    opacity: 0.8,
  },
  // ── Breakdown ──────────────────────────────────────────────────────
  breakdownContainer: {
    alignItems: "center",
    marginTop: Space.md,
  },
  breakdownTotal: {
    ...Type.captionBold,
    marginBottom: Space.xs,
  },
  breakdownRow: {
    flexDirection: "row",
    alignItems: "center",
    marginTop: 2,
  },
  countDot: {
    width: 6,
    height: 6,
    borderRadius: 3,
    marginRight: Space.xs,
  },
  breakdownText: {
    ...Type.caption,
  },
});
