// Decision Stack — TutorialOverlay
// Full-screen tutorial experience that combines Spotlight + Tooltip
// to guide first-time users through the card decision flow.
//
// Features:
//   - Semi-transparent dark overlay with animated spotlight cutout
//   - Tooltip cards that position themselves relative to highlighted elements
//   - Animated transitions between steps (fade + slide)
//   - Progress dots showing current position
//   - Skip anytime (doesn't block user interaction)
//   - "Don't show again" option persisted to AsyncStorage
//   - Analytics events on each step for completion-rate tracking
//
// Usage:
//   <TutorialOverlay
//     targets={{ card_body: cardBodyRef, source_button: sourceRef, ... }}
//     onComplete={handleTutorialComplete}
//     onSkip={handleTutorialSkip}
//   />

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  View,
  StyleSheet,
  Dimensions,
  findNodeHandle,
  UIManager,
} from "react-native";
import { Spotlight, type SpotlightTarget } from "./Spotlight";
import { TutorialTooltip, type TutorialStep } from "./TutorialTooltip";
import { useTutorial } from "../../hooks/useTutorial";

const { width: SCREEN_W, height: SCREEN_H } = Dimensions.get("window");

// ─── Tutorial Step Definitions ─────────────────────────────────────────────

export const TUTORIAL_STEPS: TutorialStep[] = [
  {
    id: "card_intro",
    title: "This is a Decision Card",
    body: "Each card represents something someone wants from you. Sarah wants Friday delivery.",
    target: "card_body",
    position: "bottom",
  },
  {
    id: "source_verify",
    title: "Tap Source to Verify",
    body: "Every claim is backed by the actual email. Tap 'Source' to see the proof.",
    target: "source_button",
    position: "top",
  },
  {
    id: "make_decision",
    title: "Make Your Decision",
    body: "Type or say what you want to do. Try: 'Tell her Friday works.'",
    target: "decision_input",
    position: "top",
  },
  {
    id: "voice_mode",
    title: "Or Use Your Voice",
    body: "Tap the microphone and speak naturally. The AI will draft a response in your voice.",
    target: "mic_button",
    position: "top",
  },
  {
    id: "approve_send",
    title: "Review and Approve",
    body: "Check the draft, edit if needed, then approve to send. You have 5 seconds to undo!",
    target: "approve_button",
    position: "top",
  },
  {
    id: "tutorial_complete",
    title: "You're Ready!",
    body: "Clear your decisions one card at a time. Let's get started.",
    target: null,
    position: "center",
  },
];

// ─── Props ─────────────────────────────────────────────────────────────────

export interface TutorialTargetRefs {
  /** Ref to the card body (DecisionCard shell) */
  card_body?: React.RefObject<View>;
  /** Ref to the Source button in CardActions */
  source_button?: React.RefObject<View>;
  /** Ref to the Decide/primary action button */
  decision_input?: React.RefObject<View>;
  /** Ref to the microphone button (voice input) */
  mic_button?: React.RefObject<View>;
  /** Ref to the approve/send button */
  approve_button?: React.RefObject<View>;
}

interface TutorialOverlayProps {
  /** Map of named refs to targetable elements */
  targets: TutorialTargetRefs;
  /** Called when user completes all tutorial steps */
  onComplete: () => void;
  /** Called when user skips the tutorial */
  onSkip: () => void;
  /** Whether the tutorial is externally forced active */
  forceActive?: boolean;
}

// ─── Component ─────────────────────────────────────────────────────────────

/**
 * TutorialOverlay — Full-screen guided walkthrough
 *
 * Manages the tutorial state machine (which step, transitions,
 * target measurements) and renders the Spotlight + Tooltip layers.
 *
 * The overlay uses `pointerEvents="box-none"` so taps pass through
 * to the actual UI — the user can interact with the app during the
 * tutorial (and skip anytime).
 */
