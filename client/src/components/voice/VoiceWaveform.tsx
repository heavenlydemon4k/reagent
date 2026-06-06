// Decision Stack — Animated Waveform Visualization
// Sound amplitude bars during voice recording
//
// Receives amplitude data from parent (useVoiceChat) which now uses
// real expo-av Audio.Metering values instead of Math.random().

import React, { useEffect, useRef } from 'react';
import {
  View,
  Animated,
  StyleSheet,
} from 'react-native';
import { palette } from '@theme/colors';
import { spacing } from '@theme/spacing';

interface VoiceWaveformProps {
  /** Array of bar heights (0–28). Filled from real audio metering in useVoiceChat. */
  amplitude: number[];
  /** Whether the waveform should animate (recording is active). */
  isActive: boolean;
  compact?: boolean;
  color?: string;
  /**
   * Optional single normalized level (0…1) for ripple-based visualization.
   * When provided, bars are derived from this value with a sine-wave ripple,
   * giving a smooth live-feedback look without needing a full amplitude array.
   */
  audioLevel?: number;
}

const DEFAULT_BAR_COUNT = 24;

/**
 * VoiceWaveform — animated vertical bars representing audio amplitude.
 * Used during voice recording to provide visual feedback.
 */
export const VoiceWaveform: React.FC<VoiceWaveformProps> = ({
  amplitude,
  isActive,
  compact = false,
  color = palette.sand[400],
  audioLevel,
}) => {
  const barAnimations = useRef<Animated.Value[]>([]);

  // Initialize animation refs
  if (barAnimations.current.length === 0) {
    barAnimations.current = Array.from(
      { length: DEFAULT_BAR_COUNT },
      () => new Animated.Value(3)
    );
  }

  // Animate bars based on real amplitude data (from audio metering)
  useEffect(() => {
    if (!isActive) {
      // Reset to flat line when inactive
      barAnimations.current.forEach((anim) => {
        Animated.timing(anim, {
          toValue: 3,
          duration: 200,
          useNativeDriver: false,
        }).start();
      });
      return;
    }

    // Derive bar heights from either audioLevel (real metering) or amplitude array
    barAnimations.current.forEach((anim, index) => {
      let value: number;
      if (audioLevel !== undefined) {
        // Smooth ripple derived from a single normalized audio level (0…1)
        const ripple = Math.sin((index / DEFAULT_BAR_COUNT) * Math.PI) * 4;
        const base = audioLevel * (compact ? 16 : 28);
        value = Math.max(3, base + ripple);
      } else {
        // Fall back to amplitude array
        value = amplitude[index % amplitude.length] ?? 3;
      }
      const clampedValue = Math.max(3, Math.min(compact ? 16 : 28, value));

      Animated.timing(anim, {
        toValue: clampedValue,
        duration: 120,
        useNativeDriver: false,
      }).start();
    });
  }, [amplitude, isActive, compact, audioLevel]);

  const barWidth = compact ? 3 : 4;
  const barGap = compact ? 2 : 3;
  const maxBarHeight = compact ? 16 : 28;

  return (
    <View style={[styles.container, compact && styles.containerCompact]}>
      {barAnimations.current.map((anim, index) => (
        <Animated.View
          key={index}
          style={[
            styles.bar,
            {
              width: barWidth,
              marginHorizontal: barGap / 2,
              height: anim,
              maxHeight: maxBarHeight,
              backgroundColor: isActive
                ? color
                : palette.ink[200],
              opacity: isActive ? 0.8 + (index % 3) * 0.1 : 0.4,
            },
          ]}
        />
      ))}
    </View>
  );
};

const styles = StyleSheet.create({
  container: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    height: 36,
    paddingHorizontal: spacing[2],
  },
  containerCompact: {
    height: 24,
  },
  bar: {
    borderRadius: 2,
  },
});
