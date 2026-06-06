// Decision Stack — Client Type Definitions
// These are the wire contracts between the Client and Sync API.
// Shared by ALL client tracks. DO NOT MODIFY without coordination.

// ============================================================================
// DECISION CARD — The core UI unit
// ============================================================================

export interface DecisionCard {
  id: string;                    // UUID
  user_id: string;
  thread_id: string;
  source_account_id: string;
  card_state: CardState;
  from: FromField;
  they_want: string;             // max 280 chars
  context: CardContext;
  need_from_user: string;        // irreducible gap
  chunk_citations: ChunkCitation[];
  urgency_score: number;         // 0.0 - 1.0
  auto_handle_rule_id?: string;
  classification_confidence?: number;
  suggested_deadline?: string;   // ISO 8601
  user_decided_at?: string;
  sent_at?: string;
  created_at: string;
  updated_at: string;
}

export type CardState =
  | "pending"
  | "consulting"
  | "drafting"
  | "approved"
  | "sent"
  | "archived"
  | "expired";

export interface FromField {
  name: string;
  email: string;
  relationship_context?: string;  // "Vendor — Website Redesign"
  last_contact?: string;          // human readable
  interaction_count: number;
}

export interface CardContext {
  history_summary?: string;
  prior_commitments?: string[];
  quoted_numbers?: string[];
  deadlines?: string[];
  sentiment?: string;             // "professional, slightly pushy"
}

export interface ChunkCitation {
  chunk_id: string;
  verbatim_snippet: string;
  email_id: string;
  paragraph_index: number;
}

// ============================================================================
// DRAFT — For Sending Session
// ============================================================================

export interface Draft {
  id: string;
  card_id: string;
  draft_body: string;
  subject_line?: string;
  tone_profile?: string;
  in_reply_to?: string;
  references?: string[];
  model_used?: string;
  tokens_used?: number;
  user_approved: boolean;
  sent_at?: string;
  created_at: string;
}

// ============================================================================
// SYNC PROTOCOL
// ============================================================================

export interface SyncRequest {
  device_id: string;
  last_sync_version: number;
  local_changes: LocalChange[];
}

export interface LocalChange {
  card_id: string;
  version: number;
  state: CardState;
  decision?: string;             // user's one-line instruction
  draft_body?: string;
  approved_draft_id?: string;
}

export interface SyncResponse {
  server_version: number;
  accepted_changes: string[];     // card_ids accepted
  rejected_changes: RejectedChange[];
  new_cards: DecisionCard[];
  updated_cards: DecisionCard[];
  removed_cards: string[];        // card_ids to remove
}

export interface RejectedChange {
  card_id: string;
  reason: string;
  server_state: CardState;
}

// ============================================================================
// BATCH
// ============================================================================

export interface BatchInfo {
  size: number;
  estimated_clear_time_minutes: number;
  cards: DecisionCard[];         // ordered by urgency desc
}

export interface BatchGateProps {
  batch: BatchInfo;
  onStart: () => void;
  onDismiss: () => void;
}

// ============================================================================
// DECISION ACTIONS
// ============================================================================

export interface DecideRequest {
  card_id: string;
  decision: "approve" | "edit" | "consult";
  input?: string;                // user's one-line instruction
}

export interface DecideResponse {
  draft_id: string;
  draft_body: string;
  subject_line?: string;
}

// ============================================================================
// CONSULTATION
// ============================================================================

export interface ConsultRequest {
  card_id: string;
  question: string;
}

export interface ConsultResponse {
  answer: string;
  citations: ChunkCitation[];
  turns_remaining: number;
}

// ============================================================================
// EMAIL ACCOUNT — Multi-account support
// ============================================================================

export interface EmailAccount {
  id: string;
  email: string;
  provider: "google" | "microsoft";
  isActive: boolean;
  connectedAt: string;
}

export interface AccountBreakdown {
  accountId: string;
  email: string;
  provider: "google" | "microsoft";
  count: number;
}

// ============================================================================
// AUTH / SECURITY
// ============================================================================

export interface AuthTokens {
  access_token: string;
  refresh_token: string;
  expires_at: number;            // unix timestamp
}

export interface SecurityStatus {
  last_token_rotation: string;
  sessions: ActiveSession[];
  residency_region: string;
}

export interface ActiveSession {
  device: string;
  location: string;
  last_active: string;
}

// ============================================================================
// OFFLINE QUEUE
// ============================================================================

export interface SyncQueueItem {
  id: string;
  operation: "update_card" | "approve_draft" | "create_draft";
  payload: unknown;
  created_at: number;            // unix timestamp
  retry_count: number;
}

// ============================================================================
// VOICE
// ============================================================================

export interface VoiceTranscription {
  text: string;
  is_final: boolean;
  confidence: number;
}

export interface VoiceModeState {
  phase: "intro" | "listening" | "transcribing" | "drafting" | "confirming" | "sending" | "undo_window";
  current_card: DecisionCard | null;
  transcription: string;
  draft_preview: string | null;
  undo_seconds_remaining: number;
}

// ============================================================================
// CHAT — Persistent conversational interface
// ============================================================================

export interface ChatMessage {
  id: string;
  conversation_id: string;
  role: "user" | "assistant";
  content: string;
  citations?: ChunkCitation[];
  audio_url?: string;           // TTS audio of this message
  transcription?: string;       // STT result if voice input
  created_at: string;
}

export interface Conversation {
  id: string;
  title: string;
  messages: ChatMessage[];
  linked_card_ids: string[];
  linked_thread_ids: string[];
  voice_enabled: boolean;
  updated_at: string;
}

export interface ConversationListItem {
  id: string;
  title: string;
  message_count: number;
  last_message_preview: string;
  updated_at: string;
}

export interface ChatRequest {
  conversation_id?: string;     // omit to start new conversation
  message: string;
  linked_card_id?: string;
  linked_thread_id?: string;
  consultation_mode?: boolean;
}

export interface ChatResponse {
  message: ChatMessage;
  conversation_id: string;
  conversation_title?: string;
  suggested_action?: "clear_batch" | "view_card" | "schedule" | "none";
  action_target_id?: string;
  audio_url?: string;
}

export interface ChatState {
  conversations: ConversationListItem[];
  activeConversation: Conversation | null;
  isLoading: boolean;
  isVoiceMode: boolean;
  inputMode: "text" | "voice";
}
