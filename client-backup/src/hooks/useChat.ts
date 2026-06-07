// Decision Stack — Chat State Management + API Calls Hook
// Manages messages, input, loading state, and server communication for a single conversation

import { useState, useCallback, useRef, useEffect } from 'react';
import { api, getCalendarEvents, checkFreeBusy, createCalendarEvent, sendDraft } from '@services/api';
import type {
  ChatMessage,
  ChatRequest,
  ChatResponse,
  ChunkCitation,
  Conversation,
  CalendarEvent,
  FreeBusyResponse,
  CalendarEventCreate,
} from '@types/cards';

export type SuggestedAction = 'clear_batch' | 'view_card' | 'schedule' | 'none';

export interface UseChatReturn {
  messages: ChatMessage[];
  isLoading: boolean;
  inputText: string;
  suggestedAction: SuggestedAction | null;
  actionTargetId: string | null;
  conversationTitle: string;
  audioUrl: string | null;

  setInputText: (text: string) => void;
  sendMessage: (text: string) => Promise<void>;
  sendVoiceMessage: (audioBlob: Blob) => Promise<void>;
  dismissSuggestedAction: () => void;
  loadConversation: (conversationId: string) => Promise<void>;
  clearMessages: () => void;

  // Calendar commands
  getCalendarEvents: (days?: number) => Promise<CalendarEvent[]>;
  checkFreeBusy: (date: string) => Promise<FreeBusyResponse>;
  createCalendarEvent: (event: CalendarEventCreate) => Promise<CalendarEvent>;
  sendDraft: (draftId: string) => Promise<{ status: string }>;
}

