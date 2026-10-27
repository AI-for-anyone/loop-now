import { MessageImages } from '@/features/images/MessageImages'
import { useId, useState } from 'react'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Schemas } from '@/api/client'
import { useAgentInboxMessage, useIdentityInboxMessage } from '@/api/inbox'
import { Markdown } from '@/features/chat/Markdown'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { DeletedMark } from './party'
import { partyDisplay } from './participants'
import { QuestionSummary } from './Question'
import { InboxNotice } from './InboxNotice'

// 只读收件箱中的一条消息属于谁：某个 Agent 的收件箱，或某个外部身份的收件箱。
export type InboxOwner = { kind: 'agent'; agentId: string } | { kind: 'identity'; identityId: string }

// 只读收件箱中的一条消息：点开后请求全文，查看不改变已读状态，也没有标记已读的操作。
// Agent 的收件箱只列收到的消息，显示发件人；外部身份的收件箱另有方向，显示对方。
export function ReadonlyMessage({ owner, message }: { owner: InboxOwner; message: Schemas['InboxMessageSummary'] }) {
  const [open, setOpen] = useState(false)
  const bodyId = useId()
  const agentFull = useAgentInboxMessage(owner.kind === 'agent' ? owner.agentId : '', message.id, open && owner.kind === 'agent')
  const identityFull = useIdentityInboxMessage(owner.kind === 'identity' ? owner.identityId : '', message.id, open && owner.kind === 'identity')
  const full = owner.kind === 'agent' ? agentFull : identityFull

  // 外部身份的收件箱中，收件方是该身份的为收到，其余为发出；按 ID 判断，地址可能被新身份重用。
  const received = owner.kind === 'agent' || message.to.id === owner.identityId
  const party = received ? message.from : message.to
  const shown = partyDisplay(party)
  // 被人取消的消息不计为未读，标明已取消。
  const cancelled = message.cancelledAt !== null
  const read = message.readAt !== null || cancelled
  const readText = cancelled
    ? t.inbox.cancelled
    : owner.kind === 'agent'
      ? read
        ? t.agentInbox.readAt(message.readAt!)
        : t.agentInbox.unread
      : received
        ? read
          ? t.identities.read
          : t.identities.unread
        : read
          ? t.inbox.readByThem
          : t.inbox.unreadByThem
  const unreadStyle = received ? 'bg-riso-pink text-ink' : 'bg-tint-yellow text-ink'

  return (
    <li className="rounded-card bg-white shadow-[3px_3px_0_rgb(24_38_94/.12)]">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={open ? bodyId : undefined}
        onClick={() => setOpen((value) => !value)}
        className="flex min-h-16 w-full items-start gap-3 rounded-card px-[18px] py-3.5 text-left text-ink outline-none focus-visible:shadow-focus"
      >
        {owner.kind === 'agent' ? (
          <span
            aria-hidden="true"
            className={cn('mt-[9px] size-[9px] shrink-0 rounded-full', !read && 'bg-riso-pink shadow-[0_0_0_2px_var(--ink)]')}
          />
        ) : (
          <span
            className={cn(
              'mt-0.5 inline-flex min-w-[46px] shrink-0 items-center justify-center rounded-full px-2 text-[12px] leading-[22px] font-black',
              received ? 'bg-tint-blue text-riso-blue' : 'bg-tint-pink-2 text-pink-text',
            )}
          >
            {received ? t.identities.directions.received : t.identities.directions.sent}
          </span>
        )}
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
            <span className={cn('text-[15px]', !read && received ? 'font-black' : 'font-bold')}>{shown.title}</span>
            {shown.deleted && <DeletedMark label={shown.deleted} />}
            {shown.address && <span className="font-mono text-[11.5px] text-ink-500">{shown.address}</span>}
            {message.whenBusy === 'interrupt' && (
              <span className="rounded-stamp bg-riso-pink px-1.5 text-[11.5px] font-black">{t.inbox.interrupt}</span>
            )}
            <time dateTime={message.createdAt} className="ml-auto text-[12.5px] font-bold text-ink-500">
              {t.inbox.time(message.createdAt)}
            </time>
          </span>
          {!open && <span className="truncate text-[14px] text-ink-700">{message.preview}</span>}
        </span>
        <span
          className={cn(
            'shrink-0 self-start rounded-full px-2.5 text-[12px] leading-[22px] font-black whitespace-nowrap',
            read ? 'bg-tint-blue text-ink-700' : unreadStyle,
          )}
        >
          {readText}
        </span>
      </button>
      {open && (
        <div id={bodyId} className="mx-[18px] mb-4 rounded-input bg-tint-blue px-4 py-3.5 text-[15px] leading-[1.85] sm:ml-[39px]">
          {full.data ? <><Markdown text={full.data.text} /><MessageImages images={full.data.images} />{full.data.question && <QuestionSummary question={full.data.question} />}{full.data.notice && <InboxNotice notice={full.data.notice} />}</> : full.isError ? <ErrorAlert error={full.error} /> : t.common.loading}
        </div>
      )}
    </li>
  )
}
