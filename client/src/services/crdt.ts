// ============================================================================
// CRDT Merge Logic — Offline-First Sync Engine
// ============================================================================
// Merge rules (from spec):
// - server_version wins on draft_body (server's draft is authoritative)
// - user_decision wins on card_state (user's choice overrides)
// - newer timestamp wins on metadata
// - if server has newer card → update local
// - if local has pending decision → upload to server
//
// Invariant: merge is idempotent — same inputs always produce same outputs.
// ============================================================================

import type {
  DecisionCard,
  CardState,
  LocalChange,
  Draft,
  SyncQueueItem,
} from "../types/cards";

// ---------------------------------------------------------------------------
// Extended local types (what we store in SQLite beyond the wire format)
// ---------------------------------------------------------------------------

export interface LocalCard extends DecisionCard {
  /** local edit sequence number, monotonically incremented per mutation */
  local_version: number;
  /** set when the user makes a decision that has not yet been synced */
  local_decision?: string;
  /** true if this card has un-uploaded local changes */
  dirty: boolean;
  /** unix ms — when the local change was made */
  local_modified_at: number;
}

export interface LocalDraft extends Draft {
  /** true if the user created / edited this draft locally */
  dirty: boolean;
  local_modified_at: number;
}

// ---------------------------------------------------------------------------
// Merge result types
// ---------------------------------------------------------------------------

export interface Conflict {
  cardId: string;
  field: string;
  localValue: unknown;
  serverValue: unknown;
  resolution: "local_wins" | "server_wins";
}

export interface MergedResult {
  /** merged card state — what should end up in local SQLite */
  cards: DecisionCard[];
  /** changes that still need to be uploaded to the server */
  toUpload: LocalChange[];
  /** human-readable conflict log (for debugging / analytics) */
  conflicts: Conflict[];
}

export interface DraftMergeResult {
  /** merged drafts — what should end up in local SQLite */
  drafts: Draft[];
  /** drafts that need uploading (user-created or user-approved) */
  toUpload: LocalDraft[];
  conflicts: Conflict[];
}

// ---------------------------------------------------------------------------
// Timestamp helpers
// ---------------------------------------------------------------------------

function parseTime(iso: string | undefined): number {
  if (!iso) return 0;
  const t = Date.parse(iso);
  return isNaN(t) ? 0 : t;
}

function newerTimestamp(local: string | undefined, server: string | undefined): string {
  return parseTime(local) > parseTime(server) ? local! : server!;
}

// ---------------------------------------------------------------------------
// Decision priority — used when user_decision conflicts with server state
// ---------------------------------------------------------------------------

const STATE_PRIORITY: Record<CardState, number> = {
  pending: 0,
  consulting: 1,
  drafting: 2,
  approved: 3,
  sent: 4,
  archived: 5,
  expired: 6,
};

/**
 * Determine whether the local decision should override the server state.
 * User decisions (approve, edit, consult) always win over server-generated
 * states (pending, consulting, drafting) because the user has explicitly
 * acted.  Server "sent" / "archived" / "expired" win over local pending
 * because they represent completed workflows.
 */
function shouldUserDecisionWin(localState: CardState, serverState: CardState): boolean {
  // If server has reached a terminal state, trust it unless local is also terminal
  const terminalStates: CardState[] = ["sent", "archived", "expired"];
  if (terminalStates.includes(serverState) && !terminalStates.includes(localState)) {
    return false;
  }
  // Otherwise, user's explicit decision wins
  return true;
}

// ============================================================================
// CRDTMerge — deterministic, idempotent merge engine
// ============================================================================

