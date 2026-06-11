import React, { useState, useCallback, useRef, useEffect } from "react";
import {
  View,
  Text,
  StyleSheet,
  Dimensions,
  SafeAreaView,
  StatusBar,
} from "react-native";
import Animated, {
  useSharedValue,
  useAnimatedStyle,
  withSpring,
  withTiming,
  runOnJS,
  interpolate,
  Extrapolation,
} from "react-native-reanimated";
import {
  Gesture,
  GestureDetector,
  GestureHandlerRootView,
} from "react-native-gesture-handler";
import { Colors, Type, Space, CardDim } from "../styles/cardStyles";
import type { DecisionCard as DecisionCardType, ChunkCitation } from "../types/cards";
import { DecisionCard, type DecisionCardTutorialTargets } from "../components/cards/DecisionCard";
import { LoadingSpinner } from "../components/common/LoadingSpinner";
import { TutorialOverlay } from "../components/tutorial/TutorialOverlay";
import { useTutorial } from "../hooks/useTutorial";
import { useStreak } from "../hooks/useStreak";
import { useTheme } from "../hooks/useTheme";
import { useKeyboardShortcuts } from "../hooks/useKeyboardShortcuts";
import { ShortcutHelpOverlay } from "../components/common/ShortcutHelpOverlay";

const { height: SCREEN_H } = Dimensions.get("window");

// ─── Props ─────────────────────────────────────────────────────────────────

interface CardStackScreenProps {
  cards: DecisionCardType[];
  onDecide: (cardId: string) => void;
  onConsult: (cardId: string) => void;
  onSource: (cardId: string, citations: ChunkCitation[]) => void;
  onSkip: (cardId: string) => void;
  onComplete: () => void;
  onPressCitation?: (citation: ChunkCitation) => void;
  /** If true, this is the user's first batch → show tutorial */
  isFirstBatch?: boolean;
}

// ─── Constants ─────────────────────────────────────────────────────────────

const SWIPE_UP_THRESHOLD = -80; // Y-translation to trigger dismiss
const SPRING_CONFIG = { damping: 20, stiffness: 200 };

// ─── Component ─────────────────────────────────────────────────────────────

/**
 * CardStackScreen — ONE card at a time
 *
 * Core UX flow:
 *  - Renders a single DecisionCard at a time
 *  - Swipe up = skip/dismiss (optional, buttons are primary)
 *  - Progress: "Card 3 of 7" at bottom
 *  - Forward only — no back button to previous cards
 *  - Streak indicator in header (flame icon + count)
 *  - Keyboard shortcuts support (? for help)
 *  - First-batch tutorial overlay (TutorialOverlay + Spotlight)
 *
 * Tutorial integration:
 *  - On first batch, useTutorial hook activates after card mounts
 *  - Tutorial refs are captured from DecisionCard imperative handle
 *  - Spotlight highlights: card body, source button, decide button
 *  - Mic button and approve button use placeholder refs (shown in later flow)
 *
 * Invariants:
 *  - NEVER shows a list
 *  - No inbox view, no unread counter, no folder list
 *  - [Source] tap → onSource callback
 *  - Swipe up = skip (optional)
 */
