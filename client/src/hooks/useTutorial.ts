// Decision Stack — useTutorial Hook
// Manages tutorial state: activation, step progression, completion,
// and persistence via AsyncStorage + uiStore.
//
// Activation rule: first batch AND NOT completed AND NOT skipped
// Persistence: AsyncStorage key 'ds_tutorial_state'
// Analytics: fires 'tutorial_step_seen' event on each step

import { useCallback, useEffect, useRef, useState } from "react";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useUIStore } from "@stores/uiStore";

// ─── Constants ─────────────────────────────────────────────────────────────

const STORAGE_KEY = "ds_tutorial_state";
const TUTORIAL_SEEN_KEY = "ds_tutorial_seen";
const ONBOARDING_API_ENDPOINT = "/users/onboarding/complete";

// ─── Types ─────────────────────────────────────────────────────────────────

export interface TutorialState {
  isActive: boolean;
  currentStep: number;
  hasCompleted: boolean;
  hasSkipped: boolean;
  dontShowAgain: boolean;
}

export interface UseTutorialOptions {
  /** Total number of tutorial steps */
  stepCount: number;
  /** Called when user completes all steps */
  onComplete?: () => void;
  /** Called when user skips the tutorial */
  onSkip?: () => void;
}

export interface UseTutorialReturn {
  /** Whether the tutorial is currently active */
  isActive: boolean;
  /** Current step index (0-based) */
  currentStep: number;
  /** Total number of steps */
  totalSteps: number;
  /** Whether the user has completed the tutorial */
  hasCompleted: boolean;
  /** Whether the user has skipped the tutorial */
  hasSkipped: boolean;
  /** Move to the next step */
  advanceStep: () => void;
  /** Skip the tutorial entirely */
  skipTutorial: () => void;
  /** Mark tutorial as completed */
  completeTutorial: () => void;
  /** Activate the tutorial (for first-batch trigger) */
  activateTutorial: () => void;
  /** Set "don't show again" preference */
  setDontShowAgain: () => void;
}

// ─── Helpers ─────────────────────────────────────────────────────────────

/**
 * Load persisted tutorial state from AsyncStorage.
 */
async function loadPersistedState(): Promise<Partial<TutorialState>> {
  try {
    const [stateJson, seenFlag] = await AsyncStorage.multiGet([
      STORAGE_KEY,
      TUTORIAL_SEEN_KEY,
    ]);

    // If "seen" flag is set (from uiStore onboarding), respect it
    const hasBeenSeen = seenFlag[1] === "1";

    if (stateJson[1]) {
      const parsed = JSON.parse(stateJson[1]) as Partial<TutorialState>;
      return {
        ...parsed,
        hasCompleted: parsed.hasCompleted ?? hasBeenSeen,
      };
    }

    return { hasCompleted: hasBeenSeen };
  } catch {
    return {};
  }
}

/**
 * Persist tutorial state to AsyncStorage.
 */
async function persistState(state: TutorialState): Promise<void> {
  try {
    await AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(state));
    if (state.hasCompleted || state.hasSkipped || state.dontShowAgain) {
      await AsyncStorage.setItem(TUTORIAL_SEEN_KEY, "1");
    }
  } catch {
    // Silently fail — tutorial state is not critical
  }
}

/**
 * Report tutorial step to analytics.
 * Fires 'tutorial_step_seen' event with step metadata.
 */
function trackStepEvent(stepIndex: number, stepId: string, action: "seen" | "next" | "skip" | "complete") {
  // In production, this integrates with your analytics provider (Segment, Amplitude, etc.)
  // For now, we log to console and provide a hook for the analytics service
  const event = {
    event: "tutorial_step_seen",
    properties: {
      step_index: stepIndex,
      step_id: stepId,
      action,
      timestamp: Date.now(),
    },
  };

  // Send to global analytics queue if available
  if (typeof globalThis !== "undefined" && (globalThis as any).__analyticsQueue) {
    (globalThis as any).__analyticsQueue.push(event);
  }

  // Also log in development
  if (__DEV__) {
    // eslint-disable-next-line no-console
    console.log(`[Tutorial] Step ${stepIndex} (${stepId}): ${action}`);
  }
}

/**
 * POST to API to mark user as onboarded.
 */
async function postOnboardingComplete(): Promise<void> {
  try {
    const { completeOnboarding } = await import("@services/api");
    await completeOnboarding();
  } catch {
    // Silently fail — onboarding marker is best-effort
    // The client-side state is the source of truth
  }
}

// ─── Hook ──────────────────────────────────────────────────────────────────

/**
 * useTutorial — Tutorial state management
 *
 * Activation flow:
 *   1. On mount: load persisted state from AsyncStorage
 *   2. If NOT completed AND NOT skipped → tutorial is eligible
 *   3. Parent calls activateTutorial() when first batch renders
 *   4. Tutorial becomes active, overlay renders
 *   5. User steps through or skips
 *   6. Completion/skipping persists state + syncs with uiStore + API
 */
