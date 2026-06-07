import React, { useMemo } from "react";
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  SafeAreaView,
} from "react-native";
import { Type, Space } from "../styles/cardStyles";
import type { BatchInfo, EmailAccount } from "../types/cards";
import { useTheme } from "../hooks/useTheme";
import { useStreak } from "../hooks/useStreak";
import { AccountBreakdownBadges } from "../components/account/AccountBadge";

// ============================================================================
// TYPES
// ============================================================================

interface BatchGateScreenProps {
  batch: BatchInfo;
  onStart: () => void;
  onDismiss: () => void;
  /** Optional account info for each card to show per-account breakdown */
  accountMap?: Record<string, Pick<EmailAccount, "id" | "email" | "provider">>;
  /** Currently active account filter (null = unified view) */
  activeAccountId?: string | null;
}

// ============================================================================
// ACCOUNT BREAKDOWN COMPUTATION
// ============================================================================

interface BreakdownEntry {
  account: Pick<EmailAccount, "id" | "email" | "provider">;
  count: number;
}

function computeAccountBreakdown(
  cards: BatchInfo["cards"],
  accountMap?: Record<string, Pick<EmailAccount, "id" | "email" | "provider">>
): Record<string, BreakdownEntry> | null {
  if (!accountMap) return null;

  const breakdown: Record<string, BreakdownEntry> = {};

  for (const card of cards) {
    const account = accountMap[card.source_account_id];
    if (!account) continue;

    const existing = breakdown[account.id];
    if (existing) {
      existing.count += 1;
    } else {
      breakdown[account.id] = {
        account,
        count: 1,
      };
    }
  }

  // Only show breakdown if decisions come from multiple accounts
  return Object.keys(breakdown).length > 1 ? breakdown : null;
}

// ============================================================================
// COMPONENT
// ============================================================================

/**
 * BatchGateScreen — "N decisions · M min · Start?"
 *
 * Displays a calm, centered gate before the user enters the CardStack.
 * - Shows count + estimated time
 * - Account breakdown badges (multi-account): "7 decisions · 3 from work (Gmail) · 4 from personal (Outlook)"
 * - Urgency hint if any card has urgency_score > 0.8
 * - Streak indicator (flame icon + count) when streak > 0
 * - Active account filter indicator (when viewing single account)
 * - "Start Clearing" primary CTA → navigates to CardStack
 * - "Later" dismiss → backgrounds the app
 * - Theme-aware: adapts to light/dark mode
 */
