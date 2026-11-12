import { MessageImages } from '@/features/images/MessageImages'
import { useId, useState } from 'react'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Schemas } from '@/api/client'
import { useAgentInboxMessage, useIdentityInboxMessage } from '@/api/inbox'
import { Markdown } from '@/features/chat/Markdown'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { DeletedMark } from './party'
import { MailIcon } from 'lucide-react'
import { avatarPair } from '@/lib/avatar'
import { partyDisplay } from './participants'
import { QuestionSummary } from './Question'
import { InboxNotice } from './InboxNotice'

// 只读收件箱中的一条消息属于谁：某个 Agent 的收件箱，或某个外部身份的收件箱。
export type InboxOwner = { kind: 'agent'; agentId: string } | { kind: 'identity'; identityId: string }

// 只读收件箱中的一条消息：点开后请求全文，查看不改变已读状态，也没有标记已读的操作。
// Agent 的收件箱只列收到的消息，显示发件人；外部身份的收件箱另有方向，显示对方。
// layout：card 是一张张卡片（外部身份的收件箱）；row 是纸张上的一行（Agent 的收件箱页签，改版通用规则第 9 条）。
export function ReadonlyMessage({ owner, message, layout = 'card' }: { owner: InboxOwner; message: Schemas['InboxMessageSummary']; layout?: 'card' | 'row' }) {
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
  const body = open && (
    <div id={bodyId} className={cn('mb-4 rounded-input bg-tint-blue px-4 py-3.5 text-[15px] leading-[1.85]', layout === 'row' ? 'mx-1' : 'mx-[18px] sm:ml-[39px]')}>
      {full.data ? <><Markdown text={full.data.text} /><MessageImages images={full.data.images} />{full.data.question && <QuestionSummary question={full.data.question} />}{full.data.notice && <InboxNotice notice={full.data.notice} />}</> : full.isError ? <ErrorAlert error={full.error} /> : <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
    </div>
  )

  if (layout === 'row') {
    return (
      <li className="border-b-[1.5px] border-dashed border-ink/25">
        <button
          type="button"
          aria-expanded={open}
          aria-controls={open ? bodyId : undefined}
          onClick={() => setOpen((value) => !value)}
          className="flex w-full flex-wrap items-center gap-x-[18px] gap-y-1.5 px-1 py-[15px] text-left text-ink outline-none focus-visible:shadow-focus"
        >
          {/* 发件方：头像、名称，下面是等宽的地址。 */}
          <span className="flex min-w-0 flex-[0_0_214px] items-center gap-2.5">
            <SenderMark party={party} />
            <span className="flex min-w-0 flex-col leading-[1.35]">
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="truncate text-[14.5px] font-black">{shown.title}</span>
                {shown.deleted && <DeletedMark label={shown.deleted} />}
              </span>
              {shown.address && <span className="truncate font-mono text-[11px] text-ink-500">{shown.address}</span>}
            </span>
          </span>
          {message.whenBusy === 'interrupt' && <span className="shrink-0 rounded-stamp bg-riso-pink px-1.5 text-[11.5px] font-black">{t.inbox.interrupt}</span>}
          {!open && <span className={cn('min-w-0 flex-[1_1_260px] truncate text-[14.5px]', read ? 'font-normal text-ink-700' : 'font-bold text-ink')}>{message.preview}</span>}
          <time dateTime={message.createdAt} className="ml-auto w-[92px] shrink-0 text-right font-mono text-[12px] text-ink-700">
            {t.inbox.time(message.createdAt)}
          </time>
          {/* 未读是略微倾斜的粉色描边印章；已读、已取消是浅蓝胶囊。 */}
          {read ? (
            <span data-read-state="read" className="shrink-0 rounded-full bg-tint-blue px-[9px] text-[12px] leading-[22px] font-bold whitespace-nowrap text-ink-700">
              {readText}
            </span>
          ) : (
            <span data-read-state="unread" className="riso-tilt inline-flex min-h-[22px] shrink-0 items-center rounded-stamp px-[9px] text-[12px] font-black tracking-[0.08em] whitespace-nowrap text-pink-text [--tilt-bg:transparent] [--tilt-shadow:inset_0_0_0_2px_var(--pink-text)] [--tilt:-4deg]">
              {readText}
            </span>
          )}
        </button>
        {body}
      </li>
    )
  }

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
      {body}
    </li>
  )
}

// 行里发件方的小头像：Agent 是两个叠印的小圆（同对话里的头像），平台、控制台与外部身份是一个藏青小方块，里面一个黄色信封。
function SenderMark({ party }: { party: Schemas['InboxParticipant'] }) {
  if (party.kind === 'agent' && party.id) {
    const [back, front] = avatarPair(party.id)
    return (
      <span aria-hidden="true" className={cn('relative block h-[18px] w-[26px] shrink-0', party.deleted && 'opacity-50')}>
        <span className="absolute top-0 left-0 size-[17px] rounded-full" style={{ background: back }} />
        <span className="absolute top-px left-[9px] size-[17px] rounded-full mix-blend-multiply" style={{ background: front }} />
      </span>
    )
  }
  return (
    <span aria-hidden="true" className={cn('flex h-[22px] w-[26px] shrink-0 items-center justify-center rounded-[6px] bg-ink text-riso-yellow', party.deleted && 'opacity-50')}>
      <MailIcon className="size-[13px]" strokeWidth={2.6} />
    </span>
  )
}
