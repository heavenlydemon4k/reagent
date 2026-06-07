import { useRef, useEffect } from 'react'
import type { Message, CardPayload } from '../store/sessionStore'
import CardRenderer from './CardRenderer'
import SourceEmailBlock from './SourceEmailBlock'

interface Props {
  messages: Message[]
  onCardAction: (cardId: string, actionId: string, payload?: any) => void
  onSourceRequest: (emailId: string, messageId: string) => void
}

export default function MessageList({ messages, onCardAction, onSourceRequest }: Props) {
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages.length])

  if (messages.length === 0) {
    return (
      <div className="h-full flex items-center justify-center text-slate-500 text-sm">
        <div className="text-center">
          <p className="mb-2">Welcome to Reagent.</p>
          <p className="text-xs">Ask about your inbox or type "start stack" to process critical emails.</p>
        </div>
      </div>
    )
  }

  return (
    <div className="p-4 space-y-4">
      {messages.map((msg) => (
        <MessageItem
          key={msg.id}
          message={msg}
          onCardAction={onCardAction}
          onSourceRequest={onSourceRequest}
        />
      ))}
      <div ref={bottomRef} />
    </div>
  )
}

function MessageItem({ message, onCardAction, onSourceRequest }: {
  message: Message
  onCardAction: (cardId: string, actionId: string, payload?: any) => void
  onSourceRequest: (emailId: string, messageId: string) => void
}) {
  const isUser = message.sender_type === 'user'
  const isAgent = message.sender_type === 'agent'
  const isSystem = message.sender_type === 'system'

  const hasSource = message.card_payload?.source_email_id || message.source_email?.id
  const sourceId = message.card_payload?.source_email_id || message.source_email?.id

  return (
    <div className={`flex ${isUser ? 'justify-end' : 'justify-start'}`}>
      <div className={`max-w-[80%] rounded-lg px-4 py-2 text-sm ${
        isUser ? 'bg-blue-600 text-white' :
        isSystem ? 'bg-slate-800 text-slate-400 text-xs italic' :
        'bg-slate-800 text-slate-100'
      }`}>
        {/* Text content */}
        {message.content_text && (
          <div className="whitespace-pre-wrap">{message.content_text}</div>
        )}

        {/* Card rendering */}
        {message.message_type === 'card' && message.card_payload && (
          <CardRenderer
            payload={message.card_payload}
            onAction={(actionId, payload) => onCardAction(message.id, actionId, payload)}
          />
        )}

        {/* Source email inline */}
        {message.message_type === 'source' && message.source_email && (
          <SourceEmailBlock email={message.source_email} />
        )}

        {/* Timestamp + Source button */}
        <div className="mt-1 flex items-center gap-2">
          <span className="text-[10px] opacity-60">
            {new Date(message.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </span>
          {hasSource && sourceId && (
            <button
              onClick={() => onSourceRequest(sourceId, message.id)}
              className="text-[10px] text-blue-400 hover:text-blue-300 underline"
            >
              Source
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
