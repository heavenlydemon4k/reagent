// styles/cardStyles.ts — Shared card styling
// Warm palette, calm typography, consistent spacing for the CardStack UI.

import { StyleSheet, Dimensions } from "react-native";

const { width: SCREEN_W, height: SCREEN_H } = Dimensions.get("window");

// ---------------------------------------------------------------------------
// Color Palette (warm, calm)
// ---------------------------------------------------------------------------
export const Colors = {
  // Backgrounds
  bgWarm:       "#FAF6F1",  // warm off-white
  bgCard:       "#FFFFFF",
  bgAccent:     "#F2EAE0",  // soft beige

  // Gradients (used as solid stops / overlay tints)
  gradientTop:  "#FDF9F5",
  gradientMid:  "#F7F0E8",
  gradientBot:  "#F0E6DA",

  // Primary accent — warm blue
  primary:       "#4A7FB5",
  primaryLight:  "#E8F0F8",

  // Secondary — muted warm
  secondary:     "#8C7B6B",
  secondaryLight:"#F3EDE7",

  // Urgency levels
  urgentRed:     "#C25450",
  urgentOrange:  "#D4924A",
  urgentBg:      "#FCEAE9",

  // Text
  textMain:      "#2A2320",  // near-black with warmth
  textSecondary: "#5E524B",  // warm gray
  textTertiary:  "#9E9289",  // muted
  textInverse:   "#FFFFFF",

  // UI
  border:        "#E6DDD3",
  borderLight:   "#F0EAE3",
  chipBg:        "#F5F0EB",
  chipText:      "#7A6E65",
};

// ---------------------------------------------------------------------------
// Typography
// ---------------------------------------------------------------------------
export const Type = {
  display:    { fontSize: 28, fontWeight: "700" as const, letterSpacing: -0.5, lineHeight: 34 },
  title:      { fontSize: 22, fontWeight: "600" as const, letterSpacing: -0.3, lineHeight: 28 },
  subtitle:   { fontSize: 16, fontWeight: "500" as const, letterSpacing: 0,   lineHeight: 22 },
  body:       { fontSize: 15, fontWeight: "400" as const, letterSpacing: 0,   lineHeight: 21 },
  bodyBold:   { fontSize: 15, fontWeight: "600" as const, letterSpacing: 0,   lineHeight: 21 },
  caption:    { fontSize: 13, fontWeight: "400" as const, letterSpacing: 0.2, lineHeight: 18 },
  captionBold:{ fontSize: 13, fontWeight: "600" as const, letterSpacing: 0.2, lineHeight: 18 },
  micro:      { fontSize: 11, fontWeight: "500" as const, letterSpacing: 0.3, lineHeight: 14 },
};

// ---------------------------------------------------------------------------
// Spacing
// ---------------------------------------------------------------------------
export const Space = {
  xs:  4,
  sm:  8,
  md:  16,
  lg:  24,
  xl:  32,
  xxl: 48,
};

// ---------------------------------------------------------------------------
// Card dimensions
// ---------------------------------------------------------------------------
export const CardDim = {
  maxWidth: SCREEN_W - Space.lg * 2,   // 16px horizontal padding each side
  borderRadius: 20,
  sectionGap: Space.md,
  innerPad: Space.md,
};

