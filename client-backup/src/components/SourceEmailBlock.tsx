import { useState } from 'react'
import type { SourceEmail } from '../store/sessionStore'

interface Props {
  email: SourceEmail
}

export default function SourceEmailBlock({ email }: Props) {
  const [expanded, setExpanded] = useState(true)

  return (
    <div className="mt-2 border border-slate-700 rounded-md bg-slate-900">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full text-left px-3 py-2 flex items-center justify-between text-xs text-slate-400 hover:text-slate-200"
      >
        <span className="truncate">{email.subject}</span>
        <span>{expanded ? '▲' : '▼'}</span>
      </button>
      {expanded && (
        <div className="px-3 pb-3 text-xs text-slate-300 space-y-1">
          <div><span className="text-slate-500">From:</span> {email.from}</div>
          <div><span className="text-slate-500">To:</span> {email.to?.join(', ')}</div>
          <div><span className="text-slate-500">Date:</span> {new Date(email.received_at).toLocaleString()}</div>
          <hr className="border-slate-700 my-2" />
          <div className="whitespace-pre-wrap text-slate-400">{email.body_text}</div>
        </div>
      )}
    </div>
  )
}
