// Decision Stack — Low-Saturation Warm Palette
// Designed for calm, focused decision-making without visual fatigue

export const palette = {
  // Base neutrals — warm undertone
  ink: {
    900: '#1a1816', // Primary text, dark backgrounds
    800: '#2d2a26',
    700: '#3f3b36',
    600: '#5c5750',
    500: '#78726a',
    400: '#948d84',
    300: '#b0a99f',
    200: '#ccc5ba',
    100: '#e8e1d6',
    50: '#f5f0e8',
  },

  // Warm sand — primary accent family
  sand: {
    900: '#5c4a32',
    800: '#7a6448',
    700: '#8f765a',
    600: '#a68b6b',
    500: '#bfa07d',
    400: '#c4a265', // Primary brand accent
    300: '#d4b88a',
    200: '#e3cfa8',
    100: '#f0e4cc',
    50: '#f9f3e8',
  },

  // Muted sage — secondary action / success
  sage: {
    900: '#3d4a3a',
    800: '#4e5e4a',
    700: '#5f7259',
    600: '#708668',
    500: '#819977',
    400: '#92ac86',
    300: '#a8bf9d',
    200: '#c2d4b8',
    100: '#dce8d4',
    50: '#eff5ea',
  },

  // Dusty rose — error / warning / destructive
  rose: {
    900: '#5a3d3d',
    800: '#6e4b4b',
    700: '#825858',
    600: '#966565',
    500: '#aa7272',
    400: '#be8080',
    300: '#cc9c9c',
    200: '#dab8b8',
    100: '#ebd4d4',
    50: '#f5eaea',
  },

  // Steel blue — info / links / consultation
  steel: {
    900: '#36404d',
    800: '#455363',
    700: '#536579',
    600: '#62778f',
    500: '#7189a5',
    400: '#809bbb',
    300: '#9bb3cc',
    200: '#b6cbdd',
    100: '#d1e3ee',
    50: '#eaf1f7',
  },
} as const;

// Semantic color tokens
export const lightTheme = {
  // Backgrounds
  background: palette.ink[50],
  surface: '#ffffff',
  surfaceElevated: '#ffffff',
  surfacePressed: palette.sand[50],
  overlay: 'rgba(26, 24, 22, 0.4)',

  // Borders
  border: palette.ink[100],
  borderSubtle: palette.ink[50],
  borderStrong: palette.ink[200],

  // Text
  textPrimary: palette.ink[900],
  textSecondary: palette.ink[600],
  textTertiary: palette.ink[400],
  textInverse: '#ffffff',

  // Accents
  primary: palette.sand[400],
  primaryHover: palette.sand[600],
  primaryPressed: palette.sand[700],
  primaryMuted: palette.sand[100],

  // Status
  success: palette.sage[600],
  successMuted: palette.sage[50],
  warning: palette.sand[600],
  warningMuted: palette.sand[50],
  error: palette.rose[600],
  errorMuted: palette.rose[50],
  info: palette.steel[600],
  infoMuted: palette.steel[50],

  // Card-specific
  cardBackground: '#ffffff',
  cardBorder: palette.ink[100],
  urgencyHigh: palette.rose[500],
  urgencyMedium: palette.sand[500],
  urgencyLow: palette.sage[500],

  // Voice mode
  voiceListening: palette.sage[500],
  voiceProcessing: palette.steel[500],
  voiceError: palette.rose[500],

  // Misc
  shadow: palette.ink[900],
  scrim: 'rgba(26, 24, 22, 0.6)',
  disabled: palette.ink[200],
  disabledText: palette.ink[300],
} as const;

export const darkTheme = {
  // Backgrounds
  background: palette.ink[900],
  surface: palette.ink[800],
  surfaceElevated: palette.ink[700],
  surfacePressed: palette.ink[600],
  overlay: 'rgba(0, 0, 0, 0.5)',

  // Borders
  border: palette.ink[700],
  borderSubtle: palette.ink[800],
  borderStrong: palette.ink[500],

  // Text
  textPrimary: palette.ink[50],
  textSecondary: palette.ink[300],
  textTertiary: palette.ink[500],
  textInverse: palette.ink[900],

  // Accents
  primary: palette.sand[400],
  primaryHover: palette.sand[300],
  primaryPressed: palette.sand[200],
  primaryMuted: palette.sand[900],

  // Status
  success: palette.sage[400],
  successMuted: palette.sage[900],
  warning: palette.sand[400],
  warningMuted: palette.sand[900],
  error: palette.rose[400],
  errorMuted: palette.rose[900],
  info: palette.steel[400],
  infoMuted: palette.steel[900],

  // Card-specific
  cardBackground: palette.ink[800],
  cardBorder: palette.ink[700],
  urgencyHigh: palette.rose[400],
  urgencyMedium: palette.sand[400],
  urgencyLow: palette.sage[400],

  // Voice mode
  voiceListening: palette.sage[400],
  voiceProcessing: palette.steel[400],
  voiceError: palette.rose[400],

  // Misc
  shadow: '#000000',
  scrim: 'rgba(0, 0, 0, 0.7)',
  disabled: palette.ink[700],
  disabledText: palette.ink[600],
} as const;

export type ThemeColors = typeof lightTheme | typeof darkTheme;
