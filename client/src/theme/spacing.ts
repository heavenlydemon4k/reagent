// Decision Stack — Spacing Scale
// 4-point grid system for consistent rhythm

export const spacing = {
  0: 0,
  px: 1,
  0.5: 2,
  0.75: 3,
  1: 4,
  1.5: 6,
  2: 8,
  2.5: 10,
  3: 12,
  3.5: 14,
  4: 16,
  4.5: 18,
  5: 20,
  5.5: 22,
  6: 24,
  7: 28,
  8: 32,
  9: 36,
  10: 40,
  11: 44,
  12: 48,
  13: 52,
  14: 56,
  16: 64,
  20: 80,
  24: 96,
  28: 112,
  32: 128,
} as const;

// Common layout measurements
export const layout = {
  // Screen
  screenPadding: spacing[4],
  screenPaddingWide: spacing[6],

  // Cards
  cardBorderRadius: spacing[3],
  cardPadding: spacing[5],
  cardGap: spacing[3],

  // Buttons
  buttonHeight: spacing[11],
  buttonBorderRadius: spacing[2.5],
  buttonPaddingHorizontal: spacing[5],

  // Input
  inputHeight: spacing[12],
  inputBorderRadius: spacing[2.5],
  inputPaddingHorizontal: spacing[4],

  // Touch targets (WCAG 2.5.5)
  minTouchTarget: spacing[11], // 44px

  // Navigation
  headerHeight: spacing[14],
  bottomTabHeight: spacing[16],

  // Max content width (for tablets)
  maxContentWidth: 540,
} as const;

// Shadow elevations
export const shadows = {
  sm: {
    shadowOffset: { width: 0, height: 1 },
    shadowRadius: 2,
    shadowOpacity: 0.06,
    elevation: 2,
  },
  md: {
    shadowOffset: { width: 0, height: 2 },
    shadowRadius: 6,
    shadowOpacity: 0.08,
    elevation: 4,
  },
  lg: {
    shadowOffset: { width: 0, height: 4 },
    shadowRadius: 12,
    shadowOpacity: 0.1,
    elevation: 8,
  },
  xl: {
    shadowOffset: { width: 0, height: 8 },
    shadowRadius: 24,
    shadowOpacity: 0.12,
    elevation: 16,
  },
} as const;
