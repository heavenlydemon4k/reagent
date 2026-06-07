import { SourceEmail } from '../store/sessionStore'

interface Props {
  email: SourceEmail
}

export default function SourceEmailBlock({ email }: Props) {
  return (
    <div className="bg-slate-900 border border-slate-700 rounded-lg p-3 mt-2 text-xs">
      <div className="flex justify-between text-slate-400 mb-1">
        <span>From: {email.from}</span>
        <span>{new Date(email.received_at).toLocaleDateString()}</span>
      </div>
      <div className="font-semibold text-slate-200 mb-1">{email.subject}</div>
      <div className="text-slate-400 whitespace-pre-wrap max-h-40 overflow-y-auto">
        {email.body_text}
      </div>
    </div>
  )
}
