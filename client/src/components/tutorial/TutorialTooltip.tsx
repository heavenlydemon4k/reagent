// Decision Stack — TutorialTooltip
// The floating tooltip card that appears next to (or centered over)
// the spotlighted element. Contains title, body, progress dots, and
// navigation actions.

import React from "react";
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  Dimensions,
} from "react-native";
import Animated, {
  useSharedValue,
  useAnimatedStyle,
  withTiming,
  Easing,
  interpolate,
} from "react-native-reanimated";
import { Type, Space } from "../../styles/cardStyles";
import { Colors } from "../../styles/cardStyles";

const { width: SCREEN_W } = Dimensions.get("window");

// ─── Types ─────────────────────────────────────────────────────────────────

export type TooltipPosition = "top" | "bottom" | "left" | "right" | "center";

export interface TutorialStep {
  id: string;
  title: string;
  body: string;
  target: string | null;
  position: TooltipPosition;
}

interface TutorialTooltipProps {
  step: TutorialStep;
  stepIndex: number;
  totalSteps: number;
  onNext: () => void;
  onSkip: () => void;
  onDontShowAgain?: () => void;
  visible: boolean;
}

// ─── Constants ─────────────────────────────────────────────────────────────

const TOOLTIP_MAX_WIDTH = Math.min(340, SCREEN_W - Space.lg * 2);
const ANIMATION_DURATION = 300;

// ─── Component ─────────────────────────────────────────────────────────────

/**
 * TutorialTooltip — Floating instruction card
 *
 * Renders a white card with:
 *   - Title (bold, primary color)
 *   - Body text (secondary color)
 *   - "Next" button (primary CTA)
 *   - "Skip Tutorial" text button
 *   - Progress dots (row of circles showing step position)
 *   - "Don't show again" checkbox (on last step)
 *
 * Animation: fade + slide in from the direction of the tooltip.
 */
