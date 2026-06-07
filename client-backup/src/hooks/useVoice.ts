// Decision Stack — Voice Mode State Machine Hook
// Manages the voice interaction flow: intro → listening → transcribing → drafting → confirming → sending → undo_window

import { useCallback, useEffect, useRef } from 'react';
import { useUIStore } from '@stores/uiStore';
import { useCardStore } from '@stores/cardStore';
import { wsClient } from '@services/websocket';
import type { VoiceModeState, VoiceTranscription } from '@types/cards';

const UNDO_WINDOW_MS = 5000;

export interface UseVoiceReturn {
  // State
  isActive: boolean;
  phase: VoiceModeState['phase'];
  transcription: string;
  draftPreview: string | null;
  undoSecondsRemaining: number;
  canUndo: boolean;

  // Lifecycle
  start: () => void;
  stop: () => void;

  // Phase transitions
  beginListening: () => void;
  submitTranscription: () => void;
  confirmSend: () => void;
  undo: () => void;

  // Streaming
  onTranscriptionUpdate: (update: VoiceTranscription) => void;
  onDraftGenerated: (draft: string, subject?: string) => void;
}

export function useVoice(): UseVoiceReturn {
  const uiStore = useUIStore();
  const cardStore = useCardStore();
  const undoTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const wsUnsubRef = useRef<(() => void) | null>(null);

  const voice = uiStore.voiceState;
  const currentCard = cardStore.getCurrentCard();

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      if (undoTimerRef.current) {
        clearInterval(undoTimerRef.current);
      }
      wsUnsubRef.current?.();
    };
  }, []);

  /**
   * Start voice mode for the current card.
   */
  const start = useCallback(() => {
    const card = cardStore.getCurrentCard();
    if (!card) return;

    uiStore.startVoiceMode();

    // Subscribe to WebSocket draft updates
    wsUnsubRef.current = wsClient.on('draft.updated', (event) => {
      const payload = event.payload as {
        draft_id: string;
        body: string;
        subject_line?: string;
      };
      uiStore.setDraftPreview(payload.body);
      uiStore.setVoicePhase('confirming');
    });

    // Connect WS and join voice session
    wsClient.connect();
    wsClient.joinVoiceSession(card.id);

    // Progress from intro → listening after brief delay
    const introDelay = setTimeout(() => {
      uiStore.setVoicePhase('listening');
    }, 1500);

    return () => clearTimeout(introDelay);
  }, [cardStore, uiStore]);

  /**
   * Stop voice mode and clean up.
   */
  const stop = useCallback(() => {
    uiStore.stopVoiceMode();
    wsUnsubRef.current?.();
    wsUnsubRef.current = null;

    if (undoTimerRef.current) {
      clearInterval(undoTimerRef.current);
      undoTimerRef.current = null;
    }
  }, [uiStore]);

  /**
   * Begin listening for voice input.
   */
  const beginListening = useCallback(() => {
    uiStore.setVoicePhase('listening');
  }, [uiStore]);

  /**
   * Submit transcription to server for draft generation.
   */
  const submitTranscription = useCallback(() => {
    if (!voice.transcription) return;

    uiStore.setVoicePhase('transcribing');

    // Send to WebSocket for server-side draft generation
    const card = cardStore.getCurrentCard();
    if (card) {
      wsClient.send('voice.transcription', {
        card_id: card.id,
        text: voice.transcription,
      });
    }

    // Transition to drafting (will be overridden when draft arrives via WS)
    const fallbackTimer = setTimeout(() => {
      uiStore.setVoicePhase('drafting');
    }, 500);

    return () => clearTimeout(fallbackTimer);
  }, [voice.transcription, cardStore, uiStore]);

  /**
   * Confirm and send the draft.
   */
  const confirmSend = useCallback(() => {
    const card = cardStore.getCurrentCard();
    if (!card) return;

    uiStore.setVoicePhase('sending');

    // Send approval via WebSocket
    wsClient.send('draft.approved', {
      card_id: card.id,
      draft_body: voice.draft_preview,
    });

    // Advance card in store
    cardStore.updateCardState(card.id, 'sent');
    cardStore.nextCard();

    // Start undo window
    uiStore.setVoicePhase('undo_window');

    let remaining = Math.floor(UNDO_WINDOW_MS / 1000);
    undoTimerRef.current = setInterval(() => {
      remaining -= 1;
      if (remaining <= 0) {
        // Undo window expired — finalize
        if (undoTimerRef.current) {
          clearInterval(undoTimerRef.current);
          undoTimerRef.current = null;
        }
        stop();
      }
    }, 1000);
  }, [voice.draft_preview, cardStore, uiStore, stop]);

  /**
   * Undo the last send action.
   */
  const undo = useCallback(() => {
    if (undoTimerRef.current) {
      clearInterval(undoTimerRef.current);
      undoTimerRef.current = null;
    }

    // Revert card state
    const card = cardStore.getCurrentCard();
    if (card) {
      cardStore.updateCardState(card.id, 'drafting');
    }

    // Return to confirming phase
    uiStore.setVoicePhase('confirming');
  }, [cardStore, uiStore]);

  /**
   * Handle streaming transcription updates.
   */
  const onTranscriptionUpdate = useCallback(
    (update: VoiceTranscription) => {
      uiStore.setVoiceTranscription(update);

      if (update.is_final) {
        // Auto-advance to transcribing
        submitTranscription();
      }
    },
    [uiStore, submitTranscription]
  );

  /**
   * Handle server-generated draft.
   */
  const onDraftGenerated = useCallback(
    (draft: string, subject?: string) => {
      uiStore.setDraftPreview(draft);
      uiStore.setVoicePhase('confirming');

      // Also update via WS if connected
      const card = cardStore.getCurrentCard();
      if (card) {
        wsClient.sendDraftUpdate(card.id, draft, subject);
      }
    },
    [uiStore, cardStore]
  );

  return {
    isActive: uiStore.isVoiceModeActive,
    phase: voice.phase,
    transcription: voice.transcription,
    draftPreview: voice.draft_preview,
    undoSecondsRemaining: voice.undo_seconds_remaining,
    canUndo: voice.phase === 'undo_window',

    start,
    stop,
    beginListening,
    submitTranscription,
    confirmSend,
    undo,
    onTranscriptionUpdate,
    onDraftGenerated,
  };
}
