// Decision Stack — Keyboard Shortcuts Hook
// Power-user keyboard shortcuts for the card-based decision flow.
// Listens for key presses and invokes action callbacks.

import { useState, useEffect, useRef, useCallback } from 'react';

// ─── Supported Shortcut Keys ───────────────────────────────────────────────

export type ShortcutKey =
  | 'j'           // Next card
  | 'k'           // Previous card
  | 'd'           // Open decision input
  | 's'           // Skip card
  | 'c'           // Consult (open chat)
  | 'a'           // Approve draft
  | 'e'           // Edit draft
  | '?';          // Show shortcuts help

// ─── Action Callbacks ──────────────────────────────────────────────────────

export interface ShortcutActions {
  /** Navigate to next card */
  onNextCard?: () => void;
  /** Navigate to previous card */
  onPrevCard?: () => void;
  /** Open decision input for current card */
  onDecide?: () => void;
  /** Skip current card */
  onSkip?: () => void;
  /** Open consultation/chat for current card */
  onConsult?: () => void;
  /** Approve the current draft */
  onApprove?: () => void;
  /** Edit the current draft */
  onEdit?: () => void;
  /** Toggle shortcuts help overlay */
  onShowHelp?: () => void;
}

// ─── Configuration ─────────────────────────────────────────────────────────

export interface KeyboardShortcutConfig {
  /** Whether shortcuts are enabled (default: true) */
  enabled?: boolean;
  /** If true, ignores shortcuts when an input element is focused */
  ignoreWhenTyping?: boolean;
  /** Additional keys to ignore (e.g., modal open states) */
  isBlocked?: () => boolean;
}

// Mapping of keyboard events to shortcut keys
const KEY_MAP: Record<string, ShortcutKey> = {
  'j': 'j',
  'ArrowRight': 'j',
  'k': 'k',
  'ArrowLeft': 'k',
  'd': 'd',
  's': 's',
  'c': 'c',
  'a': 'a',
  'e': 'e',
  '?': '?',
};

/**
 * useKeyboardShortcuts — Hook for power-user keyboard shortcuts
 *
 * Usage:
 *   const { isHelpVisible, showHelp, hideHelp } = useKeyboardShortcuts({
 *     onNextCard: () => nextCard(),
 *     onSkip: () => skipCard(),
 *     onDecide: () => openDecisionInput(),
 *     onConsult: () => openChat(),
 *     onApprove: () => approveDraft(),
 *     onEdit: () => openEditModal(),
 *     onShowHelp: () => {}, // handled internally
 *   });
 *
 *   // Conditionally render help overlay
 *   {isHelpVisible && <ShortcutHelpOverlay onClose={hideHelp} />}
 */
export function useKeyboardShortcuts(
  actions: ShortcutActions,
  config: KeyboardShortcutConfig = {}
) {
  const {
    enabled = true,
    ignoreWhenTyping = true,
    isBlocked,
  } = config;

  const [isHelpVisible, setIsHelpVisible] = useState(false);
  const actionsRef = useRef(actions);
  const isHelpVisibleRef = useRef(isHelpVisible);

  // Keep refs in sync
  useEffect(() => {
    actionsRef.current = actions;
  }, [actions]);

  useEffect(() => {
    isHelpVisibleRef.current = isHelpVisible;
  }, [isHelpVisible]);

  const showHelp = useCallback(() => setIsHelpVisible(true), []);
  const hideHelp = useCallback(() => setIsHelpVisible(false), []);
  const toggleHelp = useCallback(
    () => setIsHelpVisible((prev) => !prev),
    []
  );

  useEffect(() => {
    if (!enabled) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      // Check global block (e.g., modal is open)
      if (isBlocked?.()) return;

      // Ignore when typing in input/textarea (unless it's the '?' shortcut)
      if (ignoreWhenTyping) {
        const target = event.target as HTMLElement | null;
        const tagName = target?.tagName?.toLowerCase();
        const isEditable =
          tagName === 'input' ||
          tagName === 'textarea' ||
          target?.isContentEditable;
        if (isEditable && event.key !== '?') return;
      }

      // Check for '?' which requires shift
      let shortcutKey: ShortcutKey | undefined;

      if (event.key === '?' || (event.shiftKey && event.key === '/')) {
        shortcutKey = '?';
      } else if (!event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey) {
        shortcutKey = KEY_MAP[event.key];
      }

      if (!shortcutKey) return;

      // Execute the mapped action
      const currentActions = actionsRef.current;
      const currentHelpVisible = isHelpVisibleRef.current;

      switch (shortcutKey) {
        case 'j':
          if (!currentHelpVisible) {
            event.preventDefault();
            currentActions.onNextCard?.();
          }
          break;
        case 'k':
          if (!currentHelpVisible) {
            event.preventDefault();
            currentActions.onPrevCard?.();
          }
          break;
        case 'd':
          if (!currentHelpVisible) {
            event.preventDefault();
            currentActions.onDecide?.();
          }
          break;
        case 's':
          if (!currentHelpVisible) {
            event.preventDefault();
            currentActions.onSkip?.();
          }
          break;
        case 'c':
          if (!currentHelpVisible) {
            event.preventDefault();
            currentActions.onConsult?.();
          }
          break;
        case 'a':
          if (!currentHelpVisible) {
            event.preventDefault();
            currentActions.onApprove?.();
          }
          break;
        case 'e':
          if (!currentHelpVisible) {
            event.preventDefault();
            currentActions.onEdit?.();
          }
          break;
        case '?':
          event.preventDefault();
          if (currentActions.onShowHelp) {
            currentActions.onShowHelp();
          } else {
            toggleHelp();
          }
          break;
      }
    };

    // Only add listener on web / desktop platforms
    if (typeof document !== 'undefined') {
      document.addEventListener('keydown', handleKeyDown);
      return () => {
        document.removeEventListener('keydown', handleKeyDown);
      };
    }
  }, [enabled, ignoreWhenTyping, isBlocked, toggleHelp]);

  return {
    isHelpVisible,
    showHelp,
    hideHelp,
    toggleHelp,
  };
}

// ============================================================================
// SHORTCUT DEFINITIONS (for help overlay)
// ============================================================================

export interface ShortcutDefinition {
  key: string;
  label: string;
  description: string;
}

export const SHORTCUT_DEFINITIONS: ShortcutDefinition[] = [
  { key: 'j', label: 'J or →', description: 'Next card' },
  { key: 'k', label: 'K or ←', description: 'Previous card' },
  { key: 'd', label: 'D', description: 'Open decision input' },
  { key: 's', label: 'S', description: 'Skip card' },
  { key: 'c', label: 'C', description: 'Consult (open chat)' },
  { key: 'a', label: 'A', description: 'Approve draft' },
  { key: 'e', label: 'E', description: 'Edit draft' },
  { key: '?', label: '?', description: 'Show keyboard shortcuts' },
];
