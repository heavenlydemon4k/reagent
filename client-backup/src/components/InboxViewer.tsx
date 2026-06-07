import { useState, useEffect } from 'react'

interface Email {
  id: string
  subject: string
  from: string
  received_at: string
  labels: string[]
  is_read: boolean
}

interface Props {
  onDragToChat: (email: Email) => void
  onClose: () => void
}

export default function InboxViewer({ onDragToChat, onClose }: Props) {
  const [emails, setEmails] = useState<Email[]>([])
  const [filter, setFilter] = useState('')

  useEffect(() => {
    fetch('http://localhost:8000/chat/emails?limit=50')
      .then(r => r.json())
      .then(data => setEmails(data.emails || []))
  }, [])

  const filtered = emails.filter(e =>
    e.subject.toLowerCase().includes(filter.toLowerCase()) ||
    e.from.toLowerCase().includes(filter.toLowerCase())
  )

  return (
    <div className="fixed inset-0 z-40 bg-slate-900 flex flex-col">
      <div className="h-14 border-b border-slate-800 flex items-center justify-between px-4">
        <h2 className="font-semibold">Inbox</h2>
        <button onClick={onClose} className="text-sm text-slate-400 hover:text-white">Close</button>
      </div>
      <div className="p-4">
        <input
          value={filter}
          onChange={e => setFilter(e.target.value)}
          placeholder="Search emails..."
          className="w-full bg-slate-800 rounded px-3 py-2 text-sm"
        />
      </div>
      <div className="flex-1 overflow-y-auto px-4 pb-4 space-y-2">
        {filtered.map(email => (
          <div
            key={email.id}
            className="bg-slate-800 rounded-lg p-3 flex items-center justify-between cursor-pointer hover:bg-slate-750"
            onClick={() => onDragToChat(email)}
          >
            <div>
              <div className={`text-sm ${email.is_read ? 'text-slate-300' : 'text-white font-medium'}`}>
                {email.subject}
              </div>
              <div className="text-xs text-slate-500">{email.from} · {new Date(email.received_at).toLocaleDateString()}</div>
            </div>
            <div className="flex gap-1">
              {email.labels.map(l => (
                <span key={l} className="text-[10px] bg-slate-700 px-1.5 py-0.5 rounded text-slate-400">{l}</span>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
