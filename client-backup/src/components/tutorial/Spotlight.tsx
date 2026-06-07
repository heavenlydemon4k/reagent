// Decision Stack — Spotlight Component
// Creates an animated "hole" in the dark overlay that highlights
// a specific UI element. Uses SVG mask for smooth cutout with
// optional dashed border ring.

import React, { useEffect } from "react";
import { View, StyleSheet, Dimensions } from "react-native";
import Animated, {
  useSharedValue,
  useAnimatedProps,
  withTiming,
  Easing,
  interpolate,
  useAnimatedStyle,
} from "react-native-reanimated";
import Svg, { Rect } from "react-native-svg";

const AnimatedSvgRect = Animated.createAnimatedComponent(Rect);

const { width: SCREEN_W, height: SCREEN_H } = Dimensions.get("window");

const SPOTLIGHT_PADDING = 8;
const ANIMATION_DURATION = 350;

// ─── Types ─────────────────────────────────────────────────────────────────

export interface SpotlightTarget {
  x: number;
  y: number;
  width: number;
  height: number;
}

interface SpotlightProps {
  /** Current target element position (in screen coordinates) */
  target: SpotlightTarget | null;
  /** Whether the spotlight is visible */
  visible: boolean;
  /** Opacity of the dark overlay (0–1) */
  overlayOpacity?: number;
  /** Show dashed border around the highlight */
  showBorder?: boolean;
  /** Callback when spotlight animation completes */
  onAnimationComplete?: () => void;
}

// ─── Component ─────────────────────────────────────────────────────────────

/**
 * Spotlight — Dark overlay with animated cutout hole
 *
 * Uses an SVG rectangle with a mask to create a transparent "hole"
 * over the target element. The hole animates smoothly between positions
 * as the tutorial steps advance.
 *
 * Visual:
 *   ┌──────────────────────────────┐
 *   │ ████████████████████████████ │  ← dark overlay (70% opacity)
 *   │ ███████┌────────┐███████████ │  ← transparent hole
 *   │ ███████│ TARGET │███████████ │     (shows element underneath)
 *   │ ███████└────────┘███████████ │
 *   │ ████████████████████████████ │
 *   └──────────────────────────────┘
 */
export const Spotlight: React.FC<SpotlightProps> = ({
  target,
  visible,
  overlayOpacity = 0.72,
  showBorder = true,
  onAnimationComplete,
}) => {
  // Animated values for the hole position/size
  const holeX = useSharedValue(0);
  const holeY = useSharedValue(0);
  const holeW = useSharedValue(0);
  const holeH = useSharedValue(0);
  const opacity = useSharedValue(0);

  // Border ring animated values (slightly larger than hole)
  const borderScale = useSharedValue(1);

  // Animate to new target whenever it changes
  useEffect(() => {
    if (!visible) {
      opacity.value = withTiming(0, { duration: 200 });
      return;
    }

    if (target) {
      const tx = target.x - SPOTLIGHT_PADDING;
      const ty = target.y - SPOTLIGHT_PADDING;
      const tw = target.width + SPOTLIGHT_PADDING * 2;
      const th = target.height + SPOTLIGHT_PADDING * 2;

      const easing = Easing.bezier(0.4, 0, 0.2, 1);

      holeX.value = withTiming(tx, { duration: ANIMATION_DURATION, easing });
      holeY.value = withTiming(ty, { duration: ANIMATION_DURATION, easing });
      holeW.value = withTiming(tw, { duration: ANIMATION_DURATION, easing });
      holeH.value = withTiming(th, { duration: ANIMATION_DURATION, easing });
      opacity.value = withTiming(overlayOpacity, {
        duration: 300,
        easing,
      });

      // Pulse the border ring
      borderScale.value = 1;
      borderScale.value = withTiming(
        1.08,
        { duration: 400, easing: Easing.out(Easing.ease) },
        () => {
          borderScale.value = withTiming(1, {
            duration: 300,
            easing: Easing.inOut(Easing.ease),
          });
        }
      );

      const timeout = setTimeout(() => {
        onAnimationComplete?.();
      }, ANIMATION_DURATION);

      return () => clearTimeout(timeout);
    } else {
      // Center modal — no hole, full overlay
      opacity.value = withTiming(overlayOpacity, { duration: 300 });
    }
  }, [target, visible]);

  // ── Animated props for SVG rects ──────────────────────────────────────

  const topRectProps = useAnimatedProps(() => ({
    x: 0,
    y: 0,
    width: SCREEN_W,
    height: holeY.value,
    opacity: opacity.value,
  }));

  const bottomRectProps = useAnimatedProps(() => ({
    x: 0,
    y: holeY.value + holeH.value,
    width: SCREEN_W,
    height: Math.max(0, SCREEN_H - (holeY.value + holeH.value)),
    opacity: opacity.value,
  }));

  const leftRectProps = useAnimatedProps(() => ({
    x: 0,
    y: holeY.value,
    width: holeX.value,
    height: holeH.value,
    opacity: opacity.value,
  }));

  const rightRectProps = useAnimatedProps(() => ({
    x: holeX.value + holeW.value,
    y: holeY.value,
    width: Math.max(0, SCREEN_W - (holeX.value + holeW.value)),
    height: holeH.value,
    opacity: opacity.value,
  }));

  // ── Border ring animated style ────────────────────────────────────────

  const borderStyle = useAnimatedStyle(() => {
    if (!target || !showBorder) return { opacity: 0 };

    const scale = borderScale.value;
    const scaledW = holeW.value * scale;
    const scaledH = holeH.value * scale;
    const offsetX = (scaledW - holeW.value) / 2;
    const offsetY = (scaledH - holeH.value) / 2;

    return {
      position: "absolute",
      left: holeX.value - offsetX,
      top: holeY.value - offsetY,
      width: scaledW,
      height: scaledH,
      borderWidth: 2,
      borderColor: "rgba(255, 255, 255, 0.9)",
      borderStyle: "dashed",
      borderRadius: 14,
      opacity: interpolate(
        opacity.value,
        [0, overlayOpacity],
        [0, 1],
        "clamp"
      ),
    };
  });

  // ── Render ────────────────────────────────────────────────────────────

  if (!visible) return null;

  // When there's no target (center modal step), render full overlay
  if (!target) {
    return (
      <View style={[StyleSheet.absoluteFill, styles.container]} pointerEvents="box-none">
        <Animated.View
          style={[
            StyleSheet.absoluteFill,
            { backgroundColor: "#000", opacity },
          ]}
          pointerEvents="auto"
        />
      </View>
    );
  }

  return (
    <View style={[StyleSheet.absoluteFill, styles.container]} pointerEvents="box-none">
      {/* SVG overlay with 4 rects forming a hole around the target */}
      <Svg width={SCREEN_W} height={SCREEN_H} style={StyleSheet.absoluteFill}>
        <AnimatedSvgRect
          animatedProps={topRectProps}
          fill="#000"
          pointerEvents="auto"
        />
        <AnimatedSvgRect
          animatedProps={bottomRectProps}
          fill="#000"
          pointerEvents="auto"
        />
        <AnimatedSvgRect
          animatedProps={leftRectProps}
          fill="#000"
          pointerEvents="auto"
        />
        <AnimatedSvgRect
          animatedProps={rightRectProps}
          fill="#000"
          pointerEvents="auto"
        />
      </Svg>

      {/* Dashed border ring around the hole */}
      <Animated.View style={borderStyle} pointerEvents="none" />
    </View>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    zIndex: 100,
  },
});