export class CRDTMerge {
  /**
   * Merge local cards with server cards.
   *
   * Algorithm per card pair:
   * 1. If server.version > local.version:
   *      - Take server state for most fields (authoritative)
   *      - BUT preserve local_decision if user has pending input
   * 2. If local.dirty && local_decision exists:
   *      - Flag for upload (user decision wins)
   * 3. If conflict on card_state:
   *      - Apply shouldUserDecisionWin() rule
   * 4. Merge metadata timestamps (newer wins)
   */
  mergeCards(localCards: LocalCard[], serverCards: DecisionCard[]): MergedResult {
    const merged: DecisionCard[] = [];
    const toUpload: LocalChange[] = [];
    const conflicts: Conflict[] = [];

    // Build fast lookup for local cards
    const localMap = new Map<string, LocalCard>();
    for (const lc of localCards) {
      localMap.set(lc.id, lc);
    }

    // Process every server card
    for (const server of serverCards) {
      const local = localMap.get(server.id);

      if (!local) {
        // Server has a card we don't have locally — adopt it
        merged.push(server);
        continue;
      }

      // We have both local and server versions
      const serverVersion = parseTime(server.updated_at);
      const localVersion = parseTime(local.updated_at);
      const serverIsNewer = serverVersion > localVersion;

      // Start with server state as baseline (server is authoritative on content)
      let resultCard: DecisionCard = { ...server };

      // If local has a pending decision not yet synced, user wins
      if (local.dirty && local.local_decision) {
        const userWins = shouldUserDecisionWin(local.card_state, server.card_state);

        if (userWins) {
          resultCard.card_state = local.card_state;
          resultCard.user_decided_at = local.user_decided_at;
          conflicts.push({
            cardId: local.id,
            field: "card_state",
            localValue: local.card_state,
            serverValue: server.card_state,
            resolution: "local_wins",
          });
        } else {
          conflicts.push({
            cardId: local.id,
            field: "card_state",
            localValue: local.card_state,
            serverValue: server.card_state,
            resolution: "server_wins",
          });
        }

        // Queue the user's decision for upload
        toUpload.push({
          card_id: local.id,
          version: local.local_version,
          state: local.card_state,
          decision: local.local_decision,
        });
      }

      // If server has newer metadata, apply it (but don't clobber user's decision)
      if (serverIsNewer) {
        resultCard.urgency_score = server.urgency_score;
        resultCard.context = server.context;
        resultCard.they_want = server.they_want;
        resultCard.need_from_user = server.need_from_user;
        resultCard.chunk_citations = server.chunk_citations;
        resultCard.updated_at = server.updated_at;
      } else if (localVersion > serverVersion) {
        // Local has newer metadata (shouldn't happen often, but respect it)
        resultCard.updated_at = local.updated_at;
      }

      // Merge suggested_deadline: newer wins
      resultCard.suggested_deadline =
        parseTime(local.suggested_deadline) > parseTime(server.suggested_deadline)
          ? local.suggested_deadline
          : server.suggested_deadline;

      merged.push(resultCard);
      localMap.delete(server.id); // processed
    }

    // Any remaining local cards not on server — keep them (might be pending creation)
    for (const remaining of localMap.values()) {
      merged.push(remaining);
      if (remaining.dirty && remaining.local_decision) {
        toUpload.push({
          card_id: remaining.id,
          version: remaining.local_version,
          state: remaining.card_state,
          decision: remaining.local_decision,
        });
      }
    }

    return { cards: merged, toUpload, conflicts };
  }

  /**
   * Merge local drafts with server drafts.
   *
   * Rules:
   * - server wins on draft_body (server's LLM output is authoritative)
   * - local wins on user_approved (user's explicit approval is sacred)
   * - newer timestamp wins on metadata (tone_profile, subject_line, etc.)
   */
  mergeDrafts(localDrafts: LocalDraft[], serverDrafts: Draft[]): DraftMergeResult {
    const drafts: Draft[] = [];
    const toUpload: LocalDraft[] = [];
    const conflicts: Conflict[] = [];

    const localMap = new Map<string, LocalDraft>();
    for (const ld of localDrafts) {
      localMap.set(ld.id, ld);
    }

    for (const server of serverDrafts) {
      const local = localMap.get(server.id);

      if (!local) {
        drafts.push(server);
        continue;
      }

      // Start with server draft_body (server wins on content)
      const mergedDraft: Draft = {
        ...server,
        draft_body: server.draft_body, // server always wins
      };

      // User approval is sacred — local wins
      if (local.user_approved && !server.user_approved) {
        mergedDraft.user_approved = true;
        mergedDraft.sent_at = local.sent_at;
        conflicts.push({
          cardId: server.card_id,
          field: "user_approved",
          localValue: true,
          serverValue: false,
          resolution: "local_wins",
        });
        toUpload.push(local);
      }

      // Metadata: newer timestamp wins
      if (parseTime(local.created_at) > parseTime(server.created_at)) {
        mergedDraft.tone_profile = local.tone_profile ?? server.tone_profile;
        mergedDraft.subject_line = local.subject_line ?? server.subject_line;
        mergedDraft.in_reply_to = local.in_reply_to ?? server.in_reply_to;
        mergedDraft.references = local.references ?? server.references;
      }

      drafts.push(mergedDraft);
      localMap.delete(server.id);
    }

    // Remaining local drafts not on server
    for (const remaining of localMap.values()) {
      drafts.push(remaining);
      if (remaining.dirty || remaining.user_approved) {
        toUpload.push(remaining);
      }
    }

    return { drafts, toUpload, conflicts };
  }

  /**
   * Build a LocalChange[] payload from dirty queue items.
   * Filters out duplicates (keeps highest version per card).
   */
  buildLocalChangePayload(items: SyncQueueItem[]): LocalChange[] {
    const byCard = new Map<string, LocalChange[]>();

    for (const item of items) {
      const payload = item.payload as Partial<LocalChange> & { card_id?: string };
      const cardId = payload.card_id ?? (item.payload as { cardId?: string }).cardId;
      if (!cardId) continue;

      const change: LocalChange = {
        card_id: cardId,
        version: payload.version ?? 1,
        state: (payload.state ?? "pending") as CardState,
        decision: payload.decision,
        draft_body: payload.draft_body,
        approved_draft_id: payload.approved_draft_id,
      };

      const list = byCard.get(cardId) ?? [];
      list.push(change);
      byCard.set(cardId, list);
    }

    // For each card, keep only the highest-version change
    const result: LocalChange[] = [];
    for (const changes of byCard.values()) {
      changes.sort((a, b) => b.version - a.version);
      result.push(changes[0]!);
    }

    return result;
  }
}

// Singleton export
export const crdtMerge = new CRDTMerge();
