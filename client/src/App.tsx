import { useEffect, useState, useCallback } from 'react'
import { useSessionStore } from './store/sessionStore'
import { useWebSocket } from './hooks/useWebSocket'
import SessionSidebar from './components/SessionSidebar'
import MessageList from './components/MessageList'
import ChatInput from './components/ChatInput'

const API_HTTP = import.meta.env.VITE_API_URL ?? 'http://localhost:8000'
const DEV_USER_ID = 'dev-user-001'

function makeDevToken(): string {
  const header = btoa(JSON.stringify({ alg: 'HS256', typ: 'JWT' }))
  const now = Math.floor(Date.now() / 1000)
  const payload = btoa(JSON.stringify({ sub: DEV_USER_ID, iat: now, exp: now + 604800 }))
  return `${header}.${payload}.dev-signature-bypass`
}

export default function App() {
  const [token, setToken] = useState(() => localStorage.getItem('reagent_token') || '')
  const [ready, setReady] = useState(false)

  const sessions = useSessionStore(s => s.sessions)
  const activeSessionId = useSessionStore(s => s.activeSessionId)
  const messages = useSessionStore(s => s.messages)
  const setSessions = useSessionStore(s => s.setSessions)
  const setActiveSession = useSessionStore(s => s.setActiveSession)
  const setMessages = useSessionStore(s => s.setMessages)
  const addMessage = useSessionStore(s => s.addMessage)
  const optimisticAdd = useSessionStore(s => s.optimisticAdd)
  const undoOptimistic = useSessionStore(s => s.undoOptimistic)

  const { send, connected } = useWebSocket(token)

  useEffect(() => {
    let t = localStorage.getItem('reagent_token')
    if (!t) {
      t = makeDevToken()
      localStorage.setItem('reagent_token', t)
    }
    setToken(t)
  }, [])

  useEffect(() => {
    if (!token) return
    fetch(`${API_HTTP}/chat/sessions`, {
      headers: { Authorization: `Bearer ${token}` }
    })
      .then(r => r.ok ? r.json() : [])
      .then((data: any[]) => {
        setSessions(data)
        if (data.length > 0) {
          const first = data[0]
          setActiveSession(first.id)
          loadMessages(first.id)
        } else {
          createSession('General')
        }
        setReady(true)
      })
      .catch(() => setReady(true))
  }, [token])

  const loadMessages = (sessionId: string) => {
    fetch(`${API_HTTP}/chat/sessions/${sessionId}`, {
      headers: { Authorization: `Bearer ${token}` }
    })
      .then(r => r.ok ? r.json() : { messages: [] })
      .then(data => setMessages(sessionId, data.messages || []))
  }

  const createSession = useCallback((title: string) => {
    fetch(`${API_HTTP}/chat/sessions`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ title, context: {} })
    })
      .then(r => r.ok ? r.json() : null)
      .then(session => {
        if (!session) return
        setSessions([session, ...sessions])
        setActiveSession(session.id)
      })
  }, [token, sessions])

  const handleSelectSession = useCallback((id: string) => {
    setActiveSession(id)
    loadMessages(id)
  }, [token])

  const handleSend = useCallback((text: string) => {
    if (!activeSessionId) return
    const temp = optimisticAdd(activeSessionId, text)
    send({ type: 'message', content: text })
    if (!connected) {
      fetch(`${API_HTTP}/chat/sessions/${activeSessionId}/messages`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ content: text })
      })
        .then(r => r.ok ? r.json() : null)
        .then(data => {
          if (data) {
            undoOptimistic(activeSessionId, temp.id)
            addMessage(activeSessionId, data.message)
          }
        })
    }
  }, [activeSessionId, send, connected, token])

  const handleCardAction = useCallback((cardId: string, actionId: string, payload?: any) => {
    send({ type: 'card_action', card_id: cardId, action_id: actionId, payload })
  }, [send])

  const handleSourceRequest = useCallback((emailId: string, messageId: string) => {
    send({ type: 'source_request', email_id: emailId, message_id: messageId })
  }, [send])

  const activeMessages = activeSessionId ? (messages[activeSessionId] || []) : []
  const activeTitle = sessions.find(s => s.id === activeSessionId)?.title || 'Reagent'

  return (
    <div className="h-screen flex bg-slate-950 text-slate-100 overflow-hidden">
      <SessionSidebar
        sessions={sessions}
        activeId={activeSessionId}
        onCreate={() => createSession('New Session')}
        onSelect={handleSelectSession}
      />
      <div className="flex-1 flex flex-col min-w-0">
        <div className="h-12 border-b border-slate-800 flex items-center justify-between px-4 shrink-0">
          <div className="font-medium text-sm truncate">{activeTitle}</div>
          <div className="flex items-center gap-2">
            <div className={`w-2 h-2 rounded-full ${connected ? 'bg-emerald-500' : 'bg-amber-500'}`} />
            <span className="text-xs text-slate-400">{connected ? 'Online' : 'Reconnecting...'}</span>
          </div>
        </div>
        <div className="flex-1 overflow-y-auto min-h-0">
          <MessageList
            messages={activeMessages}
            onCardAction={handleCardAction}
            onSourceRequest={handleSourceRequest}
          />
        </div>
        <div className="shrink-0 border-t border-slate-800 p-3">
          <ChatInput onSend={handleSend} placeholder="Ask about your inbox or type 'start stack'..." />
        </div>
      </div>
    </div>
  )
}
