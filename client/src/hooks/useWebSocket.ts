import { useEffect, useRef, useCallback, useState } from 'react'
import { useSessionStore } from '../store/sessionStore'

// Sync (:8082) owns the client WebSocket hub (see PLAN.md Phase 13 / design log).
// Honor VITE_WS_URL; the dev fallback points at Sync's /ws route, not the
// deprecated Intelligence :8000/chat/ws endpoint.
const WS_URL = import.meta.env.VITE_WS_URL ?? 'ws://localhost:8082/ws'
const RECONNECT_BASE_MS = 1000
const RECONNECT_MAX_MS = 30000
const HEARTBEAT_INTERVAL_MS = 30000

// Sync requires a stable per-device identifier. Browsers cannot set the
// X-Device-ID header on a WebSocket, so it is sent as a query parameter; Sync
// reads it from the query when the header is absent. Persisted so reconnects
// and refreshes keep the same device identity (single-connection-per-device).
function getDeviceId(): string {
  try {
    let id = localStorage.getItem('reagent_device_id')
    if (!id) {
      id = crypto.randomUUID()
      localStorage.setItem('reagent_device_id', id)
    }
    return id
  } catch {
    return 'web-ephemeral'
  }
}

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
    // Sync's route is exactly /ws with query-param auth; the session is NOT a
    // path segment. Pass token + device_id (+ session_id when present) as query.
    const params = new URLSearchParams({ token, device_id: getDeviceId() })
    if (sessionId) params.set('session_id', sessionId)
    const url = `${WS_URL}?${params.toString()}`
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
        if (data.message) {
          addMessage(sid, {
            id: data.message.id || crypto.randomUUID(),
            sender_type: data.message.sender_type || 'agent',
            message_type: data.message.message_type || 'text',
            content_text: data.message.content_text || '',
            card_payload: data.message.card_payload || null,
            created_at: data.message.created_at || new Date().toISOString(),
          })
        }
      }

      if (data.type === 'card_action_result') {
        if (data.message) {
          addMessage(sid, {
            id: data.message.id || crypto.randomUUID(),
            sender_type: 'agent',
            message_type: data.message.message_type || 'card',
            content_text: data.message.content_text || '',
            card_payload: data.message.card_payload || null,
            created_at: data.message.created_at || new Date().toISOString(),
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
