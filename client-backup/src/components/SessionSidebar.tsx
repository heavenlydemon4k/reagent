interface Props {
  sessions: Array<{ id: string; title: string; status?: string }>
  activeId: string | null
  onCreate: () => void
  onSelect: (id: string) => void
}

export default function SessionSidebar({ sessions, activeId, onCreate, onSelect }: Props) {
  return (
    <div className="w-64 border-r border-slate-800 flex flex-col bg-slate-900">
      <div className="h-12 border-b border-slate-800 flex items-center px-4">
        <span className="font-semibold text-sm">Reagent</span>
      </div>
      <div className="flex-1 overflow-y-auto p-2">
        <button
          onClick={onCreate}
          className="w-full text-left px-3 py-2 rounded-md text-sm bg-slate-800 hover:bg-slate-700 mb-2 transition-colors"
        >
          + New Session
        </button>
        {sessions.map(s => (
          <button
            key={s.id}
            onClick={() => onSelect(s.id)}
            className={`w-full text-left px-3 py-2 rounded-md text-sm mb-1 transition-colors ${
              s.id === activeId ? 'bg-slate-700 text-white' : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
            }`}
          >
            <div className="truncate">{s.title}</div>
          </button>
        ))}
      </div>
    </div>
  )
}
