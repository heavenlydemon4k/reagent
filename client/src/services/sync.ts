// ============================================================================
// SyncEngine — Main offline-first sync orchestrator
// ============================================================================
// Responsibilities:
// 1. Upload local changes to server ( drained from syncQueue )
// 2. Download server updates and merge via CRDT
// 3. Produce a SyncReport for UI / analytics
//
// Invariants:
// - Idempotent: running sync() twice with the same state is a no-op
// - Queue items removed ONLY after server ack
// - CRDT merge: server wins on drafts, user wins on decisions
// - SQLCipher-backed queue survives app restart
// ============================================================================

import type {
  SyncRequest,
  SyncResponse,
  SyncQueueItem,
  DecisionCard,
  LocalChange,
  RejectedChange,
} from "../types/cards";
import { crdtMerge, type LocalCard, type LocalDraft } from "./crdt";
import { syncQueue } from "./syncQueue";

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const SYNC_ENDPOINT = `${import.meta.env.VITE_API_URL ?? "https://api.decisionstack.io"}/sync`;
const BATCH_ENDPOINT = `${import.meta.env.VITE_API_URL ?? "https://api.decisionstack.io"}/batch`;

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface SyncResult {
  uploaded: number;
  accepted: number;
  rejected: number;
  newCards: number;
  updatedCards: number;
}

export interface SyncReport extends SyncResult {
  /** ISO 8601 timestamp of this sync attempt */
  completedAt: string;
  /** milliseconds spent in sync */
  durationMs: number;
  /** any rejected changes with server reasons */
  rejections: RejectedChange[];
  /** CRDT conflict log */
  conflicts: Array<{
    cardId: string;
    field: string;
    resolution: "local_wins" | "server_wins";
  }>;
  /** errors that did not stop the sync (e.g., non-fatal merge issues) */
  warnings: string[];
  /** fatal error that aborted sync, if any */
  error?: string;
}

/** Per-device sync cursor stored in SQLite */
interface SyncCursor {
  device_id: string;
  last_sync_version: number;
  last_sync_at: number; // unix ms
}

// ---------------------------------------------------------------------------
// Database interface (provided by caller at init)
// ---------------------------------------------------------------------------

export interface SyncDatabase {
  run(sql: string, params?: unknown[]): Promise<{ changes: number }>;
  all<T>(sql: string, params?: unknown[]): Promise<T[]>;
  get<T>(sql: string, params?: unknown[]): Promise<T | undefined>;
}

let db: SyncDatabase | null = null;
let getDeviceId: (() => string) | null = null;
let getAuthToken: (() => Promise<string | null>) | null = null;

/** Initialise the sync engine with its dependencies. Call once at startup. */
export function initSyncEngine(deps: {
  database: SyncDatabase;
  getDeviceId: () => string;
  getAuthToken: () => Promise<string | null>;
}): void {
  db = deps.database;
  getDeviceId = deps.getDeviceId;
  getAuthToken = deps.getAuthToken;
}

function getDb(): SyncDatabase {
  if (!db) throw new Error("SyncEngine not initialized — call initSyncEngine() first");
  return db;
}

// ============================================================================
// SyncEngine
// ============================================================================

export class SyncEngine {
  // -------------------------------------------------------------------------
  // 1. Upload local changes
  // -------------------------------------------------------------------------

  async uploadChanges(): Promise<SyncResult> {
    const database = getDb();
    const deviceId = getDeviceId!();
    const cursor = await this.getCursor();

    // 1. Read pending items from sync_queue
    const pending = await syncQueue.getPending();
    if (pending.length === 0) {
      return { uploaded: 0, accepted: 0, rejected: 0, newCards: 0, updatedCards: 0 };
    }

    // 2. Build LocalChange[] payload via CRDT (deduped, highest version per card)
    const localChanges = crdtMerge.buildLocalChangePayload(pending);

    const request: SyncRequest = {
      device_id: deviceId,
      last_sync_version: cursor.last_sync_version,
      local_changes: localChanges,
    };

    // 3. POST /sync
    const token = await getAuthToken!();
    const response = await fetch(SYNC_ENDPOINT, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify(request),
    });

