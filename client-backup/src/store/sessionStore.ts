import { create } from 'zustand'

export interface Message {
  id: string
  sender_type: 'user' | 'agent' | 'system'
  message_type: 'text' | 'card' | 'action' | 'source'
  content_text?: string
  card_payload?: CardPayload
  source_email?: SourceEmail
  created_at: string
}

export interface CardPayload {
  card_type: 'decision' | 'form' | 'confirm' | 'display'
  title: string
  body?: string
  source_email_id?: string
  options?: { id: string; label: string; style?: string }[]
  metadata?: Record<string, unknown>
}

export interface SourceEmail {
  id: string
  subject: string
  from: string
  to: string[]
  body_text: string
  received_at: string
}

export interface Session {
  id: string
  title: string
  status: string
  last_message_at?: string
}

interface SessionState {
  sessions: Session[]
  activeSessionId: string | null
  messages: Record<string, Message[]>
  typing: Record<string, boolean>
  setSessions: (s: Session[]) => void
  setActiveSession: (id: string) => void
  addMessage: (sessionId: string, msg: Message) => void
  removeMessage: (sessionId: string, messageId: string) => void
  setMessages: (sessionId: string, msgs: Message[]) => void
  setTyping: (sessionId: string, active: boolean) => void
  resolveCard: (sessionId: string, messageId: string) => void
  addSourceEmail: (sessionId: string, messageId: string, email: SourceEmail) => void
  updateCard: (sessionId: string, messageId: string, payload: CardPayload) => void
  optimisticAdd: (sessionId: string, content: string) => Message
  undoOptimistic: (sessionId: string, tempId: string) => void
}

export const useSessionStore = create<SessionState>((set) => ({
  sessions: [],
  activeSessionId: null,
  messages: {},
  typing: {},
  setSessions: (sessions) => set({ sessions }),
  setActiveSession: (id) => set({ activeSessionId: id }),

  addMessage: (sessionId, msg) =>
    set((state) => ({
      messages: {
        ...state.messages,
        [sessionId]: [...(state.messages[sessionId] || []), msg],
      },
    })),

  removeMessage: (sessionId, messageId) =>
    set((state) => ({
      messages: {
        ...state.messages,
        [sessionId]: (state.messages[sessionId] || []).filter((m) => m.id !== messageId),
      },
    })),

  setMessages: (sessionId, msgs) =>
    set((state) => ({
      messages: { ...state.messages, [sessionId]: msgs },
    })),

  setTyping: (sessionId, active) =>
    set((state) => ({
      typing: { ...state.typing, [sessionId]: active },
    })),

  resolveCard: (sessionId, messageId) =>
    set((state) => {
      const msgs = state.messages[sessionId] || []
      return {
        messages: {
          ...state.messages,
          [sessionId]: msgs.map((m) =>
            m.id === messageId
              ? { ...m, card_payload: { ...m.card_payload, _resolved: true } as any }
              : m
          ),
        },
      }
    }),

  addSourceEmail: (sessionId, messageId, email) =>
    set((state) => {
      const msgs = state.messages[sessionId] || []
      return {
        messages: {
          ...state.messages,
          [sessionId]: msgs.map((m) =>
            m.id === messageId ? { ...m, source_email: email, message_type: 'source' } : m
          ),
        },
      }
    }),

  updateCard: (sessionId, messageId, payload) =>
    set((state) => {
      const msgs = state.messages[sessionId] || []
      return {
        messages: {
          ...state.messages,
          [sessionId]: msgs.map((m) =>
            m.id === messageId ? { ...m, card_payload: payload } : m
          ),
        },
      }
    }),

  optimisticAdd: (sessionId, content) => {
    const tempId = `temp-${Date.now()}`
    const msg: Message = {
      id: tempId,
      sender_type: 'user',
      message_type: 'text',
      content_text: content,
      created_at: new Date().toISOString(),
    }
    set((state) => ({
      messages: {
        ...state.messages,
        [sessionId]: [...(state.messages[sessionId] || []), msg],
      },
    }))
    return msg
  },

  undoOptimistic: (sessionId, tempId) =>
    set((state) => ({
      messages: {
        ...state.messages,
        [sessionId]: (state.messages[sessionId] || []).filter((m) => m.id !== tempId),
      },
    })),
}))
