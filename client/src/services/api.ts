// Decision Stack — HTTP Client with JWT Refresh
// Axios instance with automatic token rotation and offline-aware request queueing

import axios, {
  type AxiosInstance,
  type AxiosRequestConfig,
  type AxiosError,
} from 'axios';
import { useAuthStore } from '@stores/authStore';
import { useSyncStore } from '@stores/syncStore';
import NetInfo from '@react-native-community/netinfo';
import type {
  BatchInfo,
  SyncRequest,
  SyncResponse,
  DecideRequest,
  DecideResponse,
  ConsultRequest,
  ConsultResponse,
  ChatMessage,
  ChatRequest,
  ChatResponse,
  Conversation,
  ConversationListItem,
  EmailAccount,
  CalendarEvent,
  FreeBusyResponse,
  CalendarEventCreate,
} from '@types/cards';

// Base URL from Expo extra config
const API_BASE_URL =
  process.env.EXPO_PUBLIC_API_URL ??
  'https://api.decisionstack.app/v1';

let isRefreshing = false;
let refreshQueue: Array<<{
  resolve: (token: string) => void;
  reject: (err: Error) => void;
}> = [];

function processQueue(error: Error | null, token?: string): void {
  for (const promise of refreshQueue) {
    if (error || !token) {
      promise.reject(error ?? new Error('Token refresh failed'));
    } else {
      promise.resolve(token);
    }
  }
  refreshQueue = [];
}

// ============================================================================
// AXIOS INSTANCE
// ============================================================================

export const api: AxiosInstance = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
    Accept: 'application/json',
  },
});

// ============================================================================
// REQUEST INTERCEPTOR — Attach Bearer JWT
// ============================================================================

api.interceptors.request.use(
  async (config) => {
    // Check network state
    const netInfo = await NetInfo.fetch();
    if (!netInfo.isConnected) {
      // Still let the request go — axios will fail and caller handles retry
    }

    const tokens = useAuthStore.getState().tokens;
    if (tokens?.access_token) {
      config.headers = config.headers ?? {};
      config.headers.Authorization = `Bearer ${tokens.access_token}`;
    }

    // Add device ID header for sync tracking
    const deviceId = useAuthStore.getState().deviceId;
    if (deviceId) {
      config.headers['X-Device-Id'] = deviceId;
    }

    // Add active account filter header (null = unified view)
    const activeAccountId = useAuthStore.getState().activeAccountId;
    if (activeAccountId) {
      config.headers['X-Active-Account'] = activeAccountId;
    }

    return config;
  },
  (error) => Promise.reject(error)
);

// ============================================================================
// RESPONSE INTERCEPTOR — 401 → Refresh → Retry
// ============================================================================

api.interceptors.response.use(
  (response) => response,
  async (error: AxiosError) => {
    const originalRequest = error.config as AxiosRequestConfig & {
      _retry?: boolean;
    };

    if (!originalRequest) {
      return Promise.reject(error);
    }

    // 401 Unauthorized — attempt token refresh
    if (error.response?.status === 401 && !originalRequest._retry) {
      if (isRefreshing) {
        // Queue this request while refresh is in flight
        return new Promise<string>((resolve, reject) => {
          refreshQueue.push({ resolve, reject });
        })
          .then((token) => {
            originalRequest.headers = originalRequest.headers ?? {};
            originalRequest.headers.Authorization = `Bearer ${token}`;
            return api(originalRequest);
          })
          .catch((err) => Promise.reject(err));
      }

      originalRequest._retry = true;
      isRefreshing = true;

      try {
        const refreshToken = useAuthStore.getState().tokens?.refresh_token;
        if (!refreshToken) {
          throw new Error('No refresh token available');
        }

        const response = await axios.post<<{
          access_token: string;
          refresh_token: string;
          expires_at: number;
        }>(`${API_BASE_URL}/auth/refresh`, {
          refresh_token: refreshToken,
        });

        const newTokens = response.data;
        useAuthStore.getState().setTokens(newTokens);

        processQueue(null, newTokens.access_token);

        originalRequest.headers = originalRequest.headers ?? {};
        originalRequest.headers.Authorization = `Bearer ${newTokens.access_token}`;
        return api(originalRequest);
      } catch (refreshError) {
        processQueue(
          refreshError instanceof Error
            ? refreshError
            : new Error('Token refresh failed')
        );
        useAuthStore.getState().clearAuth();
        return Promise.reject(refreshError);
      } finally {
        isRefreshing = false;
      }
    }

    // 5xx errors — mark sync as potentially degraded
    if (error.response && error.response.status >= 500) {
      useSyncStore.getState().setServerHealthy(false);
    }

    return Promise.reject(error);
  }
);

