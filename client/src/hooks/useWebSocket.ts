import { useEffect, useRef, useCallback } from 'react'
import { useSessionStore } from '../store/sessionStore'

const API_URL = 'ws://localhost:8000/chat/ws'

export function useWebSocket(token: string) {
  const ws = useRef<WebSocket | null>(null)
  const addMessage = useSessionStore(s => s.addMessage)
  const setTyping = useSessionStore(s => s.setTyping)
  const resolveCard = useSessionStore(s => s.resolveCard)
  const addSourceEmail = useSessionStore(s => s.addSourceEmail)

  useEffect(() => {
    if (!token) return
    const socket = new WebSocket(`${API_URL}?token=${token}`)
    socket.onmessage = (event) => {
      const data = JSON.parse(event.data)
      if (data.type === 'message' || data.type === 'card') {
        addMessage(data.message.session_id, data.message)
      } else if (data.type === 'typing') {
        setTyping(data.session_id, data.active)
      } else if (data.type === 'card_resolved') {
        resolveCard(data.session_id, data.message_id)
      } else if (data.type === 'source_email') {
        addSourceEmail(data.session_id, data.message_id, data.email)
      }
    }
    ws.current = socket
    return () => socket.close()
  }, [token])

  const send = useCallback((payload: object) => {
    if (ws.current?.readyState === WebSocket.OPEN) {
      ws.current.send(JSON.stringify(payload))
    }
  }, [])

  return { send }
}
