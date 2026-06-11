// ContactProfileScreen — Drill-down view of a contact's relationship graph
//
// Navigated to by tapping the sender name on a DecisionCard.
// Shows: header with avatar + stats, tone trajectory chart, quick actions,
// and a scrollable timeline of all threads.
//
// Design: warm card-based layout using the existing palette. Clean shadows,
// sage/rose tone chart, loading skeletons.
//
// Invariant: This is a DRILL-DOWN, not a replacement for the one-card-at-a-time
// paradigm. Back button always returns to the card stack.

import React, { useCallback, useMemo } from "react";
import {
  View,
  Text,
  StyleSheet,
  ScrollView,
  TouchableOpacity,
  Dimensions,
  ActivityIndicator,
  RefreshControl,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import Svg, {
  Polyline,
  Line,
  Text as SvgText,
  Defs,
  LinearGradient,
  Stop,
  Rect,
} from "react-native-svg";
import { useTheme } from "../hooks/useTheme";
import { useContactCache } from "../hooks/useContactCache";
import { ContactTimeline } from "../components/contact/ContactTimeline";
import { palette } from "../theme/colors";
import { fontSize, fontWeight } from "../theme/typography";
import { spacing } from "../theme/spacing";
import type { ContactProfileRouteParams, ContactProfile } from "../types/contact";

const { width: SCREEN_W } = Dimensions.get("window");

// ─── Tone chart color mapping ──────────────────────────────────────────────

const TONE_COLORS: Record<string, string> = {
  professional: palette.steel[500],
  friendly: palette.sage[500],
  urgent: palette.rose[500],
  formal: palette.ink[500],
  casual: palette.sand[500],
};

const TONE_EMOJI: Record<string, string> = {
  professional: "💼",
  friendly: "🤝",
  urgent: "🔥",
  formal: "📜",
  casual: "☕",
};

// ─── Helpers ───────────────────────────────────────────────────────────────

function initials(name: string): string {
  return name
    .split(" ")
    .map((w) => w[0])
    .join("")
    .toUpperCase()
    .slice(0, 2);
}

function gradientForIndex(index: number): [string, string] {
  const gradients: [string, string][] = [
    [palette.sand[400], palette.sand[600]],
    [palette.steel[400], palette.steel[600]],
    [palette.sage[400], palette.sage[600]],
    [palette.rose[400], palette.rose[600]],
  ];
  return gradients[index % gradients.length];
}

function formatCurrency(n: number): string {
  if (n >= 1_000_000) return `$${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `$${(n / 1_000).toFixed(1)}k`;
  return `$${n.toFixed(0)}`;
}

function formatDuration(hours: number): string {
  if (hours < 1) return `${Math.round(hours * 60)} min`;
  if (hours < 24) return `${hours.toFixed(1)} hrs`;
  return `${(hours / 24).toFixed(1)} days`;
}

/** Build an SVG tone trajectory chart */
const ToneChart: React.FC<{
  toneHistory: ContactProfile["toneHistory"];
  width: number;
  height: number;
  isDark: boolean;
}> = ({ toneHistory, width, height, isDark }) => {
  if (toneHistory.length < 2) {
    return (
      <View style={[styles.chartEmpty, { width, height }]}>
        <Text style={{ color: isDark ? palette.ink[500] : palette.ink[400] }}>
          Not enough data yet
        </Text>
      </View>
    );
  }

  const padding = { top: 24, right: 16, bottom: 32, left: 16 };
  const chartW = width - padding.left - padding.right;
  const chartH = height - padding.top - padding.bottom;

  // Map tone values to Y positions (professional=0, friendly=1, urgent=2, formal=3, casual=4)
  const toneOrder = ["professional", "friendly", "urgent", "formal", "casual"];
  const yForTone = (tone: string) => {
    const idx = toneOrder.indexOf(tone);
    return idx >= 0 ? idx : 2; // default to middle
  };

  const maxY = toneOrder.length - 1;
  const points = toneHistory.map((entry, i) => {
    const x = padding.left + (i / (toneHistory.length - 1)) * chartW;
    const y = padding.top + (1 - yForTone(entry.tone) / maxY) * chartH;
    return { x, y, tone: entry.tone, date: entry.date };
  });

  const polylinePoints = points.map((p) => `${p.x},${p.y}`).join(" ");

  // Grid lines for each tone
  const gridLines = toneOrder.map((_, i) => {
    const y = padding.top + (1 - i / maxY) * chartH;
    return { y, label: toneOrder[i] };
  });

  return (
    <Svg width={width} height={height}>
      <Defs>
        <LinearGradient id="toneGradient" x1="0" y1="0" x2="0" y2="1">
          <Stop offset="0%" stopColor={palette.sage[400]} stopOpacity={0.3} />
          <Stop offset="100%" stopColor={palette.sage[400]} stopOpacity={0.02} />
        </LinearGradient>
      </Defs>

      {/* Grid lines */}
      {gridLines.map((g, i) => (
        <React.Fragment key={`grid-${i}`}>
          <Line
            x1={padding.left}
            y1={g.y}
            x2={width - padding.right}
            y2={g.y}
            stroke={isDark ? palette.ink[700] : palette.ink[100]}
            strokeWidth={1}
          />
          <SvgText
            x={padding.left}
            y={g.y - 4}
            fill={isDark ? palette.ink[500] : palette.ink[400]}
            fontSize={10}
          >
            {g.label}
          </SvgText>
        </React.Fragment>
      ))}

      {/* Area fill under line */}
      {points.length > 1 && (
        <Polyline
          points={`${points[0].x},${padding.top + chartH} ${polylinePoints} ${
            points[points.length - 1].x
          },${padding.top + chartH}`}
          fill="url(#toneGradient)"
        />
      )}

      {/* Line */}
      <Polyline
        points={polylinePoints}
        fill="none"
        stroke={palette.sage[500]}
        strokeWidth={2.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />

      {/* Data points */}
      {points.map((p, i) => (
        <React.Fragment key={`pt-${i}`}>
          <Rect
            x={p.x - 4}
            y={p.y - 4}
            width={8}
            height={8}
            rx={4}
            fill={TONE_COLORS[p.tone] ?? palette.steel[500]}
            stroke="#fff"
            strokeWidth={1.5}
          />
          <SvgText
            x={p.x}
            y={height - 6}
            fill={isDark ? palette.ink[500] : palette.ink[400]}
            fontSize={9}
            textAnchor="middle"
          >
            {new Date(p.date).toLocaleDateString("en-US", {
              month: "short",
              day: "numeric",
            })}
          </SvgText>
        </React.Fragment>
      ))}
    </Svg>
  );
};

// ─── Stat Card ─────────────────────────────────────────────────────────────

const StatCard: React.FC<{
  label: string;
  value: string;
  icon: string;
  index: number;
  isDark: boolean;
}> = ({ label, value, icon, index, isDark }) => {
  const [gradStart, gradEnd] = gradientForIndex(index);

  return (
    <View
      style={[
        styles.statCard,
        {
          backgroundColor: isDark ? palette.ink[800] : "#ffffff",
          shadowColor: isDark ? "#000" : palette.ink[900],
        },
      ]}
    >
      <View
        style={[
          styles.statIconCircle,
          { backgroundColor: `${gradStart}15` },
        ]}
      >
        <Text style={styles.statIcon}>{icon}</Text>
      </View>
      <Text style={[styles.statValue, { color: isDark ? palette.ink[50] : palette.ink[900] }]}>
        {value}
      </Text>
      <Text
        style={[
          styles.statLabel,
          { color: isDark ? palette.ink[400] : palette.ink[500] },
        ]}
      >
        {label}
      </Text>
    </View>
  );
};

// ─── Quick Action Button ───────────────────────────────────────────────────

const QuickAction: React.FC<{
  label: string;
  icon: string;
  onPress: () => void;
  variant?: "primary" | "secondary" | "danger";
  isDark: boolean;
}> = ({ label, icon, onPress, variant = "secondary", isDark }) => {
  const bgColor =
    variant === "primary"
      ? isDark
        ? palette.sand[600]
        : palette.sand[400]
      : variant === "danger"
      ? isDark
        ? palette.rose[900]
        : palette.rose[50]
      : isDark
      ? palette.ink[700]
      : palette.ink[50];

  const textColor =
    variant === "primary"
      ? "#ffffff"
      : variant === "danger"
      ? isDark
        ? palette.rose[400]
        : palette.rose[600]
      : isDark
      ? palette.ink[300]
      : palette.ink[700];

  return (
    <TouchableOpacity
      style={[styles.quickAction, { backgroundColor: bgColor }]}
      onPress={onPress}
      activeOpacity={0.75}
    >
      <Text style={styles.quickActionIcon}>{icon}</Text>
      <Text style={[styles.quickActionText, { color: textColor }]}>{label}</Text>
    </TouchableOpacity>
  );
};

// ─── Loading Skeleton ──────────────────────────────────────────────────────

const ProfileSkeleton: React.FC<{ isDark: boolean }> = ({ isDark }) => (
  <View style={styles.container}>
    <View style={[styles.headerSkeleton, { backgroundColor: isDark ? palette.ink[800] : "#ffffff" }]}>
      <View style={[styles.skeletonCircle, { backgroundColor: isDark ? palette.ink[700] : palette.ink[100] }]} />
      <View style={[styles.skeletonLine, { width: 160, backgroundColor: isDark ? palette.ink[700] : palette.ink[100] }]} />
      <View style={[styles.skeletonLine, { width: 120, marginTop: spacing[2], backgroundColor: isDark ? palette.ink[700] : palette.ink[100] }]} />
    </View>
    <View style={styles.statsGrid}>
      {[0, 1, 2, 3].map((i) => (
        <View
          key={i}
          style={[
            styles.statCard,
            {
              backgroundColor: isDark ? palette.ink[800] : "#ffffff",
              opacity: 0.6,
            },
          ]}
        >
          <View style={[styles.skeletonCircle, { width: 32, height: 32, borderRadius: 16, backgroundColor: isDark ? palette.ink[700] : palette.ink[100] }]} />
          <View style={[styles.skeletonLine, { width: 60, marginTop: spacing[3], backgroundColor: isDark ? palette.ink[700] : palette.ink[100] }]} />
          <View style={[styles.skeletonLine, { width: 80, marginTop: spacing[2], backgroundColor: isDark ? palette.ink[700] : palette.ink[100] }]} />
        </View>
      ))}
    </View>
  </View>
);

// ─── Main Screen ───────────────────────────────────────────────────────────

interface ContactProfileScreenProps {
  contactId: string;
  contactName?: string;
  contactEmail?: string;
  onBack: () => void;
  onPressThread?: (threadId: string) => void;
  onSendEmail?: (email: string) => void;
  onScheduleMeeting?: (contactId: string) => void;
  onMuteContact?: (contactId: string) => void;
}

export const ContactProfileScreen: React.FC<ContactProfileScreenProps> = ({
  contactId,
  contactName: initialName,
  contactEmail: initialEmail,
  onBack,
  onPressThread,
  onSendEmail,
  onScheduleMeeting,
  onMuteContact,
}) => {
  const { colors, isDark } = useTheme();
  const insets = useSafeAreaInsets();
  const { profile, timeline, isLoading, isRefreshing, error, refresh } =
    useContactCache(contactId);

  const displayName = profile?.name ?? initialName ?? "Unknown";
  const displayEmail = profile?.email ?? initialEmail ?? "";
  const gradColors = useMemo(() => gradientForIndex(contactId.charCodeAt(0)), [contactId]);

  // Handlers
  const handleSendEmail = useCallback(() => {
    if (profile?.email) onSendEmail?.(profile.email);
  }, [profile?.email, onSendEmail]);

  const handleSchedule = useCallback(() => {
    onScheduleMeeting?.(contactId);
  }, [contactId, onScheduleMeeting]);

  const handleMute = useCallback(() => {
    onMuteContact?.(contactId);
  }, [contactId, onMuteContact]);

  const handlePressThread = useCallback(
    (threadId: string) => {
      onPressThread?.(threadId);
    },
    [onPressThread]
  );

  // Loading state
  if (isLoading && !profile) {
    return (
      <View
        style={[
          styles.screen,
          {
            backgroundColor: colors.background,
            paddingTop: insets.top,
          },
        ]}
      >
        <View style={styles.navBar}>
          <TouchableOpacity onPress={onBack} style={styles.backButton}>
            <Text style={[styles.backArrow, { color: colors.textPrimary }]}>←</Text>
          </TouchableOpacity>
          <Text style={[styles.navTitle, { color: colors.textSecondary }]}>
            Loading…
          </Text>
          <View style={styles.backButton} />
        </View>
        <ProfileSkeleton isDark={isDark} />
      </View>
    );
  }

  // Error state with no data
  if (error && !profile) {
    return (
      <View
        style={[
          styles.screen,
          {
            backgroundColor: colors.background,
            paddingTop: insets.top,
          },
        ]}
      >
        <View style={styles.navBar}>
          <TouchableOpacity onPress={onBack} style={styles.backButton}>
            <Text style={[styles.backArrow, { color: colors.textPrimary }]}>←</Text>
          </TouchableOpacity>
          <Text style={[styles.navTitle, { color: colors.textSecondary }]}>
            {displayName}
          </Text>
          <View style={styles.backButton} />
        </View>
        <View style={styles.errorContainer}>
          <Text style={styles.errorEmoji}>⚠️</Text>
          <Text style={[styles.errorText, { color: colors.textSecondary }]}>
            {error}
          </Text>
          <TouchableOpacity
            style={[styles.retryButton, { backgroundColor: colors.primary }]}
            onPress={refresh}
          >
            <Text style={styles.retryButtonText}>Retry</Text>
          </TouchableOpacity>
        </View>
      </View>
    );
  }

  return (
    <View
      style={[
        styles.screen,
        {
          backgroundColor: colors.background,
          paddingTop: insets.top,
        },
      ]}
    >
      {/* ── Nav Bar ── */}
      <View style={styles.navBar}>
        <TouchableOpacity onPress={onBack} style={styles.backButton}>
          <Text style={[styles.backArrow, { color: colors.textPrimary }]}>←</Text>
        </TouchableOpacity>
        <Text
          style={[styles.navTitle, { color: colors.textSecondary }]}
          numberOfLines={1}
        >
          {displayName}
        </Text>
        <View style={styles.backButton} />
      </View>

      {/* ── Content ── */}
      <ScrollView
        showsVerticalScrollIndicator={false}
        contentContainerStyle={styles.scrollContent}
        refreshControl={
          <RefreshControl
            refreshing={isRefreshing}
            onRefresh={refresh}
            tintColor={colors.textTertiary}
          />
        }
      >
        {/* ── Header Card ── */}
        <View
          style={[
            styles.headerCard,
            {
              backgroundColor: isDark ? palette.ink[800] : "#ffffff",
              shadowColor: isDark ? "#000" : palette.ink[900],
            },
          ]}
        >
          {/* Avatar */}
          <View
            style={[
              styles.avatar,
              {
                backgroundColor: gradColors[0],
                shadowColor: gradColors[1],
              },
            ]}
          >
            <Text style={styles.avatarText}>{initials(displayName)}</Text>
          </View>

          {/* Name + Email */}
          <Text
            style={[styles.headerName, { color: isDark ? palette.ink[50] : palette.ink[900] }]}
          >
            {displayName}
          </Text>
          <Text style={[styles.headerEmail, { color: colors.textTertiary }]}>
            {displayEmail}
          </Text>

          {/* Meta row */}
          <View style={styles.metaRow}>
            {profile && (
              <>
                <View style={styles.metaItem}>
                  <Text style={[styles.metaValue, { color: colors.textSecondary }]}>
                    {new Date(profile.firstContactDate).toLocaleDateString(
                      "en-US",
                      { year: "numeric", month: "short" }
                    )}
                  </Text>
                  <Text style={[styles.metaLabel, { color: colors.textTertiary }]}>
                    First contact
                  </Text>
                </View>
                <View style={styles.metaDivider} />
                <View style={styles.metaItem}>
                  <Text style={[styles.metaValue, { color: colors.textSecondary }]}>
                    {new Date(profile.lastContactDate).toLocaleDateString(
                      "en-US",
                      { year: "numeric", month: "short", day: "numeric" }
                    )}
                  </Text>
                  <Text style={[styles.metaLabel, { color: colors.textTertiary }]}>
                    Last contact
                  </Text>
                </View>
              </>
            )}
            {!profile && (
              <Text style={[styles.metaLabel, { color: colors.textTertiary }]}>
                Tap to view full history
              </Text>
            )}
          </View>
        </View>

        {/* ── Stats Grid (2×2) ── */}
        <View style={styles.statsGrid}>
          <StatCard
            label="Interactions"
            value={profile ? String(profile.interactionCount) : "—"}
            icon="💬"
            index={0}
            isDark={isDark}
          />
          <StatCard
            label="Avg Response"
            value={profile ? formatDuration(profile.avgResponseHours) : "—"}
            icon="⏱️"
            index={1}
            isDark={isDark}
          />
          <StatCard
            label="Total Value"
            value={profile ? formatCurrency(profile.totalMonetaryValue) : "—"}
            icon="💰"
            index={2}
            isDark={isDark}
          />
          <StatCard
            label="Projects"
            value={profile ? String(profile.projects.length) : "—"}
            icon="📁"
            index={3}
            isDark={isDark}
          />
        </View>

        {/* ── Projects list (if any) ── */}
        {profile && profile.projects.length > 0 && (
          <View
            style={[
              styles.sectionCard,
              {
                backgroundColor: isDark ? palette.ink[800] : "#ffffff",
                shadowColor: isDark ? "#000" : palette.ink[900],
              },
            ]}
          >
            <Text
              style={[
                styles.sectionTitle,
                { color: isDark ? palette.ink[50] : palette.ink[900] },
              ]}
            >
              Projects
            </Text>
            <View style={styles.projectsList}>
              {profile.projects.map((project, i) => (
                <View key={i} style={styles.projectChip}>
                  <Text
                    style={[
                      styles.projectChipText,
                      {
                        color: isDark ? palette.sand[300] : palette.sand[700],
                        backgroundColor: isDark
                          ? `${palette.sand[600]}20`
                          : palette.sand[50],
                      },
                    ]}
                  >
                    {project}
                  </Text>
                </View>
              ))}
            </View>
          </View>
        )}

        {/* ── Tone Trajectory ── */}
        <View
          style={[
            styles.sectionCard,
            {
              backgroundColor: isDark ? palette.ink[800] : "#ffffff",
              shadowColor: isDark ? "#000" : palette.ink[900],
            },
          ]}
        >
          <Text
            style={[
              styles.sectionTitle,
              { color: isDark ? palette.ink[50] : palette.ink[900] },
            ]}
          >
            Tone Trajectory
          </Text>
          <Text
            style={[
              styles.sectionSubtitle,
              { color: colors.textTertiary },
            ]}
          >
            How the tone of communication has shifted over time
          </Text>
          <View style={styles.chartContainer}>
            <ToneChart
              toneHistory={profile?.toneHistory ?? []}
              width={SCREEN_W - spacing[8]}
              height={180}
              isDark={isDark}
            />
          </View>

          {/* Legend */}
          <View style={styles.legendRow}>
            {Object.entries(TONE_EMOJI).map(([tone, emoji]) => (
              <View key={tone} style={styles.legendItem}>
                <Text style={styles.legendEmoji}>{emoji}</Text>
                <Text
                  style={[
                    styles.legendText,
                    { color: colors.textTertiary },
                  ]}
                >
                  {tone}
                </Text>
              </View>
            ))}
          </View>
        </View>

        {/* ── Quick Actions ── */}
        <View style={styles.quickActionsRow}>
          <QuickAction
            label={`Email ${displayName.split(" ")[0]}`}
            icon="✉️"
            onPress={handleSendEmail}
            variant="primary"
            isDark={isDark}
          />
          <QuickAction
            label="Schedule"
            icon="📅"
            onPress={handleSchedule}
            isDark={isDark}
          />
          <QuickAction
            label="Mute"
            icon="🔕"
            onPress={handleMute}
            variant="danger"
            isDark={isDark}
          />
        </View>

        {/* ── Timeline ── */}
        <View
          style={[
            styles.timelineCard,
            {
              backgroundColor: isDark ? palette.ink[800] : "#ffffff",
              shadowColor: isDark ? "#000" : palette.ink[900],
            },
          ]}
        >
          <Text
            style={[
              styles.sectionTitle,
              { color: isDark ? palette.ink[50] : palette.ink[900] },
            ]}
          >
            Conversation History
          </Text>
          <ContactTimeline
            sections={timeline}
            onPressThread={handlePressThread}
          />
        </View>

        {/* Bottom padding */}
        <View style={{ height: spacing[8] }} />
      </ScrollView>
    </View>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    flex: 1,
  },
  screen: {
    flex: 1,
  },
  scrollContent: {
    paddingHorizontal: spacing[4],
    paddingTop: spacing[4],
  },

  // ── Nav Bar ─────────────────────────────────────────────────────────────
  navBar: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[3],
  },
  backButton: {
    width: 40,
    height: 40,
    justifyContent: "center",
    alignItems: "flex-start",
  },
  backArrow: {
    fontSize: fontSize.xl,
    fontWeight: fontWeight.semibold,
  },
  navTitle: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.medium,
    flex: 1,
    textAlign: "center",
    marginHorizontal: spacing[2],
  },

  // ── Header Card ─────────────────────────────────────────────────────────
  headerCard: {
    borderRadius: 20,
    padding: spacing[5],
    alignItems: "center",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.06,
    shadowRadius: 12,
    elevation: 4,
    borderWidth: 1,
    borderColor: "transparent",
  },
  avatar: {
    width: 72,
    height: 72,
    borderRadius: 36,
    justifyContent: "center",
    alignItems: "center",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.2,
    shadowRadius: 8,
    elevation: 6,
    marginBottom: spacing[3],
  },
  avatarText: {
    fontSize: fontSize.xl,
    fontWeight: fontWeight.bold,
    color: "#ffffff",
    textShadowColor: "rgba(0,0,0,0.15)",
    textShadowOffset: { width: 0, height: 1 },
    textShadowRadius: 2,
  },
  headerName: {
    fontSize: fontSize.xl,
    fontWeight: fontWeight.semibold,
    textAlign: "center",
  },
  headerEmail: {
    fontSize: fontSize.sm,
    marginTop: 2,
    textAlign: "center",
  },
  metaRow: {
    flexDirection: "row",
    alignItems: "center",
    marginTop: spacing[4],
    gap: spacing[4],
  },
  metaItem: {
    alignItems: "center",
  },
  metaValue: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.semibold,
  },
  metaLabel: {
    fontSize: fontSize.xs,
    marginTop: 2,
  },
  metaDivider: {
    width: 1,
    height: 28,
    backgroundColor: palette.ink[100],
  },

  // ── Stats Grid ──────────────────────────────────────────────────────────
  statsGrid: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: spacing[3],
    marginTop: spacing[4],
  },
  statCard: {
    width: (SCREEN_W - spacing[8] - spacing[3]) / 2,
    borderRadius: 16,
    padding: spacing[4],
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.04,
    shadowRadius: 8,
    elevation: 2,
    borderWidth: 1,
    borderColor: "transparent",
  },
  statIconCircle: {
    width: 36,
    height: 36,
    borderRadius: 18,
    justifyContent: "center",
    alignItems: "center",
    marginBottom: spacing[2],
  },
  statIcon: {
    fontSize: fontSize.lg,
  },
  statValue: {
    fontSize: fontSize.lg,
    fontWeight: fontWeight.bold,
    marginTop: spacing[1],
  },
  statLabel: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.medium,
    marginTop: 2,
  },

  // ── Section Card ────────────────────────────────────────────────────────
  sectionCard: {
    borderRadius: 20,
    padding: spacing[5],
    marginTop: spacing[4],
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.06,
    shadowRadius: 12,
    elevation: 4,
    borderWidth: 1,
    borderColor: "transparent",
  },
  sectionTitle: {
    fontSize: fontSize.lg,
    fontWeight: fontWeight.semibold,
  },
  sectionSubtitle: {
    fontSize: fontSize.sm,
    marginTop: 2,
    marginBottom: spacing[3],
  },

  // ── Projects ────────────────────────────────────────────────────────────
  projectsList: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: spacing[2],
    marginTop: spacing[3],
  },
  projectChip: {},
  projectChipText: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.medium,
    paddingHorizontal: spacing[3],
    paddingVertical: spacing[1] + 2,
    borderRadius: 10,
    overflow: "hidden",
  },

  // ── Tone Chart ──────────────────────────────────────────────────────────
  chartContainer: {
    marginTop: spacing[2],
    marginLeft: -spacing[1],
  },
  chartEmpty: {
    justifyContent: "center",
    alignItems: "center",
  },
  legendRow: {
    flexDirection: "row",
    justifyContent: "center",
    flexWrap: "wrap",
    gap: spacing[3],
    marginTop: spacing[3],
  },
  legendItem: {
    flexDirection: "row",
    alignItems: "center",
    gap: 4,
  },
  legendEmoji: {
    fontSize: fontSize.sm,
  },
  legendText: {
    fontSize: fontSize.xs,
    textTransform: "capitalize",
  },

  // ── Quick Actions ───────────────────────────────────────────────────────
  quickActionsRow: {
    flexDirection: "row",
    gap: spacing[3],
    marginTop: spacing[4],
  },
  quickAction: {
    flex: 1,
    borderRadius: 16,
    paddingVertical: spacing[3],
    alignItems: "center",
    justifyContent: "center",
  },
  quickActionIcon: {
    fontSize: fontSize.lg,
    marginBottom: 2,
  },
  quickActionText: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.semibold,
  },

  // ── Timeline Card ───────────────────────────────────────────────────────
  timelineCard: {
    borderRadius: 20,
    paddingTop: spacing[5],
    paddingBottom: spacing[3],
    marginTop: spacing[4],
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.06,
    shadowRadius: 12,
    elevation: 4,
    borderWidth: 1,
    borderColor: "transparent",
    overflow: "hidden",
  },

  // ── Error State ─────────────────────────────────────────────────────────
  errorContainer: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: spacing[6],
  },
  errorEmoji: {
    fontSize: 32,
    marginBottom: spacing[3],
  },
  errorText: {
    fontSize: fontSize.base,
    textAlign: "center",
  },
  retryButton: {
    marginTop: spacing[4],
    paddingVertical: spacing[3],
    paddingHorizontal: spacing[6],
    borderRadius: 14,
  },
  retryButtonText: {
    color: "#ffffff",
    fontSize: fontSize.sm,
    fontWeight: fontWeight.semibold,
  },

  // ── Skeleton ────────────────────────────────────────────────────────────
  headerSkeleton: {
    borderRadius: 20,
    padding: spacing[5],
    alignItems: "center",
    marginTop: spacing[4],
  },
  skeletonCircle: {
    width: 72,
    height: 72,
    borderRadius: 36,
    marginBottom: spacing[3],
  },
  skeletonLine: {
    height: 16,
    borderRadius: 8,
  },
});
