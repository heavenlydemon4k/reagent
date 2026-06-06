// Decision Stack — Theme Toggle Component
// A reusable button that toggles between light, dark, and system theme modes.
// Shows the current mode with an appropriate icon.

import React from 'react';
import {
  TouchableOpacity,
  Text,
  StyleSheet,
  View,
} from 'react-native';
import { useTheme, type ThemeMode } from '../../hooks/useTheme';
import { spacing } from '../../theme/spacing';

// ─── Props ─────────────────────────────────────────────────────────────────

interface ThemeToggleProps {
  /** Size variant (default: 'md') */
  size?: 'sm' | 'md' | 'lg';
  /** Optional callback when theme changes */
  onToggle?: (mode: ThemeMode) => void;
}

// ─── Icon Map ──────────────────────────────────────────────────────────────

const MODE_ICONS: Record<ThemeMode, string> = {
  light: '☀️',
  dark: '🌙',
  system: '🖥️',
};

const MODE_LABELS: Record<ThemeMode, string> = {
  light: 'Light',
  dark: 'Dark',
  system: 'Auto',
};

// ─── Size Config ───────────────────────────────────────────────────────────

const SIZES = {
  sm: { button: 36, icon: 16, padding: 6 },
  md: { button: 44, icon: 20, padding: 8 },
  lg: { button: 52, icon: 24, padding: 10 },
};

/**
 * ThemeToggle — Button to cycle through light → dark → system theme modes
 *
 * Usage:
 *   <ThemeToggle size="sm" />
 *   <ThemeToggle size="md" onToggle={(mode) => analytics.track('theme_change', mode)} />
 */
export const ThemeToggle: React.FC<ThemeToggleProps> = ({
  size = 'md',
  onToggle,
}) => {
  const { themeMode, setThemeMode, isDark, colors } = useTheme();
  const s = SIZES[size];

  const cycleMode = () => {
    const order: ThemeMode[] = ['light', 'dark', 'system'];
    const currentIndex = order.indexOf(themeMode);
    const nextMode = order[(currentIndex + 1) % order.length];
    setThemeMode(nextMode);
    onToggle?.(nextMode);
  };

  return (
    <TouchableOpacity
      style={[
        styles.button,
        {
          width: s.button,
          height: s.button,
          padding: s.padding,
          backgroundColor: colors.surfaceElevated,
          borderColor: colors.border,
        },
      ]}
      onPress={cycleMode}
      activeOpacity={0.7}
      accessibilityLabel={`Theme: ${MODE_LABELS[themeMode]}. Tap to cycle.`}
      accessibilityRole="button"
      accessibilityState={{ selected: isDark }}
    >
      <Text style={[styles.icon, { fontSize: s.icon }]}>
        {MODE_ICONS[themeMode]}
      </Text>
    </TouchableOpacity>
  );
};

/**
 * ThemeToggleRow — Horizontal row with label + toggle, for settings screens
 */
export const ThemeToggleRow: React.FC<{
  onToggle?: (mode: ThemeMode) => void;
}> = ({ onToggle }) => {
  const { themeMode, setThemeMode, colors } = useTheme();

  return (
    <View style={[styles.row, { borderBottomColor: colors.border }]}>
      <Text style={[styles.rowLabel, { color: colors.textPrimary }]}>
        Appearance
      </Text>
      <View style={styles.segments}>
        {(['light', 'dark', 'system'] as ThemeMode[]).map((mode) => (
          <TouchableOpacity
            key={mode}
            style={[
              styles.segment,
              {
                backgroundColor:
                  themeMode === mode ? colors.primary : colors.surfaceElevated,
                borderColor: colors.border,
              },
            ]}
            onPress={() => {
              setThemeMode(mode);
              onToggle?.(mode);
            }}
            activeOpacity={0.7}
            accessibilityLabel={MODE_LABELS[mode]}
            accessibilityRole="button"
            accessibilityState={{ selected: themeMode === mode }}
          >
            <Text
              style={{
                fontSize: 12,
                fontWeight: themeMode === mode ? '600' : '400',
                color:
                  themeMode === mode ? colors.textInverse : colors.textSecondary,
              }}
            >
              {MODE_ICONS[mode]} {MODE_LABELS[mode]}
            </Text>
          </TouchableOpacity>
        ))}
      </View>
    </View>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  button: {
    borderRadius: 10,
    borderWidth: 1,
    justifyContent: 'center',
    alignItems: 'center',
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.05,
    shadowRadius: 4,
    elevation: 2,
  },
  icon: {
    lineHeight: 22,
  },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingVertical: spacing[3],
    paddingHorizontal: spacing[4],
    borderBottomWidth: 1,
  },
  rowLabel: {
    fontSize: 16,
    fontWeight: '500',
  },
  segments: {
    flexDirection: 'row',
    borderRadius: 8,
    overflow: 'hidden',
    borderWidth: 1,
  },
  segment: {
    paddingVertical: 6,
    paddingHorizontal: 10,
    borderRightWidth: 1,
  },
});
