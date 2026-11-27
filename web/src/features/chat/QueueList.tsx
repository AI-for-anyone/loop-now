import { useId, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ChevronDownIcon, ChevronUpIcon, CornerDownRightIcon, MailIcon, PenLineIcon, XIcon, ZapIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { agentQueueAction, queueItemKey, type QueueOperation } from '@/api/agent-queue'
import { imageDraftsOf, messageImageURL } from '@/api/message-images'
import { keys, updateRunInMessages } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { ImageDraft } from '@/features/images/image-drafts'
import { partyDisplay } from '@/features/inbox/participants'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { conflictNotice, useQueueActions, useQueueNotice } from './queue-actions'

// One row of the queue list, i.e. one queued message. An interrupt row has a light pink fill and is marked as handled next; actions go
// at the end of the row, e.g. interrupt.
export interface QueueRow {
  key: string
  interrupt: boolean
  // Icon at the start of the row: sent by yourself, or an Inbox message. Task node rows use no icon, only the sender label.
  kind?: 'own' | 'inbox'
  // Sender label: an outlined “你” (You) for your own messages, otherwise the sender's name.
  sender?: { name: string; own: boolean }
  text: string
  images: Schemas['MessageImage'][]
  actions?: ReactNode
}

interface QueuePanelProps {
  // Total queued count: may be more than the messages listed (messages not loaded yet only count toward the total).
  count: number
  // Already in the server's processing order.
  rows: QueueRow[]
  // Optional collapsible behavior: when collapsed it takes a single line with the total, the first message's sender and summary,
  // and the expand control.
  collapse?: { expanded: boolean; onToggle: () => void }
  // Show the ordering hint ("Handled in order after the current reply") when expanded. Defaults to true when non-collapsible.
  showHint?: boolean
}

// The queue tray docked directly on top of the composer card: inset cleanly with mx-4 (matching rounded-2xl) and no bottom border so
// the two read as one cohesive piece; takes no space when nothing is queued, and a message moves into the conversation flow once it starts running.
export function QueuePanel({ count, rows, collapse, showHint = collapse === undefined }: QueuePanelProps) {
  const listId = useId()
  if (count === 0 && rows.length === 0) return null
  const collapsible = collapse !== undefined
  const collapsed = collapsible && !collapse.expanded
  const first = rows[0]
  return (
    <section
      aria-label={t.chat.queueList.region}
      className={cn(
        'mx-4 -mb-0.5 flex min-w-0 flex-col rounded-t-xl border border-b-0 border-hairline-strong bg-surface-subtle',
        collapsed ? 'py-0.5 pr-1.5 pl-2.5' : 'px-1.5 pt-1 pb-1.5',
      )}
    >
      <div className={cn('flex min-w-0 items-center gap-1.5 text-[11.5px] text-ink-500', collapsed ? 'min-h-7' : 'min-h-6 px-1.5')}>
        <h2 className="shrink-0 text-[11.5px] font-semibold text-ink-700">{t.chat.queueList.title(count)}</h2>
        {!collapsed && showHint && (
          <>
            <span aria-hidden="true">·</span>
            <span className="min-w-0 truncate">{t.chat.queueList.hint}</span>
          </>
        )}
        {collapsed && first && (
          <p data-queue-summary className="flex min-w-0 flex-1 items-center gap-1.5">
            {first.interrupt && (
              <>
                <InterruptMark />
                <span className="sr-only">{t.chat.queueList.next}</span>
              </>
            )}
            {first.sender && <SenderChip {...first.sender} />}
            <span className="min-w-0 truncate text-[12px] leading-[18px] text-ink-700" title={first.text || undefined}>
              {first.text || t.chat.queueList.imagesOnly(first.images.length)}
            </span>
          </p>
        )}
        {collapse && rows.length > 0 && (
          <button
            type="button"
            aria-expanded={collapse.expanded}
            aria-controls={listId}
            onClick={collapse.onToggle}
            className="ml-auto inline-flex h-6 shrink-0 cursor-pointer items-center gap-0.5 rounded-full px-1.5 text-xs font-semibold text-ink-700 hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus"
          >
            {collapse.expanded ? t.chat.queueList.collapse : t.chat.queueList.expand}
            {collapse.expanded ? <ChevronDownIcon aria-hidden="true" className="size-3" strokeWidth={2.6} /> : <ChevronUpIcon aria-hidden="true" className="size-3" strokeWidth={2.6} />}
          </button>
        )}
      </div>
      {rows.length > 0 && (
        <ol id={listId} hidden={collapsed} className={cn('flex flex-col overflow-y-auto', collapsible ? 'max-h-36' : 'max-h-48')}>
          {rows.map(({ key, ...row }) => (
            <QueueItem key={key} dense={collapsible} {...row} />
          ))}
        </ol>
      )}
    </section>
  )
}

function InterruptMark() {
  return <ZapIcon aria-hidden="true" className="size-3.5 shrink-0 text-pink-text" strokeWidth={2.2} />
}

function SenderChip({ name, own }: { name: string; own: boolean }) {
  return (
    <span className={cn('max-w-32 shrink-0 truncate rounded-stamp px-[5px] text-[11px] font-bold text-ink', own ? 'border border-ink/30 leading-[14px]' : 'bg-tint-blue leading-4')}>
      {name}
    </span>
  )
}

const kindIcons = {
  own: <CornerDownRightIcon aria-hidden="true" className="size-3.5 shrink-0 text-ink-500" strokeWidth={2} />,
  inbox: <MailIcon aria-hidden="true" className="size-3.5 shrink-0 text-riso-blue" strokeWidth={2} />,
}

// The text is truncated to one line by default; hovering shows the full text, and clicking expands or collapses it.
function QueueItem({ dense, interrupt, kind, sender, text, images, actions }: Omit<QueueRow, 'key'> & { dense: boolean }) {
  const [expanded, setExpanded] = useState(false)
  return (
    <li
      data-kind={interrupt ? 'interrupt' : 'queue'}
      className={cn('flex min-w-0 items-center rounded-[8px]', dense ? 'min-h-[30px] gap-1.5 pr-0.5 pl-1.5' : 'min-h-8 gap-2 pr-1 pl-1.5', interrupt && 'bg-tint-pink-2')}
    >
      {interrupt ? <InterruptMark /> : kind && kindIcons[kind]}
      {sender && <SenderChip {...sender} />}
      {text !== '' && (
        <button
          type="button"
          aria-expanded={expanded}
          title={expanded ? undefined : text}
          onClick={() => setExpanded((value) => !value)}
          className={cn(
            'min-w-0 flex-1 cursor-pointer py-1.5 text-left',
            dense ? 'text-[12.5px]/[18px]' : 'text-[13px]/[18px]',
            expanded ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : 'truncate',
          )}
        >
          {text}
        </button>
      )}
      {images.length > 0 && (
        <ul aria-label={t.images.history} className="flex shrink-0 gap-1">
          {images.map((image, imageIndex) => {
            const name = t.images.name(imageIndex + 1, image.width, image.height)
            return (
              <li key={image.id}>
                <img src={messageImageURL(image.id)} alt={name} title={name} loading="lazy" className="h-6 w-8 rounded-stamp border border-ink/20 object-cover" />
              </li>
            )
          })}
        </ul>
      )}
      {(interrupt || actions) && (
        <span className={cn('ml-auto flex shrink-0 items-center', dense ? 'gap-1.5' : 'gap-2')}>
          {interrupt && <span className="text-[11.5px] font-black text-pink-text">{t.chat.queueList.next}</span>}
          {actions}
        </span>
      )}
    </li>
  )
}

// The interrupt at the end of a row: promote to an interrupt, handled next. Disabled while the request is in flight, to prevent repeated
// clicks.
export function PromoteButton({ pending, disabled = false, label = t.chat.queueList.promote, onClick }: { pending: boolean; disabled?: boolean; label?: string; onClick: () => void }) {
  return (
    <button
      type="button"
      title={t.chat.queueList.promoteTitle}
      disabled={pending || disabled}
      onClick={onClick}
      className="inline-flex h-6 shrink-0 cursor-pointer items-center gap-[3px] rounded-full border border-hairline-strong bg-surface-control px-2 text-xs font-semibold whitespace-nowrap text-ink hover:bg-surface-hover focus-visible:shadow-focus disabled:cursor-default disabled:opacity-50"
    >
      <ZapIcon aria-hidden="true" className="size-3 text-pink-text" strokeWidth={2.6} />
      {pending ? t.chat.queueList.promoting : label}
    </button>
  )
}

const iconButtonClass = 'inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-[6px] text-ink-500 hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus disabled:cursor-default disabled:opacity-50'

// Edit (✎, only for your own messages) and cancel (×) at the end of a row: edit withdraws this message and puts its text and images back
// into the input box.
// Rows that can't be edited leave a blank of the same width, so interrupt and cancel line up in two fixed columns across rows.
export function RowButtons({ disabled, onEdit, onCancel, cancelLabel = t.chat.queueList.cancel }: { disabled: boolean; onEdit?: () => void; onCancel: () => void; cancelLabel?: string }) {
  return (
    <span className="flex shrink-0">
      {onEdit ? (
        <button type="button" aria-label={t.chat.queueList.edit} title={t.chat.queueList.editTitle} disabled={disabled} onClick={onEdit} className={iconButtonClass}>
          <PenLineIcon aria-hidden="true" className="size-3.5" strokeWidth={2} />
        </button>
      ) : (
        <span aria-hidden="true" data-edit-spacer className="size-6 shrink-0" />
      )}
      <button type="button" aria-label={cancelLabel} title={t.chat.queueList.cancelTitle} disabled={disabled} onClick={onCancel} className={iconButtonClass}>
        <XIcon aria-hidden="true" className="size-3.5" strokeWidth={2} />
      </button>
    </span>
  )
}

// A light one-line notice when a queued message can't be acted on: no dialog, doesn't block anything (see useQueueNotice).
export function QueueNotice({ text }: { text: string | null }) {
  if (!text) return null
  return (
    <p role="status" className="px-1 text-[12.5px] font-bold text-ink-700">
      {text}
    </p>
  )
}

// Light notices and errors above the tray; when there are none they take no space and the tray stays attached to the input box.
export function QueueMessages({ notice, error }: { notice: string | null; error: unknown }) {
  return (
    <div className="mb-2 grid gap-2 empty:hidden">
      <QueueNotice text={notice} />
      <ErrorAlert error={error} />
    </div>
  )
}

interface QueueListProps {
  agentId: string
  // The per-item queue list, already in the server's processing order.
  items: Schemas['QueueItem'][]
  // live.queueSeq when the action started: if a new list arrived before the receipt, don't overwrite it with the stale list in the
  // receipt.
  queueSeq: number
  // Auto-collapse into a 1-line summary tray when more than one message is queued, expandable on click.
  collapsible?: boolean
  onReceipt: (receipt: Schemas['AgentQueueActionReceipt'], seq: number) => void
  // Edit: after a successful withdrawal, hand the original text and images to the input box.
  onEdit: (text: string, images: ImageDraft[]) => void
}

// The queue list of an ordinary conversation, one row per message. Every row can be promoted to an interrupt (except interrupt rows)
// and cancelled; your own messages can also be edited.
// The order follows the list returned by the server and queue.updated.
export function QueueList({ agentId, items, queueSeq, collapsible = false, onReceipt, onEdit }: QueueListProps) {
  const client = useQueryClient()
  const actions = useQueueActions()
  const notice = useQueueNotice()
  const [error, setError] = useState<unknown>(null)
  const [expandedOverride, setExpandedOverride] = useState<boolean | null>(null)
  if (items.length <= 1 && expandedOverride !== null) {
    setExpandedOverride(null)
  }
  const canCollapse = collapsible && items.length > 1
  const expanded = expandedOverride ?? false

  const act = async (item: Schemas['QueueItem'], operation: QueueOperation, edit = false) => {
    const row = queueItemKey(item), seq = queueSeq
    setError(null)
    notice.clear()
    // Edit fetches the images first and then withdraws: if the images can't be read, the message stays in the queue and isn't lost.
    // Edit and cancel each use their own actionId.
    let drafts: ImageDraft[] = []
    const result = await actions.run(row, edit ? 'edit' : operation, async (actionId) => {
      if (edit && item.images.length > 0) drafts = await imageDraftsOf(item.images)
      return agentQueueAction(agentId, item, operation, actionId)
    })
    if (!result) return
    if (result.ok) {
      if (result.value.run) updateRunInMessages(client, agentId, result.value.run)
      onReceipt(result.value, seq)
      if (edit) {
        const original = result.value.item ?? item
        onEdit(original.text, drafts)
      }
      return
    }
    const text = conflictNotice(result.error, operation)
    if (!text) {
      setError(result.error)
      return
    }
    // No longer in the queue: show the notice, then reread so the queue list updates accordingly.
    notice.show(text)
    void client.invalidateQueries({ queryKey: keys.agentQueue(agentId) })
    void client.invalidateQueries({ queryKey: keys.agentStatus(agentId) })
    void client.invalidateQueries({ queryKey: keys.messages(agentId) })
  }

  const rows = items.map((item): QueueRow => {
    const row = queueItemKey(item)
    const own = item.kind === 'run' || item.from.kind === 'console'
    const interrupt = item.whenBusy === 'interrupt'
    const busy = actions.pending.has(row)
    return {
      key: row,
      interrupt,
      kind: own ? 'own' : 'inbox',
      sender: own ? undefined : { name: partyDisplay(item.from).title, own: false },
      text: item.text,
      images: item.images,
      actions: (
        <>
          {!interrupt && <PromoteButton pending={busy} label={actions.isUnknown(row, 'promote') ? t.chat.queueList.retryPromote : undefined} onClick={() => void act(item, 'promote')} />}
          <RowButtons
            disabled={busy}
            onEdit={own ? () => void act(item, 'cancel', true) : undefined}
            onCancel={() => void act(item, 'cancel')}
            cancelLabel={actions.isUnknown(row, 'cancel') ? t.chat.queueList.retryCancel : undefined}
          />
        </>
      ),
    }
  })
  return (
    <>
      <QueueMessages notice={notice.text} error={error} />
      <QueuePanel
        count={items.length}
        rows={rows}
        showHint
        collapse={canCollapse ? { expanded, onToggle: () => setExpandedOverride(!expanded) } : undefined}
      />
    </>
  )
}
