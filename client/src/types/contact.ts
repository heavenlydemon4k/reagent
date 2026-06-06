// Contact Profile + Timeline Type Definitions
// Wire contracts for the Contact Profile screen and Timeline component.

// ============================================================================
// CONTACT PROFILE
// ============================================================================

/** A single tone history entry — timestamped tone classification */
export interface ToneEntry {
  date: string; // ISO 8601
  tone: "professional" | "friendly" | "urgent" | "formal" | "casual" | string;
}

/** Full contact profile — surfaced from Neo4j relationship graph */
export interface ContactProfile {
  id: string; // contact UUID
  name: string;
  email: string;
  avatarUrl?: string;

  // Interaction stats
  interactionCount: number;
  avgResponseHours: number;
  totalMonetaryValue: number;
  projects: string[];

  // Tone trajectory over time
  toneHistory: ToneEntry[];

  // Temporal bounds
  firstContactDate: string; // ISO 8601
  lastContactDate: string; // ISO 8601

  // Optional enrichment
  company?: string;
  title?: string;
  relationshipStrength?: number; // 0.0 - 1.0
}

// ============================================================================
// CONTACT TIMELINE
// ============================================================================

/** A single thread summary entry in the contact timeline */
export interface ThreadSummary {
  id: string; // thread UUID
  subject: string;
  date: string; // ISO 8601
  messageCount: number;
  decision?: string; // user's decision text if card was cleared
  status: "active" | "resolved" | "archived";
  preview?: string; // first ~120 chars of most recent message
}

/** Timeline section header — one per month */
export interface TimelineSection {
  title: string; // e.g. "June 2025"
  data: ThreadSummary[];
}

// ============================================================================
// CACHE
// ============================================================================

/** Cached contact profile entry with TTL */
export interface CachedContactProfile {
  profile: ContactProfile;
  timeline: ThreadSummary[];
  cachedAt: number; // unix timestamp (ms)
  ttlMs: number; // default: 1 hour = 3_600_000
}

// ============================================================================
// API REQUESTS / RESPONSES
// ============================================================================

export interface GetContactProfileResponse {
  profile: ContactProfile;
}

export interface GetContactTimelineResponse {
  items: ThreadSummary[];
  hasMore: boolean;
  nextCursor?: string;
}

// ============================================================================
// NAVIGATION
// ============================================================================

/** Route params for ContactProfileScreen */
export interface ContactProfileRouteParams {
  contactId: string;
  contactName: string;
  contactEmail: string;
}
