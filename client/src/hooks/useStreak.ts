// Decision Stack — Streak Tracking Hook
// Gamification that tracks consecutive days with >=1 decision cleared.
// Reads/writes streak data from local SQLite.

import { useState, useCallback, useEffect } from 'react';
import { openDatabase, getStreakData, recordDecisionDay } from '../services/db';

export interface StreakData {
  currentStreak: number;
  lastDecisionDate: string | null;
  longestStreak: number;
}

const STREAK_KEY = 'ds_streak_data';
const MS_PER_DAY = 24 * 60 * 60 * 1000;
const RESET_THRESHOLD_HOURS = 48;
const RESET_THRESHOLD_MS = RESET_THRESHOLD_HOURS * 60 * 60 * 1000;

/**
 * useStreak — Hook for tracking consecutive-day decision streaks
 *
 * Streak rules:
 *   - Increment when user clears >=1 decision in a day
 *   - Reset to 0 if >48 hours since last decision
 *   - Track longest streak for lifetime high score
 *
 * Usage:
 *   const { streak, incrementStreak, longestStreak } = useStreak();
 *   // Shows flame icon with count in header when streak > 0
 */
export function useStreak() {
  const [streak, setStreak] = useState<number>(0);
  const [longestStreak, setLongestStreak] = useState<number>(0);
  const [lastDecisionDate, setLastDecisionDate] = useState<string | null>(null);
  const [isHydrated, setIsHydrated] = useState(false);

  /**
   * Hydrate streak data from SQLite on mount.
   */
  useEffect(() => {
    let cancelled = false;

    async function hydrate() {
      try {
        await openDatabase();
        const data = getStreakData();
        if (!cancelled) {
          // Check if streak has expired (>48h)
          const effectiveStreak = computeEffectiveStreak(data);
          setStreak(effectiveStreak);
          setLongestStreak(data.longestStreak);
          setLastDecisionDate(data.lastDecisionDate);
          setIsHydrated(true);

          // If streak expired, update stored value
          if (effectiveStreak === 0 && data.currentStreak > 0) {
            recordDecisionDay(); // This resets it properly
          }
        }
      } catch {
        // DB not ready yet
        if (!cancelled) setIsHydrated(true);
      }
    }

    hydrate();
    return () => {
      cancelled = true;
    };
  }, []);

  /**
   * Increment the streak when user clears a decision.
   * Should be called after ANY decision is cleared (approved, sent, etc.).
   */
  const incrementStreak = useCallback(async () => {
    try {
      await openDatabase();
      const updated = recordDecisionDay();
      const effectiveStreak = computeEffectiveStreak(updated);
      setStreak(effectiveStreak);
      setLongestStreak(updated.longestStreak);
      setLastDecisionDate(updated.lastDecisionDate);
    } catch {
      // Fail silently — streak is non-critical
    }
  }, []);

  return {
    streak,
    longestStreak,
    lastDecisionDate,
    isHydrated,
    incrementStreak,
  };
}

/**
 * Compute the effective current streak, accounting for the 48-hour reset rule.
 */
function computeEffectiveStreak(data: StreakData): number {
  if (!data.lastDecisionDate || data.currentStreak === 0) {
    return 0;
  }

  const lastDate = new Date(data.lastDecisionDate);
  const now = new Date();

  // Normalize to start of day for day-boundary comparison
  const lastDayStart = new Date(
    lastDate.getFullYear(),
    lastDate.getMonth(),
    lastDate.getDate()
  );
  const todayStart = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate()
  );

  const msSinceLastDecision = todayStart.getTime() - lastDayStart.getTime();

  // If more than 48 hours have passed, streak is reset
  if (msSinceLastDecision > RESET_THRESHOLD_MS) {
    return 0;
  }

  // Streak is still valid
  return data.currentStreak;
}

/**
 * Get streak data synchronously (for non-hook usage).
 * Returns current streak data from the database.
 */
export function getStreakDataSync(): StreakData & { effectiveStreak: number } {
  try {
    const data = getStreakData();
    return {
      ...data,
      effectiveStreak: computeEffectiveStreak(data),
    };
  } catch {
    return {
      currentStreak: 0,
      lastDecisionDate: null,
      longestStreak: 0,
      effectiveStreak: 0,
    };
  }
}
