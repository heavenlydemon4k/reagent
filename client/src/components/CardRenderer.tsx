import { CardPayload } from '../store/sessionStore'

interface Props {
  payload: CardPayload
  onAction: (actionId: string, payload?: object) => void
  resolved?: boolean
}

export default function CardRenderer({ payload, onAction, resolved }: Props) {
  if (resolved) {
    return (
      <div className="bg-slate-800 border border-slate-700 rounded-lg p-4 opacity-60">
        <div className="text-sm font-semibold text-slate-300">{payload.title}</div>
        <div className="text-xs text-slate-500 mt-1">Resolved</div>
      </div>
    )
  }

  return (
    <div className="bg-slate-800 border border-slate-700 rounded-lg p-4 max-w-md">
      <div className="text-sm font-semibold text-white mb-1">{payload.title}</div>
      {payload.body && <div className="text-sm text-slate-300 mb-3">{payload.body}</div>}
      <div className="flex gap-2 flex-wrap">
        {payload.options?.map(opt => (
          <button
            key={opt.id}
            onClick={() => onAction(opt.id)}
            className={`px-3 py-1.5 rounded text-sm font-medium transition
              ${opt.style === 'danger' ? 'bg-red-600 hover:bg-red-500 text-white' :
                opt.style === 'primary' ? 'bg-blue-600 hover:bg-blue-500 text-white' :
                'bg-slate-700 hover:bg-slate-600 text-slate-200'}`}
          >
            {opt.label}
          </button>
        ))}
      </div>
    </div>
  )
}