    if (!response.ok) {
      const body = await response.text();
      throw new Error(`Sync upload failed: ${response.status} ${body}`);
    }

    const result: SyncResponse = await response.json();

    // 4. Handle accepted_changes — mark queue items completed
    const acceptedIds = new Set(result.accepted_changes);
    const completedQueueIds = pending
      .filter((item) => {
        const payload = item.payload as { card_id?: string; cardId?: string };
        const cardId = payload.card_id ?? payload.cardId;
        return cardId && acceptedIds.has(cardId);
      })
      .map((item) => item.id);

    await syncQueue.completeBatch(completedQueueIds);

    // 5. Handle rejected_changes — increment retry, log for analytics
    for (const rej of result.rejected_changes) {
      const queueItem = pending.find((item) => {
        const payload = item.payload as { card_id?: string; cardId?: string };
        return (payload.card_id ?? payload.cardId) === rej.card_id;
      });
      if (queueItem) {
        await syncQueue.incrementRetry(queueItem.id);
      }
    }

    // 6. Update last_sync_version
    await this.saveCursor({
      device_id: deviceId,
      last_sync_version: result.server_version,
      last_sync_at: Date.now(),
    });

    // 7. Apply new_cards and updated_cards to local SQLite
    await this.applyServerCards(result.new_cards, "insert");
    await this.applyServerCards(result.updated_cards, "update");
    await this.removeServerCards(result.removed_cards);

