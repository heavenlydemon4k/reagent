// Decision Stack — SQLCipher Local Database via op-sqlite
// Offline-first persistence with encrypted at-rest storage
// Raw email bodies are NEVER stored locally — only card metadata and user decisions

import {
  open,
  type DB,
  type SQLCmdNames,
} from 'op-sqlite';
import { getOrCreateEncryptionKey } from './crypto';
import type {
  DecisionCard,
  FromField,
  CardContext,
  ChunkCitation,
  Draft,
  SyncQueueItem,
  CardState,
} from '@types/cards';

const DB_NAME = 'decisionstack';
let dbInstance: DB | null = null;

// ============================================================================
// INITIALIZATION
// ============================================================================

/**
 * Open (or create) the SQLCipher-encrypted database.
 * Must be called before any other DB operation.
 */
export async function openDatabase(): Promise<DB> {
  if (dbInstance) {
    return dbInstance;
  }

  const encryptionKey = await getOrCreateEncryptionKey();

  dbInstance = open({
    name: DB_NAME,
    encryptionKey,
  });

  await migrateSchema();
  return dbInstance;
}

/**
 * Close the database connection.
 */
export async function closeDatabase(): Promise<void> {
  if (dbInstance) {
    dbInstance.close();
    dbInstance = null;
  }
}

/**
 * Get the current DB instance (throws if not opened).
 */
export function getDB(): DB {
  if (!dbInstance) {
    throw new Error('Database not opened. Call openDatabase() first.');
  }
  return dbInstance;
}

// ============================================================================
// SCHEMA
// ============================================================================

const MIGRATIONS: string[] = [
  // Migration 0: Initial schema
  `
  CREATE TABLE IF NOT EXISTS local_cards (
    id TEXT PRIMARY KEY,
    server_version INTEGER DEFAULT 0,
    card_state TEXT CHECK(card_state IN ('pending','consulting','drafting','approved','sent','archived','expired')),
    from_json TEXT,
    they_want TEXT,
    context_json TEXT,
    need_from_user TEXT,
    citations_json TEXT,
    urgency_score REAL,
    local_decision TEXT,
    created_at TEXT,
    updated_at TEXT
  );

  CREATE TABLE IF NOT EXISTS local_drafts (
    id TEXT PRIMARY KEY,
    card_id TEXT NOT NULL,
    draft_body TEXT NOT NULL,
    subject_line TEXT,
    is_approved INTEGER DEFAULT 0,
    sent_at TEXT,
    created_at TEXT NOT NULL
  );

  CREATE TABLE IF NOT EXISTS sync_queue (
    id TEXT PRIMARY KEY,
    operation TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    retry_count INTEGER DEFAULT 0
  );

  CREATE INDEX IF NOT EXISTS idx_cards_state ON local_cards(card_state);
  CREATE INDEX IF NOT EXISTS idx_cards_urgency ON local_cards(urgency_score DESC);
  CREATE INDEX IF NOT EXISTS idx_drafts_card ON local_drafts(card_id);
  CREATE INDEX IF NOT EXISTS idx_sync_queue_created ON sync_queue(created_at);

  -- Streak tracking table for gamification
  CREATE TABLE IF NOT EXISTS streak_tracking (
    id INTEGER PRIMARY KEY CHECK(id = 1),
    current_streak INTEGER NOT NULL DEFAULT 0,
    last_decision_date TEXT,
    longest_streak INTEGER NOT NULL DEFAULT 0
  );
  `,

  // Migration 1: Multi-account support — email accounts table
  `
  CREATE TABLE IF NOT EXISTS email_accounts (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    provider TEXT NOT NULL CHECK(provider IN ('google', 'microsoft')),
    is_active INTEGER NOT NULL DEFAULT 1,
    connected_at TEXT NOT NULL
  );

  CREATE INDEX IF NOT EXISTS idx_accounts_email ON email_accounts(email);
  CREATE INDEX IF NOT EXISTS idx_accounts_active ON email_accounts(is_active);
  `,
];

async function migrateSchema(): Promise<void> {
  const db = getDB();

  db.execute(`
    CREATE TABLE IF NOT EXISTS schema_version (
      version INTEGER PRIMARY KEY
    );
  `);

  const result = db.execute('SELECT version FROM schema_version LIMIT 1;');
  let currentVersion = 0;
  if (result.rows && result.rows.length > 0) {
    currentVersion = (result.rows[0] as { version: number }).version;
  } else {
    db.execute('INSERT INTO schema_version (version) VALUES (0);');
  }

  for (let v = currentVersion; v < MIGRATIONS.length; v++) {
    db.execute(MIGRATIONS[v]);
    db.execute('UPDATE schema_version SET version = ?;', [v + 1]);
  }
}

