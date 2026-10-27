import { ZapIcon } from 'lucide-react'
import { MessageImages } from '@/features/images/MessageImages'
import { UserChatBubble, AgentChatReply, SleepWakeLine } from './ChatBubble'
import { Fragment, useEffect, useLayoutEffect, useRef, type ReactNode } from 'react'
import type { Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { config } from '@/config'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import type { Turn } from './turns'
import { Cursor, Markdown } from './Markdown'
import { RunRecord } from './RunRecord'
import { FailureNote, RunStatusBadge, ToolList } from './RunStatus'
import { runStartedAt } from './time'
import { InboxReminder } from './InboxReminder'

function UserBubble({ message, run }: { message: Schemas['Message']; run: Schemas['Run'] }) {
  return <UserChatBubble images={<MessageImages images={message.images} />} text={message.text} time={t.chat.time(message.createdAt)} interrupted={run.whenBusy === 'interrupt'} wakeSleepId={run.wakeSleepId} />
}
// slept: how long the sleep lasted, when the turn before it is the one that went to sleep.
function PlatformReminder({ agentId, message, run, slept }: { agentId: string; message: Schemas['Message']; run: Schemas['Run']; slept?: number }) {
  if (message.origin === 'inbox_reminder') return <InboxReminder agentId={agentId} messageId={run.triggerInboxMessageId ?? message.run?.triggerInboxMessageId} at={message.createdAt} wakeSleepId={run.wakeSleepId ?? message.run?.wakeSleepId} />
  return <SleepWakeLine text={message.text} at={message.createdAt} sleepId={run.wakeSleepId ?? message.run?.wakeSleepId} slept={slept} />
}
// A deadline wake follows straight on from the turn that went to sleep: sleep starts only once that turn
// completes, and any new work in between would have woken it early. So the gap between them is the sleep.
function sleptBefore(previous: Turn | undefined, turn: Turn) {
  if (turn.user?.origin !== 'sleep_wake' || !previous || previous.sessionEpoch !== turn.sessionEpoch || previous.run.status !== 'completed' || !previous.run.endedAt) return undefined
  const ms = Date.parse(turn.user.createdAt) - Date.parse(previous.run.endedAt)
  return Number.isFinite(ms) && ms >= 0 ? ms : undefined
}

// Agent 回复不加卡片：上方一行是头像、名称、时间（进行中时为阶段）和结局印章，下面直接排正文，最后是运行记录。
// 未正常完成的回复保留已产生的文字，用次要文字色显示。
function ReplyBubble({ turn, agent, located }: { turn: Turn; agent: Pick<Schemas['Agent'], 'id' | 'name'>; located: boolean }) {
  const { run, reply } = turn
  const ended = run.status === 'completed' || run.status === 'failed' || run.status === 'interrupted'
  const streaming = reply?.live === true && run.status === 'running'
  return (
    <AgentChatReply agent={agent} metadata={<>{ended && <span>{t.chat.time(run.endedAt ?? runStartedAt(run))}</span>}<RunStatusBadge run={run} /></>}>
      <FailureNote run={run} />
      {reply && reply.text !== '' ? (
        <div className={cn(ended && run.status !== 'completed' && 'text-ink-500')}>
          <Markdown text={reply.text} cursor={streaming} />
        </div>
      ) : (
        streaming && (
          <p>
            <Cursor />
          </p>
        )
      )}
      <ToolList tools={run.tools} interrupted={run.status === 'interrupted' && run.endReason === 'user_interrupt'} />
      <RunRecord agentId={agent.id} run={run} defaultOpen={located || config.runRecordExpanded} />
    </AgentChatReply>
  )
}

// 上一轮被打断、这一轮是打断发出的消息时，两轮之间画一道粉色虚线。
function interruptedBy(previous: Turn, turn: Turn) {
  return turn.run.whenBusy === 'interrupt' && previous.run.status === 'interrupted' && previous.run.endReason === 'user_interrupt'
}

function InterruptDivider() {
  return (
    <div role="separator" aria-label={t.chat.interruptDivider} className="flex items-center gap-2.5">
      <span aria-hidden="true" className="h-0 flex-1 border-t-[1.5px] border-dashed border-riso-pink" />
      <span aria-hidden="true" className="inline-flex items-center gap-[5px] text-xs font-black text-pink-text">
        <ZapIcon className="size-[13px]" strokeWidth={2.4} />
        {t.chat.interruptDivider}
      </span>
      <span aria-hidden="true" className="h-0 flex-1 border-t-[1.5px] border-dashed border-riso-pink" />
    </div>
  )
}

// 设置滚动位置并记下实际结果（浏览器会把位置限制在可滚动的范围内）。
function scrollTo(el: HTMLElement, top: number, last: { current: number }) {
  el.scrollTop = top
  last.current = el.scrollTop
}

interface MessageListProps {
  // 当前 Agent，用于回复上方的头像与名称。
  agent: Pick<Schemas['Agent'], 'id' | 'name'>
  // 已开始的对话轮次（见 buildTurns）。排队中的消息显示在输入框上方的排队列表，不在这里。
  turns: Turn[]
  // 有排队中的消息时，对话流为空也不显示“还没有对话”。
  hasQueued?: boolean
  hasOlder: boolean
  loadingOlder: boolean
  // 整页重新获取进行中；此时不加载更早的消息。
  refreshing: boolean
  onLoadOlder: () => void
  // 定位到某条回复时：滚动到它、突出显示并展开它的运行记录；下方还有更新的消息时可以向下加载。
  targetRunId?: string | null
  hasNewer?: boolean
  loadingNewer?: boolean
  onLoadNewer?: () => void
  // 不属于对话轮次、按时间插进对话流的内容（Agent 发给你的消息与问题）。
  inserts?: TimelineInsert[]
}

export interface TimelineInsert {
  key: string
  at: string
  node: ReactNode
}

const turnAt = (turn: Turn) => Date.parse(turn.user?.createdAt ?? turn.run.queuedAt ?? '')

// 每条插入内容排在开始时间不晚于它的最后一轮之后（那一轮进行中发出的消息排在这一轮的回复后面）。
// 早于已加载的第一轮、而更早的历史还没加载时先不显示，免得排到顶部；定位时晚于已加载的最后一轮、而更新的历史还没加载时同样先不显示。
function placeInserts(turns: Turn[], inserts: TimelineInsert[], hasOlder: boolean, hasNewer: boolean) {
  const last = turns.length ? turnAt(turns[turns.length - 1]) : -Infinity
  const placed = new Map<number, TimelineInsert[]>()
  for (const item of [...inserts].sort((a, b) => Date.parse(a.at) - Date.parse(b.at))) {
    const at = Date.parse(item.at)
    let index = -1
    for (let i = 0; i < turns.length && turnAt(turns[i]) <= at; i++) index = i
    if ((index === -1 && hasOlder) || (hasNewer && at > last)) continue
    placed.set(index, [...(placed.get(index) ?? []), item])
  }
  return placed
}

export function MessageList({
  agent,
  turns,
  hasQueued = false,
  hasOlder,
  loadingOlder,
  refreshing,
  onLoadOlder,
  targetRunId = null,
  hasNewer = false,
  loadingNewer = false,
  onLoadNewer,
  inserts = [],
}: MessageListProps) {
  const container = useRef<HTMLDivElement>(null)
  const content = useRef<HTMLDivElement>(null)
  const firstKey = turns[0]?.key
  const lastKey = turns.at(-1)?.key
  // 进行中的回复是最后一轮，它的文字随增量变长。
  const lastText = turns.at(-1)?.reply?.text
  const wasLoadingOlder = useRef(false)
  // 开始加载更早的消息时的内容高度。之后内容可能因图片加载等原因变高，补偿位置要以开始时为准。
  const olderFrom = useRef<number | null>(null)
  // 定位时从目标回复开始看，不贴底；加载到最新、人滚到底部后才恢复贴底。
  const stickToBottom = useRef(targetRunId === null)
  const located = useRef(false)
  // 最近一次滚动后的位置。程序设置的位置也记下，用来区分人往上滚和程序滚动后迟到的滚动事件。
  const lastScrollTop = useRef(0)

  // 加载更早的消息后保持当前位置；在底部时跟随新内容滚动。
  useLayoutEffect(() => {
    const el = container.current
    if (!el) return
    if (olderFrom.current !== null && wasLoadingOlder.current && !loadingOlder) {
      // 加载结束：更早的消息插在顶部，按增加的高度补偿。
      scrollTo(el, el.scrollTop + el.scrollHeight - olderFrom.current, lastScrollTop)
      olderFrom.current = null
    } else if (stickToBottom.current) {
      scrollTo(el, el.scrollHeight, lastScrollTop)
    }
    wasLoadingOlder.current = loadingOlder
  }, [firstKey, lastKey, lastText, loadingOlder])

  // 定位：目标回复第一次出现时滚动到它，让它出现在对话区上部。
  useLayoutEffect(() => {
    const el = container.current
    if (!el || targetRunId === null || located.current) return
    const target = el.querySelector<HTMLElement>(`[data-run-id="${targetRunId}"]`)
    if (!target) return
    located.current = true
    scrollTo(el, el.scrollTop + target.getBoundingClientRect().top - el.getBoundingClientRect().top - 24, lastScrollTop)
  }, [targetRunId, turns])

  // 对话区或内容的尺寸变化不一定伴随上面的更新：顶部出现或去掉会话丢失的说明、图片加载完成、
  // 工具标记换行、状态文字变化。原本停在底部时继续贴底。
  useEffect(() => {
    const el = container.current
    const inner = content.current
    if (!el || !inner || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(() => {
      if (stickToBottom.current) scrollTo(el, el.scrollHeight, lastScrollTop)
    })
    observer.observe(el)
    observer.observe(inner)
    return () => observer.disconnect()
  }, [])

  const placed = placeInserts(turns, inserts, hasOlder, hasNewer)

  const loadOlder = () => {
    const el = container.current
    if (!el || !hasOlder || loadingOlder || refreshing) return
    olderFrom.current = el.scrollHeight
    onLoadOlder()
  }

  const loadNewer = () => {
    if (!hasNewer || loadingNewer || refreshing) return
    onLoadNewer?.()
  }

  const onScroll = () => {
    const el = container.current
    if (!el) return
    // 回到底部附近就贴底；只有人往上滚才离开底部。程序贴底后内容又一下子变高时，
    // 迟到的滚动事件里位置没有变小，不能据此取消贴底。下方还有更新的消息时不贴底，而是继续向下加载。
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80
    if (nearBottom && hasNewer) loadNewer()
    else if (nearBottom) stickToBottom.current = true
    else if (el.scrollTop < lastScrollTop.current) stickToBottom.current = false
    lastScrollTop.current = el.scrollTop
    if (el.scrollTop < 80) loadOlder()
  }

  return (
    // 滚动区本身是定位容器：里面绝对定位的元素（未读圆点、脚注标题的 sr-only 说明等）随内容滚动并被裁剪，
    // 否则它们按未滚动时的位置排在整页里，把页面撑高，输入框下面能拖出一大片空白。
    <div ref={container} onScroll={onScroll} className="relative h-full overflow-y-auto py-6" role="log" aria-label={t.chat.messagesLabel}>
      {/* 右侧留出气泡、代码块印刷投影的位置，免得被滚动区域裁掉。 */}
      <div ref={content} className="flex min-w-0 w-full flex-col gap-7 pr-2">
        {loadingOlder && <p className="text-center text-[12.5px] font-bold text-ink-500">{t.chat.loadingOlder}</p>}
        {hasOlder && !loadingOlder && (
          <Button variant="ghost" size="sm" className="self-center" disabled={refreshing} onClick={loadOlder}>
            {t.chat.loadOlder}
          </Button>
        )}
        {turns.length === 0 && !hasQueued && placed.size === 0 && <p className="text-center text-[15px] text-ink-500">{t.chat.noMessages}</p>}
        {placed.get(-1)?.map((item) => <Fragment key={item.key}>{item.node}</Fragment>)}
        {turns.map((turn, index) => {
          const isTarget = turn.run.id === targetRunId
          return (
            <Fragment key={turn.key}>
              {index > 0 && turns[index - 1].sessionEpoch !== turn.sessionEpoch && (
                <p className="text-center text-[13px] font-bold text-pink-text" role="separator">
                  <span aria-hidden="true">{t.chat.sessionDividerMark}</span> {t.chat.sessionDivider}{' '}
                  <span aria-hidden="true">{t.chat.sessionDividerMark}</span>
                </p>
              )}
              {index > 0 && interruptedBy(turns[index - 1], turn) && <InterruptDivider />}
              {/* 一轮对话：用户消息与回复。定位到的那一轮用浅黄底突出显示。 */}
              <div
                data-run-id={turn.run.id}
                data-located={isTarget || undefined}
                className={cn('flex flex-col gap-7', isTarget && 'rounded-panel bg-tint-yellow p-4')}
              >
                {isTarget && <span className="sr-only">{t.timeline.locate.target}</span>}
                {turn.user &&
                  ((turn.user.origin === 'inbox_reminder' || turn.user.origin === 'sleep_wake') ? (
                    <PlatformReminder agentId={agent.id} message={turn.user} run={turn.run} slept={sleptBefore(turns[index - 1], turn)} />
                  ) : (
                    <UserBubble message={turn.user} run={turn.run} />
                  ))}
                <ReplyBubble turn={turn} agent={agent} located={isTarget} />
              </div>
              {placed.get(index)?.map((item) => <Fragment key={item.key}>{item.node}</Fragment>)}
            </Fragment>
          )
        })}
        {loadingNewer && <p className="text-center text-[12.5px] font-bold text-ink-500">{t.timeline.locate.loadingNewer}</p>}
        {hasNewer && !loadingNewer && (
          <Button variant="ghost" size="sm" className="self-center" disabled={refreshing} onClick={loadNewer}>
            {t.timeline.locate.loadNewer}
          </Button>
        )}
      </div>
    </div>
  )
}