export function useChat(conversationId?: string): UseChatReturn {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [inputText, setInputText] = useState('');
  const [suggestedAction, setSuggestedAction] = useState<SuggestedAction | null>(null);
  const [actionTargetId, setActionTargetId] = useState<string | null>(null);
  const [conversationTitle, setConversationTitle] = useState('Chat');
  const [audioUrl, setAudioUrl] = useState<string | null>(null);

  // Keep conversation ID in a ref for callbacks
  const convIdRef = useRef(conversationId);
  useEffect(() => {
    convIdRef.current = conversationId;
  }, [conversationId]);

  /**
   * Load full conversation from server (history + metadata).
   */
  const loadConversation = useCallback(async (id: string) => {
    setIsLoading(true);
    try {
      const response = await api.get<Conversation>(`/chat/conversations/${id}`);
      const conv = response.data;
      setMessages(conv.messages);
      setConversationTitle(conv.title);
    } catch (err) {
      console.error('[useChat] Failed to load conversation:', err);
    } finally {
      setIsLoading(false);
    }
  }, []);

  /**
   * Send a text message. Optimistically updates UI, then syncs with server.
   */
  const sendMessage = useCallback(
    async (text: string) => {
      if (!text.trim()) return;
      const convId = convIdRef.current;

      // 1. Optimistically add user message
      const optimisticUserMsg: ChatMessage = {
        id: `local-${Date.now()}`,
        conversation_id: convId ?? 'new',
        role: 'user',
        content: text.trim(),
        created_at: new Date().toISOString(),
      };
      setMessages((prev) => [...prev, optimisticUserMsg]);
      setInputText('');
      setIsLoading(true);
      setSuggestedAction(null);
      setAudioUrl(null);

      try {
        // 2. Send to server
        const request: ChatRequest = {
          conversation_id: convId,
          message: text.trim(),
        };
        const response = await api.post<ChatResponse>('/chat/messages', request);
        const data = response.data;

        // 3. Add assistant message
        const assistantMsg: ChatMessage = {
          ...data.message,
          id: data.message.id ?? `assistant-${Date.now()}`,
          role: 'assistant',
          created_at: data.message.created_at ?? new Date().toISOString(),
        };
        setMessages((prev) =>
          prev.map((m) => (m.id === optimisticUserMsg.id ? { ...m, id: `confirmed-${m.id}` } : m)).concat(assistantMsg)
        );

        // 4. Update conversation metadata
        if (data.conversation_title) {
          setConversationTitle(data.conversation_title);
        }

        // 5. Handle suggested action
        if (data.suggested_action && data.suggested_action !== 'none') {
          setSuggestedAction(data.suggested_action);
          setActionTargetId(data.action_target_id ?? null);
        }

        // 6. Preload audio if available
        if (data.audio_url) {
          setAudioUrl(data.audio_url);
        }
      } catch (err) {
        console.error('[useChat] Failed to send message:', err);
        // Mark the optimistic message as failed (optional enhancement)
      } finally {
        setIsLoading(false);
      }
    },
    []
  );

  /**
   * Send a voice message (audio blob). Server transcribes and responds.
   */
  const sendVoiceMessage = useCallback(async (audioBlob: Blob) => {
    const convId = convIdRef.current;
    if (!convId) return;

    setIsLoading(true);
    setSuggestedAction(null);
    setAudioUrl(null);

    try {
      // 1. Upload audio via multipart form
      const formData = new FormData();
      formData.append('audio', audioBlob, 'voice-message.webm');

      const response = await api.post<ChatResponse>(
        `/chat/conversations/${convId}/voice`,
        formData,
        {
          headers: {
            'Content-Type': 'multipart/form-data',
          },
        }
      );
      const data = response.data;

      // 2. Show transcription as user message
      const transcription = data.message.transcription ?? '(Voice message)';
      const userMsg: ChatMessage = {
        id: `voice-user-${Date.now()}`,
        conversation_id: convId,
        role: 'user',
        content: transcription,
        transcription,
        created_at: new Date().toISOString(),
      };

      // 3. Add assistant response
      const assistantMsg: ChatMessage = {
        ...data.message,
        id: data.message.id ?? `voice-assistant-${Date.now()}`,
        role: 'assistant',
        content: data.message.content,
        audio_url: data.audio_url,
        created_at: data.message.created_at ?? new Date().toISOString(),
      };

      setMessages((prev) => [...prev, userMsg, assistantMsg]);

      // 4. Handle suggested action
      if (data.suggested_action && data.suggested_action !== 'none') {
        setSuggestedAction(data.suggested_action);
        setActionTargetId(data.action_target_id ?? null);
      }

      // 5. Auto-play TTS audio
      if (data.audio_url) {
        setAudioUrl(data.audio_url);
      }
    } catch (err) {
      console.error('[useChat] Failed to send voice message:', err);
    } finally {
      setIsLoading(false);
    }
  }, []);

  /**
   * Dismiss the currently shown suggested action chip.
   */
  const dismissSuggestedAction = useCallback(() => {
    setSuggestedAction(null);
    setActionTargetId(null);
  }, []);

  /**
   * Clear all messages (e.g., when leaving a conversation).
   */
  const clearMessages = useCallback(() => {
    setMessages([]);
    setSuggestedAction(null);
    setActionTargetId(null);
    setAudioUrl(null);
    setInputText('');
  }, []);

  /**
   * Fetch calendar events for the user.
   */
  const getCalendarEventsCb = useCallback(async (days?: number) => {
    return getCalendarEvents(days);
  }, []);

  /**
   * Check free/busy slots for a specific date.
   */
  const checkFreeBusyCb = useCallback(async (date: string) => {
    return checkFreeBusy(date);
  }, []);

  /**
   * Create a new calendar event.
   */
  const createCalendarEventCb = useCallback(async (event: CalendarEventCreate) => {
    return createCalendarEvent(event);
  }, []);

  /**
   * Send an approved draft via chat command.
   */
  const sendDraftCb = useCallback(async (draftId: string) => {
    return sendDraft(draftId);
  }, []);

  // Load conversation history when conversationId changes
  useEffect(() => {
    if (conversationId) {
      loadConversation(conversationId);
    } else {
      clearMessages();
    }
  }, [conversationId, loadConversation, clearMessages]);

  return {
    messages,
    isLoading,
    inputText,
    suggestedAction,
    actionTargetId,
    conversationTitle,
    audioUrl,

    setInputText,
    sendMessage,
    sendVoiceMessage,
    dismissSuggestedAction,
    loadConversation,
    clearMessages,

    // Calendar commands
    getCalendarEvents: getCalendarEventsCb,
    checkFreeBusy: checkFreeBusyCb,
    createCalendarEvent: createCalendarEventCb,
    sendDraft: sendDraftCb,
  };
}
