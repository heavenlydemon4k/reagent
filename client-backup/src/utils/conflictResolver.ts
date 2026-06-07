// ============================================================================
// Conflict Resolver — deterministic server-vs-local resolution rules
// ============================================================================
// This module encodes the "CRDT: server wins on drafts, user wins on decisions"
// policy into explicit, testable rules.
//
// Every conflict resolution produces:
// - the winning value
// - a human-readable reason string (for debugging / analytics)
// - the resolution type (local_wins | server_wins)
// ============================================================================

import type { CardState } from "../types/cards";

// ---------------------------------------------------------------------------
// Resolution outcome
// ---------------------------------------------------------------------------

export interface Resolution<T> {
  winner: "local" | "server";
  value: T;
  reason: string;
}

// ---------------------------------------------------------------------------
// Field-level resolution functions
// ---------------------------------------------------------------------------

/**
 * Resolve card_state conflicts.
 *
 * Rule: user's explicit decision wins over server-generated intermediate
 * states, BUT server terminal states (sent, archived, expired) win over
 * everything because they represent completed workflow steps.
 */
export function resolveCardState(
  localState: CardState,
  serverState: CardState,
  localHasDecision: boolean,
): Resolution<CardState> {
  const terminalStates: CardState[] = ["sent", "archived", "expired"];
  const serverIsTerminal = terminalStates.includes(serverState);
  const localIsTerminal = terminalStates.includes(localState);

  // Server terminal always wins — workflow is complete
  if (serverIsTerminal && !localIsTerminal) {
    return {
      winner: "server",
      value: serverState,
      reason: `Server reached terminal state "${serverState}"; workflow complete`,
    };
  }

  // Local terminal vs server non-terminal — user action wins
  if (localIsTerminal && !serverIsTerminal) {
    return {
      winner: "local",
      value: localState,
      reason: `User explicitly moved card to "${localState}"`,
    };
  }

  // Both terminal — server wins (server is source of truth for finality)
  if (localIsTerminal && serverIsTerminal) {
    return {
      winner: "server",
      value: serverState,
      reason: `Both terminal; server state "${serverState}" wins as source of truth`,
    };
  }

  // Neither terminal — if user made a decision, their state wins
  if (localHasDecision) {
    return {
      winner: "local",
      value: localState,
      reason: `User decision present; "${localState}" overrides server "${serverState}"`,
    };
  }

  // No user decision — server wins (server is authoritative on auto-generated states)
  return {
    winner: "server",
    value: serverState,
    reason: `No user decision; server state "${serverState}" wins`,
  };
}

/**
 * Resolve draft_body conflicts.
 *
 * Rule: server ALWAYS wins on draft_body. The server's LLM-generated draft
 * is the canonical content. Local edits are treated as suggestions that
 * must be re-approved.
 */
export function resolveDraftBody(
  localBody: string | undefined,
  serverBody: string | undefined,
): Resolution<string | undefined> {
  if (serverBody && serverBody.trim().length > 0) {
    return {
      winner: "server",
      value: serverBody,
      reason: "Server draft_body is authoritative (LLM-generated canonical content)",
    };
  }

  // No server body — fall back to local
  return {
    winner: "local",
    value: localBody,
    reason: "No server draft present; using local draft",
  };
}

/**
 * Resolve user_approved conflicts on drafts.
 *
 * Rule: local (user) ALWAYS wins on approval. An explicit user approval
 * is sacred and cannot be overwritten by the server.
 */
export function resolveUserApproved(
  localApproved: boolean,
  serverApproved: boolean,
): Resolution<boolean> {
  if (localApproved) {
    return {
      winner: "local",
      value: true,
      reason: "User approval is sacred; cannot be overwritten by server",
    };
  }

  return {
    winner: "server",
    value: serverApproved,
    reason: "No local approval; using server value",
  };
}

