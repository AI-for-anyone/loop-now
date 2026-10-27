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
import { imageText } from '@/i18n/images'
import { cn } from '@/lib/utils'
import { conflictNotice, useQueueActions, useQueueNotice } from './queue-actions'

// 排队列表的一行，即一条排队消息。打断的行底色浅粉、标明下一条处理；actions 放在行尾，例如打断。
export interface QueueRow {
  key: string
  interrupt: boolean
  // 行首图标：自己发的或 Inbox 消息。任务节点的行不用图标，只用发送方标签。
  kind?: 'own' | 'inbox'
  // 发送方标签：自己发的为描边的“你”，其余为发送方名称。
  sender?: { name: string; own: boolean }
  text: string
  images: Schemas['MessageImage'][]
  actions?: ReactNode
}

interface QueuePanelProps {
  // 排队总数：可能多于已列出的消息（消息还没有加载到的只计入总数）。
  count: number
  // 已按服务端的处理顺序排列。
  rows: QueueRow[]
  // 任务节点面板里的排队列表：行更紧凑，可以收起，收起时只占一行：总数、第一条的发送方与摘要、展开控件。
  // 展开状态由调用方保存。不传为普通对话的排队列表，总是展开。
  collapse?: { expanded: boolean; onToggle: () => void }
}

// 贴在输入框顶上的排队托盘：比输入框收进一些，没有底边，与输入框连成一体；没有排队时不占位置，消息开始执行后进入对话流。
export function QueuePanel({ count, rows, collapse }: QueuePanelProps) {
  const listId = useId()
  if (count === 0 && rows.length === 0) return null
  const node = collapse !== undefined
  const collapsed = node && !collapse.expanded
  const first = rows[0]
  return (
    <section
      aria-label={t.chat.queueList.region}
      className={cn(
        '-mb-0.5 flex min-w-0 flex-col border-[1.5px] border-b-0 border-ink/28 bg-paper-raised',
        node ? cn('mx-[18px] rounded-t-[12px] pt-0.5', collapsed ? 'pr-1 pb-[3px] pl-2.5' : 'px-1 pb-[5px]') : 'mx-[26px] rounded-t-[14px] px-1.5 pt-[3px] pb-1.5',
      )}
    >
      <div className={cn('flex min-w-0 items-center gap-1.5 text-[11.5px] text-ink-500', node ? (collapsed ? 'min-h-7' : 'min-h-[26px] pl-1.5') : 'h-[22px] px-1.5')}>
        <h2 className="shrink-0 text-[11.5px] font-black text-ink-700">{t.chat.queueList.title(count)}</h2>
        {!node && (
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
            <span className="min-w-0 truncate text-[12.5px] leading-[18px] text-ink-700" title={first.text || undefined}>
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
            className="ml-auto inline-flex h-6 shrink-0 cursor-pointer items-center gap-0.5 rounded-full px-1.5 text-xs font-bold text-riso-blue hover:bg-tint-blue focus-visible:shadow-focus"
          >
            {collapse.expanded ? t.chat.queueList.collapse : t.chat.queueList.expand}
            {collapse.expanded ? <ChevronDownIcon aria-hidden="true" className="size-3" strokeWidth={2.6} /> : <ChevronUpIcon aria-hidden="true" className="size-3" strokeWidth={2.6} />}
          </button>
        )}
      </div>
      {rows.length > 0 && (
        <ol id={listId} hidden={collapsed} className={cn('flex flex-col overflow-y-auto', node ? 'max-h-32' : 'max-h-48')}>
          {rows.map(({ key, ...row }) => (
            <QueueItem key={key} dense={node} {...row} />
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

// 正文默认截成一行，悬停显示全文，点击展开或收起。
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
        <ul aria-label={imageText.history} className="flex shrink-0 gap-1">
          {images.map((image, imageIndex) => {
            const name = imageText.name(imageIndex + 1, image.width, image.height)
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

// 行尾的打断：转为打断，排到下一条处理。请求进行中禁用，防止连点。
export function PromoteButton({ pending, disabled = false, label = t.chat.queueList.promote, onClick }: { pending: boolean; disabled?: boolean; label?: string; onClick: () => void }) {
  return (
    <button
      type="button"
      title={t.chat.queueList.promoteTitle}
      disabled={pending || disabled}
      onClick={onClick}
      className="inline-flex h-6 shrink-0 cursor-pointer items-center gap-[3px] rounded-full border border-ink/35 bg-white px-2 text-xs font-bold whitespace-nowrap text-ink hover:bg-tint-pink-2 focus-visible:shadow-focus disabled:cursor-default disabled:opacity-50"
    >
      <ZapIcon aria-hidden="true" className="size-3 text-pink-text" strokeWidth={2.6} />
      {pending ? t.chat.queueList.promoting : label}
    </button>
  )
}

const iconButtonClass = 'inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-[6px] text-ink-500 hover:bg-tint-blue hover:text-ink focus-visible:shadow-focus disabled:cursor-default disabled:opacity-50'

// 行尾的编辑（✎，只有自己发的）与取消（×）：编辑撤回这条消息，把文字与图片放回输入框。
// 不能编辑的行留出同样宽的空位，各行的打断与取消对齐成固定的两列。
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

// 无法操作排队消息时的一行轻提示：不弹窗、不挡操作（见 useQueueNotice）。
export function QueueNotice({ text }: { text: string | null }) {
  if (!text) return null
  return (
    <p role="status" className="px-1 text-[12.5px] font-bold text-ink-700">
      {text}
    </p>
  )
}

// 托盘上方的轻提示与错误；都没有时不占位置，托盘仍贴着输入框。
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
  // 逐条排队列表，已按服务端的处理顺序排列。
  items: Schemas['QueueItem'][]
  // 动作开始时的 live.queueSeq：回执到达前又收到过新列表时，不用回执里的旧列表覆盖。
  queueSeq: number
  onReceipt: (receipt: Schemas['AgentQueueActionReceipt'], seq: number) => void
  // 编辑：撤回成功后把原文与图片交给输入框。
  onEdit: (text: string, images: ImageDraft[]) => void
}

// 普通对话的排队列表，每条消息一行。每一行都能转为打断（打断的行除外）和取消；自己发的还能编辑。
// 顺序以服务端返回的列表与 queue.updated 为准。
export function QueueList({ agentId, items, queueSeq, onReceipt, onEdit }: QueueListProps) {
  const client = useQueryClient()
  const actions = useQueueActions()
  const notice = useQueueNotice()
  const [error, setError] = useState<unknown>(null)

  const act = async (item: Schemas['QueueItem'], operation: QueueOperation, edit = false) => {
    const row = queueItemKey(item), seq = queueSeq
    setError(null)
    notice.clear()
    // 编辑先取回图片再撤回：图片读不出来时消息留在队列里，不会丢。编辑与取消各用各的 actionId。
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
    // 已不在队列：提示后重新读取，排队列表随之更新。
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
      <QueuePanel count={items.length} rows={rows} />
    </>
  )
}
