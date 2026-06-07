import { useSessionStore, Message } from '../store/sessionStore'
import CardRenderer from './CardRenderer'
import SourceEmailBlock from './SourceEmailBlock'
import { formatDistanceToNow } from 'date-fns'

interface Props {
  sessionId: string
  onCardAction: (cardId: string, actionId: string) => void
  onSourceRequest: (emailId: string, messageId: string) => void
}

export default function MessageList({ sessionId, onCardAction, onSourceRequest }: Props) {
  const messages = useSessionStore(s => s.messages[sessionId] || [])
  const typing = useSessionStore(s => s.typing[sessionId])

  return (
    <div className="flex-1 overflow-y-auto p-4 space-y-4">
      {messages.map((msg) => (
        <MessageItem
          key={msg.id}
          message={msg}
          onCardAction={onCardAction}
          onSourceRequest={onSourceRequest}
        />
      ))}
      {typing && (
        <div className="flex items-center gap-2 text-slate-500 text-sm">
          <div className="w-2 h-2 bg-slate-500 rounded-full animate-bounce" />
          <div className="w-2 h-2 bg-slate-500 rounded-full animate-bounce delay-75" />
          <div className="w-2 h-2 bg-slate-500 rounded-full animate-bounce delay-150" />
          <span>Agent is typing...</span>
        </div>
      )}
    </div>
  )
}

function MessageItem({ message, onCardAction, onSourceRequest }: {
  message: Message
  onCardAction: (cardId: string, actionId: string) => void
  onSourceRequest: (emailId: string, messageId: string) => void
}) {
  const isUser = message.sender_type === 'user'
  const isAgent = message.sender_type === 'agent'

  const hasSource = message.card_payload?.source_email_id || (message.source_email_ids && message.source_email_ids.length > 0)
  const sourceId = message.card_payload?.source_email_id || message.source_email_ids?.[0]

  return (
    <div className={`flex ${isUser ? 'justify-end' : 'justify-start'}`}>
      <div className={`max-w-[80%] ${isUser ? 'bg-blue-600 text-white' : 'bg-slate-800 text-slate-200'} rounded-lg px-4 py-2`}>
        {message.message_type === 'text' && (
          <div className="text-sm whitespace-pre-wrap">{message.content_text}</div>
        )}
        {message.message_type === 'card' && message.card_payload && (
          <div className="mt-1">
            <CardRenderer
              payload={message.card_payload}
              onAction={(actionId) => onCardAction(message.id, actionId)}
              resolved={(message.card_payload as any)._resolved}
            />
          </div>
        )}
        {message.message_type === 'source' && message.source_email && (
          <SourceEmailBlock email={message.source_email} />
        )}
        <div className="flex items-center gap-2 mt-1">
          <div className="text-[10px] opacity-60">
            {formatDistanceToNow(new Date(message.created_at), { addSuffix: true })}
          </div>
          {hasSource && sourceId && (
            <button
              onClick={() => onSourceRequest(sourceId, message.id)}
              className="text-[10px] bg-slate-700 hover:bg-slate-600 text-slate-300 px-1.5 py-0.5 rounded"
            >
              Source
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
