import { useSessionStore } from '../store/sessionStore'

interface Props {
  onCreate: () => void
  onSelect: (id: string) => void
}

export default function SessionSidebar({ onCreate, onSelect }: Props) {
  const sessions = useSessionStore(s => s.sessions)
  const activeId = useSessionStore(s => s.activeSessionId)

  return (
    <div className="w-64 bg-slate-850 border-r border-slate-800 flex flex-col">
      <div className="p-4 border-b border-slate-800 flex items-center justify-between">
        <h2 className="font-semibold text-sm text-slate-300">Sessions</h2>
        <button
          onClick={onCreate}
          className="text-xs bg-slate-700 hover:bg-slate-600 text-white px-2 py-1 rounded"
        >
          + New
        </button>
      </div>
      <div className="flex-1 overflow-y-auto">
        {sessions.map(s => (
          <button
            key={s.id}
            onClick={() => onSelect(s.id)}
            className={`w-full text-left px-4 py-3 text-sm border-b border-slate-800/50 transition
              ${activeId === s.id ? 'bg-slate-800 text-white' : 'text-slate-400 hover:bg-slate-800/50 hover:text-slate-200'}`}
          >
            <div className="font-medium truncate">{s.title}</div>
            <div className="text-xs opacity-60 mt-0.5">
              {s.last_message_at ? new Date(s.last_message_at).toLocaleDateString() : 'No messages'}
            </div>
          </button>
        ))}
      </div>
    </div>
  )
}