// ---------------------------------------------------------------------------
// Shared StyleSheet
// ---------------------------------------------------------------------------
export const cardStyles = StyleSheet.create({
  // --- Container / Layout ---
  screenContainer: {
    flex: 1,
    backgroundColor: Colors.bgWarm,
  },
  screenCenter: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: Space.lg,
  },
  bgGradientFallback: {
    flex: 1,
    backgroundColor: Colors.gradientMid,
  },

  // --- Card shell ---
  cardShell: {
    backgroundColor: Colors.bgCard,
    borderRadius: CardDim.borderRadius,
    padding: CardDim.innerPad,
    width: CardDim.maxWidth,
    shadowColor: "#000",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.06,
    shadowRadius: 12,
    elevation: 4,
    borderWidth: 1,
    borderColor: Colors.borderLight,
  },

  // --- Card sections ---
  cardHeader: {
    marginBottom: Space.sm,
  },
  cardSection: {
    marginTop: Space.md,
    paddingTop: Space.md,
    borderTopWidth: 1,
    borderTopColor: Colors.borderLight,
  },
  sectionLabel: {
    ...Type.captionBold,
    color: Colors.textTertiary,
    textTransform: "uppercase",
    marginBottom: Space.xs,
  },

  // --- From field ---
  fromName: {
    ...Type.subtitle,
    color: Colors.textMain,
  },
  fromContext: {
    ...Type.caption,
    color: Colors.textSecondary,
    marginTop: 2,
  },
  fromMeta: {
    ...Type.micro,
    color: Colors.textTertiary,
    marginTop: Space.xs,
  },

  // --- They want ---
  theyWantText: {
    ...Type.body,
    color: Colors.textMain,
    lineHeight: 23,
  },

  // --- Context ---
  contextBullet: {
    ...Type.body,
    color: Colors.textSecondary,
    lineHeight: 22,
    marginLeft: Space.sm,
    marginBottom: Space.xs,
  },
  contextSentiment: {
    ...Type.caption,
    color: Colors.secondary,
    fontStyle: "italic",
    marginTop: Space.sm,
  },

  // --- Need from user ---
  needContainer: {
    backgroundColor: Colors.primaryLight,
    borderRadius: 12,
    padding: Space.md,
    borderLeftWidth: 3,
    borderLeftColor: Colors.primary,
  },
  needText: {
    ...Type.body,
    color: Colors.textMain,
  },

  // --- Actions ---
  actionRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginTop: Space.lg,
    paddingHorizontal: Space.xs,
  },
  actionBtnPrimary: {
    backgroundColor: Colors.primary,
    paddingVertical: Space.sm + 2,
    paddingHorizontal: Space.lg,
    borderRadius: 14,
    minWidth: 100,
    alignItems: "center",
  },
  actionBtnSecondary: {
    backgroundColor: Colors.secondaryLight,
    paddingVertical: Space.sm + 2,
    paddingHorizontal: Space.md,
    borderRadius: 14,
    alignItems: "center",
  },
  actionBtnText: {
    ...Type.captionBold,
    color: Colors.primary,
    paddingVertical: Space.sm + 2,
    paddingHorizontal: Space.md,
  },
  actionBtnSkip: {
    paddingVertical: Space.sm + 2,
    paddingHorizontal: Space.md,
  },
  actionBtnSkipText: {
    ...Type.caption,
    color: Colors.textTertiary,
  },
  actionBtnPrimaryText: {
    ...Type.captionBold,
    color: Colors.textInverse,
  },
  actionBtnSecondaryText: {
    ...Type.captionBold,
    color: Colors.textSecondary,
  },

  // --- Progress ---
  progressContainer: {
    position: "absolute",
    bottom: Space.xl,
    left: 0,
    right: 0,
    alignItems: "center",
  },
  progressText: {
    ...Type.caption,
    color: Colors.textTertiary,
  },
  progressBar: {
    width: CardDim.maxWidth * 0.5,
    height: 4,
    backgroundColor: Colors.border,
    borderRadius: 2,
    marginTop: Space.xs,
    overflow: "hidden",
  },
  progressFill: {
    height: "100%",
    backgroundColor: Colors.primary,
    borderRadius: 2,
  },

  // --- Urgency ---
  urgencyDot: {
    width: 8,
    height: 8,
    borderRadius: 4,
    marginRight: Space.xs,
  },
  urgencyBadge: {
    flexDirection: "row",
    alignItems: "center",
    alignSelf: "flex-start",
    paddingHorizontal: Space.sm,
    paddingVertical: Space.xs,
    borderRadius: 8,
    marginTop: Space.sm,
  },
  urgencyText: {
    ...Type.micro,
  },

  // --- Citations ---
  citationRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    marginTop: Space.sm,
    gap: Space.xs,
  },
  citationChip: {
    backgroundColor: Colors.chipBg,
    paddingHorizontal: Space.sm,
    paddingVertical: Space.xs,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: Colors.border,
  },
  citationChipText: {
    ...Type.micro,
    color: Colors.chipText,
  },

  // --- Gate screen ---
  gateCount: {
    ...Type.display,
    color: Colors.textMain,
    textAlign: "center",
  },
  gateSubtitle: {
    ...Type.subtitle,
    color: Colors.textSecondary,
    textAlign: "center",
    marginTop: Space.sm,
  },
  gateMeta: {
    ...Type.caption,
    color: Colors.textTertiary,
    textAlign: "center",
    marginTop: Space.md,
  },
  gateUrgencyHint: {
    ...Type.captionBold,
    color: Colors.urgentOrange,
    textAlign: "center",
    marginTop: Space.md,
  },
  gateButton: {
    backgroundColor: Colors.primary,
    paddingVertical: Space.md,
    paddingHorizontal: Space.xl,
    borderRadius: 16,
    marginTop: Space.xl,
    minWidth: 220,
    alignItems: "center",
  },
  gateButtonText: {
    ...Type.subtitle,
    color: Colors.textInverse,
  },
  gateDismiss: {
    marginTop: Space.md,
    padding: Space.sm,
  },
  gateDismissText: {
    ...Type.caption,
    color: Colors.textTertiary,
  },

  // --- Source viewer ---
  sourceContainer: {
    flex: 1,
    backgroundColor: Colors.bgWarm,
    padding: Space.lg,
  },
  sourceHeader: {
    ...Type.captionBold,
    color: Colors.textTertiary,
    marginBottom: Space.md,
    textAlign: "center",
  },
  sourceDivider: {
    height: 1,
    backgroundColor: Colors.border,
    marginVertical: Space.md,
  },
  sourceQuote: {
    ...Type.body,
    color: Colors.textMain,
    lineHeight: 24,
    fontStyle: "italic",
  },
  sourceMeta: {
    marginTop: Space.md,
    gap: Space.xs,
  },
  sourceMetaText: {
    ...Type.caption,
    color: Colors.textSecondary,
  },
  sourceBackBtn: {
    marginTop: Space.xl,
    alignSelf: "center",
    paddingVertical: Space.sm,
    paddingHorizontal: Space.lg,
    borderRadius: 12,
    backgroundColor: Colors.bgCard,
    borderWidth: 1,
    borderColor: Colors.border,
  },
  sourceBackBtnText: {
    ...Type.captionBold,
    color: Colors.textSecondary,
  },

  // --- Loading / Error ---
  loadingText: {
    ...Type.caption,
    color: Colors.textTertiary,
    marginTop: Space.md,
  },
  errorText: {
    ...Type.body,
    color: Colors.urgentRed,
    textAlign: "center",
    marginTop: Space.md,
  },
  retryBtn: {
    marginTop: Space.md,
    paddingVertical: Space.sm,
    paddingHorizontal: Space.lg,
    backgroundColor: Colors.primaryLight,
    borderRadius: 12,
  },
  retryBtnText: {
    ...Type.captionBold,
    color: Colors.primary,
  },
});