// ============================================================================
// SERIALIZATION HELPERS
// ============================================================================

function serializeFrom(from: FromField): string {
  return JSON.stringify(from);
}

function deserializeFrom(json: string): FromField {
  return JSON.parse(json) as FromField;
}

function serializeContext(ctx: CardContext): string {
  return JSON.stringify(ctx);
}

function deserializeContext(json: string): CardContext {
  return JSON.parse(json) as CardContext;
}

function serializeCitations(citations: ChunkCitation[]): string {
  return JSON.stringify(citations);
}

function deserializeCitations(json: string): ChunkCitation[] {
  return JSON.parse(json) as ChunkCitation[];
}

// ============================================================================
// CARD CRUD
// ============================================================================

/**
 * Convert a DB row to DecisionCard.
 */
function rowToCard(row: Record<string, unknown>): DecisionCard {
  return {
    id: row.id as string,
    user_id: '', // Not stored locally — populated from auth context
    thread_id: '', // Not stored locally
    source_account_id: '', // Not stored locally
    card_state: row.card_state as CardState,
    from: deserializeFrom(row.from_json as string),
    they_want: row.they_want as string,
    context: row.context_json ? deserializeContext(row.context_json as string) : {},
    need_from_user: row.need_from_user as string,
    chunk_citations: row.citations_json ? deserializeCitations(row.citations_json as string) : [],
    urgency_score: row.urgency_score as number,
    created_at: row.created_at as string,
    updated_at: row.updated_at as string,
    user_decided_at: row.local_decision ? row.updated_at as string : undefined,
  };
}

/**
 * Upsert a card from server (server_version for conflict detection).
 */
export function upsertCard(card: DecisionCard, serverVersion: number): void {
  const db = getDB();
  db.execute(
    `
    INSERT INTO local_cards (
      id, server_version, card_state, from_json, they_want,
      context_json, need_from_user, citations_json, urgency_score,
      local_decision, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT (id) DO UPDATE SET
      server_version = excluded.server_version,
      card_state = excluded.card_state,
      from_json = excluded.from_json,
      they_want = excluded.they_want,
      context_json = excluded.context_json,
      need_from_user = excluded.need_from_user,
      citations_json = excluded.citations_json,
      urgency_score = excluded.urgency_score,
      updated_at = excluded.updated_at
    WHERE local_cards.server_version <= excluded.server_version;
    `,
    [
      card.id,
      serverVersion,
      card.card_state,
      serializeFrom(card.from),
      card.they_want,
      serializeContext(card.context),
      card.need_from_user,
      serializeCitations(card.chunk_citations),
      card.urgency_score,
      null, // local_decision — only set by user action
      card.created_at,
      card.updated_at,
    ]
  );
}

/**
 * Insert or update a batch of cards from server.
 */
export function upsertCards(cards: DecisionCard[], serverVersion: number): void {
  const db = getDB();
  db.transaction((tx) => {
    for (const card of cards) {
      tx.execute(
        `
        INSERT INTO local_cards (
          id, server_version, card_state, from_json, they_want,
          context_json, need_from_user, citations_json, urgency_score,
          local_decision, created_at, updated_at
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT (id) DO UPDATE SET
          server_version = excluded.server_version,
          card_state = CASE WHEN local_cards.server_version <= excluded.server_version
            THEN excluded.card_state ELSE local_cards.card_state END,
          from_json = CASE WHEN local_cards.server_version <= excluded.server_version
            THEN excluded.from_json ELSE local_cards.from_json END,
          they_want = CASE WHEN local_cards.server_version <= excluded.server_version
            THEN excluded.they_want ELSE local_cards.they_want END,
          context_json = CASE WHEN local_cards.server_version <= excluded.server_version
            THEN excluded.context_json ELSE local_cards.context_json END,
          need_from_user = CASE WHEN local_cards.server_version <= excluded.server_version
            THEN excluded.need_from_user ELSE local_cards.need_from_user END,
          citations_json = CASE WHEN local_cards.server_version <= excluded.server_version
            THEN excluded.citations_json ELSE local_cards.citations_json END,
          urgency_score = CASE WHEN local_cards.server_version <= excluded.server_version
            THEN excluded.urgency_score ELSE local_cards.urgency_score END,
          updated_at = CASE WHEN local_cards.server_version <= excluded.server_version
            THEN excluded.updated_at ELSE local_cards.updated_at END
        WHERE local_cards.server_version <= excluded.server_version;
        `,
        [
          card.id,
          serverVersion,
          card.card_state,
          serializeFrom(card.from),
          card.they_want,
          serializeContext(card.context),
          card.need_from_user,
          serializeCitations(card.chunk_citations),
          card.urgency_score,
          null,
          card.created_at,
          card.updated_at,
        ]
      );
    }
  });
}

