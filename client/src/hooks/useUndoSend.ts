// Decision Stack — Undo Send Hook
// Provides a 5-second undo window after draft approval in text mode.
// Cancels the send by calling POST /drafts/{id}/cancel and removing from sync queue.

import { useState, useCallback, useRef, useEffect } from 'react';
import { api } from '../services/api';

export interface UndoSendState {
  /** Whether the undo toast is currently visible */
  isVisible: boolean;
  /** Draft ID that was just approved */
  draftId: string | null;
  /** Card ID associated with the draft */
  cardId: string | null;
  /** Seconds remaining in the undo window */
  secondsRemaining: number;
}

const UNDO_WINDOW_SECONDS = 5;

/**
 * useUndoSend — Hook for managing the undo send toast flow
 *
 * Usage:
 *   const { state, showUndo, performUndo, dismissUndo } = useUndoSend();
 *
 *   // After user approves a draft:
 *   showUndo(draftId, cardId);
 *
 *   // If user taps "Undo" in the toast:
 *   await performUndo();
 *
 *   // The toast auto-dismisses after 5 seconds
 */
export function useUndoSend() {
  const [state, setState] = useState<UndoSendState>({
    isVisible: false,
    draftId: null,
    cardId: null,
    secondsRemaining: 0,
  });

  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Clean up timers on unmount
  useEffect(() => {
    return () => {
      if (timerRef.current) clearInterval(timerRef.current);
      if (timeoutRef.current) clearTimeout(timeoutRef.current);
    };
  }, []);

  /**
   * Show the undo toast with a 5-second countdown.
   */
  const showUndo = useCallback((draftId: string, cardId: string) => {
    // Clear any existing timers
    if (timerRef.current) clearInterval(timerRef.current);
    if (timeoutRef.current) clearTimeout(timeoutRef.current);

    setState({
      isVisible: true,
      draftId,
      cardId,
      secondsRemaining: UNDO_WINDOW_SECONDS,
    });

    // Start countdown interval
    timerRef.current = setInterval(() => {
      setState((prev) => {
        const next = prev.secondsRemaining - 1;
        if (next <= 0) {
          return { ...prev, secondsRemaining: 0 };
        }
        return { ...prev, secondsRemaining: next };
      });
    }, 1000);

    // Auto-dismiss after window expires
    timeoutRef.current = setTimeout(() => {
      if (timerRef.current) clearInterval(timerRef.current);
      setState({
        isVisible: false,
        draftId: null,
        cardId: null,
        secondsRemaining: 0,
      });
    }, UNDO_WINDOW_SECONDS * 1000);
  }, []);

  /**
   * Perform the undo action: call cancel API and clear state.
   * Returns true if undo was successful.
   */
  const performUndo = useCallback(async (): Promise<boolean> => {
    const { draftId, cardId } = state;
    if (!draftId || !cardId) return false;

    // Clear timers
    if (timerRef.current) clearInterval(timerRef.current);
    if (timeoutRef.current) clearTimeout(timeoutRef.current);

    try {
      await cancelDraftSend(draftId);

      // Hide toast
      setState({
        isVisible: false,
        draftId: null,
        cardId: null,
        secondsRemaining: 0,
      });

      return true;
    } catch {
      // Even if API fails, hide the toast — the draft may already be sent
      setState({
        isVisible: false,
        draftId: null,
        cardId: null,
        secondsRemaining: 0,
      });
      return false;
    }
  }, [state]);

  /**
   * Dismiss the undo toast without performing undo.
   */
  const dismissUndo = useCallback(() => {
    if (timerRef.current) clearInterval(timerRef.current);
    if (timeoutRef.current) clearTimeout(timeoutRef.current);

    setState({
      isVisible: false,
      draftId: null,
      cardId: null,
      secondsRemaining: 0,
    });
  }, []);

  return {
    state,
    showUndo,
    performUndo,
    dismissUndo,
  };
}

// ============================================================================
// API
// ============================================================================

/**
 * Cancel a draft send before it leaves the sync queue.
 * POST /drafts/{id}/cancel
 */
export async function cancelDraftSend(draftId: string): Promise<void> {
  await api.post(`/drafts/${draftId}/cancel`);
}
