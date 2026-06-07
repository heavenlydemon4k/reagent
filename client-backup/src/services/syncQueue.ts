// ============================================================================
// Sync Queue — Persistent local operation queue (SQLCipher-backed)
// ============================================================================
// Every user action that must eventually reach the server is enqueued here.
// The queue survives app restarts (stored in SQLCipher) and is drained by
// SyncEngine.uploadChanges() on network reconnect.
//
// Invariants:
// - Items are removed ONLY after successful server ack.
// - retry_count is capped at 5; failed items are kept for inspection.
// - cleanup() removes completed items older than 7 days.
// ============================================================================

import type { SyncQueueItem } from "../types/cards";

// ---------------------------------------------------------------------------
// Queue operation types
// ---------------------------------------------------------------------------

export type QueueOperation =
  | "update_card"      // user decided on a card (approve / edit / consult)
  | "approve_draft"    // user approved a draft for sending
  | "create_draft";    // user created or edited a draft locally

// ---------------------------------------------------------------------------
// SQLite schema (for reference — table managed elsewhere)
// ---------------------------------------------------------------------------
// CREATE TABLE IF NOT EXISTS sync_queue (
//   id           TEXT PRIMARY KEY,
//   operation    TEXT NOT NULL CHECK(operation IN ('update_card','approve_draft','create_draft')),
//   payload_json TEXT NOT NULL,
//   created_at   INTEGER NOT NULL,  -- unix timestamp (ms)
//   retry_count  INTEGER NOT NULL DEFAULT 0,
//   completed_at INTEGER           -- set when item is done
// );
// CREATE INDEX IF NOT EXISTS idx_sync_queue_created ON sync_queue(created_at);
// CREATE INDEX IF NOT EXISTS idx_sync_queue_completed ON sync_queue(completed_at) WHERE completed_at IS NULL;
// ---------------------------------------------------------------------------

// Minimal DB interface — actual SQLite client injected at init
export interface QueueDatabase {
  run(sql: string, params?: unknown[]): Promise<{ changes: number }>;
  all<T>(sql: string, params?: unknown[]): Promise<T[]>;
  get<T>(sql: string, params?: unknown[]): Promise<T | undefined>;
}

let db: QueueDatabase | null = null;

/** Inject the SQLite database instance. Call once at app startup. */
export function initSyncQueue(database: QueueDatabase): void {
  db = database;
}

/** Ensure DB is initialized before use. */
function getDb(): QueueDatabase {
  if (!db) throw new Error("SyncQueue not initialized — call initSyncQueue() first");
  return db;
}

// ============================================================================
// SyncQueue — operation queue manager
// ============================================================================

export class SyncQueue {
  /**
   * Queue a local operation for later sync.
   *
   * @param operation  The type of operation
   * @param payload    Operation-specific data (must include card_id or cardId)
   */
  async enqueue(operation: QueueOperation, payload: unknown): Promise<void> {
    const database = getDb();
    const id = generateQueueId();
    const payloadJson = JSON.stringify(payload);
    const now = Date.now();

    await database.run(
      `INSERT INTO sync_queue (id, operation, payload_json, created_at, retry_count)
       VALUES (?, ?, ?, ?, 0)`,
      [id, operation, payloadJson, now],
    );
  }

  /** Retrieve all pending (not yet completed) operations, oldest first. */
  async getPending(): Promise<SyncQueueItem[]> {
    const database = getDb();
    const rows = await database.all<{
      id: string;
      operation: string;
      payload_json: string;
      created_at: number;
      retry_count: number;
    }>(
      `SELECT id, operation, payload_json, created_at, retry_count
       FROM sync_queue
       WHERE completed_at IS NULL
       ORDER BY created_at ASC`,
    );

    return rows.map((row) => ({
      id: row.id,
      operation: row.operation as SyncQueueItem["operation"],
      payload: JSON.parse(row.payload_json) as unknown,
      created_at: row.created_at,
      retry_count: row.retry_count,
    }));
  }

  /** Count pending operations (cheap query for UI badges). */
  async countPending(): Promise<number> {
    const database = getDb();
    const row = await database.get<{ count: number }>(
      `SELECT COUNT(*) as count FROM sync_queue WHERE completed_at IS NULL`,
    );
    return row?.count ?? 0;
  }

  /** Mark an operation as completed (removes from active pending set). */
  async complete(id: string): Promise<void> {
    const database = getDb();
    await database.run(
      `UPDATE sync_queue SET completed_at = ? WHERE id = ?`,
      [Date.now(), id],
    );
  }

  /** Mark multiple operations as completed in a single transaction. */
  async completeBatch(ids: string[]): Promise<void> {
    if (ids.length === 0) return;
    const database = getDb();
    const now = Date.now();
    // SQLite supports multiple value tuples in a single UPDATE via CASE
    const placeholders = ids.map(() => "?").join(",");
    await database.run(
      `UPDATE sync_queue SET completed_at = ? WHERE id IN (${placeholders})`,
      [now, ...ids],
    );
  }

  /** Increment retry count for an item. */
  async incrementRetry(id: string): Promise<void> {
    const database = getDb();
    await database.run(
      `UPDATE sync_queue SET retry_count = retry_count + 1 WHERE id = ?`,
      [id],
    );
  }

  /**
   * Remove completed items older than the given retention period.
   * Call this periodically (e.g., once per day) to prevent queue bloat.
   *
   * @param maxAgeMs  Maximum age in ms (default: 7 days)
   */
  async cleanup(maxAgeMs: number = 7 * 24 * 60 * 60 * 1000): Promise<void> {
    const database = getDb();
    const cutoff = Date.now() - maxAgeMs;
    await database.run(
      `DELETE FROM sync_queue WHERE completed_at IS NOT NULL AND completed_at < ?`,
      [cutoff],
    );
  }

  /**
   * Get items that have exceeded max retries (for error reporting / dead-letter).
   *
   * @param maxRetries  Default: 5
   */
  async getFailed(maxRetries: number = 5): Promise<SyncQueueItem[]> {
    const database = getDb();
    const rows = await database.all<{
      id: string;
      operation: string;
      payload_json: string;
      created_at: number;
      retry_count: number;
    }>(
      `SELECT id, operation, payload_json, created_at, retry_count
       FROM sync_queue
       WHERE completed_at IS NULL AND retry_count >= ?
       ORDER BY created_at ASC`,
      [maxRetries],
    );

    return rows.map((row) => ({
      id: row.id,
      operation: row.operation as SyncQueueItem["operation"],
      payload: JSON.parse(row.payload_json) as unknown,
      created_at: row.created_at,
      retry_count: row.retry_count,
    }));
  }

  /** Reset retry count for failed items (e.g., after server fix). */
  async resetRetries(ids: string[]): Promise<void> {
    if (ids.length === 0) return;
    const database = getDb();
    const placeholders = ids.map(() => "?").join(",");
    await database.run(
      `UPDATE sync_queue SET retry_count = 0 WHERE id IN (${placeholders})`,
      ids,
    );
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Generate a unique queue item ID. */
function generateQueueId(): string {
  return `sq_${Date.now()}_${Math.random().toString(36).slice(2, 9)}`;
}

// Singleton export
export const syncQueue = new SyncQueue();