export const TutorialTooltip: React.FC<TutorialTooltipProps> = ({
  step,
  stepIndex,
  totalSteps,
  onNext,
  onSkip,
  onDontShowAgain,
  visible,
}) => {
  // Animation values
  const progress = useSharedValue(0);
  const isLastStep = stepIndex === totalSteps - 1;
  const isFirstStep = stepIndex === 0;

  // Trigger entry animation whenever step changes
  React.useEffect(() => {
    if (visible) {
      progress.value = 0;
      progress.value = withTiming(1, {
        duration: ANIMATION_DURATION,
        easing: Easing.bezier(0.4, 0, 0.2, 1),
      });
    } else {
      progress.value = withTiming(0, { duration: 200 });
    }
  }, [step.id, visible]);

  // ── Animated styles ───────────────────────────────────────────────────

  const containerStyle = useAnimatedStyle(() => ({
    opacity: interpolate(progress.value, [0, 0.3, 1], [0, 0.5, 1], "clamp"),
    transform: [
      {
        translateY: interpolate(
          progress.value,
          [0, 1],
          step.position === "bottom" ? [20, 0] : [-20, 0],
          "clamp"
        ),
      },
      {
        scale: interpolate(progress.value, [0, 1], [0.95, 1], "clamp"),
      },
    ],
  }));

  // ── Render progress dots ──────────────────────────────────────────────

  const renderDots = () => (
    <View style={styles.dotsContainer}>
      {Array.from({ length: totalSteps }).map((_, i) => (
        <View
          key={`dot-${i}`}
          style={[
            styles.dot,
            i === stepIndex ? styles.dotActive : styles.dotInactive,
          ]}
        />
      ))}
    </View>
  );

  // ── Render ────────────────────────────────────────────────────────────

  return (
    <View style={[StyleSheet.absoluteFill, styles.overlay]} pointerEvents="box-none">
      <Animated.View
        style={[styles.tooltipContainer, containerStyle]}
        pointerEvents="auto"
      >
        {/* Progress dots at top */}
        {renderDots()}

        {/* Title */}
        <Text style={styles.title}>{step.title}</Text>

        {/* Body */}
        <Text style={styles.body}>{step.body}</Text>

        {/* Action buttons */}
        <View style={styles.actionsRow}>
          <TouchableOpacity
            onPress={onSkip}
            activeOpacity={0.7}
            hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
          >
            <Text style={styles.skipText}>
              {isLastStep ? "Close" : "Skip Tutorial"}
            </Text>
          </TouchableOpacity>

          <TouchableOpacity
            style={styles.nextButton}
            onPress={onNext}
            activeOpacity={0.8}
          >
            <Text style={styles.nextButtonText}>
              {isLastStep ? "Get Started" : isFirstStep ? "Start" : "Next"}
            </Text>
          </TouchableOpacity>
        </View>

        {/* "Don't show again" on last step */}
        {isLastStep && onDontShowAgain && (
          <TouchableOpacity
            style={styles.dontShowRow}
            onPress={onDontShowAgain}
            activeOpacity={0.7}
          >
            <View style={styles.checkbox}>
              <View style={styles.checkboxInner} />
            </View>
            <Text style={styles.dontShowText}>Don't show this again</Text>
          </TouchableOpacity>
        )}
      </Animated.View>
    </View>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  overlay: {
    zIndex: 101,
    justifyContent: "center",
    alignItems: "center",
  },
  tooltipContainer: {
    backgroundColor: Colors.bgCard,
    borderRadius: 16,
    padding: Space.lg,
    width: TOOLTIP_MAX_WIDTH,
    shadowColor: "#000",
    shadowOffset: { width: 0, height: 8 },
    shadowOpacity: 0.15,
    shadowRadius: 24,
    elevation: 8,
    borderWidth: 1,
    borderColor: Colors.borderLight,
  },
  // ── Progress dots ─────────────────────────────────────────────────────
  dotsContainer: {
    flexDirection: "row",
    justifyContent: "center",
    alignItems: "center",
    marginBottom: Space.md,
    gap: Space.xs,
  },
  dot: {
    width: 8,
    height: 8,
    borderRadius: 4,
  },
  dotActive: {
    backgroundColor: Colors.primary,
    width: 20,
    borderRadius: 4,
  },
  dotInactive: {
    backgroundColor: Colors.border,
  },
  // ── Content ───────────────────────────────────────────────────────────
  title: {
    ...Type.title,
    color: Colors.textMain,
    fontSize: 20,
    textAlign: "center",
  },
  body: {
    ...Type.body,
    color: Colors.textSecondary,
    textAlign: "center",
    marginTop: Space.sm,
    lineHeight: 22,
  },
  // ── Actions ───────────────────────────────────────────────────────────
  actionsRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginTop: Space.lg,
  },
  skipText: {
    ...Type.caption,
    color: Colors.textTertiary,
    paddingVertical: Space.xs,
  },
  nextButton: {
    backgroundColor: Colors.primary,
    paddingVertical: Space.sm + 4,
    paddingHorizontal: Space.lg,
    borderRadius: 14,
    minWidth: 100,
    alignItems: "center",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.15,
    shadowRadius: 6,
    elevation: 2,
  },
  nextButtonText: {
    ...Type.captionBold,
    color: Colors.textInverse,
  },
  // ── Don't show again ─────────────────────────────────────────────────
  dontShowRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    marginTop: Space.md,
    paddingTop: Space.md,
    borderTopWidth: 1,
    borderTopColor: Colors.borderLight,
  },
  checkbox: {
    width: 18,
    height: 18,
    borderRadius: 4,
    borderWidth: 2,
    borderColor: Colors.textTertiary,
    justifyContent: "center",
    alignItems: "center",
    marginRight: Space.sm,
  },
  checkboxInner: {
    width: 10,
    height: 10,
    borderRadius: 2,
    backgroundColor: Colors.primary,
  },
  dontShowText: {
    ...Type.caption,
    color: Colors.textTertiary,
  },
});
