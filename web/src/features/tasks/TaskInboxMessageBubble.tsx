import { MessageImages } from '@/features/images/MessageImages'
import type { ReactNode } from 'react'
import type { Schemas } from '@/api/client'
import { UserChatBubble, AgentChatReply, PlatformChatReminder } from '@/features/chat/ChatBubble'
import { Markdown } from '@/features/chat/Markdown'
import { partyDisplay } from '@/features/inbox/participants'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'
import { taskInboxText as copy } from '@/i18n/task-inbox'

// This is the original formal Inbox message, including durable image references.
// Viewing it never marks the message read as the Agent.
export function TaskInboxMessageBubble({ message, children }: { message: Schemas['InboxMessage']; children?: ReactNode }) {
  const sender = partyDisplay(message.from)
  const source = <Badge tone="info">{message.inReplyTo ? copy.reply : copy.message}</Badge>
  const reference = <p className="text-xs [overflow-wrap:anywhere]">{copy.original} · {message.id}{message.inReplyTo && <> · {copy.inReplyTo} · {message.inReplyTo}</>}</p>
  return <div className="flex min-w-0 flex-col gap-2" data-inbox-message-id={message.id}>
    {message.from.kind === 'console' ? <>
      <UserChatBubble images={<MessageImages images={message.images} />} text={message.text} time={t.chat.time(message.createdAt)} interrupted={message.whenBusy === 'interrupt'} />
      <div className="flex flex-wrap justify-end gap-2 text-xs">{source}{message.cancelledAt ? <Badge>{t.inbox.cancelled}</Badge> : <Badge tone={message.readAt ? 'success' : 'warning'}>{message.readAt ? copy.read : copy.unread}</Badge>}</div>
    </> : message.from.kind === 'agent' ? <AgentChatReply agent={{ id: message.from.id ?? message.from.address ?? message.from.name, name: sender.title }} metadata={<><time dateTime={message.createdAt}>{t.chat.time(message.createdAt)}</time>{source}{sender.deleted && <Badge>{sender.deleted}</Badge>}</>}><Markdown text={message.text} /><MessageImages images={message.images} /></AgentChatReply>
      : <><PlatformChatReminder text={message.text} at={message.createdAt} label={sender.title} />{source}<MessageImages images={message.images} /></>}
    {children}{reference}
  </div>
}
