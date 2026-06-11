// Decision Stack — Card Selection and Navigation Hook
// Bridges cardStore with DB operations and sync queueing

import { useCallback, useEffect } from 'react';
import { useCardStore } from '@stores/cardStore';
import { useSyncStore } from '@stores/syncStore';
import {
  openDatabase,
  getAllCards,
  decideCard as dbDecideCard,
  queueCardDecision,
} from '@services/db';
import { fetchBatch } from '@services/api';
import type { DecisionCard, CardState } from '../types/cards';

export interface UseCardsReturn {
  // State
  currentCard: DecisionCard | null;
  pendingCount: number;
  progress: { current: number; total: number };
  isLoading: boolean;
  hasCards: boolean;

  // Navigation
  loadCards: () => Promise<void>;
  nextCard: () => void;
  skipCard: () => void;

  // Actions
  decide: (input: string) => Promise<void>;
  approve: () => Promise<void>;
  consult: () => void;
  refreshBatch: () => Promise<void>;
}

export function useCards(): UseCardsReturn {
  const store = useCardStore();
  const syncStore = useSyncStore();

  const currentCard = store.getCurrentCard();
  const pendingCount = store.getPendingCount();
  const progress = store.getProgress();

  // Hydrate cards from local DB on mount
  useEffect(() => {
    let cancelled = false;

    async function hydrate() {
      try {
        await openDatabase();
        const localCards = getAllCards();
        if (!cancelled && localCards.length > 0) {
          store.loadBatch(localCards);
        }
      } catch {
        // DB not ready yet — will retry on explicit load
      }
    }

    hydrate();
    return () => {
      cancelled = true;
    };
  }, []);

  /**
   * Load cards from local DB and optionally fetch new batch from server.
   */
  const loadCards = useCallback(async () => {
    store.setLoading(true);
    try {
      await openDatabase();
      const localCards = getAllCards();
      store.loadBatch(localCards);

      // If we're low on cards, try server
      if (localCards.length < 3 && syncStore.networkAvailable) {
        try {
          const batch = await fetchBatch(10);
          if (batch.cards.length > 0) {
            store.loadBatch(batch.cards);
          }
        } catch {
          // Offline — local cards are sufficient
        }
      }
    } finally {
      store.setLoading(false);
    }
  }, [syncStore.networkAvailable]);

  /**
   * Move to the next card in the queue.
   */
  const nextCard = useCallback(() => {
    store.nextCard();
  }, [store]);

  /**
   * Skip current card (de-prioritize, move to end).
   */
  const skipCard = useCallback(() => {
    store.skipCard();
  }, [store]);

  /**
   * Submit a decision for the current card.
   * Offline-first: writes to DB + sync queue, then attempts immediate sync.
   */
  const decide = useCallback(
    async (input: string) => {
      const card = store.getCurrentCard();
      if (!card) return;

      // 1. Update local DB
      await openDatabase();
      dbDecideCard(card.id, input, 'drafting');

      // 2. Update store
      store.decideCurrent(input);

      // 3. Queue for sync
      queueCardDecision(card.id, 1, 'drafting', input);

      // 4. Attempt background sync
      if (syncStore.networkAvailable) {
        const { performSync } = await import('@services/sync');
        performSync().catch(() => {});
      }
    },
    [store, syncStore.networkAvailable]
  );

  /**
   * Approve the current card's draft.
   */
  const approve = useCallback(async () => {
    const card = store.getCurrentCard();
    if (!card) return;

    await openDatabase();
    store.updateCardState(card.id, 'approved');

    queueCardDecision(card.id, 1, 'approved');

    if (syncStore.networkAvailable) {
      const { performSync } = await import('@services/sync');
      performSync().catch(() => {});
    }
  }, [store, syncStore.networkAvailable]);

  /**
   * Enter consultation mode for the current card.
   */
  const consult = useCallback(() => {
    const card = store.getCurrentCard();
    if (!card) return;

    store.updateCardState(card.id, 'consulting');
  }, [store]);

  /**
   * Explicitly refresh card batch from server.
   */
  const refreshBatch = useCallback(async () => {
    if (!syncStore.networkAvailable) return;

    store.setLoading(true);
    try {
      const batch = await fetchBatch(10);
      store.loadBatch(batch.cards);
    } finally {
      store.setLoading(false);
    }
  }, [syncStore.networkAvailable]);

  return {
    currentCard,
    pendingCount,
    progress,
    isLoading: store.isLoading,
    hasCards: pendingCount > 0,

    loadCards,
    nextCard,
    skipCard,
    decide,
    approve,
    consult,
    refreshBatch,
  };
}
