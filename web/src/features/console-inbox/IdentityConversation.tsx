import { useId, useRef, useState } from 'react'
import { PluginSenderChip } from '@/features/plugins/ChatLines'
import { PenLineIcon, ReplyIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { useIdentityConversation } from '@/api/console-inbox'
import { useReplyInbox, useSendInbox } from '@/api/inbox'
import { useContextInfo } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { UserChatBubble } from '@/features/chat/ChatBubble'
import { Markdown } from '@/features/chat/Markdown'
import { MessageImages } from '@/features/images/MessageImages'
import { InboxNotice } from '@/features/inbox/InboxNotice'
import { t } from '@/i18n'
import { consoleInboxText as text } from '@/i18n/console-inbox'
import { QuestionChoices, UnreadDot } from './ConsoleMessage'
import { useReadOnView } from './useAnswerQuestion'

type Message = Schemas['InboxMessage']
const encoder = new TextEncoder()

// 外部身份与你的往来：它发给你的消息与你发给它的消息按时间排列，发给你的进入视野时标为已读。
// 回复接在最近一条消息之后（最近一条是待回答的问题时，回复就是它的答案）；新消息另起一条线索。已删除的身份只能查看。
export function IdentityConversation({ identity }: { identity: Schemas['ExternalIdentity'] }) {
  const conversation = useIdentityConversation(identity.id)
  const titleId = useId()
  const [composing, setComposing] = useState(false)
  const deleted = identity.deletedAt !== null
  const last = conversation.messages.at(-1)
  return (
    <section aria-labelledby={titleId} className="flex min-w-0 flex-col gap-4 rounded-panel bg-paper-raised p-5 shadow-print-rest">
      <div className="flex flex-col gap-1">
        <h3 id={titleId} className="font-display-cn text-[28px] leading-tight text-riso-blue">{text.conversation}</h3>
        <p className="text-[13px] text-ink-500">{text.conversationIntro}</p>
      </div>
      <ErrorAlert error={conversation.error} />
      {conversation.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
      {!conversation.isPending && conversation.messages.length === 0 && <p className="text-[14px] text-ink-500">{text.conversationEmpty}</p>}
      {conversation.messages.length > 0 && (
        <ol aria-label={text.conversation} className="flex min-w-0 flex-col gap-5">
          {conversation.messages.map((message) => <ConversationItem key={message.id} message={message} />)}
        </ol>
      )}
      {!deleted && last && <ReplyBox key={last.id} last={last} name={identity.name} />}
      {!deleted && (composing ? <NewMessageBox identity={identity} onClose={() => setComposing(false)} /> : (
        <Button variant="secondary" className="self-start" onClick={() => setComposing(true)}>
          <PenLineIcon />
          {text.newMessage}
        </Button>
      ))}
    </section>
  )
}

function ConversationItem({ message }: { message: Message }) {
  const ref = useRef<HTMLLIElement>(null)
  useReadOnView(ref, message)
  if (message.from.kind === 'console') {
    return (
      <li className="min-w-0">
        <UserChatBubble text={message.text} time={t.inbox.time(message.createdAt)} images={<MessageImages images={message.images} />} />
        <p className="mt-1 text-right text-xs text-ink-500">{message.readAt ? t.inbox.readByThem : t.inbox.unreadByThem}</p>
      </li>
    )
  }
  const unread = message.to.kind === 'console' && !message.readAt && !message.cancelledAt
  return (
    <li ref={ref} data-console-message-id={message.id} className="flex min-w-0 flex-col gap-1.5">
      <span className="inline-flex flex-wrap items-center gap-2 text-[12.5px] font-bold text-ink-500">
        {message.from.kind === 'plugin' ? <PluginSenderChip participant={message.from} /> : <span className="text-ink">{message.from.name}</span>}
        <span aria-hidden="true">{t.chat.metaSeparator}</span>
        <time dateTime={message.createdAt}>{t.inbox.time(message.createdAt)}</time>
        {unread && <UnreadDot />}
        {message.cancelledAt && <Badge className="shadow-none">{t.inbox.cancelled}</Badge>}
      </span>
      <div className="min-w-0 rounded-[4px_20px_20px_20px] bg-white px-5 py-3.5 text-[15.5px] leading-[1.8] shadow-[3px_3px_0_rgb(24_38_94/.14)]">
        <Markdown text={message.text} />
        <MessageImages images={message.images} />
        {message.notice && <InboxNotice notice={message.notice} />}
      </div>
      <QuestionChoices message={message} />
    </li>
  )
}

// 回复最近一条消息：收件人由服务端确定为线索中的另一方。
function ReplyBox({ last, name }: { last: Message; name: string }) {
  const reply = useReplyInbox()
  const context = useContextInfo()
  const id = useId()
  const [value, setValue] = useState('')
  const [localError, setLocalError] = useState<string | null>(null)
  const max = context.data?.limits.maxInboxMessage
  const submit = async () => {
    setLocalError(null)
    reply.reset()
    if (!value.trim()) { setLocalError(t.chat.emptyMessage); return }
    if (max !== undefined && encoder.encode(value).length > max) { setLocalError(t.chat.tooLong(max)); return }
    const sent = value
    const message = await reply.mutateAsync({ messageId: last.id, text: sent }).catch(() => null)
    if (message) setValue((current) => (current.startsWith(sent) ? current.slice(sent.length) : current))
  }
  return (
    <div className="flex flex-col gap-2">
      <label htmlFor={id} className="text-[13px] font-bold">{t.inbox.replyTo(name)}</label>
      <Textarea id={id} rows={3} value={value} onChange={(event) => setValue(event.target.value)} aria-invalid={localError ? true : undefined} />
      {localError && <p role="alert" className="text-[13px] font-black text-red-text">{localError}</p>}
      <ErrorAlert error={reply.error} />
      <Button className="self-end" disabled={reply.isPending} onClick={() => void submit()}>
        <ReplyIcon />
        {reply.isPending ? t.inbox.sending : t.inbox.reply}
      </Button>
    </div>
  )
}

function NewMessageBox({ identity, onClose }: { identity: Schemas['ExternalIdentity']; onClose: () => void }) {
  const send = useSendInbox()
  const context = useContextInfo()
  const id = useId()
  const [value, setValue] = useState('')
  const [localError, setLocalError] = useState<string | null>(null)
  const max = context.data?.limits.maxInboxMessage
  const submit = async () => {
    setLocalError(null)
    send.reset()
    if (!value.trim()) { setLocalError(t.chat.emptyMessage); return }
    if (max !== undefined && encoder.encode(value).length > max) { setLocalError(t.chat.tooLong(max)); return }
    const message = await send.mutateAsync({ to: identity.address, text: value }).catch(() => null)
    if (message) onClose()
  }
  return (
    <div className="flex flex-col gap-2 rounded-card bg-white p-4">
      <label htmlFor={id} className="text-[13px] font-bold">{text.newMessageLabel(identity.name)}</label>
      <p className="text-xs text-ink-500">{text.newMessageHint}</p>
      <Textarea id={id} rows={3} value={value} onChange={(event) => setValue(event.target.value)} aria-invalid={localError ? true : undefined} />
      {localError && <p role="alert" className="text-[13px] font-black text-red-text">{localError}</p>}
      <ErrorAlert error={send.error} />
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onClose}>{text.cancel}</Button>
        <Button disabled={send.isPending} onClick={() => void submit()}>{send.isPending ? t.inbox.sending : t.inbox.send}</Button>
      </div>
    </div>
  )
}
