// Decision Stack — Typography Scale
// Optimized for mobile readability at arm's length

export const fontFamily = {
  // System font stack — clean, native feel
  sans: 'System' as const,
  mono: 'Menlo, Monaco, Consolas, monospace' as const,
};

export const fontSize = {
  xs: 11,      // Captions, timestamps, metadata
  sm: 13,      // Secondary text, labels
  base: 15,    // Body text (iOS default)
  md: 16,      // Android body, inputs
  lg: 18,      // Lead paragraphs
  xl: 20,      // Card titles, section headers
  '2xl': 24,   // Screen titles
  '3xl': 30,   // Large headings
  '4xl': 36,   // Hero numbers (e.g., pending count)
} as const;

export const lineHeight = {
  tight: 1.2,   // Headlines, single lines
  snug: 1.35,   // Subheadings
  normal: 1.5,  // Body text (default)
  relaxed: 1.65,// Long-form reading
} as const;

export const fontWeight = {
  light: '300' as const,
  normal: '400' as const,
  medium: '500' as const,
  semibold: '600' as const,
  bold: '700' as const,
} as const;

export const letterSpacing = {
  tight: -0.5,
  normal: 0,
  wide: 0.5,
  wider: 1,
} as const;

// Pre-composed text styles for common UI elements
export const textStyles = {
  // Display
  heroNumber: {
    fontSize: fontSize['4xl'],
    fontWeight: fontWeight.bold,
    lineHeight: lineHeight.tight,
    letterSpacing: letterSpacing.tight,
  },
  screenTitle: {
    fontSize: fontSize['2xl'],
    fontWeight: fontWeight.semibold,
    lineHeight: lineHeight.tight,
    letterSpacing: letterSpacing.tight,
  },

  // Headings
  cardTitle: {
    fontSize: fontSize.xl,
    fontWeight: fontWeight.semibold,
    lineHeight: lineHeight.snug,
  },
  sectionHeader: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.semibold,
    lineHeight: lineHeight.tight,
    letterSpacing: letterSpacing.wide,
    textTransform: 'uppercase' as const,
  },

  // Body
  body: {
    fontSize: fontSize.base,
    fontWeight: fontWeight.normal,
    lineHeight: lineHeight.normal,
  },
  bodyLarge: {
    fontSize: fontSize.lg,
    fontWeight: fontWeight.normal,
    lineHeight: lineHeight.relaxed,
  },

  // Utility
  label: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.medium,
    lineHeight: lineHeight.tight,
  },
  caption: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.normal,
    lineHeight: lineHeight.normal,
  },
  captionMedium: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.medium,
    lineHeight: lineHeight.normal,
  },

  // Mono (for technical data)
  monoData: {
    fontFamily: fontFamily.mono,
    fontSize: fontSize.sm,
    fontWeight: fontWeight.normal,
    lineHeight: lineHeight.normal,
  },
} as const;
