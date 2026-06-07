// Decision Stack — Draft Review Screen
//
// User reviews the AI-generated draft and can:
// - Approve (with confirmation dialog) → queue for send
// - Edit → opens EditModal for full modification
// - Shorten → re-submits with "make it shorter"
// - Formalize → re-submits with "make it more formal"
// - Back → returns to DecisionInputScreen (re-enter instruction)
//
// Invariants:
// - user_approved MUST be TRUE before any send
// - [Approve] has confirmation dialog (prevent accidental sends)
// - Every approval queued in sync_queue for upload
// - Optimistic UI: show approved state immediately, sync in background
// - UNDO: After text approval, shows a 5s undo toast to cancel the send

import React, { useState, useCallback, useEffect } from "react";
import {
  View,
  Text,
  StyleSheet,
  ScrollView,
  Alert,
  TouchableOpacity,
  Dimensions,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import Animated, {
  useSharedValue,
  useAnimatedStyle,
  withTiming,
} from "react-native-reanimated";
import { Colors as StaticColors, Type, Space, CardDim } from "../styles/cardStyles";
import type { DecisionCard, Draft } from "../types/cards";
import { useDrafting } from "../hooks/useDrafting";
import { useApproval } from "../hooks/useApproval";
import { useUndoSend } from "../hooks/useUndoSend";
import { useTheme } from "../hooks/useTheme";
import { DraftBody } from "../components/draft/DraftBody";
import { DraftActions } from "../components/draft/DraftActions";
import { EditModal } from "../components/draft/EditModal";
import { LoadingSpinner } from "../components/common/LoadingSpinner";

const { height: SCREEN_H } = Dimensions.get("window");

// ─── Props ─────────────────────────────────────────────────────────────────

export interface DraftReviewScreenProps {
  card: DecisionCard;
  initialDraft: Draft | null;
  onBack: () => void;
  onApproved: (draftId: string) => void;
  onComplete: () => void;
}

/**
 * DraftReviewScreen — Review AI-generated draft before sending
 *
 * Layout:
 *   Status bar (drafting / approved)
 *   DraftBody (rendered draft with highlights)
 *   DraftActions (Edit / Shorten / Formalize / Approve / Back)
 *   UndoToast (5-second undo window after text approval)
 */
export const DraftReviewScreen: React.FC<DraftReviewScreenProps> = ({
  card,
  initialDraft,
  onBack,
  onApproved,
  onComplete,
}) => {
  const { colors } = useTheme();
  // Drafting state machine
  const {
    draft,
    phase,
    isLoading,
    error,
    modifyDraft,
    updateDraftBody,
    markApproved,
  } = useDrafting(card.id);

  // Approval logic
  const {
    approve,
    confirmApprove,
    cancelConfirm,
    approvedDrafts,
    isConfirming,
  } = useApproval();

  // Undo send logic (5-second window)
  const { state: undoState, showUndo, performUndo, dismissUndo } = useUndoSend();

  // Local UI state
  const [isEditModalVisible, setIsEditModalVisible] = useState(false);
  const [originalDraftBody, setOriginalDraftBody] = useState("");
  const [showApprovalSuccess, setShowApprovalSuccess] = useState(false);
  const [isUndoing, setIsUndoing] = useState(false);

  // Use either the initial draft (passed from DecisionInputScreen)
  // or the one managed by useDrafting (from modification)
  const currentDraft = draft ?? initialDraft;

  // Track original for comparison
  useEffect(() => {
    if (currentDraft && originalDraftBody === "") {
      setOriginalDraftBody(currentDraft.draft_body);
    }
  }, [currentDraft, originalDraftBody]);

  // Animated approval success banner
  const bannerOpacity = useSharedValue(0);
  const bannerTranslateY = useSharedValue(-20);

  const bannerStyle = useAnimatedStyle(() => ({
    opacity: bannerOpacity.value,
    transform: [{ translateY: bannerTranslateY.value }],
  }));

  const showBanner = useCallback(() => {
    bannerOpacity.value = withTiming(1, { duration: 300 });
    bannerTranslateY.value = withTiming(0, { duration: 300 });
    setTimeout(() => {
      bannerOpacity.value = withTiming(0, { duration: 500 });
      bannerTranslateY.value = withTiming(-20, { duration: 500 });
    }, 2500);
  }, [bannerOpacity, bannerTranslateY]);

  // ── Action Handlers ──────────────────────────────────────────────────────

  /** Open edit modal */
  const handleEdit = useCallback(() => {
    if (currentDraft) {
      setIsEditModalVisible(true);
    }
  }, [currentDraft]);

  /** Save edited draft */
  const handleSaveEdit = useCallback(
    (newBody: string) => {
      updateDraftBody(newBody);
      setIsEditModalVisible(false);
    },
    [updateDraftBody]
  );

  /** Shorten the draft */
  const handleShorten = useCallback(async () => {
    try {
      await modifyDraft("make it shorter");
    } catch {
      // Error handled in hook
    }
  }, [modifyDraft]);

  /** Formalize the draft */
  const handleFormalize = useCallback(async () => {
    try {
      await modifyDraft("make it more formal");
    } catch {
      // Error handled in hook
    }
  }, [modifyDraft]);

  /** Handle undo from the toast */
  const handleUndo = useCallback(async () => {
    setIsUndoing(true);
    const success = await performUndo();
    setIsUndoing(false);
    if (success) {
      // Revert the approval state
      setShowApprovalSuccess(false);
      // User stays on draft review screen to re-approve or edit
    }
  }, [performUndo]);

  /** Approve with confirmation dialog */
  const handleApprove = useCallback(() => {
    if (!currentDraft) return;

    // Show confirmation dialog (text mode)
    Alert.alert(
      "Approve and Send?",
      "This will queue the draft to be sent. You'll have 5 seconds to undo if needed.",
      [
        {
          text: "Cancel",
          style: "cancel",
          onPress: cancelConfirm,
        },
        {
          text: "Approve",
          style: "default",
          onPress: async () => {
            await confirmApprove(currentDraft.id, card.id);
            markApproved();
            setShowApprovalSuccess(true);
            showBanner();

            // Show undo toast with 5-second countdown
            showUndo(currentDraft.id, card.id);

            onApproved(currentDraft.id);

            // Auto-dismiss after undo window expires
            setTimeout(() => {
              onComplete();
            }, 6000);
          },
        },
      ],
      { cancelable: true }
    );
  }, [
    currentDraft,
    card.id,
    cancelConfirm,
    confirmApprove,
    markApproved,
    showBanner,
    showUndo,
    onApproved,
    onComplete,
  ]);

  // ── Render States ────────────────────────────────────────────────────────

  // Loading state (initial or modification)
  if (isLoading || phase === "loading") {
    return (
      <SafeAreaView style={[styles.container, { backgroundColor: colors.background }]}>
        <View style={styles.loadingContainer}>
          <LoadingSpinner message="Re-drafting..." />
          <Text style={[styles.loadingSubtext, { color: colors.textTertiary }]}>
            Updating your draft
          </Text>
        </View>
      </SafeAreaView>
    );
  }

  // Error state
  if (error || phase === "error") {
    return (
      <SafeAreaView style={[styles.container, { backgroundColor: colors.background }]}>
        <View style={styles.errorContainer}>
          <Text style={styles.errorIcon}>⚠️</Text>
          <Text style={[styles.errorTitle, { color: colors.textPrimary }]}>Draft failed</Text>
          <Text style={[styles.errorText, { color: colors.textSecondary }]}>
            {error ?? "Something went wrong generating your draft."}
          </Text>
          <View style={styles.errorActions}>
            <TouchableOpacity
              style={[styles.retryButton, { backgroundColor: colors.primary }]}
              onPress={onBack}
              activeOpacity={0.8}
            >
              <Text style={styles.retryButtonText}>Try Again</Text>
            </TouchableOpacity>
          </View>
        </View>
      </SafeAreaView>
    );
  }

  // No draft state (shouldn't happen in normal flow)
  if (!currentDraft) {
    return (
      <SafeAreaView style={[styles.container, { backgroundColor: colors.background }]}>
        <View style={styles.errorContainer}>
          <Text style={[styles.errorTitle, { color: colors.textPrimary }]}>No draft found</Text>
          <TouchableOpacity
            style={[styles.retryButton, { backgroundColor: colors.primary }]}
            onPress={onBack}
            activeOpacity={0.8}
          >
            <Text style={styles.retryButtonText}>Go Back</Text>
          </TouchableOpacity>
        </View>
      </SafeAreaView>
    );
  }

  // ── Main Render ──────────────────────────────────────────────────────────

  const isApproved = approvedDrafts.includes(currentDraft.id);

  return (
    <SafeAreaView style={[styles.container, { backgroundColor: colors.background }]}>
      {/* Approval success banner */}
      <Animated.View style={[styles.successBanner, bannerStyle, { backgroundColor: colors.success }]}>
        <Text style={[styles.successBannerText, { color: colors.textInverse }]}>
          ✓ Draft approved and queued for send
        </Text>
      </Animated.View>

      {/* Undo Send Toast */}
      {undoState.isVisible && (
        <View style={[styles.undoToast, { backgroundColor: colors.surfaceElevated, borderColor: colors.border }]}>
          <View style={styles.undoToastContent}>
            <Text style={[styles.undoToastText, { color: colors.textPrimary }]}>
              Sent!{" "}
              <Text style={[styles.undoTimer, { color: colors.info }]}>
                Undo? ({undoState.secondsRemaining}s)
              </Text>
            </Text>
            <TouchableOpacity
              style={[styles.undoButton, { backgroundColor: colors.infoMuted }]}
              onPress={handleUndo}
              disabled={isUndoing}
              activeOpacity={0.7}
              accessibilityLabel="Undo send"
              accessibilityRole="button"
            >
              <Text style={[styles.undoButtonText, { color: colors.info }]}>
                {isUndoing ? "Undoing…" : "Undo"}
              </Text>
            </TouchableOpacity>
          </View>
        </View>
      )}

      {/* Header */}
      <View style={[styles.header, { borderBottomColor: colors.border }]}>
        <Text style={[styles.headerTitle, { color: colors.textPrimary }]}>Review Draft</Text>
        {isApproved && (
          <View style={[styles.approvedBadge, { backgroundColor: colors.success }]}>
            <Text style={[styles.approvedBadgeText, { color: colors.textInverse }]}>Approved</Text>
          </View>
        )}
      </View>

      <ScrollView
        style={styles.scrollView}
        contentContainerStyle={styles.scrollContent}
        showsVerticalScrollIndicator={false}
      >
        {/* Draft preview card */}
        <View style={[styles.draftCard, { backgroundColor: colors.cardBackground, borderColor: colors.border }]}>
          <DraftBody
            draft={currentDraft}
            fromName={card.from.name}
            relationshipContext={card.from.relationship_context}
          />
        </View>

        {/* Modification notice if applicable */}
        {currentDraft.draft_body !== originalDraftBody && originalDraftBody !== "" && (
          <View style={[styles.modifiedNotice, { backgroundColor: colors.infoMuted, borderLeftColor: colors.info }]}>
            <Text style={[styles.modifiedNoticeText, { color: colors.info }]}>
              ✎ You've edited this draft from the original
            </Text>
          </View>
        )}

        {/* Spacer for actions */}
        <View style={styles.spacer} />
      </ScrollView>

      {/* Fixed action footer */}
      <View style={[styles.actionsFooter, { backgroundColor: colors.background, borderTopColor: colors.border }]}>
        <DraftActions
          onEdit={handleEdit}
          onShorten={handleShorten}
          onFormalize={handleFormalize}
          onApprove={handleApprove}
          onBack={onBack}
          isLoading={isLoading}
          isApproved={isApproved}
        />
      </View>

      {/* Edit Modal */}
      <EditModal
        visible={isEditModalVisible}
        originalBody={originalDraftBody || currentDraft.draft_body}
        currentBody={currentDraft.draft_body}
        onSave={handleSaveEdit}
        onCancel={() => setIsEditModalVisible(false)}
      />
    </SafeAreaView>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    flex: 1,
  },
  successBanner: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    paddingVertical: Space.sm,
    paddingHorizontal: Space.lg,
    zIndex: 100,
    alignItems: "center",
  },
  successBannerText: {
    ...Type.captionBold,
  },
  // ── Undo Toast ──────────────────────────────────────────────────────────
  undoToast: {
    position: "absolute",
    top: 44, // below status bar
    left: Space.lg,
    right: Space.lg,
    zIndex: 99,
    borderRadius: 12,
    borderWidth: 1,
    paddingVertical: Space.md,
    paddingHorizontal: Space.lg,
    shadowColor: "#000",
    shadowOffset: { width: 0, height: 4 },
    shadowOpacity: 0.1,
    shadowRadius: 12,
    elevation: 6,
  },
  undoToastContent: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
  },
  undoToastText: {
    ...Type.body,
    flex: 1,
  },
  undoTimer: {
    ...Type.bodyBold,
  },
  undoButton: {
    paddingVertical: Space.sm,
    paddingHorizontal: Space.md,
    borderRadius: 8,
    marginLeft: Space.md,
  },
  undoButtonText: {
    ...Type.captionBold,
  },
  // ── Header ──────────────────────────────────────────────────────────────
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: Space.lg,
    paddingVertical: Space.md,
    borderBottomWidth: 1,
    position: "relative",
  },
  headerTitle: {
    ...Type.title,
    fontWeight: "600",
  },
  approvedBadge: {
    position: "absolute",
    right: Space.lg,
    paddingHorizontal: Space.md,
    paddingVertical: Space.xs,
    borderRadius: 10,
  },
  approvedBadgeText: {
    ...Type.micro,
    fontWeight: "600",
  },
  scrollView: {
    flex: 1,
  },
  scrollContent: {
    padding: Space.lg,
    paddingBottom: Space.md,
  },
  draftCard: {
    width: "100%",
    borderRadius: CardDim.borderRadius,
    borderWidth: 1,
    padding: CardDim.innerPad,
  },
  modifiedNotice: {
    marginTop: Space.md,
    borderRadius: 10,
    padding: Space.md,
    borderLeftWidth: 3,
  },
  modifiedNoticeText: {
    ...Type.caption,
  },
  spacer: {
    height: Space.md,
  },
  actionsFooter: {
    padding: Space.lg,
    borderTopWidth: 1,
  },
  loadingContainer: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: Space.lg,
  },
  loadingSubtext: {
    ...Type.caption,
    marginTop: Space.md,
    textAlign: "center",
  },
  errorContainer: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: Space.lg,
  },
  errorIcon: {
    fontSize: 48,
    marginBottom: Space.md,
  },
  errorTitle: {
    ...Type.title,
    marginBottom: Space.sm,
    textAlign: "center",
  },
  errorText: {
    ...Type.body,
    textAlign: "center",
    marginBottom: Space.lg,
    lineHeight: 22,
  },
  errorActions: {
    flexDirection: "row",
    gap: Space.md,
  },
  retryButton: {
    paddingVertical: Space.md,
    paddingHorizontal: Space.xl,
    borderRadius: 14,
    minWidth: 140,
    alignItems: "center",
  },
  retryButtonText: {
    ...Type.subtitle,
    color: StaticColors.textInverse,
    fontWeight: "600",
  },
});