/**
 * Get a single card by ID.
 */
export function getCardById(id: string): DecisionCard | null {
  const db = getDB();
  const result = db.execute('SELECT * FROM local_cards WHERE id = ?;', [id]);
  if (!result.rows || result.rows.length === 0) {
    return null;
  }
  return rowToCard(result.rows[0] as Record<string, unknown>);
}

/**
 * Get all cards ordered by urgency (highest first).
 */
export function getAllCards(): DecisionCard[] {
  const db = getDB();
  const result = db.execute(
    "SELECT * FROM local_cards WHERE card_state IN ('pending','consulting','drafting') ORDER BY urgency_score DESC, created_at ASC;"
  );
  if (!result.rows) {
    return [];
  }
  return result.rows.map((r) => rowToCard(r as Record<string, unknown>));
}

/**
 * Get cards filtered by state.
 */
export function getCardsByState(state: CardState): DecisionCard[] {
  const db = getDB();
  const result = db.execute(
    'SELECT * FROM local_cards WHERE card_state = ? ORDER BY urgency_score DESC, created_at ASC;',
    [state]
  );
  if (!result.rows) {
    return [];
  }
  return result.rows.map((r) => rowToCard(r as Record<string, unknown>));
}

/**
 * Get count of pending cards.
 */
export function getPendingCount(): number {
  const db = getDB();
  const result = db.execute(
    "SELECT COUNT(*) as count FROM local_cards WHERE card_state IN ('pending','consulting','drafting');"
  );
  if (!result.rows || result.rows.length === 0) {
    return 0;
  }
  return (result.rows[0] as { count: number }).count;
}

/**
 * Record a user's decision on a card (offline-first).
 */
export function decideCard(
  cardId: string,
  decision: string,
  newState: CardState
): void {
  const db = getDB();
  const now = new Date().toISOString();
  db.execute(
    `
    UPDATE local_cards
    SET local_decision = ?, card_state = ?, updated_at = ?
    WHERE id = ?;
    `,
    [decision, newState, now, cardId]
  );
}

/**
 * Update card state (e.g., after sync acknowledgment).
 */
export function updateCardState(cardId: string, newState: CardState): void {
  const db = getDB();
  const now = new Date().toISOString();
  db.execute(
    "UPDATE local_cards SET card_state = ?, updated_at = ? WHERE id = ?;",
    [newState, now, cardId]
  );
}

/**
 * Remove a card (soft-delete via server directive).
 */
export function removeCard(cardId: string): void {
  const db = getDB();
  db.execute("DELETE FROM local_cards WHERE id = ?;", [cardId]);
  // Cascade: remove associated drafts
  db.execute("DELETE FROM local_drafts WHERE card_id = ?;", [cardId]);
}

/**
 * Archive cards that have been sent or expired.
 */
export function archiveCompletedCards(): void {
  const db = getDB();
  const now = new Date().toISOString();
  db.execute(
    "UPDATE local_cards SET card_state = 'archived', updated_at = ? WHERE card_state IN ('sent','expired');",
    [now]
  );
}

// ============================================================================
// DRAFT CRUD
// ============================================================================

/**
 * Convert a DB row to Draft.
 */
function rowToDraft(row: Record<string, unknown>): Draft {
  return {
    id: row.id as string,
    card_id: row.card_id as string,
    draft_body: row.draft_body as string,
    subject_line: (row.subject_line as string) ?? undefined,
    user_approved: (row.is_approved as number) === 1,
    sent_at: (row.sent_at as string) ?? undefined,
    created_at: row.created_at as string,
  };
}

/**
 * Create a new draft for a card.
 */