// ============================================================================
// HEALTH CHECK
// ============================================================================

/**
 * Quick ping to check server availability.
 */
export async function checkServerHealth(): Promise<boolean> {
  try {
    await api.get('/health', { timeout: 5000 });
    useSyncStore.getState().setServerHealthy(true);
    return true;
  } catch {
    useSyncStore.getState().setServerHealthy(false);
    return false;
  }
}

// ============================================================================
// API ENDPOINTS — CARDS & DECISIONS
// ============================================================================

/**
 * Fetch a new batch of decision cards.
 */
export async function fetchBatch(
  maxCards?: number
): Promise<<BatchInfo> {
  const response = await api.get<<BatchInfo>('/batch', {
    params: maxCards ? { limit: maxCards } : undefined,
  });
  return response.data;
}

/**
 * Submit local changes and receive server updates.
 */
export async function syncWithServer(
  request: SyncRequest
): Promise<<SyncResponse> {
  const response = await api.post<<SyncResponse>('/sync', request);
  return response.data;
}

/**
 * Submit a decision for a card.
 */
export async function submitDecision(
  request: DecideRequest
): Promise<<DecideResponse> {
  const response = await api.post<<DecideResponse>(
    `/cards/${request.card_id}/decide`,
    {
      decision: request.decision,
      input: request.input,
    }
  );
  return response.data;
}

/**
 * Fetch source email chunks for a card (for deep consultation).
 * Raw email bodies are streamed — NEVER cached locally per invariant.
 */
export async function fetchCardSource(
  cardId: string,
  chunkIds?: string[]
): Promise<<{
  card_id: string;
  chunks: Array<<{
    chunk_id: string;
    content: string;
    email_id: string;
    paragraph_index: number;
  }>;
}> {
  const response = await api.get(`/cards/${cardId}/source`, {
    params: chunkIds ? { chunks: chunkIds.join(',') } : undefined,
  });
  return response.data;
}

/**
 * Send an approved draft via the chat command endpoint.
 * POST /v1/chat/drafts/{draft_id}/send
 */
export async function sendDraft(draftId: string): Promise<<{
  status: string;
  sent_at?: string;
  message_id?: string;
}> {
  const response = await api.post(`/chat/drafts/${draftId}/send`);
  return response.data;
}

/**
 * Cancel a draft send before it leaves the sync queue.
 * Removes the draft from the pending sync queue.
 */
export async function cancelDraft(draftId: string): Promise<void> {
  await api.post(`/drafts/${draftId}/cancel`);
}

// ============================================================================
// CALENDAR — Chat Command Endpoints
// ============================================================================

/**
 * Fetch calendar events for the authenticated user.
 * GET /v1/chat/calendar/events
 */
export async function getCalendarEvents(days?: number): Promise<<CalendarEvent[]> {
  const response = await api.get<<CalendarEvent[]>('/chat/calendar/events', {
    params: days ? { days } : undefined,
  });
  return response.data;
}

/**
 * Check free/busy status for a specific date.
 * GET /v1/chat/calendar/freebusy
 */
export async function checkFreeBusy(date: string): Promise<<FreeBusyResponse> {
  const response = await api.get<<FreeBusyResponse>('/chat/calendar/freebusy', {
    params: { date },
  });
  return response.data;
}

