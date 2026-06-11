// Decision Stack — Conversation List Management Hook
// Fetch, create, and delete chat conversations

import { useState, useCallback, useEffect } from 'react';
import { api } from '@services/api';
import type { ConversationListItem, Conversation } from '../types/cards';

export interface UseConversationsReturn {
  conversations: ConversationListItem[];
  isLoading: boolean;
  error: string | null;

  loadConversations: () => Promise<void>;
  createConversation: (linkedCardId?: string) => Promise<Conversation | null>;
  deleteConversation: (id: string) => Promise<void>;
  refresh: () => Promise<void>;
}

export function useConversations(): UseConversationsReturn {
  const [conversations, setConversations] = useState<ConversationListItem[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  /**
   * Load all conversations from the server.
   */
  const loadConversations = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const response = await api.get<ConversationListItem[]>('/chat/conversations');
      setConversations(response.data);
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to load conversations';
      setError(message);
    } finally {
      setIsLoading(false);
    }
  }, []);

  /**
   * Create a new conversation, optionally linked to a card.
   */
  const createConversation = useCallback(
    async (linkedCardId?: string): Promise<Conversation | null> => {
      setIsLoading(true);
      setError(null);
      try {
        const response = await api.post<Conversation>('/chat/conversations', {
          linked_card_id: linkedCardId,
        });
        const conversation = response.data;

        // Optimistically add to list
        const listItem: ConversationListItem = {
          id: conversation.id,
          title: conversation.title,
          message_count: 0,
          last_message_preview: 'New conversation',
          updated_at: conversation.updated_at,
        };
        setConversations((prev) => [listItem, ...prev]);

        return conversation;
      } catch (err) {
        const message = err instanceof Error ? err.message : 'Failed to create conversation';
        setError(message);
        return null;
      } finally {
        setIsLoading(false);
      }
    },
    []
  );

  /**
   * Delete a conversation by ID.
   */
  const deleteConversation = useCallback(
    async (id: string): Promise<void> => {
      // Optimistically remove from list
      setConversations((prev) => prev.filter((c) => c.id !== id));

      try {
        await api.delete(`/chat/conversations/${id}`);
      } catch (err) {
        const message = err instanceof Error ? err.message : 'Failed to delete conversation';
        setError(message);
        // Reload to restore consistent state
        loadConversations();
      }
    },
    [loadConversations]
  );

  /**
   * Refresh conversations list.
   */
  const refresh = useCallback(async () => {
    await loadConversations();
  }, [loadConversations]);

  // Load on mount
  useEffect(() => {
    loadConversations();
  }, [loadConversations]);

  return {
    conversations,
    isLoading,
    error,
    loadConversations,
    createConversation,
    deleteConversation,
    refresh,
  };
}