export const CardStackScreen: React.FC<CardStackScreenProps> = ({
  cards,
  onDecide,
  onConsult,
  onSource,
  onSkip,
  onComplete,
  onPressCitation,
  isFirstBatch = false,
}) => {
  const { colors, isDark } = useTheme();
  const { streak } = useStreak();
  const [currentIndex, setCurrentIndex] = useState(0);
  const isTransitioning = useSharedValue(false);

  // ── Tutorial state ────────────────────────────────────────────────────
  const {
    activateTutorial,
    isActive: isTutorialActive,
    hasCompleted: hasCompletedTutorial,
  } = useTutorial({
    stepCount: 6,
    onComplete: useCallback(() => {
      // Analytics: tutorial completion tracked in useTutorial hook
    }, []),
    onSkip: useCallback(() => {
      // Analytics: tutorial skip tracked in useTutorial hook
    }, []),
  });

  // ── Tutorial target refs ──────────────────────────────────────────────
  // These refs are populated from DecisionCard's imperative handle
  const cardTargetsRef = useRef<DecisionCardTutorialTargets | null>(null);

  // Fallback View refs for elements that DecisionCard doesn't directly expose
  const micButtonRef = useRef<View>(null);
  const approveButtonRef = useRef<View>(null);

  // Build the TutorialTargetRefs map for the overlay
  const tutorialTargets = useRef<{
    card_body: View | null;
    source_button: View | null;
    decision_input: View | null;
    mic_button: View | null;
    approve_button: View | null;
  }>({
    card_body: null,
    source_button: null,
    decision_input: null,
    mic_button: null,
    approve_button: null,
  });

  // ── Activate tutorial on first batch ──────────────────────────────────
  useEffect(() => {
    if (isFirstBatch && cards.length > 0 && !hasCompletedTutorial) {
      // Delay to allow card layout to settle before measuring
      const timeout = setTimeout(() => {
        activateTutorial();
      }, 600);
      return () => clearTimeout(timeout);
    }
  }, [isFirstBatch, cards.length, hasCompletedTutorial, activateTutorial]);

  // ── Update tutorial targets when card changes ─────────────────────────
  const handleCardRef = useCallback((ref: DecisionCardTutorialTargets | null) => {
    cardTargetsRef.current = ref;
    if (ref) {
      tutorialTargets.current = {
        card_body: ref.cardBody,
        source_button: ref.sourceButton,
        decision_input: ref.decideButton,
        mic_button: micButtonRef.current,
        approve_button: approveButtonRef.current,
      };
    }
  }, []);

  // Shared values for swipe animation
  const translateY = useSharedValue(0);
  const scale = useSharedValue(1);
  const opacity = useSharedValue(1);

  // Current card reference (avoid stale closure in worklets)
  const indexRef = useRef(currentIndex);
  indexRef.current = currentIndex;

  const totalCards = cards.length;
  const currentCard = cards[currentIndex];

  // ── Keyboard Shortcuts ─────────────────────────────────────────────────
  const { isHelpVisible, showHelp, hideHelp } = useKeyboardShortcuts(
    {
      onNextCard: useCallback(() => {
        if (currentCard) {
          onSkip(currentCard.id);
          exitCard();
        }
      }, [currentCard, onSkip]),
      onDecide: useCallback(() => {
        if (currentCard) onDecide(currentCard.id);
      }, [currentCard, onDecide]),
      onSkip: useCallback(() => {
        if (currentCard) {
          onSkip(currentCard.id);
          exitCard();
        }
      }, [currentCard, onSkip]),
      onConsult: useCallback(() => {
        if (currentCard) onConsult(currentCard.id);
      }, [currentCard, onConsult]),
      onShowHelp: () => { /* resolved below */ },
    },
    { enabled: true, ignoreWhenTyping: true }
  );

  // ── Progress ────────────────────────────────────────────────────────────
  const progress = totalCards > 0 ? (currentIndex + 1) / totalCards : 0;
  const progressPercent = Math.round(progress * 100);

  // ── Advance to next card ────────────────────────────────────────────────
  const advance = useCallback(() => {
    const nextIndex = indexRef.current + 1;
    if (nextIndex >= totalCards) {
      onComplete();
    } else {
      setCurrentIndex(nextIndex);
      isTransitioning.value = false;
      // Reset animated values
      translateY.value = 0;
      scale.value = 1;
      opacity.value = 1;
    }
  }, [totalCards, onComplete, isTransitioning, translateY, scale, opacity]);

  // ── Animated exit (card slides up and fades out) ────────────────────────
  const exitCard = useCallback(() => {
    if (isTransitioning.value) return;
    isTransitioning.value = true;

    translateY.value = withTiming(
      -SCREEN_H * 0.6,
      { duration: 300 },
      (finished) => {
        if (finished) {
          runOnJS(advance)();
        }
      }
    );
    opacity.value = withTiming(0, { duration: 280 });
    scale.value = withTiming(0.92, { duration: 280 });
  }, [isTransitioning, translateY, opacity, scale, advance]);

  // ── Gesture: swipe up to skip ───────────────────────────────────────────
  const swipeGesture = Gesture.Pan()
    .activeOffsetY([-10, 10]) // only vertical
    .onUpdate((event) => {
      "worklet";
      if (isTransitioning.value) return;
      // Only allow upward movement (negative Y)
      if (event.translationY < 0) {
        translateY.value = event.translationY;
        scale.value = interpolate(
          event.translationY,
          [0, -SCREEN_H * 0.5],
          [1, 0.92],
          Extrapolation.CLAMP
        );
        opacity.value = interpolate(
          event.translationY,
          [0, -SCREEN_H * 0.5],
          [1, 0.7],
          Extrapolation.CLAMP
        );
      }
    })
    .onEnd((event) => {
      "worklet";
      if (isTransitioning.value) return;
      if (event.translationY < SWIPE_UP_THRESHOLD) {
        // Swipe up threshold met — skip card
        runOnJS(exitCard)();
      } else {
        // Snap back
        translateY.value = withSpring(0, SPRING_CONFIG);
        scale.value = withSpring(1, SPRING_CONFIG);
        opacity.value = withSpring(1, SPRING_CONFIG);
      }
    });

  // ── Animated style ──────────────────────────────────────────────────────
  const animatedCardStyle = useAnimatedStyle(() => ({
    transform: [
      { translateY: translateY.value },
      { scale: scale.value },
    ],
    opacity: opacity.value,
  }));

  // ── Button handlers ─────────────────────────────────────────────────────
  const handleDecide = useCallback(
    (cardId: string) => {
      onDecide(cardId);
      // Card stays visible until user takes further action
    },
    [onDecide]
  );

  const handleConsult = useCallback(
    (cardId: string) => {
      onConsult(cardId);
    },
    [onConsult]
  );

  const handleSource = useCallback(
    (cardId: string) => {
      const card = cards.find((c) => c.id === cardId);
      if (card) {
        onSource(cardId, card.chunk_citations);
      }
    },
    [cards, onSource]
  );

  const handleSkip = useCallback(
    (cardId: string) => {
      onSkip(cardId);
      exitCard();
    },
    [onSkip, exitCard]
  );

  // ── Tutorial overlay target refs wrapper ──────────────────────────────
  // Convert imperative handle refs to the format TutorialOverlay expects
  const getTutorialTargetRefs = useCallback(() => {
    return {
      card_body: { current: tutorialTargets.current.card_body },
      source_button: { current: tutorialTargets.current.source_button },
      decision_input: { current: tutorialTargets.current.decision_input },
      mic_button: { current: tutorialTargets.current.mic_button },
      approve_button: { current: tutorialTargets.current.approve_button },
    };
  }, []);

  // ── Render ──────────────────────────────────────────────────────────────

  if (!currentCard) {
    if (totalCards === 0) {
      return (
        <SafeAreaView style={[styles.container, { backgroundColor: colors.background }]}>
          <View style={styles.emptyContainer}>
            <Text style={[styles.emptyTitle, { color: colors.textPrimary }]}>All caught up!</Text>
            <Text style={[styles.emptySubtitle, { color: colors.textSecondary }]}>
              No decisions need your attention right now.
            </Text>
          </View>
        </SafeAreaView>
      );
    }
    return <LoadingSpinner message="Loading cards..." />;
  }

  return (
    <GestureHandlerRootView style={[styles.container, { backgroundColor: colors.background }]}>
      <StatusBar
        barStyle={isDark ? "light-content" : "dark-content"}
        backgroundColor={colors.background}
      />
      <SafeAreaView style={[styles.container, { backgroundColor: colors.background }]}>
        {/* Warm gradient background */}
        <View style={[styles.gradientBg, { backgroundColor: colors.surfacePressed, opacity: 0.6 }]} />

        {/* Streak Header */}
        {streak > 0 && (
          <View style={styles.streakHeader}>
            <Text style={styles.streakFlame}>🔥</Text>
            <Text style={[styles.streakText, { color: colors.textSecondary }]}>
              Streak: {streak} day{streak !== 1 ? "s" : ""}
            </Text>
          </View>
        )}

        {/* Card area with gesture */}
        <View style={styles.cardArea}>
          <GestureDetector gesture={swipeGesture}>
            <Animated.View style={[styles.cardWrapper, animatedCardStyle]}>
              <DecisionCard
                ref={handleCardRef}
                card={currentCard}
                onDecide={handleDecide}
                onConsult={handleConsult}
                onSource={handleSource}
                onSkip={handleSkip}
                onPressCitation={onPressCitation}
              />
            </Animated.View>
          </GestureDetector>
        </View>

        {/* Progress indicator at bottom */}
        <View style={styles.progressContainer}>
          <Text style={[styles.progressText, { color: colors.textTertiary }]}>
            {`Card ${currentIndex + 1} of ${totalCards}`}
          </Text>
          <View style={[styles.progressBar, { backgroundColor: colors.border }]}>
            <View
              style={[
                styles.progressFill,
                { width: `${progressPercent}%`, backgroundColor: colors.primary },
              ]}
            />
          </View>
        </View>
      </SafeAreaView>

      {/* Keyboard Shortcuts Help Overlay */}
      <ShortcutHelpOverlay visible={isHelpVisible} onClose={hideHelp} />

      {/* First-batch Tutorial Overlay */}
      {isFirstBatch && (
        <TutorialOverlay
          targets={getTutorialTargetRefs()}
          onComplete={() => {
            // Completion tracked in useTutorial hook
          }}
          onSkip={() => {
            // Skip tracked in useTutorial hook
          }}
        />
      )}
    </GestureHandlerRootView>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    flex: 1,
  },
  gradientBg: {
    ...StyleSheet.absoluteFillObject,
    opacity: 0.6,
  },
  // ── Streak Header ───────────────────────────────────────────────────────
  streakHeader: {
    position: "absolute",
    top: 8,
    right: Space.lg,
    flexDirection: "row",
    alignItems: "center",
    zIndex: 10,
    paddingVertical: Space.xs,
    paddingHorizontal: Space.sm,
  },
  streakFlame: {
    fontSize: 16,
    marginRight: 4,
  },
  streakText: {
    ...Type.captionBold,
    fontSize: 13,
  },
  // ── Card Area ───────────────────────────────────────────────────────────
  cardArea: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    paddingHorizontal: Space.lg,
  },
  cardWrapper: {
    width: CardDim.maxWidth,
  },
  // ── Progress ────────────────────────────────────────────────────────────
  progressContainer: {
    position: "absolute",
    bottom: Space.xl,
    left: 0,
    right: 0,
    alignItems: "center",
  },
  progressText: {
    ...Type.caption,
  },
  progressBar: {
    width: CardDim.maxWidth * 0.5,
    height: 4,
    borderRadius: 2,
    marginTop: Space.xs,
    overflow: "hidden",
  },
  progressFill: {
    height: "100%",
    borderRadius: 2,
  },
  // ── Empty State ─────────────────────────────────────────────────────────
  emptyContainer: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: Space.lg,
  },
  emptyTitle: {
    ...Type.title,
    textAlign: "center",
  },
  emptySubtitle: {
    ...Type.body,
    textAlign: "center",
    marginTop: Space.sm,
  },
});