/**
 * Resolve metadata timestamp conflicts.
 *
 * Rule: newer timestamp always wins. This is deterministic and
 * automatically idempotent.
 */
export function resolveTimestamp(
  localTime: string | undefined,
  serverTime: string | undefined,
): Resolution<string | undefined> {
  const localMs = localTime ? Date.parse(localTime) : 0;
  const serverMs = serverTime ? Date.parse(serverTime) : 0;

  if (localMs > serverMs) {
    return {
      winner: "local",
      value: localTime,
      reason: `Local timestamp (${localTime}) is newer`,
    };
  }

  return {
    winner: "server",
    value: serverTime,
    reason: serverMs > localMs
      ? `Server timestamp (${serverTime}) is newer`
      : "Timestamps equal; defaulting to server",
  };
}

/**
 * Resolve urgency_score conflicts.
 *
 * Rule: server wins on urgency_score because it's derived from ML models
 * that have full context. Local scores are stale.
 */
export function resolveUrgencyScore(
  localScore: number | undefined,
  serverScore: number | undefined,
): Resolution<number | undefined> {
  if (serverScore !== undefined) {
    return {
      winner: "server",
      value: serverScore,
      reason: `Server urgency score (${serverScore}) wins (ML-derived)`,
    };
  }

  return {
    winner: "local",
    value: localScore,
    reason: "No server score; using local",
  };
}

// ---------------------------------------------------------------------------
// Composite resolver — applies all field rules in order
// ---------------------------------------------------------------------------

export interface FullConflictResolution {
  cardId: string;
  fieldResolutions: Array<{
    field: string;
    resolution: Resolution<unknown>;
  }>;
  finalLocalWins: string[];
  finalServerWins: string[];
}

/**
 * Run the full resolution pipeline for a single card.
 * Returns per-field resolutions and summary of which fields each side won.
 */
export function resolveCardConflict(params: {
  cardId: string;
  localState: CardState;
  serverState: CardState;
  localHasDecision: boolean;
  localDraftBody?: string;
  serverDraftBody?: string;
  localApproved?: boolean;
  serverApproved?: boolean;
  localUpdatedAt?: string;
  serverUpdatedAt?: string;
  localUrgency?: number;
  serverUrgency?: number;
}): FullConflictResolution {
  const {
    cardId,
    localState,
    serverState,
    localHasDecision,
    localDraftBody,
    serverDraftBody,
    localApproved = false,
    serverApproved = false,
    localUpdatedAt,
    serverUpdatedAt,
    localUrgency,
    serverUrgency,
  } = params;

  const fieldResolutions: FullConflictResolution["fieldResolutions"] = [];

  // card_state
  const stateRes = resolveCardState(localState, serverState, localHasDecision);
  fieldResolutions.push({ field: "card_state", resolution: stateRes as Resolution<unknown> });

  // draft_body
  const bodyRes = resolveDraftBody(localDraftBody, serverDraftBody);
  fieldResolutions.push({ field: "draft_body", resolution: bodyRes as Resolution<unknown> });

  // user_approved
  const approvedRes = resolveUserApproved(localApproved, serverApproved);
  fieldResolutions.push({ field: "user_approved", resolution: approvedRes as Resolution<unknown> });

  // updated_at
  const timeRes = resolveTimestamp(localUpdatedAt, serverUpdatedAt);
  fieldResolutions.push({ field: "updated_at", resolution: timeRes as Resolution<unknown> });

  // urgency_score
  const urgencyRes = resolveUrgencyScore(localUrgency, serverUrgency);
  fieldResolutions.push({ field: "urgency_score", resolution: urgencyRes as Resolution<unknown> });

  const finalLocalWins = fieldResolutions
    .filter((f) => f.resolution.winner === "local")
    .map((f) => f.field);

  const finalServerWins = fieldResolutions
    .filter((f) => f.resolution.winner === "server")
    .map((f) => f.field);

  return { cardId, fieldResolutions, finalLocalWins, finalServerWins };
}
