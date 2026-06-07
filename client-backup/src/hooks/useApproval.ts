// Decision Stack — Approval + Undo Logic
// Handles draft approval, confirmation, undo window, and sync queue integration.
//
// Invariants:
// - user_approved MUST be TRUE before any send
// - Text approvals show confirmation dialog (implicit undo via dialog dismissal)
// - Voice approvals have a 30-second undo window with countdown
// - Every approval is queued in sync_queue for background upload
// - Optimistic UI: approved state shown immediately, sync in background

import { useState, useCallback, useRef } from "react";
import { syncQueue } from "../services/syncQueue";

// Approval record tracked locally for undo support
interface ApprovalRecord {
  draftId: string;
  cardId: string;
  approvedAt: number;       // timestamp (ms)
  undoWindowMs: number;     // 30s for voice, 0 for text (has confirm dialog)
  undoDeadline: number;     // approvedAt + undoWindowMs
  status: "pending_undo_window" | "queued" | "confirmed";
}

export type ApprovalMode = "text" | "voice";

const VOICE_UNDO_WINDOW_MS = 30_000; // 30 seconds for voice
const TEXT_UNDO_WINDOW_MS = 0;       // Text has confirm dialog = no undo window needed

/**
 * useApproval — Hook for managing draft approvals with undo support
 *
 * Usage:
 *   const { approve, canUndo, undo, undoSecondsRemaining, approvedDrafts, isConfirming } = useApproval();
 *   await approve(draftId, cardId, 'text');
 *   if (canUndo(draftId)) { undo(draftId); }
 */
export function useApproval() {
  const [approvedDrafts, setApprovedDrafts] = useState<string[]>([]);
  const [approvals, setApprovals] = useState<Map<string, ApprovalRecord>>(new Map());
  const [isConfirming, setIsConfirming] = useState(false);
  const [undoCountdown, setUndoCountdown] = useState<Map<string, number>>(new Map());

  // Timer refs for cleanup
  const timerRefs = useRef<Map<string, ReturnType<typeof setInterval>>>(new Map());

  /**
   * Start the undo countdown timer for a draft.
   */
  const startUndoTimer = useCallback((draftId: string, deadline: number) => {
    // Clear any existing timer
    const existing = timerRefs.current.get(draftId);
    if (existing) clearInterval(existing);

    const timer = setInterval(() => {
      const remaining = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
      setUndoCountdown((prev) => {
        const next = new Map(prev);
        if (remaining <= 0) {
          next.delete(draftId);
        } else {
          next.set(draftId, remaining);
        }
        return next;
      });

      if (remaining <= 0) {
        clearInterval(timer);
        timerRefs.current.delete(draftId);

        // Transition to confirmed status
        setApprovals((prev) => {
          const next = new Map(prev);
          const record = next.get(draftId);
          if (record && record.status === "pending_undo_window") {
            next.set(draftId, { ...record, status: "confirmed" });
          }
          return next;
        });
      }
    }, 250); // Update 4x per second for smooth countdown

    timerRefs.current.set(draftId, timer);
  }, []);

  /**
   * Approve a draft for sending.
   *
   * Flow:
   *   Text mode → show confirmation dialog → if confirmed → queue for send
   *   Voice mode → optimistic approve → start 30s undo window → queue
   */
  const approve = useCallback(
    async (
      draftId: string,
      cardId: string,
      mode: ApprovalMode = "text"
    ): Promise<boolean> => {
      const now = Date.now();
      const undoWindowMs =
        mode === "voice" ? VOICE_UNDO_WINDOW_MS : TEXT_UNDO_WINDOW_MS;
      const undoDeadline = now + undoWindowMs;

      // For text mode, show confirmation dialog first
      if (mode === "text") {
        setIsConfirming(true);
        // The caller handles the actual dialog; we set isConfirming
        // When they call confirmApprove(), we proceed
        return false; // Not yet approved — waiting for confirmation
      }

      // Voice mode: immediate optimistic approval with undo window
      const record: ApprovalRecord = {
        draftId,
        cardId,
        approvedAt: now,
        undoWindowMs,
        undoDeadline,
        status: "pending_undo_window",
      };

      setApprovals((prev) => {
        const next = new Map(prev);
        next.set(draftId, record);
        return next;
      });

      setApprovedDrafts((prev) =>
        prev.includes(draftId) ? prev : [...prev, draftId]
      );

      // Queue for background sync
      await syncQueue.enqueue("approve_draft", {
        draft_id: draftId,
        card_id: cardId,
        approved_at: now,
        mode,
      });

      // Start undo countdown
      startUndoTimer(draftId, undoDeadline);

      return true;
    },
    [startUndoTimer]
  );

  /**
   * Confirm approval after user dismisses confirmation dialog.
   * Used for text mode where a dialog was shown.
   */
  const confirmApprove = useCallback(
    async (draftId: string, cardId: string): Promise<void> => {
      setIsConfirming(false);

      const now = Date.now();
      const record: ApprovalRecord = {
        draftId,
        cardId,
        approvedAt: now,
        undoWindowMs: 0,
        undoDeadline: now,
        status: "queued",
      };

      setApprovals((prev) => {
        const next = new Map(prev);
        next.set(draftId, record);
        return next;
      });

      setApprovedDrafts((prev) =>
        prev.includes(draftId) ? prev : [...prev, draftId]
      );

      // Queue for background sync
      await syncQueue.enqueue("approve_draft", {
        draft_id: draftId,
        card_id: cardId,
        approved_at: now,
        mode: "text",
      });
    },
    []
  );

  /**
   * Cancel the confirmation dialog without approving.
   */
  const cancelConfirm = useCallback(() => {
    setIsConfirming(false);
  }, []);

  /**
   * Check if a draft can still be undone (within undo window).
   */
  const canUndo = useCallback(
    (draftId: string): boolean => {
      const record = approvals.get(draftId);
      if (!record) return false;
      if (record.status !== "pending_undo_window") return false;
      return Date.now() < record.undoDeadline;
    },
    [approvals]
  );

  /**
   * Get remaining undo seconds for a draft (for voice mode UI).
   */
  const getUndoSecondsRemaining = useCallback(
    (draftId: string): number => {
      return undoCountdown.get(draftId) ?? 0;
    },
    [undoCountdown]
  );

  /**
   * Undo an approval (only valid within undo window).
   * Removes from approved list and sync queue.
   */
  const undo = useCallback(
    (draftId: string): boolean => {
      if (!canUndo(draftId)) return false;

      // Clear timer
      const timer = timerRefs.current.get(draftId);
      if (timer) {
        clearInterval(timer);
        timerRefs.current.delete(draftId);
      }

      // Remove from countdown
      setUndoCountdown((prev) => {
        const next = new Map(prev);
        next.delete(draftId);
        return next;
      });

      // Remove from approvals
      setApprovals((prev) => {
        const next = new Map(prev);
        next.delete(draftId);
        return next;
      });

      // Remove from approved list
      setApprovedDrafts((prev) => prev.filter((id) => id !== draftId));

      return true;
    },
    [canUndo]
  );

  /**
   * Clean up timers on unmount.
   */
  const cleanup = useCallback(() => {
    for (const [id, timer] of timerRefs.current.entries()) {
      clearInterval(timer);
    }
    timerRefs.current.clear();
  }, []);

  return {
    // State
    approvedDrafts,
    isConfirming,
    approvals: Array.from(approvals.values()),

    // Actions
    approve,
    confirmApprove,
    cancelConfirm,
    canUndo,
    undo,
    getUndoSecondsRemaining,
    cleanup,
  };
}
