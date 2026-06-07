// Decision Stack — Account SQLite Persistence
// Offline-first storage for connected email accounts.
// Mirrors AsyncStorage state into SQLCipher for durability.

import { getDB } from './db';
import type { EmailAccount } from '@types/cards';

// ============================================================================
// SCHEMA (part of db.ts migrations — Migration 1 adds this table)
// ============================================================================

export const ACCOUNT_TABLE_MIGRATION = `
  CREATE TABLE IF NOT EXISTS email_accounts (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    provider TEXT NOT NULL CHECK(provider IN ('google', 'microsoft')),
    is_active INTEGER NOT NULL DEFAULT 1,
    connected_at TEXT NOT NULL
  );

  CREATE INDEX IF NOT EXISTS idx_accounts_email ON email_accounts(email);
  CREATE INDEX IF NOT EXISTS idx_accounts_active ON email_accounts(is_active);
`;

// ============================================================================
// SERIALIZATION HELPERS
// ============================================================================

function rowToAccount(row: Record<string, unknown>): EmailAccount {
  return {
    id: row.id as string,
    email: row.email as string,
    provider: row.provider as 'google' | 'microsoft',
    isActive: (row.is_active as number) === 1,
    connectedAt: row.connected_at as string,
  };
}

function accountToRow(account: EmailAccount): {
  id: string;
  email: string;
  provider: string;
  is_active: number;
  connected_at: string;
} {
  return {
    id: account.id,
    email: account.email,
    provider: account.provider,
    is_active: account.isActive ? 1 : 0,
    connected_at: account.connectedAt,
  };
}

// ============================================================================
// CRUD OPERATIONS
// ============================================================================

/**
 * Upsert an account (insert or update).
 */
export function upsertAccount(account: EmailAccount): void {
  const db = getDB();
  const row = accountToRow(account);
  db.execute(
    `
    INSERT INTO email_accounts (id, email, provider, is_active, connected_at)
    VALUES (?, ?, ?, ?, ?)
    ON CONFLICT (id) DO UPDATE SET
      email = excluded.email,
      provider = excluded.provider,
      is_active = excluded.is_active,
      connected_at = excluded.connected_at;
    `,
    [row.id, row.email, row.provider, row.is_active, row.connected_at]
  );
}

/**
 * Batch upsert accounts from server sync.
 */
export function upsertAccounts(accounts: EmailAccount[]): void {
  const db = getDB();
  db.transaction((tx) => {
    for (const account of accounts) {
      const row = accountToRow(account);
      tx.execute(
        `
        INSERT INTO email_accounts (id, email, provider, is_active, connected_at)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT (id) DO UPDATE SET
          email = excluded.email,
          provider = excluded.provider,
          is_active = excluded.is_active,
          connected_at = excluded.connected_at;
        `,
        [row.id, row.email, row.provider, row.is_active, row.connected_at]
      );
    }
  });
}

/**
 * Get all connected accounts.
 */
export function getAllAccounts(): EmailAccount[] {
  const db = getDB();
  const result = db.execute(
    'SELECT id, email, provider, is_active, connected_at FROM email_accounts ORDER BY connected_at DESC;'
  );
  if (!result.rows) {
    return [];
  }
  return result.rows.map((r) => rowToAccount(r as Record<string, unknown>));
}

/**
 * Get a single account by ID.
 */
export function getAccountById(accountId: string): EmailAccount | null {
  const db = getDB();
  const result = db.execute(
    'SELECT id, email, provider, is_active, connected_at FROM email_accounts WHERE id = ?;',
    [accountId]
  );
  if (!result.rows || result.rows.length === 0) {
    return null;
  }
  return rowToAccount(result.rows[0] as Record<string, unknown>);
}

/**
 * Get account by email address.
 */
export function getAccountByEmail(email: string): EmailAccount | null {
  const db = getDB();
  const result = db.execute(
    'SELECT id, email, provider, is_active, connected_at FROM email_accounts WHERE email = ?;',
    [email]
  );
  if (!result.rows || result.rows.length === 0) {
    return null;
  }
  return rowToAccount(result.rows[0] as Record<string, unknown>);
}

/**
 * Delete an account by ID.
 */
export function deleteAccount(accountId: string): void {
  const db = getDB();
  db.execute('DELETE FROM email_accounts WHERE id = ?;', [accountId]);
}

/**
 * Delete all accounts (logout / account reset).
 */
export function deleteAllAccounts(): void {
  const db = getDB();
  db.execute('DELETE FROM email_accounts;');
}

/**
 * Set the active flag on an account.
 */
export function setAccountActive(accountId: string, isActive: boolean): void {
  const db = getDB();
  db.execute(
    'UPDATE email_accounts SET is_active = ? WHERE id = ?;',
    [isActive ? 1 : 0, accountId]
  );
}

/**
 * Get the total number of connected accounts.
 */
export function getAccountCount(): number {
  const db = getDB();
  const result = db.execute('SELECT COUNT(*) as count FROM email_accounts;');
  if (!result.rows || result.rows.length === 0) {
    return 0;
  }
  return (result.rows[0] as { count: number }).count;
}

/**
 * Sync accounts from server response into local DB + Zustand store.
 * Called after fetching connected accounts from API.
 */
export async function syncAccountsFromServer(
  accounts: EmailAccount[],
  storeSetAccounts: (accounts: EmailAccount[]) => void
): Promise<void> {
  // 1. Persist to SQLite
  upsertAccounts(accounts);

  // 2. Sync to Zustand / AsyncStorage
  storeSetAccounts(accounts);
}
