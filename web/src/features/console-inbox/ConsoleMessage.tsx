import { useRef, type ReactNode } from 'react'
import { CheckIcon, HandIcon, ReplyIcon, XIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { ErrorAlert } from '@/components/ErrorAlert'
import { AgentChatReply } from '@/features/chat/ChatBubble'
import { Markdown } from '@/features/chat/Markdown'
import { MessageImages } from '@/features/images/MessageImages'
import { InboxNotice } from '@/features/inbox/InboxNotice'
import { partyName } from '@/features/inbox/participants'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAnswerQuestion, useReadOnView } from './useAnswerQuestion'

const text = t.consoleInbox

type Message = Schemas['InboxMessage']

// A message an Agent sent you, placed in its chat: styled like an Agent reply, with “发给你” (For you) on the name line and a
// dot while unread.
// A question with options lists option buttons below the body. The message is marked read once most of it scrolls into view.
// When answerable is false (for example, the task says the current identity can't answer), only the question's state is
// shown; onReply puts the composer into the mode for replying to this question.
// meta goes at the end of the name line and children below the question (task nodes use these to show the Inbox source and
// the linked run).
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
  return <span className="rounded-stamp border border-pink-text/30 bg-tint-yellow px-1.5 text-[11.5px] leading-[18px] font-semibold text-pink-text [[data-skin=plate]_&]:border-riso-pink/45 [[data-skin=plate]_&]:bg-tint-pink-2 [[data-skin=plate]_&]:font-black">{text.toYou}</span>
}

export function UnreadDot() {
  return (
    <span title={text.unread} className="inline-flex">
      <span aria-hidden="true" className="size-2 rounded-full bg-pink-text [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:shadow-[0_0_0_2px_var(--ink)]" />
      <span className="sr-only">{text.unread}</span>
    </span>
  )
}

const optionBase = 'inline-flex min-h-9 max-w-full items-center gap-1.5 rounded-full border px-3.5 text-left text-[13.5px] leading-5 font-medium [overflow-wrap:anywhere] outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:font-bold'
const optionStyle = {
  open: 'cursor-pointer border-hairline-strong bg-surface-control text-ink hover:border-ink hover:bg-surface-hover disabled:cursor-default disabled:opacity-60 [[data-skin=plate]_&]:border-ink/35 [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:shadow-[2px_2px_0_rgb(36_67_181/.18)] [[data-skin=plate]_&]:hover:border-riso-blue [[data-skin=plate]_&]:hover:bg-tint-blue',
  chosen: 'border-cta-bg bg-cta-bg text-cta-fg [[data-skin=plate]_&]:border-riso-blue [[data-skin=plate]_&]:bg-riso-blue [[data-skin=plate]_&]:text-white',
  faded: 'border-hairline bg-transparent text-ink-500 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/20',
}

// The question's options: while waiting for an answer, each option is a button that submits itself as the answer when
// clicked; once answered, the chosen option is highlighted and the rest fade; after expiring, closing or being cancelled, all
// fade and the state is written out. Text answers go through the composer's reply mode (onReply).
export function QuestionChoices({ message, answerable = true, onReply }: { message: Message; answerable?: boolean; onReply?: (message: Message) => void }) {
  const answer = useAnswerQuestion()
  const question = answer.latest ?? message.question
  if (!question) return null
  const open = question.state === 'open' && message.to.kind === 'console' && answerable && answer.outcome !== 'rejected' && answer.outcome !== 'answered'
  const chosen = question.answer?.optionId ?? null
  const choose = (optionId: string) => {
    if (!open || answer.pending) return
    answer.submit(message, { optionId }).catch(() => {
      // The error is shown below; when the original question can no longer be answered, the buttons fade accordingly.
    })
  }
  return (
    <section aria-label={text.question} className="flex min-w-0 flex-col gap-2">
      {question.kind === 'permission' && (
        <p data-permission-question className="inline-flex items-center gap-1.5 self-start rounded-stamp border border-hairline-strong bg-tint-yellow px-2 text-[12px] leading-[22px] font-semibold text-ink [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:shadow-[inset_0_0_0_1.5px_var(--ink)]">
          <HandIcon aria-hidden="true" className="size-3.5" strokeWidth={2.4} />
          {t.permissions.inbox.badge}
        </p>
      )}
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
    if (result.by.kind !== 'console') return <span>{text.answeredBy(partyName(result.by), time)}</span>
    return <span>{result.optionLabel ? text.chosen(time, result.optionLabel) : text.answeredByText(time, result.textPreview)}</span>
  }
  // An approval question closes as expired when its request is cancelled; it never waited out its time.
  if (question.kind === 'permission' && question.state === 'expired') return <span>{t.permissions.inbox.cancelled}</span>
  return <span>{t.inbox.question.state[question.state]}{question.state === 'expired' && ` ${t.chat.metaSeparator} ${t.inbox.question.expires(question.expiresAt)}`}</span>
}

// The row attached above the text line while the composer is in reply-to-question mode: whose question is being answered,
// a summary of the question, and cancel.
export function ReplyChip({ name, preview, onCancel }: { name: string; preview: string; onCancel: () => void }) {
  return (
    <div className="flex min-w-0 items-center gap-2 px-1 pt-0.5 pb-1.5 text-[12.5px] leading-5">
      <ReplyIcon aria-hidden="true" className="size-3.5 shrink-0 text-riso-blue" strokeWidth={2.4} />
      <span className="shrink-0 font-bold text-ink">{text.replying(name)}</span>
      <span className="min-w-0 flex-1 truncate text-ink-500" title={preview}>{preview}</span>
      <button type="button" aria-label={text.cancelReply} title={text.cancelReply} onClick={onCancel} className="grid size-6 shrink-0 cursor-pointer place-items-center rounded-full text-ink-700 outline-none hover:bg-surface-hover focus-visible:shadow-focus [[data-skin=plate]_&]:hover:bg-tint-blue">
        <XIcon aria-hidden="true" className="size-3.5" strokeWidth={2.6} />
      </button>
    </div>
  )
}
