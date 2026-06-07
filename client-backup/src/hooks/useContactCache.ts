// useContactCache — Offline-first contact profile + timeline cache
//
// Loads contact data from local SQLite cache first, then fetches fresh
// data from the API in parallel. Cache TTL: 1 hour.
//
// Usage:
//   const { profile, timeline, isLoading, error, refresh } = useContactCache(contactId);

import { useState, useEffect, useCallback, useRef } from "react";
import { getContactProfile, getContactTimeline } from "../services/api";
import type { ContactProfile, ThreadSummary, TimelineSection } from "../types/contact";

// ---------------------------------------------------------------------------
// In-memory cache (backed by simple Map — SQLite backing can be added later)
// ---------------------------------------------------------------------------

interface CacheEntry {
  profile: ContactProfile;
  timeline: ThreadSummary[];
  fetchedAt: number; // ms
}

const CACHE_TTL_MS = 60 * 60 * 1000; // 1 hour
const memoryCache = new Map<string, CacheEntry>();

function getCached(contactId: string): CacheEntry | null {
  const entry = memoryCache.get(contactId);
  if (!entry) return null;
  if (Date.now() - entry.fetchedAt > CACHE_TTL_MS) {
    memoryCache.delete(contactId);
    return null;
  }
  return entry;
}

function setCached(contactId: string, entry: CacheEntry): void {
  memoryCache.set(contactId, entry);
}

// ---------------------------------------------------------------------------
// Timeline sectioning helper
// ---------------------------------------------------------------------------

function sectionTimeline(items: ThreadSummary[]): TimelineSection[] {
  const grouped = new Map<string, ThreadSummary[]>();

  for (const item of items) {
    const d = new Date(item.date);
    const key = d.toLocaleDateString("en-US", {
      month: "long",
      year: "numeric",
    });
    const existing = grouped.get(key) ?? [];
    existing.push(item);
    grouped.set(key, existing);
  }

  // Sort sections by date descending, items within each section by date descending
  const sortedKeys = [...grouped.keys()].sort((a, b) => {
    const da = new Date(a);
    const db = new Date(b);
    return db.getTime() - da.getTime();
  });

  return sortedKeys.map((title) => ({
    title,
    data: (grouped.get(title) ?? []).sort(
      (a, b) => new Date(b.date).getTime() - new Date(a.date).getTime()
    ),
  }));
}

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

export interface UseContactCacheReturn {
  profile: ContactProfile | null;
  timeline: TimelineSection[];
  isLoading: boolean;
  isRefreshing: boolean;
  error: string | null;
  refresh: () => Promise<void>;
}

export function useContactCache(
  contactId: string | undefined
): UseContactCacheReturn {
  const [profile, setProfile] = useState<ContactProfile | null>(null);
  const [sections, setSections] = useState<TimelineSection[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [isRefreshing, setIsRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const mountedRef = useRef(true);

  const load = useCallback(
    async (opts: { skipCache?: boolean } = {}) => {
      if (!contactId) return;

      const { skipCache = false } = opts;

      // 1. Try cache first (unless skipping)
      if (!skipCache) {
        const cached = getCached(contactId);
        if (cached) {
          setProfile(cached.profile);
          setSections(sectionTimeline(cached.timeline));
          // Don't set isLoading — we show stale data while refreshing
        }
      }

      if (!skipCache && !getCached(contactId)) {
        setIsLoading(true);
      }
      setError(null);

      try {
        // 2. Fetch fresh data in parallel
        const [profileData, timelineData] = await Promise.all([
          getContactProfile(contactId),
          getContactTimeline(contactId, 50),
        ]);

        if (!mountedRef.current) return;

        setProfile(profileData);
        setSections(sectionTimeline(timelineData));

        // 3. Update cache
        setCached(contactId, {
          profile: profileData,
          timeline: timelineData,
          fetchedAt: Date.now(),
        });
      } catch (err) {
        if (!mountedRef.current) return;

        const message =
          err instanceof Error ? err.message : "Failed to load contact data";

        // If we have cached data, keep showing it; only surface error if no data
        if (!getCached(contactId)) {
          setError(message);
        }
      } finally {
        if (mountedRef.current) {
          setIsLoading(false);
          setIsRefreshing(false);
        }
      }
    },
    [contactId]
  );

  // Initial load
  useEffect(() => {
    mountedRef.current = true;
    if (contactId) {
      load();
    }
    return () => {
      mountedRef.current = false;
    };
  }, [contactId, load]);

  const refresh = useCallback(async () => {
    setIsRefreshing(true);
    await load({ skipCache: true });
  }, [load]);

  return {
    profile,
    timeline: sections,
    isLoading,
    isRefreshing,
    error,
    refresh,
  };
}