export const BatchGateScreen: React.FC<BatchGateScreenProps> = ({
  batch,
  onStart,
  onDismiss,
  accountMap,
  activeAccountId,
}) => {
  const { colors } = useTheme();
  const { streak } = useStreak();

  const hasUrgent = useMemo(
    () => batch.cards.some((c) => c.urgency_score > 0.8),
    [batch.cards]
  );

  const urgentCount = useMemo(
    () => batch.cards.filter((c) => c.urgency_score > 0.8).length,
    [batch.cards]
  );

  // Compute per-account breakdown for multi-account display
  const accountBreakdown = useMemo(
    () => computeAccountBreakdown(batch.cards, accountMap),
    [batch.cards, accountMap]
  );

  // Get active account name for filter indicator
  const activeAccountName = useMemo(() => {
    if (!activeAccountId || !accountMap) return null;
    const account = Object.values(accountMap).find(
      (a) => a.id === activeAccountId
    );
    if (!account) return null;
    return account.email.split("@")[0];
  }, [activeAccountId, accountMap]);

  return (
    <SafeAreaView style={[styles.container, { backgroundColor: colors.background }]}>
      {/* Streak indicator */}
      {streak > 0 && (
        <View style={styles.streakHeader}>
          <Text style={styles.streakFlame}>🔥</Text>
          <Text style={[styles.streakText, { color: colors.textSecondary }]}>
            {streak} day{streak !== 1 ? "s" : ""}
          </Text>
        </View>
      )}

      <View style={styles.content}>
        {/* Decision count — large, calm */}
        <Text style={[styles.countText, { color: colors.textPrimary }]}>
          {`${batch.size} decision${batch.size !== 1 ? "s" : ""}`}
        </Text>

        {/* Estimated time */}
        <Text style={[styles.subtitle, { color: colors.textSecondary }]}>
          {`Estimated ${batch.estimated_clear_time_minutes} min`}
        </Text>

        {/* Active account filter indicator */}
        {activeAccountName && (
          <View
            style={[
              styles.filterIndicator,
              { backgroundColor: colors.primaryMuted },
            ]}
          >
            <Text
              style={[
                styles.filterText,
                { color: colors.primary },
              ]}
            >
              {`Viewing: ${activeAccountName}`}
            </Text>
          </View>
        )}

        {/* Account breakdown — multi-account colored badges */}
        {accountBreakdown && (
          <AccountBreakdownBadges
            breakdown={accountBreakdown}
            total={batch.size}
          />
        )}

        {/* Contextual prompt */}
        <Text style={[styles.metaText, { color: colors.textTertiary }]}>
          {"Ready to clear your stack?"}
        </Text>

        {/* Urgency hint */}
        {hasUrgent && (
          <View style={[styles.urgencyContainer, { backgroundColor: colors.errorMuted }]}>
            <View style={[styles.urgencyDot, { backgroundColor: colors.error }]} />
            <Text style={[styles.urgencyHint, { color: colors.error }]}>
              {urgentCount === 1
                ? "1 urgent item"
                : `${urgentCount} urgent items`}
            </Text>
          </View>
        )}

        {/* Primary CTA */}
        <TouchableOpacity
          style={[styles.startButton, { backgroundColor: colors.primary }]}
          onPress={onStart}
          activeOpacity={0.85}
        >
          <Text style={[styles.startButtonText, { color: colors.textInverse }]}>
            Start Clearing
          </Text>
        </TouchableOpacity>

        {/* Dismiss */}
        <TouchableOpacity
          style={styles.dismissButton}
          onPress={onDismiss}
          activeOpacity={0.7}
        >
          <Text style={[styles.dismissText, { color: colors.textTertiary }]}>Later</Text>
        </TouchableOpacity>
      </View>
    </SafeAreaView>
  );
};

// ============================================================================
// STYLES
// ============================================================================

const styles = StyleSheet.create({
  container: {
    flex: 1,
  },
  // ── Streak Header ───────────────────────────────────────────────────────
  streakHeader: {
    position: "absolute",
    top: 16,
    right: Space.lg,
    flexDirection: "row",
    alignItems: "center",
    paddingVertical: Space.xs,
    paddingHorizontal: Space.sm,
  },
  streakFlame: {
    fontSize: 16,
    marginRight: 4,
  },
  streakText: {
    ...Type.captionBold,
    fontSize: 13,
  },
  // ── Content ─────────────────────────────────────────────────────────────
  content: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: Space.lg,
  },
  countText: {
    ...Type.display,
    textAlign: "center",
  },
  subtitle: {
    ...Type.subtitle,
    textAlign: "center",
    marginTop: Space.sm,
  },
  // ── Active Filter Indicator ─────────────────────────────────────────────
  filterIndicator: {
    marginTop: Space.md,
    paddingHorizontal: Space.md,
    paddingVertical: Space.xs + 2,
    borderRadius: 10,
  },
  filterText: {
    ...Type.captionBold,
  },
  // ── Meta / Prompt ───────────────────────────────────────────────────────
  metaText: {
    ...Type.caption,
    textAlign: "center",
    marginTop: Space.md,
  },
  // ── Urgency ─────────────────────────────────────────────────────────────
  urgencyContainer: {
    flexDirection: "row",
    alignItems: "center",
    marginTop: Space.lg,
    paddingHorizontal: Space.md,
    paddingVertical: Space.sm,
    borderRadius: 10,
  },
  urgencyDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    marginRight: Space.sm,
  },
  urgencyHint: {
    ...Type.captionBold,
  },
  // ── CTAs ────────────────────────────────────────────────────────────────
  startButton: {
    paddingVertical: Space.md,
    paddingHorizontal: Space.xl,
    borderRadius: 16,
    marginTop: Space.xl,
    minWidth: 220,
    alignItems: "center",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.2,
    shadowRadius: 8,
    elevation: 3,
  },
  startButtonText: {
    ...Type.subtitle,
  },
  dismissButton: {
    marginTop: Space.md,
    padding: Space.sm,
  },
  dismissText: {
    ...Type.caption,
  },
});