/**
 * Create a new calendar event.
 * POST /v1/chat/calendar/events
 */
export async function createCalendarEvent(
  event: CalendarEventCreate
): Promise<<CalendarEvent> {
  const response = await api.post<<CalendarEvent>('/chat/calendar/events', event);
  return response.data;
}

/**
 * Request a consultation on a card.
 */
export async function consultOnCard(
  request: ConsultRequest
): Promise<<ConsultResponse> {
  const response = await api.post<<ConsultResponse>(
    `/cards/${request.card_id}/consult`,
    { question: request.question }
  );
  return response.data;
}

// ============================================================================
// CHAT
// ============================================================================

/**
 * Fetch all conversations for the current user.
 */
export async function fetchConversations(): Promise<<ConversationListItem[]> {
  const response = await api.get<<ConversationListItem[]>('/chat/conversations');
  return response.data;
}

/**
 * Fetch a single conversation with full message history.
 */
export async function fetchConversation(id: string): Promise<<Conversation> {
  const response = await api.get<<Conversation>(`/chat/conversations/${id}`);
  return response.data;
}

/**
 * Create a new conversation.
 */
export async function createConversation(
  linkedCardId?: string
): Promise<<Conversation> {
  const response = await api.post<<Conversation>('/chat/conversations', {
    linked_card_id: linkedCardId,
  });
  return response.data;
}

/**
 * Delete a conversation.
 */
export async function deleteConversation(id: string): Promise<void> {
  await api.delete(`/chat/conversations/${id}`);
}

/**
 * Send a text message in a conversation.
 */
export async function sendChatMessage(
  request: ChatRequest
): Promise<<ChatResponse> {
  const response = await api.post<<ChatResponse>('/chat/messages', request);
  return response.data;
}

/**
 * Send a voice message (audio blob) for transcription and response.
 */
export async function sendVoiceMessage(
  conversationId: string,
  audioBlob: Blob
): Promise<<ChatResponse> {
  const formData = new FormData();
  formData.append('audio', audioBlob, 'voice-message.webm');

  const response = await api.post<<ChatResponse>(
    `/chat/conversations/${conversationId}/voice`,
    formData,
    {
      headers: {
        'Content-Type': 'multipart/form-data',
      },
    }
  );
  return response.data;
}

// ============================================================================
// ACCOUNT MANAGEMENT — Multi-account OAuth
// ============================================================================

/**
 * Initiate OAuth flow for connecting an additional email account.
 * Returns the OAuth URL to open in a web browser or WebBrowser.
 */
export async function initiateOAuth(
  provider: 'google' | 'microsoft'
): Promise<string> {
  const response = await api.post<<{
    auth_url: string;
    state: string;
  }>('/auth/add-account', { provider });
  return response.data.auth_url;
}

/**
 * Complete OAuth callback — exchange code for account connection.
 * Called after the OAuth provider redirects back to the app.
 */
export async function completeOAuthCallback(
  code: string,
  state: string
): Promise<<EmailAccount> {
  const response = await api.post<<{
    account: EmailAccount;
  }>('/auth/add-account/callback', { code, state });
  return response.data.account;
}

/**
 * Fetch all connected email accounts for the current user.
 */
export async function getConnectedAccounts(): Promise<<EmailAccount[]> {
  const response = await api.get<<{
    accounts: EmailAccount[];
  }>('/auth/accounts');
  return response.data.accounts;
}

/**
 * Disconnect an email account.
 * Removes the account and all associated data from the server.
 */
export async function disconnectAccount(accountId: string): Promise<void> {
  await api.post(`/auth/disconnect/${accountId}`);
}

/**
 * Set the active account for filtered views.
 * Pass null to switch to unified view (all accounts).
 */
export async function setServerActiveAccount(
  accountId: string | null
): Promise<void> {
  await api.post('/auth/active-account', {
    account_id: accountId,
  });
}

// ============================================================================
// ONBOARDING
// ============================================================================

