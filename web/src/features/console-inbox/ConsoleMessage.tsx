import { useRef, type ReactNode } from 'react'
import { CheckIcon, ReplyIcon, XIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { ErrorAlert } from '@/components/ErrorAlert'
import { AgentChatReply } from '@/features/chat/ChatBubble'
import { Markdown } from '@/features/chat/Markdown'
import { MessageImages } from '@/features/images/MessageImages'
import { InboxNotice } from '@/features/inbox/InboxNotice'
import { t } from '@/i18n'
import { consoleInboxText as text } from '@/i18n/console-inbox'
import { cn } from '@/lib/utils'
import { useAnswerQuestion, useReadOnView } from './useAnswerQuestion'

type Message = Schemas['InboxMessage']

// Agent 发给你的一条消息，排在它的对话里：Agent 回复的样式，名称一行带“发给你”，未读时带一个圆点。
// 带选项的问题在正文下面列出选项按钮。消息大部分进入视野时标为已读。
// answerable 为 false 时（例如任务说当前身份不能回答）只显示问题的状态；onReply 让输入框进入回复这个问题的模式。
// meta 接在名称一行末尾、children 排在问题下面（任务节点用来显示 Inbox 来源与关联的执行）。
export function ConsoleMessage({ message, agent, answerable = true, onReply, meta, children }: { message: Message; agent: { id: string; name: string }; answerable?: boolean; onReply?: (message: Message) => void; meta?: ReactNode; children?: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  useReadOnView(ref, message)
  const unread = message.to.kind === 'console' && !message.readAt && !message.cancelledAt
  return (
    <div ref={ref} data-console-message-id={message.id}>
      <AgentChatReply
        agent={agent}
        metadata={
          <>
            <time dateTime={message.createdAt}>{t.chat.time(message.createdAt)}</time>
            <ToYouTag />
            {unread && <UnreadDot />}
            {message.cancelledAt && <Badge className="shadow-none">{t.inbox.cancelled}</Badge>}
            {meta}
          </>
        }
      >
        <div className="min-w-0">
          <Markdown text={message.text} />
          <MessageImages images={message.images} />
          {message.notice && <InboxNotice notice={message.notice} />}
        </div>
        <QuestionChoices message={message} answerable={answerable} onReply={onReply} />
        {children}
      </AgentChatReply>
    </div>
  )
}

export function ToYouTag() {
  return <span className="rounded-stamp border border-riso-pink/45 bg-tint-pink-2 px-1.5 text-[11.5px] leading-[18px] font-black text-pink-text">{text.toYou}</span>
}

export function UnreadDot() {
  return (
    <span title={text.unread} className="inline-flex">
      <span aria-hidden="true" className="size-2 rounded-full bg-riso-pink shadow-[0_0_0_2px_var(--ink)]" />
      <span className="sr-only">{text.unread}</span>
    </span>
  )
}

const optionBase = 'inline-flex min-h-9 max-w-full items-center gap-1.5 rounded-full border-[1.5px] px-3.5 text-left text-[13.5px] leading-5 font-bold [overflow-wrap:anywhere] outline-none focus-visible:shadow-focus'
const optionStyle = {
  open: 'cursor-pointer border-ink/35 bg-white text-ink shadow-[2px_2px_0_rgb(36_67_181/.18)] hover:border-riso-blue hover:bg-tint-blue disabled:cursor-default disabled:opacity-60',
  chosen: 'border-riso-blue bg-riso-blue text-white',
  faded: 'border-dashed border-ink/20 bg-transparent text-ink-500',
}

// 问题的选项：待回答时每个选项是一个按钮，点了就作为答案提交；回答过的高亮所选的一项，其余变淡；
// 到期、关闭或取消后都变淡并写明状态。文字回答走输入框的回复模式（onReply）。
export function QuestionChoices({ message, answerable = true, onReply }: { message: Message; answerable?: boolean; onReply?: (message: Message) => void }) {
  const answer = useAnswerQuestion()
  const question = answer.latest ?? message.question
  if (!question) return null
  const open = question.state === 'open' && message.to.kind === 'console' && answerable && answer.outcome !== 'rejected' && answer.outcome !== 'answered'
  const chosen = question.answer?.optionId ?? null
  const choose = (optionId: string) => {
    if (!open || answer.pending) return
    answer.submit(message, { optionId }).catch(() => {
      // 错误显示在下方；原题已不能回答时按钮随之变淡。
    })
  }
  return (
    <section aria-label={text.question} className="flex min-w-0 flex-col gap-2">
      {question.options.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {question.options.map((option) => {
            const picked = option.id === chosen
            return (
              <button
                key={option.id}
                type="button"
                aria-pressed={picked}
                disabled={!open || answer.pending}
                onClick={() => choose(option.id)}
                className={cn(optionBase, picked ? optionStyle.chosen : open ? optionStyle.open : optionStyle.faded)}
              >
                {picked && <CheckIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={3} />}
                {option.label}
              </button>
            )
          })}
        </div>
      )}
      <p className="flex flex-wrap items-center gap-x-1.5 text-xs leading-5 text-ink-500">
        <QuestionCaption message={message} question={question} answerable={answerable} />
        {open && onReply && (
          <>
            <span aria-hidden="true">{t.chat.metaSeparator}</span>
            <button type="button" onClick={() => onReply(message)} className="inline-flex cursor-pointer items-center gap-1 font-bold text-riso-blue underline-offset-2 outline-none hover:underline focus-visible:shadow-focus">
              <ReplyIcon aria-hidden="true" className="size-3" strokeWidth={2.4} />
              {text.replyToQuestion}
            </button>
          </>
        )}
      </p>
      {answer.outcome === 'ordinary' && <p role="status" className="text-[13px] font-bold">{t.inbox.question.ordinary}</p>}
      <ErrorAlert error={answer.error} />
    </section>
  )
}

function QuestionCaption({ message, question, answerable }: { message: Message; question: Schemas['InboxQuestion']; answerable: boolean }) {
  if (question.state === 'open') {
    if (message.to.kind !== 'console' || !answerable) return <span>{text.cannotAnswer}</span>
    return <span>{text.question} {t.chat.metaSeparator} {text.openUntil(t.inbox.time(question.expiresAt))}</span>
  }
  const result = question.answer
  if (question.state === 'answered' && result) {
    const time = t.inbox.time(result.at)
    if (result.by.kind !== 'console') return <span>{text.answeredBy(result.by.name, time)}</span>
    return <span>{result.optionLabel ? text.chosen(time, result.optionLabel) : text.answeredByText(time, result.textPreview)}</span>
  }
  return <span>{t.inbox.question.state[question.state]}{question.state === 'expired' && ` ${t.chat.metaSeparator} ${t.inbox.question.expires(question.expiresAt)}`}</span>
}

// 输入框在回复问题模式时贴在文字行上方的一行：回复谁的问题、问题摘要和取消。
export function ReplyChip({ name, preview, onCancel }: { name: string; preview: string; onCancel: () => void }) {
  return (
    <div className="flex min-w-0 items-center gap-2 px-1 pt-0.5 pb-1.5 text-[12.5px] leading-5">
      <ReplyIcon aria-hidden="true" className="size-3.5 shrink-0 text-riso-blue" strokeWidth={2.4} />
      <span className="shrink-0 font-bold text-ink">{text.replying(name)}</span>
      <span className="min-w-0 flex-1 truncate text-ink-500" title={preview}>{preview}</span>
      <button type="button" aria-label={text.cancelReply} title={text.cancelReply} onClick={onCancel} className="grid size-6 shrink-0 cursor-pointer place-items-center rounded-full text-ink-700 outline-none hover:bg-tint-blue focus-visible:shadow-focus">
        <XIcon aria-hidden="true" className="size-3.5" strokeWidth={2.6} />
      </button>
    </div>
  )
}
