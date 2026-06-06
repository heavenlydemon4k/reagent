// Decision Stack — Theme Hook
// Returns the appropriate color set based on current theme mode + system preference.
// Combines uiStore theme state with the full light/dark palette from colors.ts.

import { useEffect, useState, useCallback } from 'react';
import { Appearance, useColorScheme } from 'react-native';
import { useUIStore } from '../stores/uiStore';
import { lightTheme, darkTheme, type ThemeColors } from '../theme/colors';

export type ThemeMode = 'light' | 'dark' | 'system';

export interface UseThemeReturn {
  /** The resolved color set (light or dark) based on effective theme */
  colors: ThemeColors;
  /** The current theme mode setting (light/dark/system) */
  themeMode: ThemeMode;
  /** The effective color scheme (always light or dark) */
  colorScheme: 'light' | 'dark';
  /** Whether the current theme is dark */
  isDark: boolean;
  /** Set theme mode directly */
  setThemeMode: (mode: ThemeMode) => void;
  /** Toggle between light and dark */
  toggleTheme: () => void;
}

/**
 * useTheme — Hook for accessing the current theme and colors
 *
 * Usage:
 *   const { colors, isDark, toggleTheme } = useTheme();
 *   <View style={{ backgroundColor: colors.background }} />
 *
 * The hook listens to system color scheme changes when themeMode is 'system'.
 * It returns the full ThemeColors object appropriate for the current scheme.
 */
export function useTheme(): UseThemeReturn {
  const uiStore = useUIStore();
  const systemColorScheme = useColorScheme();

  // Track effective color scheme locally for responsive updates
  const [effectiveScheme, setEffectiveScheme] = useState<'light' | 'dark'>(
    uiStore.colorScheme
  );

  // Re-compute effective scheme when store or system changes
  useEffect(() => {
    const themeMode = uiStore.themeMode;
    if (themeMode === 'system') {
      setEffectiveScheme(systemColorScheme ?? 'light');
    } else {
      setEffectiveScheme(themeMode);
    }
  }, [uiStore.themeMode, systemColorScheme, uiStore.colorScheme]);

  // Listen to Appearance changes for system mode
  useEffect(() => {
    const subscription = Appearance.addChangeListener(({ colorScheme: scheme }) => {
      if (uiStore.themeMode === 'system' && scheme) {
        setEffectiveScheme(scheme);
        uiStore.setColorScheme(scheme);
      }
    });

    return () => {
      subscription.remove();
    };
  }, [uiStore.themeMode]);

  const colors = effectiveScheme === 'dark' ? darkTheme : lightTheme;
  const isDark = effectiveScheme === 'dark';

  const setThemeMode = useCallback(
    (mode: ThemeMode) => {
      uiStore.setThemeMode(mode);
    },
    [uiStore]
  );

  const toggleTheme = useCallback(() => {
    const next = isDark ? 'light' : 'dark';
    uiStore.setThemeMode(next);
  }, [isDark, uiStore]);

  return {
    colors,
    themeMode: uiStore.themeMode as ThemeMode,
    colorScheme: effectiveScheme,
    isDark,
    setThemeMode,
    toggleTheme,
  };
}