export function createDraft(
  draft: Omit<Draft, 'user_approved' | 'sent_at'>
): void {
  const db = getDB();
  db.execute(
    `
    INSERT INTO local_drafts (id, card_id, draft_body, subject_line, is_approved, sent_at, created_at)
    VALUES (?, ?, ?, ?, 0, NULL, ?);
    `,
    [draft.id, draft.card_id, draft.draft_body, draft.subject_line ?? null, draft.created_at]
  );
}

/**
 * Get drafts for a card, newest first.
 */
export function getDraftsForCard(cardId: string): Draft[] {
  const db = getDB();
  const result = db.execute(
    'SELECT * FROM local_drafts WHERE card_id = ? ORDER BY created_at DESC;',
    [cardId]
  );
  if (!result.rows) {
    return [];
  }
  return result.rows.map((r) => rowToDraft(r as Record<string, unknown>));
}

/**
 * Get a single draft by ID.
 */
export function getDraftById(id: string): Draft | null {
  const db = getDB();
  const result = db.execute('SELECT * FROM local_drafts WHERE id = ?;', [id]);
  if (!result.rows || result.rows.length === 0) {
    return null;
  }
  return rowToDraft(result.rows[0] as Record<string, unknown>);
}

/**
 * Mark a draft as approved.
 */
export function approveDraft(draftId: string): void {
  const db = getDB();
  db.execute("UPDATE local_drafts SET is_approved = 1 WHERE id = ?;", [draftId]);
}

/**
 * Mark a draft as sent.
 */
export function markDraftSent(draftId: string): void {
  const db = getDB();
  const now = new Date().toISOString();
  db.execute("UPDATE local_drafts SET sent_at = ? WHERE id = ?;", [now, draftId]);
}

/**
 * Delete a draft.
 */
export function deleteDraft(draftId: string): void {
  const db = getDB();
  db.execute("DELETE FROM local_drafts WHERE id = ?;", [draftId]);
}

// ============================================================================
// SYNC QUEUE CRUD
// ============================================================================

/**
 * Add an operation to the sync queue.
 */
export function enqueueOperation(
  id: string,
  operation: string,
  payload: unknown
): void {
  const db = getDB();
  const now = Date.now();
  db.execute(
    'INSERT OR REPLACE INTO sync_queue (id, operation, payload_json, created_at, retry_count) VALUES (?, ?, ?, ?, 0);',
    [id, operation, JSON.stringify(payload), now]
  );
}

/**
 * Get all pending sync queue items, oldest first.
 */
export function getSyncQueue(): SyncQueueItem[] {
  const db = getDB();
  const result = db.execute(
    'SELECT * FROM sync_queue ORDER BY created_at ASC;'
  );
  if (!result.rows) {
    return [];
  }
  return result.rows.map((r) => ({
    id: (r as Record<string, unknown>).id as string,
    operation: (r as Record<string, unknown>).operation as SyncQueueItem['operation'],
    payload: JSON.parse((r as Record<string, unknown>).payload_json as string),
    created_at: (r as Record<string, unknown>).created_at as number,
    retry_count: (r as Record<string, unknown>).retry_count as number,
  }));
}

/**
 * Remove a completed item from the sync queue.
 */
export function dequeueOperation(id: string): void {
  const db = getDB();
  db.execute("DELETE FROM sync_queue WHERE id = ?;", [id]);
}

/**
 * Increment retry count for a queue item.
 */
export function incrementRetry(id: string): void {
  const db = getDB();
  db.execute(
    "UPDATE sync_queue SET retry_count = retry_count + 1 WHERE id = ?;",
    [id]
  );
}

/**
 * Remove items that have exceeded max retries.
 */
export function purgeFailedOperations(maxRetries = 10): void {
  const db = getDB();
  db.execute("DELETE FROM sync_queue WHERE retry_count >= ?;", [maxRetries]);
}

/**
 * Get count of pending sync operations.
 */
export function getSyncQueueCount(): number {
  const db = getDB();
  const result = db.execute('SELECT COUNT(*) as count FROM sync_queue;');
  if (!result.rows || result.rows.length === 0) {
    return 0;
  }
  return (result.rows[0] as { count: number }).count;
}

// ============================================================================
// STREAK TRACKING
// ============================================================================

export interface StreakRow {
  current_streak: number;
  last_decision_date: string;
  longest_streak: number;
}

const STREAK_TABLE_ROW_ID = 1;
const MS_PER_DAY = 24 * 60 * 60 * 1000;
const RESET_THRESHOLD_MS = 48 * 60 * 60 * 1000;

/**
 * Get streak data from the database.
 * Returns default values if no streak record exists yet.
 */