export const TutorialOverlay: React.FC<TutorialOverlayProps> = ({
  targets,
  onComplete,
  onSkip,
  forceActive,
}) => {
  const {
    isActive,
    currentStep,
    totalSteps,
    hasSkipped,
    advanceStep,
    skipTutorial,
    completeTutorial,
    setDontShowAgain,
  } = useTutorial({
    stepCount: TUTORIAL_STEPS.length,
    onComplete,
    onSkip,
  });

  // Spotlight target measurement
  const [spotlightTarget, setSpotlightTarget] = useState<SpotlightTarget | null>(null);

  // Track whether spotlight animation is in progress (blocks rapid clicks)
  const isAnimating = useRef(false);

  // Determine if tutorial should be rendered
  const shouldShow = forceActive || isActive;

  // Get current step data
  const stepData = TUTORIAL_STEPS[currentStep] ?? TUTORIAL_STEPS[0];

  // ── Measure target element position ───────────────────────────────────

  const measureTarget = useCallback(
    (targetKey: string | null): Promise<SpotlightTarget | null> => {
      return new Promise((resolve) => {
        if (!targetKey) {
          resolve(null);
          return;
        }

        const ref = targets[targetKey as keyof TutorialTargetRefs];
        if (!ref?.current) {
          // Target ref not available — try again after a short delay
          setTimeout(() => {
            const retryRef = targets[targetKey as keyof TutorialTargetRefs];
            if (retryRef?.current) {
              measureElement(retryRef.current, resolve);
            } else {
              resolve(null);
            }
          }, 100);
          return;
        }

        measureElement(ref.current, resolve);
      });
    },
    [targets]
  );

  // Helper: measure a single View element to screen coordinates
  const measureElement = (
    element: View,
    callback: (target: SpotlightTarget | null) => void
  ) => {
    const node = findNodeHandle(element);
    if (!node) {
      callback(null);
      return;
    }

    UIManager.measureInWindow(node, (x, y, width, height) => {
      if (x == null || y == null) {
        callback(null);
        return;
      }
      callback({ x, y, width, height });
    });
  };

  // ── Update spotlight when step changes ────────────────────────────────

  useEffect(() => {
    if (!shouldShow) {
      setSpotlightTarget(null);
      return;
    }

    isAnimating.current = true;

    const updateTarget = async () => {
      const measured = await measureTarget(stepData.target);
      setSpotlightTarget(measured);
      isAnimating.current = false;
    };

    // Small delay to allow layout to settle
    const timeout = setTimeout(updateTarget, 50);
    return () => clearTimeout(timeout);
  }, [currentStep, shouldShow, stepData.target, measureTarget]);

  // ── Handle orientation change ─────────────────────────────────────────

  useEffect(() => {
    const subscription = Dimensions.addEventListener("change", () => {
      if (shouldShow && stepData.target) {
        // Re-measure target after orientation change
        setTimeout(() => {
          measureTarget(stepData.target).then(setSpotlightTarget);
        }, 100);
      }
    });

    return () => subscription?.remove();
  }, [shouldShow, stepData.target, measureTarget]);

  // ── Step navigation handlers ──────────────────────────────────────────

  const handleNext = useCallback(() => {
    if (isAnimating.current) return;

    if (currentStep >= totalSteps - 1) {
      completeTutorial();
    } else {
      advanceStep();
    }
  }, [currentStep, totalSteps, advanceStep, completeTutorial]);

  const handleSkip = useCallback(() => {
    skipTutorial();
  }, [skipTutorial]);

  // ── Render ────────────────────────────────────────────────────────────

  if (!shouldShow) return null;

  return (
    <View style={[StyleSheet.absoluteFill, styles.container]} pointerEvents="box-none">
      {/* Spotlight layer (dark overlay with cutout) */}
      <Spotlight
        target={spotlightTarget}
        visible={shouldShow}
        overlayOpacity={0.72}
        showBorder={stepData.target !== null}
      />

      {/* Tooltip layer */}
      <TutorialTooltip
        step={stepData}
        stepIndex={currentStep}
        totalSteps={totalSteps}
        onNext={handleNext}
        onSkip={handleSkip}
        onDontShowAgain={
          currentStep === totalSteps - 1 ? setDontShowAgain : undefined
        }
        visible={shouldShow}
      />
    </View>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    zIndex: 99,
    elevation: 99,
  },
});
