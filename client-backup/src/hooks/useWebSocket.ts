import { useEffect, useRef, useCallback, useState } from 'react'
import { useSessionStore } from '../store/sessionStore'

const WS_URL = import.meta.env.VITE_WS_URL ?? 'ws://localhost:8000/chat/ws'
const RECONNECT_BASE_MS = 1000
const RECONNECT_MAX_MS = 30000
const HEARTBEAT_INTERVAL_MS = 30000

export function useWebSocket(token: string) {
  const ws = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const heartbeatTimer = useRef<ReturnType<typeof setInterval> | null>(null)
  const reconnectAttempts = useRef(0)
  const [connected, setConnected] = useState(false)

  const addMessage = useSessionStore((s) => s.addMessage)
  const setTyping = useSessionStore((s) => s.setTyping)
  const resolveCard = useSessionStore((s) => s.resolveCard)
  const addSourceEmail = useSessionStore((s) => s.addSourceEmail)
  const updateCard = useSessionStore((s) => s.updateCard)
  const undoOptimistic = useSessionStore((s) => s.undoOptimistic)

  const connect = useCallback(() => {
    if (!token) return
    if (ws.current?.readyState === WebSocket.OPEN || ws.current?.readyState === WebSocket.CONNECTING) return

    const sessionId = useSessionStore.getState().activeSessionId
    const url = sessionId ? `${WS_URL}/${sessionId}?token=${token}` : `${WS_URL}?token=${token}`
    const socket = new WebSocket(url)

    socket.onopen = () => {
      reconnectAttempts.current = 0
      setConnected(true)
      heartbeatTimer.current = setInterval(() => {
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: 'ping' }))
        }
      }, HEARTBEAT_INTERVAL_MS)
    }

    socket.onmessage = (event) => {
      let data
      try {
        data = JSON.parse(event.data)
      } catch {
        return
      }

      if (data.type === 'pong') return

      const sid = data.session_id || sessionId || 'default'

      if (data.type === 'typing') {
        setTyping(sid, data.active)
        return
      }

      if (data.type === 'message' || data.type === 'card') {
        // Remove any optimistic temp message with same content pattern
        if (data.message?.role === 'agent') {
          // Agent response arrived
          addMessage(sid, {
            id: data.message.id || crypto.randomUUID(),
            sender_type: 'agent',
            message_type: data.message.type || 'text',
            content_text: data.message.content || '',
            card_payload: data.message.card || null,
            created_at: new Date().toISOString(),
          })
        }
      }

      if (data.type === 'card_action_result') {
        if (data.card_payload) {
          addMessage(sid, {
            id: crypto.randomUUID(),
            sender_type: 'agent',
            message_type: 'card',
            content_text: data.card_payload.title || '',
            card_payload: data.card_payload,
            created_at: new Date().toISOString(),
          })
        }
        if (data.card_resolved) {
          resolveCard(sid, data.card_resolved.card_id)
        }
      }

      if (data.type === 'source_email') {
        addSourceEmail(sid, data.message_id || '', data.email)
      }

      if (data.type === 'stack_complete') {
        addMessage(sid, {
          id: `stack-complete-${Date.now()}`,
          sender_type: 'system',
          message_type: 'text',
          content_text: 'Stack complete. No more critical emails.',
          created_at: new Date().toISOString(),
        })
      }

      if (data.type === 'error') {
        addMessage(sid, {
          id: `error-${Date.now()}`,
          sender_type: 'system',
          message_type: 'text',
          content_text: `Error: ${data.message || 'Unknown error'}`,
          created_at: new Date().toISOString(),
        })
      }
    }

    socket.onclose = () => {
      setConnected(false)
      if (heartbeatTimer.current) clearInterval(heartbeatTimer.current)
      ws.current = null
      const delay = Math.min(RECONNECT_BASE_MS * 2 ** reconnectAttempts.current, RECONNECT_MAX_MS)
      reconnectAttempts.current += 1
      reconnectTimer.current = setTimeout(connect, delay)
    }

    socket.onerror = () => {
      socket.close()
    }

    ws.current = socket
  }, [token])

  useEffect(() => {
    connect()
    return () => {
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current)
      if (heartbeatTimer.current) clearInterval(heartbeatTimer.current)
      ws.current?.close()
    }
  }, [connect])

  const send = useCallback((payload: object) => {
    if (ws.current?.readyState === WebSocket.OPEN) {
      ws.current.send(JSON.stringify(payload))
    }
  }, [])

  return { send, connected }
}