export function getStreakData(): {
  currentStreak: number;
  lastDecisionDate: string | null;
  longestStreak: number;
} {
  const db = getDB();
  const result = db.execute(
    'SELECT current_streak, last_decision_date, longest_streak FROM streak_tracking WHERE id = ?;',
    [STREAK_TABLE_ROW_ID]
  );

  if (!result.rows || result.rows.length === 0) {
    // Initialize default streak record
    db.execute(
      'INSERT OR IGNORE INTO streak_tracking (id, current_streak, last_decision_date, longest_streak) VALUES (?, 0, NULL, 0);',
      [STREAK_TABLE_ROW_ID]
    );
    return { currentStreak: 0, lastDecisionDate: null, longestStreak: 0 };
  }

  const row = result.rows[0] as StreakRow;
  return {
    currentStreak: row.current_streak ?? 0,
    lastDecisionDate: row.last_decision_date ?? null,
    longestStreak: row.longest_streak ?? 0,
  };
}

/**
 * Record a decision day. Increments streak if this is a new day,
 * resets if >48 hours since last decision, preserves longest streak.
 * Call this whenever user clears (approves/sends) a decision.
 */
export function recordDecisionDay(): {
  currentStreak: number;
  lastDecisionDate: string | null;
  longestStreak: number;
} {
  const db = getDB();
  const now = new Date();
  const nowIso = now.toISOString();

  // Ensure table has row
  db.execute(
    'INSERT OR IGNORE INTO streak_tracking (id, current_streak, last_decision_date, longest_streak) VALUES (?, 0, NULL, 0);',
    [STREAK_TABLE_ROW_ID]
  );

  // Get current values
  const current = getStreakData();

  let newStreak = current.currentStreak;
  let newLongest = current.longestStreak;

  if (!current.lastDecisionDate) {
    // First ever decision
    newStreak = 1;
    newLongest = Math.max(newLongest, 1);
  } else {
    const lastDate = new Date(current.lastDecisionDate);
    const lastDayStart = new Date(
      lastDate.getFullYear(), lastDate.getMonth(), lastDate.getDate()
    );
    const todayStart = new Date(
      now.getFullYear(), now.getMonth(), now.getDate()
    );
    const msSinceLast = todayStart.getTime() - lastDayStart.getTime();

    if (msSinceLast > RESET_THRESHOLD_MS) {
      // More than 48 hours — reset streak
      newStreak = 1;
    } else if (msSinceLast >= MS_PER_DAY) {
      // New day within threshold — increment streak
      newStreak = current.currentStreak + 1;
      newLongest = Math.max(newLongest, newStreak);
    }
    // Same day: do nothing (streak already counted for today)
  }

  // Persist
  db.execute(
    'UPDATE streak_tracking SET current_streak = ?, last_decision_date = ?, longest_streak = ? WHERE id = ?;',
    [newStreak, nowIso, newLongest, STREAK_TABLE_ROW_ID]
  );

  return {
    currentStreak: newStreak,
    lastDecisionDate: nowIso,
    longestStreak: newLongest,
  };
}

/**
 * Reset streak to 0 (e.g., on logout).
 */
export function resetStreak(): void {
  const db = getDB();
  db.execute(
    'UPDATE streak_tracking SET current_streak = 0, last_decision_date = NULL WHERE id = ?;',
    [STREAK_TABLE_ROW_ID]
  );
}

// ============================================================================
// BATCH OPERATIONS
// ============================================================================

/**
 * Clear all local data (logout / account reset).
 */
export function clearAllData(): void {
  const db = getDB();
  db.execute("DELETE FROM local_cards;");
  db.execute("DELETE FROM local_drafts;");
  db.execute("DELETE FROM sync_queue;");
}

/**
 * Get database statistics for diagnostics.
 */
export function getDatabaseStats(): {
  cards: number;
  drafts: number;
  syncQueue: number;
} {
  const db = getDB();
  const cards = db.execute("SELECT COUNT(*) as c FROM local_cards;");
  const drafts = db.execute("SELECT COUNT(*) as c FROM local_drafts;");
  const syncQ = db.execute("SELECT COUNT(*) as c FROM sync_queue;");
  return {
    cards: (cards.rows?.[0] as { c: number })?.c ?? 0,
    drafts: (drafts.rows?.[0] as { c: number })?.c ?? 0,
    syncQueue: (syncQ.rows?.[0] as { c: number })?.c ?? 0,
  };
}
