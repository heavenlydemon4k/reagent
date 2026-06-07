import { useEffect, useState, useCallback } from 'react'
import { useSessionStore } from './store/sessionStore'
import { useWebSocket } from './hooks/useWebSocket'
import SessionSidebar from './components/SessionSidebar'
import MessageList from './components/MessageList'
import ChatInput from './components/ChatInput'
import ProfileDrawer from './components/ProfileDrawer'
import InboxViewer from './components/InboxViewer'

const API_HTTP = 'http://localhost:8000'

export default function App() {
  const [token, setToken] = useState(localStorage.getItem('token') || '')
  const [profileOpen, setProfileOpen] = useState(false)
  const [inboxOpen, setInboxOpen] = useState(false)
  const [profile, setProfile] = useState(null)

  const sessions = useSessionStore(s => s.sessions)
  const activeSessionId = useSessionStore(s => s.activeSessionId)
  const setSessions = useSessionStore(s => s.setSessions)
  const setActiveSession = useSessionStore(s => s.setActiveSession)
  const setMessages = useSessionStore(s => s.setMessages)
  const addMessage = useSessionStore(s => s.addMessage)

  const { send } = useWebSocket(token)

  useEffect(() => {
    if (!token) return
    fetch(`${API_HTTP}/chat/sessions`, { headers: { Authorization: `Bearer ${token}` } })
      .then(r => r.json())
      .then(data => {
        setSessions(data)
        if (data.length > 0 && !activeSessionId) {
          setActiveSession(data[0].id)
          loadMessages(data[0].id)
        }
      })
    fetch(`${API_HTTP}/profile/me`, { headers: { Authorization: `Bearer ${token}` } })
      .then(r => r.json())
      .then(setProfile)
  }, [token])

  const loadMessages = (sessionId: string) => {
    fetch(`${API_HTTP}/chat/sessions/${sessionId}`, { headers: { Authorization: `Bearer ${token}` } })
      .then(r => r.json())
      .then(data => setMessages(sessionId, data.messages || []))
  }

  const handleCreateSession = useCallback(() => {
    fetch(`${API_HTTP}/chat/sessions`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ user_id: 'me', title: 'New Session' })
    })
      .then(r => r.json())
      .then(session => {
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
    addMessage(activeSessionId, {
      id: crypto.randomUUID(),
      sender_type: 'user',
      message_type: 'text',
      content_text: text,
      created_at: new Date().toISOString(),
    })
    send({ type: 'message', session_id: activeSessionId, content: text })
  }, [activeSessionId, send])

  const handleCardAction = useCallback((messageId: string, actionId: string) => {
    send({ type: 'card_action', message_id: messageId, action_id: actionId })
  }, [send])

  const handleSourceRequest = useCallback((emailId: string, messageId: string) => {
    send({ type: 'source_request', email_id: emailId, message_id: messageId })
  }, [send])

  const handleSaveProfile = useCallback((form: any) => {
    fetch(`${API_HTTP}/profile/me`, {
      method: 'PUT',
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
      body: JSON.stringify(form)
    }).then(() => setProfileOpen(false))
  }, [token])

  const handleDragToChat = useCallback((email: any) => {
    setInboxOpen(false)
    if (!activeSessionId) return
    send({ type: 'message', session_id: activeSessionId, content: `Discuss this email: ${email.id}` })
  }, [activeSessionId, send])

  if (!token) {
    return (
      <div className="h-screen flex items-center justify-center bg-slate-900">
        <div className="bg-slate-800 p-6 rounded-lg w-80">
          <h1 className="text-xl font-bold mb-4">Reagent</h1>
          <p className="text-sm text-slate-400 mb-4">Paste your API token:</p>
          <input
            placeholder="Token"
            className="w-full bg-slate-700 rounded px-3 py-2 text-sm mb-3"
            onChange={e => setToken(e.target.value)}
          />
          <button
            onClick={() => { localStorage.setItem('token', token); window.location.reload() }}
            className="w-full bg-blue-600 rounded py-2 text-sm font-medium"
          >
            Enter
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="h-screen flex bg-slate-900 text-slate-100">
      <SessionSidebar onCreate={handleCreateSession} onSelect={handleSelectSession} />
      <div className="flex-1 flex flex-col">
        <div className="h-14 border-b border-slate-800 flex items-center justify-between px-4">
          <div className="font-medium text-sm">
            {sessions.find(s => s.id === activeSessionId)?.title || 'Select a session'}
          </div>
          <div className="flex gap-3">
            <button onClick={() => setInboxOpen(true)} className="text-xs text-slate-400 hover:text-white">Inbox</button>
            <button onClick={() => setProfileOpen(true)} className="text-xs text-slate-400 hover:text-white">Profile</button>
          </div>
        </div>
        <MessageList
          sessionId={activeSessionId || ''}
          onCardAction={handleCardAction}
          onSourceRequest={handleSourceRequest}
        />
        <ChatInput onSend={handleSend} disabled={!activeSessionId} />
      </div>
      <ProfileDrawer open={profileOpen} onClose={() => setProfileOpen(false)} onSave={handleSaveProfile} profile={profile} />
      {inboxOpen && <InboxViewer onDragToChat={handleDragToChat} onClose={() => setInboxOpen(false)} />}
    </div>
  )
}
