import { useState } from 'react'
import type { CardPayload } from '../store/sessionStore'

interface Props {
  payload: CardPayload
  onAction: (actionId: string, payload?: any) => void
}

export default function CardRenderer({ payload, onAction }: Props) {
  const [editText, setEditText] = useState('')
  const [editing, setEditing] = useState(false)

  const isConfirm = payload.card_type === 'confirm'
  const isDecision = payload.card_type === 'decision'

  return (
    <div className="mt-2 border border-slate-700 rounded-md overflow-hidden">
      <div className="bg-slate-850 px-3 py-2 border-b border-slate-700">
        <div className="font-medium text-sm">{payload.title}</div>
        {payload.body && <div className="text-xs text-slate-400 mt-1">{payload.body}</div>}
      </div>

      {isConfirm && editing && (
        <div className="p-3">
          <textarea
            value={editText}
            onChange={e => setEditText(e.target.value)}
            className="w-full bg-slate-900 text-slate-100 rounded p-2 text-sm min-h-[80px]"
          />
          <div className="flex gap-2 mt-2">
            <button
              onClick={() => { onAction('edit', { edit_text: editText }); setEditing(false) }}
              className="px-3 py-1 rounded text-xs bg-blue-600 hover:bg-blue-500"
            >
              Save Edit
            </button>
            <button
              onClick={() => setEditing(false)}
              className="px-3 py-1 rounded text-xs bg-slate-700 hover:bg-slate-600"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      <div className="p-2 flex gap-2">
        {payload.options?.map(opt => (
          <button
            key={opt.id}
            onClick={() => {
              if (opt.id === 'edit' && isConfirm) {
                setEditText(payload.body || '')
                setEditing(true)
              } else {
                onAction(opt.id)
              }
            }}
            className={`px-3 py-1.5 rounded text-xs font-medium transition-colors ${
              opt.style === 'primary'
                ? 'bg-blue-600 hover:bg-blue-500 text-white'
                : opt.style === 'danger'
                ? 'bg-red-600 hover:bg-red-500 text-white'
                : 'bg-slate-700 hover:bg-slate-600 text-slate-200'
            }`}
          >
            {opt.label}
          </button>
        ))}
      </div>
    </div>
  )
}