    return {
      uploaded: pending.length,
      accepted: result.accepted_changes.length,
      rejected: result.rejected_changes.length,
      newCards: result.new_cards.length,
      updatedCards: result.updated_cards.length,
    };
  }

  // -------------------------------------------------------------------------
  // 2. Download updates from server
  // -------------------------------------------------------------------------

  async downloadUpdates(): Promise<{
    newCards: number;
    updatedCards: number;
    conflicts: SyncReport["conflicts"];
  }> {
    const token = await getAuthToken!();
    const response = await fetch(BATCH_ENDPOINT, {
      headers: {
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
    });

    if (!response.ok) {
      const body = await response.text();
      throw new Error(`Batch download failed: ${response.status} ${body}`);
    }

    const serverCards: DecisionCard[] = await response.json();

    // Fetch local cards for merge
    const localCards = await this.getLocalCards();

    // CRDT merge
    const mergeResult = crdtMerge.mergeCards(localCards, serverCards);

    // Apply merged cards to local DB
    await this.applyMergedCards(mergeResult.cards);

    // Queue any pending uploads that the merge identified
    for (const change of mergeResult.toUpload) {
      await syncQueue.enqueue("update_card", change);
    }

    return {
      newCards: serverCards.length,
      updatedCards: mergeResult.cards.length,
      conflicts: mergeResult.conflicts.map((c) => ({
        cardId: c.cardId,
        field: c.field,
        resolution: c.resolution,
      })),
    };
  }

  // -------------------------------------------------------------------------
  // 3. Full sync cycle
  // -------------------------------------------------------------------------

  async sync(): Promise<SyncReport> {
    const startedAt = Date.now();
    const report: SyncReport = {
      uploaded: 0,
      accepted: 0,
      rejected: 0,
      newCards: 0,
      updatedCards: 0,
      completedAt: new Date().toISOString(),
      durationMs: 0,
      rejections: [],
      conflicts: [],
      warnings: [],
    };

    try {
      // Phase 1: upload local changes
      const uploadResult = await this.uploadChanges();
      report.uploaded = uploadResult.uploaded;
      report.accepted = uploadResult.accepted;
      report.rejected = uploadResult.rejected;
      report.newCards = uploadResult.newCards;
      report.updatedCards = uploadResult.updatedCards;

      // Phase 2: download server updates
      const downloadResult = await this.downloadUpdates();
      report.newCards += downloadResult.newCards;
      report.updatedCards += downloadResult.updatedCards;
      report.conflicts = downloadResult.conflicts;
    } catch (err) {
      report.error = err instanceof Error ? err.message : String(err);
      report.warnings.push(report.error);
    }

    report.durationMs = Date.now() - startedAt;
    report.completedAt = new Date().toISOString();

    // Persist last sync time for UI
    await this.recordSyncAttempt(report.error ? "failed" : "success", report.durationMs);

    return report;
  }

  // -------------------------------------------------------------------------
  // Local SQLite helpers
  // -------------------------------------------------------------------------

  private async getCursor(): Promise<SyncCursor> {
    const database = getDb();
    const row = await database.get<{
      device_id: string;
      last_sync_version: number;
      last_sync_at: number;
    }>(
      `SELECT device_id, last_sync_version, last_sync_at FROM sync_cursor WHERE id = 1`,
    );

    if (row) {
      return {
        device_id: row.device_id,
        last_sync_version: row.last_sync_version,
        last_sync_at: row.last_sync_at,
      };
    }

    // First sync — create cursor
    const deviceId = getDeviceId!();
    const cursor: SyncCursor = {
      device_id: deviceId,
      last_sync_version: 0,
      last_sync_at: 0,
    };
    await database.run(
      `INSERT INTO sync_cursor (id, device_id, last_sync_version, last_sync_at) VALUES (1, ?, 0, 0)`,
      [deviceId],
    );
    return cursor;
  }

  private async saveCursor(cursor: SyncCursor): Promise<void> {
    const database = getDb();
    await database.run(
      `UPDATE sync_cursor SET device_id = ?, last_sync_version = ?, last_sync_at = ? WHERE id = 1`,
      [cursor.device_id, cursor.last_sync_version, cursor.last_sync_at],
    );
  }

  private async getLocalCards(): Promise<LocalCard[]> {
    const database = getDb();
    const rows = await database.all<{
      id: string;
      user_id: string;
      thread_id: string;
      source_account_id: string;
      card_state: string;
      from_json: string;
      they_want: string;
      context_json: string;
      need_from_user: string;
      chunk_citations_json: string;
      urgency_score: number;
      auto_handle_rule_id?: string;
      classification_confidence?: number;
      suggested_deadline?: string;
      user_decided_at?: string;
      sent_at?: string;
      created_at: string;
      updated_at: string;
      local_version: number;
      local_decision?: string;
      dirty: number;
      local_modified_at: number;
    }>(`SELECT * FROM cards`);

    return rows.map((row) => ({
      id: row.id,
      user_id: row.user_id,
      thread_id: row.thread_id,
      source_account_id: row.source_account_id,
      card_state: row.card_state as DecisionCard["card_state"],
      from: JSON.parse(row.from_json) as DecisionCard["from"],
      they_want: row.they_want,
      context: JSON.parse(row.context_json) as DecisionCard["context"],
      need_from_user: row.need_from_user,
      chunk_citations: JSON.parse(row.chunk_citations_json) as DecisionCard["chunk_citations"],
      urgency_score: row.urgency_score,
      auto_handle_rule_id: row.auto_handle_rule_id,
      classification_confidence: row.classification_confidence,
      suggested_deadline: row.suggested_deadline,
      user_decided_at: row.user_decided_at,
      sent_at: row.sent_at,
      created_at: row.created_at,
      updated_at: row.updated_at,
      local_version: row.local_version,
      local_decision: row.local_decision ?? undefined,
      dirty: Boolean(row.dirty),
      local_modified_at: row.local_modified_at,
    }));
  }

  private async applyServerCards(
    cards: DecisionCard[],
    mode: "insert" | "update",
  ): Promise<void> {
    const database = getDb();
    for (const card of cards) {
      if (mode === "insert") {
        await database.run(
          `INSERT OR REPLACE INTO cards (
            id, user_id, thread_id, source_account_id, card_state,
            from_json, they_want, context_json, need_from_user,
            chunk_citations_json, urgency_score, auto_handle_rule_id,
            classification_confidence, suggested_deadline, user_decided_at,
            sent_at, created_at, updated_at, local_version, dirty, local_modified_at
          ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
          [
            card.id, card.user_id, card.thread_id, card.source_account_id,
            card.card_state, JSON.stringify(card.from), card.they_want,
            JSON.stringify(card.context), card.need_from_user,
            JSON.stringify(card.chunk_citations), card.urgency_score,
            card.auto_handle_rule_id ?? null, card.classification_confidence ?? null,
            card.suggested_deadline ?? null, card.user_decided_at ?? null,
            card.sent_at ?? null, card.created_at, card.updated_at,
            0, Date.now(),
          ],
        );
      } else {
        await database.run(
          `UPDATE cards SET
            user_id = ?, thread_id = ?, source_account_id = ?, card_state = ?,
            from_json = ?, they_want = ?, context_json = ?, need_from_user = ?,
            chunk_citations_json = ?, urgency_score = ?, auto_handle_rule_id = ?,
            classification_confidence = ?, suggested_deadline = ?, user_decided_at = ?,
            sent_at = ?, updated_at = ?, dirty = 0
           WHERE id = ?`,
          [
            card.user_id, card.thread_id, card.source_account_id, card.card_state,
            JSON.stringify(card.from), card.they_want, JSON.stringify(card.context),
            card.need_from_user, JSON.stringify(card.chunk_citations), card.urgency_score,
            card.auto_handle_rule_id ?? null, card.classification_confidence ?? null,
            card.suggested_deadline ?? null, card.user_decided_at ?? null,
            card.sent_at ?? null, card.updated_at, card.id,
          ],
        );
      }
    }
  }

  private async removeServerCards(cardIds: string[]): Promise<void> {
    if (cardIds.length === 0) return;
    const database = getDb();
    const placeholders = cardIds.map(() => "?").join(",");
    await database.run(`DELETE FROM cards WHERE id IN (${placeholders})`, cardIds);
  }

  private async applyMergedCards(cards: DecisionCard[]): Promise<void> {
    const database = getDb();
    for (const card of cards) {
      await database.run(
        `INSERT OR REPLACE INTO cards (
          id, user_id, thread_id, source_account_id, card_state,
          from_json, they_want, context_json, need_from_user,
          chunk_citations_json, urgency_score, auto_handle_rule_id,
          classification_confidence, suggested_deadline, user_decided_at,
          sent_at, created_at, updated_at, dirty, local_modified_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
        [
          card.id, card.user_id, card.thread_id, card.source_account_id,
          card.card_state, JSON.stringify(card.from), card.they_want,
          JSON.stringify(card.context), card.need_from_user,
          JSON.stringify(card.chunk_citations), card.urgency_score,
          card.auto_handle_rule_id ?? null, card.classification_confidence ?? null,
          card.suggested_deadline ?? null, card.user_decided_at ?? null,
          card.sent_at ?? null, card.created_at, card.updated_at, Date.now(),
        ],
      );
    }
  }

  private async recordSyncAttempt(
    status: "success" | "failed",
    durationMs: number,
  ): Promise<void> {
    const database = getDb();
    await database.run(
      `INSERT INTO sync_history (status, duration_ms, created_at) VALUES (?, ?, ?)`,
      [status, durationMs, Date.now()],
    );
  }
}

// Singleton export
export const syncEngine = new SyncEngine();

/**
 * Convenience wrapper: perform a full sync cycle using the singleton engine.
 * Re-throws errors so callers can handle them.
 */
export async function performSync(): Promise<SyncReport> {
  return syncEngine.sync();
}

/**
 * Re-export background sync functions for unified sync imports.
 */
export {
  registerBackgroundSync,
  unregisterBackgroundSync,
} from './backgroundSync';