/**
 * Mark the current user as having completed onboarding.
 * Sets users.onboarded_at = NOW() on the server.
 */
export async function completeOnboarding(): Promise<<{
  success: boolean;
  onboarded_at: string;
}> {
  const response = await api.post<<{
    success: boolean;
    onboarded_at: string;
  }>('/users/onboarding/complete');
  return response.data;
}

// ============================================================================
// CONTACT PROFILE
// ============================================================================

/**
 * Fetch full contact profile from Neo4j relationship graph.
 * Includes interaction stats, tone history, projects, and monetary value.
 */
export async function getContactProfile(
  contactId: string
): Promise<<ContactProfile> {
  const response = await api.get<<ContactProfile>(
    `/contacts/${contactId}/profile`
  );
  return response.data;
}

/**
 * Fetch chronological timeline of all threads with this contact.
 */
export async function getContactTimeline(
  contactId: string,
  limit = 20,
  cursor?: string
): Promise<<ThreadSummary[]> {
  const response = await api.get<<ThreadSummary[]>(
    `/contacts/${contactId}/timeline`,
    {
      params: { limit, cursor },
    }
  );
  return response.data;
}

/**
 * Mute a contact — suppresses decision cards from this sender.
 */
export async function muteContact(contactId: string): Promise<void> {
  await api.post(`/contacts/${contactId}/mute`);
}

/**
 * Unmute a previously muted contact.
 */
export async function unmuteContact(contactId: string): Promise<void> {
  await api.post(`/contacts/${contactId}/unmute`);
}

// ============================================================================
// CHAT SESSIONS (NEW)
// ============================================================================

/**
 * Create a new chat session about a specific decision or topic.
 */
export async function createChatSession(
  userId: string,
  title?: string,
  context?: object
): Promise<{ id: string; title: string; created_at: number }> {
  const response = await api.post('/chat/sessions', {
    user_id: userId,
    title,
    context,
  });
  return response.data;
}

/**
 * List all chat sessions for the current user.
 */
export async function listChatSessions(
  userId: string
): Promise<Array<{ id: string; title: string; created_at: number; updated_at: number; message_count: number }>> {
  const response = await api.get(`/chat/sessions?user_id=${userId}`);
  return response.data;
}

/**
 * Get a specific chat session with full message history.
 */
export async function getChatSession(
  sessionId: string
): Promise<{ session_id: string; messages: Array<<unknown> }> {
  const response = await api.get(`/chat/sessions/${sessionId}`);
  return response.data;
}

/**
 * Send a message in a chat session.
 */
export async function sendChatSessionMessage(
  sessionId: string,
  userId: string,
  content: string
): Promise<<{
  session_id: string;
  message: unknown;
  cost_usd: number;
  model: string;
}> {
  const response = await api.post(`/chat/sessions/${sessionId}/messages`, {
    user_id: userId,
    content,
  });
  return response.data;
}

/**
 * Send a decision card as a chat message.
 */
export async function sendChatCard(
  sessionId: string,
  cardData: object
): Promise<<unknown> {
  const response = await api.post(`/chat/sessions/${sessionId}/cards`, cardData);
  return response.data;
}

// ============================================================================
// PROFILE (NEW)
// ============================================================================

/**
 * Get user profile and personalization settings.
 */
export async function getProfile(userId: string): Promise<<unknown> {
  const response = await api.get(`/profile/${userId}`);
  return response.data;
}

/**
 * Update user profile settings.
 */
export async function updateProfile(
  userId: string,
  updates: object
): Promise<<unknown> {
  const response = await api.put(`/profile/${userId}`, updates);
  return response.data;
}

/**
 * Get agent behavior preferences.
 */
export async function getPreferences(userId: string): Promise<<{
  agent_tone: string;
  agent_detail_level: string;
  auto_handle_confidence: number;
  voice_enabled: boolean;
  notifications_enabled: boolean;
}> {
  const response = await api.get(`/profile/${userId}/preferences`);
  return response.data;
}