export function useTutorial(options: UseTutorialOptions): UseTutorialReturn {
  const { stepCount, onComplete, onSkip } = options;

  // Local state
  const [isActive, setIsActive] = useState(false);
  const [currentStep, setCurrentStep] = useState(0);
  const [hasCompleted, setHasCompleted] = useState(false);
  const [hasSkipped, setHasSkipped] = useState(false);
  const [dontShowAgain, setDontShowAgainState] = useState(false);
  const [isHydrated, setIsHydrated] = useState(false);

  // Refs to track step history for analytics
  const stepHistory = useRef<Set<number>>(new Set());

  // Zustand store actions
  const uiSetOnboardingComplete = useUIStore((s) => s.setHasCompletedOnboarding);
  const storeHasCompleted = useUIStore((s) => s.hasCompletedOnboarding);

  // ── Hydration: load persisted state on mount ──────────────────────────

  useEffect(() => {
    let cancelled = false;

    const hydrate = async () => {
      const persisted = await loadPersistedState();

      if (cancelled) return;

      // If store says completed but AsyncStorage doesn't, sync them
      const completed = persisted.hasCompleted ?? storeHasCompleted;
      const skipped = persisted.hasSkipped ?? false;

      setHasCompleted(completed);
      setHasSkipped(skipped);
      setIsHydrated(true);

      // Sync with Zustand store
      if (completed) {
        uiSetOnboardingComplete(true);
      }
    };

    hydrate();

    return () => {
      cancelled = true;
    };
  }, []);

  // ── Activation ────────────────────────────────────────────────────────

  const activateTutorial = useCallback(() => {
    if (!isHydrated) return;
    if (hasCompleted || hasSkipped || dontShowAgain) return;

    setIsActive(true);
    setCurrentStep(0);
    stepHistory.current.clear();
    stepHistory.current.add(0);

    trackStepEvent(0, "card_intro", "seen");
  }, [isHydrated, hasCompleted, hasSkipped, dontShowAgain]);

  // ── Step navigation ───────────────────────────────────────────────────

  const advanceStep = useCallback(() => {
    setCurrentStep((prev) => {
      const next = Math.min(prev + 1, stepCount - 1);

      // Track analytics
      const stepId = getStepId(next);
      if (!stepHistory.current.has(next)) {
        stepHistory.current.add(next);
        trackStepEvent(next, stepId, "seen");
      }
      trackStepEvent(prev, getStepId(prev), "next");

      return next;
    });
  }, [stepCount]);

  // ── Skip ──────────────────────────────────────────────────────────────

  const skipTutorial = useCallback(() => {
    trackStepEvent(currentStep, getStepId(currentStep), "skip");

    setIsActive(false);
    setHasSkipped(true);

    persistState({
      isActive: false,
      currentStep,
      hasCompleted: false,
      hasSkipped: true,
      dontShowAgain,
    });

    uiSetOnboardingComplete(true);
    onSkip?.();
  }, [currentStep, dontShowAgain, onSkip, uiSetOnboardingComplete]);

  // ── Complete ──────────────────────────────────────────────────────────

  const completeTutorial = useCallback(() => {
    trackStepEvent(currentStep, getStepId(currentStep), "complete");

    setIsActive(false);
    setHasCompleted(true);

    persistState({
      isActive: false,
      currentStep: stepCount - 1,
      hasCompleted: true,
      hasSkipped: false,
      dontShowAgain,
    });

    uiSetOnboardingComplete(true);

    // Fire-and-forget API call
    postOnboardingComplete().catch(() => {});

    onComplete?.();
  }, [currentStep, stepCount, dontShowAgain, onComplete, uiSetOnboardingComplete]);

  // ── Don't show again ──────────────────────────────────────────────────

  const setDontShowAgain = useCallback(() => {
    setDontShowAgainState(true);
    AsyncStorage.setItem(TUTORIAL_SEEN_KEY, "1").catch(() => {});
  }, []);

  // ── Return ────────────────────────────────────────────────────────────

  return {
    isActive,
    currentStep,
    totalSteps: stepCount,
    hasCompleted,
    hasSkipped,
    advanceStep,
    skipTutorial,
    completeTutorial,
    activateTutorial,
    setDontShowAgain,
  };
}

// ─── Helpers ─────────────────────────────────────────────────────────────

/** Map step index to step ID for analytics */
function getStepId(index: number): string {
  const stepIds = [
    "card_intro",
    "source_verify",
    "make_decision",
    "voice_mode",
    "approve_send",
    "tutorial_complete",
  ];
  return stepIds[index] ?? "unknown";
}
