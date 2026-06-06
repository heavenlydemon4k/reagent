// Decision Stack — Draft Generation State Machine
// Manages the full lifecycle of AI draft creation and refinement.
//
// State machine:
//   idle → loading → success|error
//   success → loading (modification: shorten/formalize/edit)
//   error → idle (retry) | loading (re-submit)

import { useState, useCallback, useRef } from "react";
import type { Draft } from "../types/cards";
import { submitDecision } from "../services/api";

export type DraftingPhase =
  | "idle"        // No draft generated yet
  | "loading"     // Waiting for AI response
  | "success"     // Draft received and valid
  | "error";      // Failed to generate draft

export interface DraftingState {
  phase: DraftingPhase;
  draft: Draft | null;
  error: string | null;
  originalInput: string;       // The user's initial instruction (preserved for Back)
  modificationCount: number;   // Track how many times user modified
}

const initialState: DraftingState = {
  phase: "idle",
  draft: null,
  error: null,
  originalInput: "",
  modificationCount: 0,
};

/**
 * useDrafting — Hook for draft generation state machine
 *
 * Usage:
 *   const { state, submitDecision: submit, modifyDraft, reset, isLoading } = useDrafting(cardId);
 *   await submit("Tell Sarah 9500, 2 weeks");
 *   await modifyDraft("make it shorter");
 */
export function useDrafting(cardId: string) {
  const [state, setState] = useState<DraftingState>(initialState);

  // Track abort controller for in-flight requests
  const abortRef = useRef<AbortController | null>(null);

  /**
   * Submit the user's initial decision instruction.
   * POST /cards/{cardId}/decide → receive draft → transition to success
   */
  const submitDecisionInput = useCallback(
    async (input: string) => {
      // Cancel any in-flight request
      if (abortRef.current) {
        abortRef.current.abort();
      }
      abortRef.current = new AbortController();

      setState((prev) => ({
        ...prev,
        phase: "loading",
        error: null,
        originalInput: input,
      }));

      try {
        const response = await submitDecision({
          card_id: cardId,
          decision: "approve",
          input,
        });

        const draft: Draft = {
          id: response.draft_id,
          card_id: cardId,
          draft_body: response.draft_body,
          subject_line: response.subject_line,
          user_approved: false,
          created_at: new Date().toISOString(),
        };

        setState((prev) => ({
          ...prev,
          phase: "success",
          draft,
          error: null,
        }));

        return draft;
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to generate draft";
        setState((prev) => ({
          ...prev,
          phase: "error",
          error: message,
        }));
        throw err;
      }
    },
    [cardId]
  );

  /**
   * Modify an existing draft with an instruction (shorten, formalize, etc.).
   * Re-submits with the modification instruction appended.
   */
  const modifyDraft = useCallback(
    async (instruction: string) => {
      if (abortRef.current) {
        abortRef.current.abort();
      }
      abortRef.current = new AbortController();

      const baseInput = state.originalInput;
      const modifiedInput = `${baseInput} (${instruction})`;

      setState((prev) => ({
        ...prev,
        phase: "loading",
        error: null,
        modificationCount: prev.modificationCount + 1,
      }));

      try {
        const response = await submitDecision({
          card_id: cardId,
          decision: "edit",
          input: modifiedInput,
        });

        const draft: Draft = {
          id: response.draft_id,
          card_id: cardId,
          draft_body: response.draft_body,
          subject_line: response.subject_line,
          user_approved: false,
          created_at: new Date().toISOString(),
        };

        setState((prev) => ({
          ...prev,
          phase: "success",
          draft,
          error: null,
        }));

        return draft;
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to modify draft";
        setState((prev) => ({
          ...prev,
          phase: "error",
          error: message,
        }));
        throw err;
      }
    },
    [cardId, state.originalInput]
  );

  /**
   * Reset the state machine to idle (used when navigating Back).
   * Preserves originalInput so user can re-submit.
   */
  const reset = useCallback(() => {
    if (abortRef.current) {
      abortRef.current.abort();
    }
    setState((prev) => ({
      ...initialState,
      originalInput: prev.originalInput, // Preserve for re-submission
    }));
  }, []);

  /**
   * Update draft body from inline editing.
   * Does NOT re-call the API — updates locally.
   */
  const updateDraftBody = useCallback((newBody: string) => {
    setState((prev) => {
      if (!prev.draft) return prev;
      return {
        ...prev,
        draft: {
          ...prev.draft,
          draft_body: newBody,
        },
      };
    });
  }, []);

  /**
   * Mark the current draft as user-approved locally.
   * This is an optimistic update before sync queue processes.
   */
  const markApproved = useCallback(() => {
    setState((prev) => {
      if (!prev.draft) return prev;
      return {
        ...prev,
        draft: {
          ...prev.draft,
          user_approved: true,
        },
      };
    });
  }, []);

  const isLoading = state.phase === "loading";

  return {
    // State
    phase: state.phase,
    draft: state.draft,
    error: state.error,
    originalInput: state.originalInput,
    modificationCount: state.modificationCount,
    isLoading,

    // Actions
    submitDecision: submitDecisionInput,
    modifyDraft,
    reset,
    updateDraftBody,
    markApproved,
  };
}
