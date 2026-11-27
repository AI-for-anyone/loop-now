import { MessageImages } from '@/features/images/MessageImages'
import type { ReactNode } from 'react'
import type { Schemas } from '@/api/client'
import { UserChatBubble, AgentChatReply, PlatformChatReminder } from '@/features/chat/ChatBubble'
import type { ReplyFolderContext } from '@/features/chat/local-images'
import { Markdown } from '@/features/chat/Markdown'
import { useAgentReplyFolder, useTaskNodeReplyFolder } from '@/features/chat/use-reply-folder'
import { partyDisplay } from '@/features/inbox/participants'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

const copy = t.taskInbox

// This is the original formal Inbox message, including durable image references.
// Viewing it never marks the message read as the Agent.
export function TaskInboxMessageBubble({
  message,
  folder,
  nodeContext,
  children,
}: {
  message: Schemas['InboxMessage']
  folder?: ReplyFolderContext
  nodeContext?: { nodeRunId: string; agentId: string }
  children?: ReactNode
}) {
  const sender = partyDisplay(message.from)
  // When an Agent other than this task node sent the message, resolve its folder (another task node if message.task names one,
  // otherwise that regular Agent's folder).
  const senderTask =
    !folder && message.from.kind === 'agent' && message.task?.nodeRunId && message.task.nodeRunId !== nodeContext?.nodeRunId
      ? { taskId: message.task.taskId, nodeRunId: message.task.nodeRunId }
      : undefined
  const senderAgentId =
    !folder && !senderTask && message.from.kind === 'agent' && message.from.id && message.from.id !== nodeContext?.agentId
      ? message.from.id
      : undefined
  const senderTaskFolder = useTaskNodeReplyFolder(senderTask?.taskId, senderTask?.nodeRunId)
  const senderAgentFolder = useAgentReplyFolder(senderAgentId)
  const replyFolder = folder ?? senderTaskFolder ?? senderAgentFolder
  const source = <Badge tone="info">{message.inReplyTo ? copy.reply : copy.message}</Badge>
  const reference = <p className="text-xs [overflow-wrap:anywhere]">{copy.original} · {message.id}{message.inReplyTo && <> · {copy.inReplyTo} · {message.inReplyTo}</>}</p>
  return <div className="flex min-w-0 flex-col gap-2" data-inbox-message-id={message.id}>
    {message.from.kind === 'console' ? <>
      <UserChatBubble images={<MessageImages images={message.images} />} text={message.text} time={t.chat.time(message.createdAt)} interrupted={message.whenBusy === 'interrupt'} />
      <div className="flex flex-wrap justify-end gap-2 text-xs">{source}{message.cancelledAt ? <Badge>{t.inbox.cancelled}</Badge> : <Badge tone={message.readAt ? 'success' : 'warning'}>{message.readAt ? copy.read : copy.unread}</Badge>}</div>
    </> : message.from.kind === 'agent' ? <AgentChatReply agent={{ id: message.from.id ?? message.from.address ?? message.from.name, name: sender.title }} metadata={<><time dateTime={message.createdAt}>{t.chat.time(message.createdAt)}</time>{source}{sender.deleted && <Badge>{sender.deleted}</Badge>}</>}><Markdown text={message.text} folder={replyFolder} /><MessageImages images={message.images} /></AgentChatReply>
      : <><PlatformChatReminder text={message.text} at={message.createdAt} label={sender.title} />{source}<MessageImages images={message.images} /></>}
    {children}{reference}
  </div>
}
