// ContactTimeline — Chronological list of all threads with a contact
// Sectioned FlatList with month headers. Each row shows subject, date,
// decision outcome, and tap-to-expand.

import React, { useCallback } from "react";
import {
  View,
  Text,
  FlatList,
  StyleSheet,
  TouchableOpacity,
} from "react-native";
import { useTheme } from "../../hooks/useTheme";
import { palette } from "../../theme/colors";
import { fontSize, fontWeight } from "../../theme/typography";
import { spacing } from "../../theme/spacing";
import type { ThreadSummary, TimelineSection } from "../../types/contact";

// ─── Props ─────────────────────────────────────────────────────────────────

interface ContactTimelineProps {
  sections: TimelineSection[];
  onPressThread: (threadId: string) => void;
  isLoading?: boolean;
}

// ─── Helpers ───────────────────────────────────────────────────────────────

/** Format an ISO date into "Jun 12" or "Dec 5" */
function formatShortDate(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
  });
}

/** Derive a subtle icon from decision text */
function decisionEmoji(decision?: string): string {
  if (!decision) return "";
  const d = decision.toLowerCase();
  if (d.includes("approve")) return "✓";
  if (d.includes("reject")) return "✗";
  if (d.includes("delegate")) return "→";
  if (d.includes("schedule")) return "📅";
  if (d.includes("defer")) return "⏸";
  return "•";
}

/** Status badge color */
function statusColor(
  status: ThreadSummary["status"],
  isDark: boolean
): string {
  switch (status) {
    case "active":
      return isDark ? palette.sage[400] : palette.sage[600];
    case "resolved":
      return isDark ? palette.steel[400] : palette.steel[600];
    case "archived":
      return isDark ? palette.ink[500] : palette.ink[400];
    default:
      return palette.ink[300];
  }
}

// ─── Sub-components ────────────────────────────────────────────────────────

const ThreadRow: React.FC<{
  item: ThreadSummary;
  onPress: (id: string) => void;
}> = ({ item, onPress }) => {
  const { colors, isDark } = useTheme();

  return (
    <TouchableOpacity
      style={[styles.row, { borderBottomColor: colors.border }]}
      onPress={() => onPress(item.id)}
      activeOpacity={0.7}
    >
      <View style={styles.rowLeft}>
        <Text
          style={[styles.rowDate, { color: colors.textTertiary }]}
          numberOfLines={1}
        >
          {formatShortDate(item.date)}
        </Text>
        <View style={styles.rowContent}>
          <Text
            style={[styles.rowSubject, { color: colors.textPrimary }]}
            numberOfLines={1}
          >
            {item.subject}
          </Text>
          {item.preview && (
            <Text
              style={[styles.rowPreview, { color: colors.textSecondary }]}
              numberOfLines={1}
            >
              {item.preview}
            </Text>
          )}
        </View>
      </View>

      <View style={styles.rowRight}>
        {/* Decision badge */}
        {item.decision && (
          <View
            style={[
              styles.decisionBadge,
              {
                backgroundColor: isDark
                  ? palette.sage[900]
                  : palette.sage[50],
              },
            ]}
          >
            <Text
              style={[
                styles.decisionText,
                { color: isDark ? palette.sage[400] : palette.sage[700] },
              ]}
            >
              {decisionEmoji(item.decision)} {item.decision}
            </Text>
          </View>
        )}

        {/* Status dot */}
        <View
          style={[
            styles.statusDot,
            { backgroundColor: statusColor(item.status, isDark) },
          ]}
        />
      </View>
    </TouchableOpacity>
  );
};

const SectionHeader: React.FC<{ title: string }> = ({ title }) => {
  const { colors } = useTheme();

  return (
    <View style={styles.sectionHeader}>
      <Text style={[styles.sectionTitle, { color: colors.textSecondary }]}>
        {title}
      </Text>
    </View>
  );
};

// ─── Main Component ────────────────────────────────────────────────────────

export const ContactTimeline: React.FC<ContactTimelineProps> = ({
  sections,
  onPressThread,
  isLoading = false,
}) => {
  const { colors } = useTheme();

  // Flatten sections for FlatList rendering with inline headers
  const flatData = React.useMemo(() => {
    const rows: (
      | { type: "header"; title: string; key: string }
      | { type: "item"; item: ThreadSummary; key: string }
    )[] = [];

    for (const section of sections) {
      rows.push({ type: "header", title: section.title, key: `h-${section.title}` });
      for (const item of section.data) {
        rows.push({ type: "item", item, key: item.id });
      }
    }

    return rows;
  }, [sections]);

  const renderItem = useCallback(
    ({
      item,
    }: {
      item:
        | { type: "header"; title: string; key: string }
        | { type: "item"; item: ThreadSummary; key: string };
    }) => {
      if (item.type === "header") {
        return <SectionHeader title={item.title} />;
      }
      return <ThreadRow item={item.item} onPress={onPressThread} />;
    },
    [onPressThread]
  );

  if (isLoading) {
    return (
      <View style={styles.loadingContainer}>
        {[...Array(4)].map((_, i) => (
          <View
            key={i}
            style={[
              styles.skeletonRow,
              { backgroundColor: colors.surfacePressed },
            ]}
          />
        ))}
      </View>
    );
  }

  if (flatData.length === 0) {
    return (
      <View style={styles.emptyContainer}>
        <Text style={[styles.emptyText, { color: colors.textTertiary }]}>
          No conversations yet
        </Text>
      </View>
    );
  }

  return (
    <FlatList
      data={flatData}
      renderItem={renderItem}
      keyExtractor={(item) => item.key}
      showsVerticalScrollIndicator={false}
      contentContainerStyle={styles.listContent}
    />
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  listContent: {
    paddingBottom: spacing[6],
  },
  // ── Section Header ──────────────────────────────────────────────────────
  sectionHeader: {
    paddingHorizontal: spacing[4],
    paddingTop: spacing[4],
    paddingBottom: spacing[2],
  },
  sectionTitle: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.semibold,
    textTransform: "uppercase",
    letterSpacing: 0.5,
  },
  // ── Thread Row ──────────────────────────────────────────────────────────
  row: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[3],
    borderBottomWidth: 1,
  },
  rowLeft: {
    flexDirection: "row",
    alignItems: "flex-start",
    flex: 1,
    marginRight: spacing[3],
  },
  rowDate: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.medium,
    width: 50,
    marginTop: 2,
  },
  rowContent: {
    flex: 1,
  },
  rowSubject: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.medium,
    lineHeight: 20,
  },
  rowPreview: {
    fontSize: fontSize.xs,
    marginTop: 2,
    lineHeight: 16,
  },
  rowRight: {
    flexDirection: "row",
    alignItems: "center",
    gap: spacing[2],
  },
  // ── Decision Badge ──────────────────────────────────────────────────────
  decisionBadge: {
    paddingHorizontal: spacing[2],
    paddingVertical: 2,
    borderRadius: 6,
  },
  decisionText: {
    fontSize: 10,
    fontWeight: fontWeight.medium,
  },
  // ── Status Dot ──────────────────────────────────────────────────────────
  statusDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  // ── Loading Skeleton ────────────────────────────────────────────────────
  loadingContainer: {
    paddingHorizontal: spacing[4],
    paddingTop: spacing[4],
    gap: spacing[3],
  },
  skeletonRow: {
    height: 56,
    borderRadius: 8,
    opacity: 0.5,
  },
  // ── Empty State ─────────────────────────────────────────────────────────
  emptyContainer: {
    paddingVertical: spacing[8],
    alignItems: "center",
  },
  emptyText: {
    fontSize: fontSize.sm,
  },
});
