// Decision Stack — Tutorial-specific TypeScript types
// Used by TutorialOverlay, useTutorial hook, and related components.

import type { View } from "react-native";

// ─── Tutorial Step Definition ──────────────────────────────────────────────

export type TutorialTargetKey =
  | "card_body"
  | "source_button"
  | "decision_input"
  | "mic_button"
  | "approve_button";

export type TooltipPosition = "top" | "bottom" | "left" | "right" | "center";

export interface TutorialStep {
  /** Unique identifier for the step (used in analytics) */
  id: string;
  /** Short heading displayed in the tooltip */
  title: string;
  /** Descriptive text explaining the highlighted element */
  body: string;
  /** Which element to highlight (null = center modal, no spotlight) */
  target: TutorialTargetKey | null;
  /** Where to position the tooltip relative to the target */
  position: TooltipPosition;
}

// ─── Spotlight ─────────────────────────────────────────────────────────────

export interface SpotlightTarget {
  /** Screen X coordinate of the target element */
  x: number;
  /** Screen Y coordinate of the target element */
  y: number;
  /** Width of the target element */
  width: number;
  /** Height of the target element */
  height: number;
}

// ─── Tutorial State (persisted) ────────────────────────────────────────────

export interface PersistedTutorialState {
  isActive: boolean;
  currentStep: number;
  hasCompleted: boolean;
  hasSkipped: boolean;
  dontShowAgain: boolean;
}

// ─── Target Refs Map ───────────────────────────────────────────────────────

export interface TutorialTargetRefs {
  card_body: React.RefObject<View>;
  source_button: React.RefObject<View>;
  decision_input: React.RefObject<View>;
  mic_button: React.RefObject<View>;
  approve_button: React.RefObject<View>;
}

// ─── Analytics Events ──────────────────────────────────────────────────────

export interface TutorialAnalyticsEvent {
  event: "tutorial_step_seen";
  properties: {
    step_index: number;
    step_id: string;
    action: "seen" | "next" | "skip" | "complete";
    timestamp: number;
  };
}
