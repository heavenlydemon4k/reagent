// Decision Stack — Card Store (Zustand)
// Manages the one-card-at-a-time queue: pending, current, history

import { create } from 'zustand';
import type { DecisionCard, CardState } from '@types/cards';

export interface CardStore {
  // ── State ──────────────────────────────────────────────────────
  cards: DecisionCard[];
  currentIndex: number;
  batchSize: number;
  isLoading: boolean;

  // ── Computed (via selectors) ───────────────────────────────────
  getCurrentCard: () => DecisionCard | null;
  getPendingCount: () => number;
  getProgress: () => { current: number; total: number };

  // ── Actions ────────────────────────────────────────────────────
  loadBatch: (cards: DecisionCard[]) => void;
  nextCard: () => void;
  skipCard: () => void;
  decideCurrent: (input: string) => void;
  updateCardState: (cardId: string, newState: CardState) => void;
  removeCard: (cardId: string) => void;
  resetQueue: () => void;
  setLoading: (loading: boolean) => void;
}

export const useCardStore = create<CardStore>((set, get) => ({
  // ── Initial State ──────────────────────────────────────────────
  cards: [],
  currentIndex: 0,
  batchSize: 10,
  isLoading: false,

  // ── Selectors ──────────────────────────────────────────────────

  getCurrentCard: () => {
    const { cards, currentIndex } = get();
    if (cards.length === 0 || currentIndex >= cards.length) {
      return null;
    }
    return cards[currentIndex];
  },

  getPendingCount: () => {
    const { cards, currentIndex } = get();
    return Math.max(0, cards.length - currentIndex);
  },

  getProgress: () => {
    const { cards, currentIndex } = get();
    return {
      current: cards.length > 0 ? currentIndex + 1 : 0,
      total: cards.length,
    };
  },

  // ── Actions ────────────────────────────────────────────────────

  loadBatch: (newCards: DecisionCard[]) =>
    set((state) => {
      const merged = [...state.cards];
      for (const card of newCards) {
        const idx = merged.findIndex((c) => c.id === card.id);
        if (idx >= 0) {
          // Replace if server version is newer (check updated_at as proxy)
          if (card.updated_at > merged[idx].updated_at) {
            merged[idx] = card;
          }
        } else {
          merged.push(card);
        }
      }
      // Sort by urgency desc
      merged.sort((a, b) => b.urgency_score - a.urgency_score);
      return {
        cards: merged,
        batchSize: newCards.length,
        isLoading: false,
      };
    }),

  nextCard: () =>
    set((state) => ({
      currentIndex: Math.min(state.currentIndex + 1, state.cards.length),
    })),

  skipCard: () =>
    set((state) => {
      const skipped = state.cards[state.currentIndex];
      if (!skipped) return state;

      const reordered = [...state.cards];
      // Move skipped card to end of queue
      reordered.splice(state.currentIndex, 1);
      reordered.push({ ...skipped, urgency_score: skipped.urgency_score * 0.5 });

      return {
        cards: reordered,
        currentIndex: state.currentIndex, // stay at same index
      };
    }),

  decideCurrent: (input: string) =>
    set((state) => {
      const card = state.cards[state.currentIndex];
      if (!card) return state;

      const updated = [...state.cards];
      updated[state.currentIndex] = {
        ...card,
        card_state: 'drafting',
        user_decided_at: new Date().toISOString(),
      };

      return { cards: updated };
    }),

  updateCardState: (cardId: string, newState: CardState) =>
    set((state) => {
      const idx = state.cards.findIndex((c) => c.id === cardId);
      if (idx === -1) return state;

      const updated = [...state.cards];
      updated[idx] = { ...updated[idx], card_state: newState };
      return { cards: updated };
    }),

  removeCard: (cardId: string) =>
    set((state) => {
      const idx = state.cards.findIndex((c) => c.id === cardId);
      if (idx === -1) return state;

      const updated = [...state.cards];
      updated.splice(idx, 1);

      // Adjust currentIndex if we removed before or at current
      const newIndex =
        idx <= state.currentIndex
          ? Math.max(0, state.currentIndex - 1)
          : state.currentIndex;

      return {
        cards: updated,
        currentIndex: Math.min(newIndex, updated.length),
      };
    }),

  resetQueue: () =>
    set({
      cards: [],
      currentIndex: 0,
      batchSize: 10,
      isLoading: false,
    }),

  setLoading: (loading: boolean) => set({ isLoading: loading }),
}));
