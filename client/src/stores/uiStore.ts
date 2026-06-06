// Decision Stack — UI State Store (Zustand)
// Voice mode, theme, animations, and ephemeral UI state

import { create } from 'zustand';
import { Appearance } from 'react-native';
import AsyncStorage from '@react-native-async-storage/async-storage';
import type { VoiceModeState, VoiceTranscription } from '@types/cards';

type ThemeMode = 'light' | 'dark' | 'system';
type ColorScheme = 'light' | 'dark';

export interface UIStore {
  // ── Theme ──────────────────────────────────────────────────────
  themeMode: ThemeMode;
  colorScheme: ColorScheme;

  // ── Voice Mode ─────────────────────────────────────────────────
  voiceState: VoiceModeState;
  isVoiceModeActive: boolean;

  // ── Navigation / Flow ──────────────────────────────────────────
  currentScreen: string;
  previousScreen: string | null;
  isBottomSheetOpen: boolean;
  activeModal: string | null;

  // ── Feedback ───────────────────────────────────────────────────
  toast: {
    message: string;
    type: 'success' | 'error' | 'info';
    visible: boolean;
  } | null;

  // ── Onboarding ─────────────────────────────────────────────────
  hasCompletedOnboarding: boolean;

  // ── Computed ───────────────────────────────────────────────────
  effectiveTheme: () => ColorScheme;
  isDark: () => boolean;

  // ── Actions ────────────────────────────────────────────────────
  setThemeMode: (mode: ThemeMode) => void;
  setColorScheme: (scheme: ColorScheme) => void;

  startVoiceMode: () => void;
  stopVoiceMode: () => void;
  setVoicePhase: (phase: VoiceModeState['phase']) => void;
  setVoiceTranscription: (t: VoiceTranscription) => void;
  setDraftPreview: (preview: string | null) => void;

  navigateTo: (screen: string) => void;
  goBack: () => void;
  setBottomSheetOpen: (open: boolean) => void;
  setActiveModal: (modal: string | null) => void;

  showToast: (message: string, type?: 'success' | 'error' | 'info') => void;
  hideToast: () => void;

  setHasCompletedOnboarding: (completed: boolean) => void;
  hydrate: () => Promise<void>;
}

const VOICE_INITIAL: VoiceModeState = {
  phase: 'intro',
  current_card: null,
  transcription: '',
  draft_preview: null,
  undo_seconds_remaining: 0,
};

const TOAST_DURATION_MS = 3000;

export const useUIStore = create<UIStore>((set, get) => ({
  // ── Initial State ──────────────────────────────────────────────
  themeMode: 'system',
  colorScheme: Appearance.getColorScheme() ?? 'light',

  voiceState: { ...VOICE_INITIAL },
  isVoiceModeActive: false,

  currentScreen: 'BatchGate',
  previousScreen: null,
  isBottomSheetOpen: false,
  activeModal: null,

  toast: null,

  hasCompletedOnboarding: false,

  // ── Computed ───────────────────────────────────────────────────

  effectiveTheme: () => {
    const { themeMode, colorScheme } = get();
    if (themeMode === 'system') return colorScheme;
    return themeMode;
  },

  isDark: () => {
    return get().effectiveTheme() === 'dark';
  },

  // ── Actions ────────────────────────────────────────────────────

  setThemeMode: (mode) => {
    const effective =
      mode === 'system' ? Appearance.getColorScheme() ?? 'light' : mode;
    AsyncStorage.setItem('ds_theme_mode', mode).catch(() => {});
    set({ themeMode: mode, colorScheme: effective });
  },

  setColorScheme: (scheme) => set({ colorScheme: scheme }),

  // ── Voice ──────────────────────────────────────────────────────

  startVoiceMode: () =>
    set({
      isVoiceModeActive: true,
      voiceState: { ...VOICE_INITIAL, phase: 'intro' },
    }),

  stopVoiceMode: () =>
    set({
      isVoiceModeActive: false,
      voiceState: { ...VOICE_INITIAL },
    }),

  setVoicePhase: (phase) =>
    set((state) => ({
      voiceState: { ...state.voiceState, phase },
    })),

  setVoiceTranscription: (t) =>
    set((state) => ({
      voiceState: {
        ...state.voiceState,
        transcription: t.is_final
          ? t.text
          : state.voiceState.transcription + t.text,
      },
    })),

  setDraftPreview: (preview) =>
    set((state) => ({
      voiceState: { ...state.voiceState, draft_preview: preview },
    })),

  // ── Navigation ─────────────────────────────────────────────────

  navigateTo: (screen) =>
    set((state) => ({
      previousScreen: state.currentScreen,
      currentScreen: screen,
    })),

  goBack: () =>
    set((state) => ({
      currentScreen: state.previousScreen ?? state.currentScreen,
      previousScreen: null,
    })),

  setBottomSheetOpen: (open) => set({ isBottomSheetOpen: open }),

  setActiveModal: (modal) => set({ activeModal: modal }),

  // ── Toast ──────────────────────────────────────────────────────

  showToast: (message, type = 'info') => {
    set({ toast: { message, type, visible: true } });
    setTimeout(() => {
      set({ toast: null });
    }, TOAST_DURATION_MS);
  },

  hideToast: () => set({ toast: null }),

  // ── Onboarding ─────────────────────────────────────────────────

  setHasCompletedOnboarding: (completed) => {
    AsyncStorage.setItem('ds_onboarding', completed ? '1' : '0').catch(
      () => {}
    );
    set({ hasCompletedOnboarding: completed });
  },

  hydrate: async () => {
    try {
      const [themeMode, onboarding] = await AsyncStorage.multiGet([
        'ds_theme_mode',
        'ds_onboarding',
      ]);

      const resolvedTheme =
        (themeMode[1] as ThemeMode) ??
        'system';
      const effective =
        resolvedTheme === 'system'
          ? Appearance.getColorScheme() ?? 'light'
          : resolvedTheme;

      set({
        themeMode: resolvedTheme,
        colorScheme: effective as ColorScheme,
        hasCompletedOnboarding: onboarding[1] === '1',
        isHydrated: true,
      });
    } catch {
      set({ isHydrated: true });
    }
  },
}));
