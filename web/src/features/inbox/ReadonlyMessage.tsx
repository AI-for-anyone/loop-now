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
import { AgentAvatar } from '@/components/AgentAvatar'
import { partyDisplay } from './participants'
import { QuestionSummary } from './Question'
import { InboxNotice } from './InboxNotice'

// Whose read-only inbox a message is shown in: an Agent's inbox or an external identity's inbox.
export type InboxOwner = { kind: 'agent'; agentId: string } | { kind: 'identity'; identityId: string }

// One message in a read-only inbox: opening it fetches the full text; viewing doesn't change the read state, and there is no
// action to mark it read.
// An Agent's inbox only lists received messages and shows the sender; an external identity's inbox also has a direction and
// shows the other party.
// layout: card renders separate cards (external identity inbox); row is one row on the sheet (the Agent's Inbox tab, redesign
// general rule 9).
export function ReadonlyMessage({ owner, message, layout = 'card' }: { owner: InboxOwner; message: Schemas['InboxMessageSummary']; layout?: 'card' | 'row' }) {
  const [open, setOpen] = useState(false)
  const bodyId = useId()
  const agentFull = useAgentInboxMessage(owner.kind === 'agent' ? owner.agentId : '', message.id, open && owner.kind === 'agent')
  const identityFull = useIdentityInboxMessage(owner.kind === 'identity' ? owner.identityId : '', message.id, open && owner.kind === 'identity')
  const full = owner.kind === 'agent' ? agentFull : identityFull

  // In an external identity's inbox, messages whose recipient is this identity are received and the rest are sent; decided by
  // ID, because the address may be reused by a new identity.
  const received = owner.kind === 'agent' || message.to.id === owner.identityId
  const party = received ? message.from : message.to
  const shown = partyDisplay(party)
  // A message someone cancelled doesn't count as unread and is marked as cancelled.
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
  const unreadStyle = received ? 'bg-cta-bg text-cta-fg' : 'bg-tint-yellow text-ink'
  const body = open && (
    <div id={bodyId} className={cn('mb-4 rounded-input border border-hairline bg-surface-subtle px-4 py-3.5 text-[15px] leading-[1.85]', layout === 'row' ? 'mx-1' : 'mx-[18px] sm:ml-[39px]')}>
      {full.data ? <><Markdown text={full.data.text} /><MessageImages images={full.data.images} />{full.data.question && <QuestionSummary question={full.data.question} />}{full.data.notice && <InboxNotice notice={full.data.notice} />}</> : full.isError ? <ErrorAlert error={full.error} /> : <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
    </div>
  )

  if (layout === 'row') {
    return (
      <li className="border-b border-hairline">
        <button
          type="button"
          aria-expanded={open}
          aria-controls={open ? bodyId : undefined}
          onClick={() => setOpen((value) => !value)}
          className="flex w-full flex-wrap items-center gap-x-4 gap-y-1.5 px-1 py-2.5 text-left text-ink outline-none hover:bg-surface-hover/50 focus-visible:shadow-focus"
        >
          {owner.kind === 'identity' && (
            <span
              className={cn(
                'inline-flex min-w-[46px] shrink-0 items-center justify-center rounded-full px-2 text-[11.5px] leading-[20px] font-semibold',
                received
                  ? 'border border-hairline bg-surface-subtle text-ink-700'
                  : 'bg-tint-yellow text-pink-text',
              )}
            >
              {received ? t.identities.directions.received : t.identities.directions.sent}
            </span>
          )}
          {/* Sender / other party: avatar and name, with the monospace address below. */}
          <span className="flex min-w-0 flex-[0_0_188px] items-center gap-2.5">
            <SenderMark party={party} />
            <span className="flex min-w-0 flex-col leading-[1.3]">
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="truncate text-[14px] font-semibold">{shown.title}</span>
                {shown.deleted && <DeletedMark label={shown.deleted} />}
              </span>
              {shown.address && <span className="truncate font-mono text-[11px] text-ink-500">{shown.address}</span>}
            </span>
          </span>
          {message.whenBusy === 'interrupt' && <span className="shrink-0 rounded-stamp border border-pink-text/30 bg-tint-yellow px-1.5 text-[11.5px] font-semibold text-pink-text">{t.inbox.interrupt}</span>}
          {!open && <span className={cn('min-w-0 flex-[1_1_220px] truncate text-[14px]', read ? 'font-normal text-ink-700' : 'font-semibold text-ink')}>{message.preview}</span>}
          <time dateTime={message.createdAt} className="ml-auto w-[88px] shrink-0 text-right font-mono text-[12px] text-ink-700">
            {t.inbox.time(message.createdAt)}
          </time>
          {/* Unread is a clean amber tag in mono skin (tilted pink-outlined stamp in Plate skin); read and cancelled are subtle pills. */}
          {read ? (
            <span data-read-state="read" className="shrink-0 rounded-full border border-hairline bg-surface-subtle px-[9px] text-[12px] leading-[22px] font-medium whitespace-nowrap text-ink-700">
              {readText}
            </span>
          ) : (
            <span data-read-state="unread" className="riso-tilt inline-flex min-h-[22px] shrink-0 items-center rounded-stamp px-[9px] text-[12px] font-semibold whitespace-nowrap text-pink-text [--tilt-bg:var(--tint-yellow)] [--tilt-shadow:inset_0_0_0_1px_var(--hairline-strong)]">
              {readText}
            </span>
          )}
        </button>
        {body}
      </li>
    )
  }

  return (
    <li className="rounded-card border border-hairline bg-surface-control shadow-surface">
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
            className={cn('mt-[9px] size-[9px] shrink-0 rounded-full', !read && 'bg-pink-text')}
          />
        ) : (
          <span
            className={cn(
              'mt-0.5 inline-flex min-w-[46px] shrink-0 items-center justify-center rounded-full px-2 text-[12px] leading-[22px] font-semibold',
              received ? 'border border-hairline bg-surface-subtle text-ink-700' : 'bg-tint-yellow text-pink-text',
            )}
          >
            {received ? t.identities.directions.received : t.identities.directions.sent}
          </span>
        )}
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
            <span className={cn('text-[15px]', !read && received ? 'font-semibold' : 'font-medium')}>{shown.title}</span>
            {shown.deleted && <DeletedMark label={shown.deleted} />}
            {shown.address && <span className="font-mono text-[11.5px] text-ink-500">{shown.address}</span>}
            {message.whenBusy === 'interrupt' && (
              <span className="rounded-stamp border border-pink-text/30 bg-tint-yellow px-1.5 text-[11.5px] font-semibold text-pink-text">{t.inbox.interrupt}</span>
            )}
            <time dateTime={message.createdAt} className="ml-auto text-[12.5px] font-medium text-ink-500">
              {t.inbox.time(message.createdAt)}
            </time>
          </span>
          {!open && <span className="truncate text-[14px] text-ink-700">{message.preview}</span>}
        </span>
        <span
          className={cn(
            'shrink-0 self-start rounded-full px-2.5 text-[12px] leading-[22px] font-semibold whitespace-nowrap',
            read ? 'border border-hairline bg-surface-subtle text-ink-700' : unreadStyle,
          )}
        >
          {readText}
        </span>
      </button>
      {body}
    </li>
  )
}

// The sender's small avatar in a row: an Agent gets two circles (same as its avatar in chat); the platform,
// the console and external identities get a small square with an envelope inside.
function SenderMark({ party }: { party: Schemas['InboxParticipant'] }) {
  if (party.kind === 'agent' && party.id) {
    return <AgentAvatar id={party.id} size="sm" className={cn(party.deleted && 'opacity-50')} />
  }
  return (
    <span aria-hidden="true" className={cn('flex h-[22px] w-[26px] shrink-0 items-center justify-center rounded-[6px] border border-hairline bg-surface-subtle text-ink', party.deleted && 'opacity-50')}>
      <MailIcon className="size-[13px]" strokeWidth={2.4} />
    </span>
  )
}
