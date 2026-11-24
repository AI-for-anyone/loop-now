import { ScissorsIcon, ZapIcon } from 'lucide-react'
import { CutLine } from '@/components/CutLine'
import { MessageImages } from '@/features/images/MessageImages'
import { UserChatBubble, AgentChatReply, SleepWakeLine } from './ChatBubble'
import { Fragment, useEffect, useLayoutEffect, useRef, type ReactNode } from 'react'
import type { Schemas } from '@/api/client'
import { HollowNumber } from '@/components/NumberedSection'
import { Stub } from '@/components/Sheet'
import { Button } from '@/components/ui/button'
import { config } from '@/config'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { handoffBefore, modelChangeBefore, type Turn } from './turns'
import { HandoffRow, ModelChangeLine } from './HandoffRow'
import { Cursor, Markdown } from './Markdown'
import { RunRecord } from './RunRecord'
import { FailureNote, RunStatusBadge, ToolList } from './RunStatus'
import { runStartedAt } from './time'
import { InboxReminder } from './InboxReminder'
import { MessageRuler } from './MessageRuler'
import { turnPreview, useVisibleTurns } from './ruler'

function UserBubble({ message, run }: { message: Schemas['Message']; run: Schemas['Run'] }) {
  return <UserChatBubble wide images={<MessageImages images={message.images} />} text={message.text} time={t.chat.time(message.createdAt)} interrupted={run.whenBusy === 'interrupt'} wakeSleepId={run.wakeSleepId} />
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

// Agent replies have no card: a top line with the avatar, name, time (the phase while in progress) and the outcome stamp, the body
// directly below, and the run record last.
// A reply that didn't complete normally keeps the text it produced, shown in the secondary text color.
function ReplyBubble({ turn, agent, located }: { turn: Turn; agent: Pick<Schemas['Agent'], 'id' | 'name'>; located: boolean }) {
  const { run, reply } = turn
  const ended = run.status === 'completed' || run.status === 'failed' || run.status === 'interrupted'
  const streaming = reply?.live === true && run.status === 'running'
  // /pi: an extension command handled directly by Pi and never passed to the model: the turn ends normally with no assistant reply.
  // The command's output is the messages the extension sent via notify, shown in order where the reply would be; only when there
  // are no messages (including old runs that finished before the upgrade) is a single line
  // "命令已执行，没有回复" (Command ran; no reply) shown.
  const command = run.piCommand?.handled === true && run.status === 'completed' && (!reply || reply.text === '') ? run.piCommand : null
  return (
    <AgentChatReply agent={agent} metadata={<>{ended && <span>{t.chat.time(run.endedAt ?? runStartedAt(run))}</span>}<RunStatusBadge run={run} /></>}>
      <FailureNote run={run} />
      {reply && reply.text !== '' ? (
        <div className={cn(ended && run.status !== 'completed' && 'text-ink-500')}>
          <Markdown text={reply.text} cursor={streaming} />
        </div>
      ) : command ? (
        (command.notices ?? []).length > 0 ? (
          <PiCommandNotices notices={command.notices} truncated={command.truncated} />
        ) : (
          <p data-pi-command-handled className="text-[14.5px] text-ink-500">{t.chat.piCommandHandled}</p>
        )
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

type Notice = NonNullable<Schemas['Run']['piCommand']>['notices'][number]
const noticeTone: Record<Notice['level'], string> = {
  info: 'bg-ink/[0.04] text-ink',
  warning: 'bg-tint-yellow text-ink',
  error: 'bg-tint-red text-red-text',
}

// Output of an extension command: one block of plain text per notify, monospaced, keeping line breaks and spaces (the output often
// aligns with indentation); warnings and errors get their own background color and spell out the level, not relying on color alone.
function PiCommandNotices({ notices, truncated }: { notices: Notice[]; truncated: boolean }) {
  return (
    <div data-pi-command-notices className="grid min-w-0 gap-1.5">
      {notices.map((notice, index) => (
        <div key={index} className={cn('grid min-w-0 gap-0.5 rounded-input px-3 py-2', noticeTone[notice.level])}>
          {notice.level !== 'info' && <span className="text-[11.5px] font-black">{t.chat.noticeLevel[notice.level]}</span>}
          <pre className="m-0 min-w-0 font-mono text-[13px] leading-[1.6] whitespace-pre-wrap [overflow-wrap:anywhere]">{notice.message}</pre>
        </div>
      ))}
      {truncated && <p className="text-[12.5px] text-ink-500">{t.chat.piCommandTruncated}</p>}
    </div>
  )
}

// When the previous turn was interrupted and this turn is the message sent as the interrupt, draw a cut line between the two turns.
function interruptedBy(previous: Turn, turn: Turn) {
  return turn.run.whenBusy === 'interrupt' && previous.run.status === 'interrupted' && previous.run.endReason === 'user_interrupt'
}

// Dividers between turns (CutLine, components/CutLine.tsx): “会话已切换，页面历史保留” (Session switched; page history is kept)
// with scissors where the session changes, and “已打断，先处理这条” (Interrupted; handling this first) with a lightning bolt.
// Where who runs it changed: the handover to another runtime, or a quieter line for a change of model on the same runtime.
function TurnChange({ previous, turn }: { previous: Turn | undefined; turn: Turn }) {
  const handoff = handoffBefore(previous, turn)
  if (handoff) return <HandoffRow handoff={handoff} />
  const change = modelChangeBefore(previous, turn)
  return change ? <ModelChangeLine from={change.from} to={change.to} /> : null
}

// Set the scroll position and record the actual result (the browser clamps the position to the scrollable range).
function scrollTo(el: HTMLElement, top: number, last: { current: number }) {
  el.scrollTop = top
  last.current = el.scrollTop
}

interface MessageListProps {
  // The current Agent, for the avatar and name above replies.
  agent: Pick<Schemas['Agent'], 'id' | 'name'>
  // Conversation turns that have started (see buildTurns). Queued messages show in the queue list above the input box, not here.
  turns: Turn[]
  // When there are queued messages, “还没有对话” (No conversation yet) isn't shown even if the conversation flow is empty.
  hasQueued?: boolean
  hasOlder: boolean
  loadingOlder: boolean
  // A full refetch is in progress; earlier messages aren't loaded meanwhile.
  refreshing: boolean
  onLoadOlder: () => void
  // When locating a reply: scroll to it, highlight it and expand its run record; when there are newer messages below, they can be
  // loaded downward.
  targetRunId?: string | null
  hasNewer?: boolean
  loadingNewer?: boolean
  onLoadNewer?: () => void
  // Content that isn't part of a conversation turn and is placed into the conversation flow by time (messages and questions the
  // Agent sent you).
  inserts?: TimelineInsert[]
  // Turn numbering (contract MessagePage): how many turns come before the first loaded turn, taken from turnsBefore of the earliest
  // loaded page; numbering here continues from it.
  // Unknown, and not passed, while the history hasn't been fetched: then no numbers are shown, neither on the turns nor on the ruler,
  // only the ticks.
  turnsBefore?: number
  // The total turn count at the bottom of the ruler, computed by the chat page; not passed when unknown.
  turnsTotal?: number
}

// The number column is 36px wide: two digits at 26px, shrinking step by step from three digits on, so even five digits fit in the
// column without pushing into the body on the right.
const numberSize = (number: number) => (number >= 10000 ? 'text-[12px]' : number >= 1000 ? 'text-[15px]' : number >= 100 ? 'text-[20px]' : 'text-[26px]')

// The hollow number to the left of each turn: one size smaller than section numbers (26px, 1.3px stroke), in a 36px column, 12px from
// the body.
const turnNumberClass = 'w-9 pt-[22px] tracking-[-0.03em] [-webkit-text-stroke:1.3px_var(--riso-blue)]'

// Content placed into the conversation flow lines up with the body of the turns: on wide screens it leaves room for the number column.
function Insert({ children }: { children: ReactNode }) {
  return <div className="flex min-w-0 flex-col nav:pl-12">{children}</div>
}

export interface TimelineInsert {
  key: string
  at: string
  node: ReactNode
}

const turnAt = (turn: Turn) => Date.parse(turn.user?.createdAt ?? turn.run.queuedAt ?? '')

// Each inserted item goes after the last turn that started no later than it (a message sent while that turn was in progress goes after
// that turn's reply).
// Items earlier than the first loaded turn are held back while older history hasn't been loaded yet, so they don't pile up at the top;
// likewise, while locating, items later than the last loaded turn are held back while newer history hasn't been loaded yet.
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
  turnsBefore,
  turnsTotal,
}: MessageListProps) {
  const container = useRef<HTMLDivElement>(null)
  const content = useRef<HTMLDivElement>(null)
  const firstKey = turns[0]?.key
  const lastKey = turns.at(-1)?.key
  // The reply in progress is the last turn, and its text grows with each delta.
  const lastText = turns.at(-1)?.reply?.text
  const wasLoadingOlder = useRef(false)
  // Content height when loading earlier messages started. The content may grow afterwards (images loading, etc.), so the position
  // compensation must be based on the height at the start.
  const olderFrom = useRef<number | null>(null)
  // While locating, start viewing from the target reply and don't stick to the bottom; sticking to the bottom resumes only once
  // everything up to the latest is loaded and the person has scrolled to the bottom.
  const stickToBottom = useRef(targetRunId === null)
  const located = useRef(false)
  // The position after the most recent scroll. Positions set by code are recorded too, to tell a person scrolling up apart from a late
  // scroll event after a programmatic scroll.
  const lastScrollTop = useRef(0)
  // The position reached by a jump from the ruler: scroll events caused by that jump don't load earlier or newer messages (the ruler
  // itself never triggers loading).
  const rulerJump = useRef<number | null>(null)
  const visibleTurns = useVisibleTurns(container, content, turns.map((turn) => turn.run.id).join(','))

  // Keep the current position after loading earlier messages; when at the bottom, follow new content.
  useLayoutEffect(() => {
    const el = container.current
    if (!el) return
    if (olderFrom.current !== null && wasLoadingOlder.current && !loadingOlder) {
      // Loading finished: the earlier messages were inserted at the top, so compensate by the added height.
      scrollTo(el, el.scrollTop + el.scrollHeight - olderFrom.current, lastScrollTop)
      olderFrom.current = null
    } else if (stickToBottom.current) {
      scrollTo(el, el.scrollHeight, lastScrollTop)
    }
    wasLoadingOlder.current = loadingOlder
  }, [firstKey, lastKey, lastText, loadingOlder])

  // Locating: when the target reply first appears, scroll to it so it shows in the upper part of the conversation area. The target can
  // also be content placed into the conversation flow (a located manual compaction), which appears a bit later.
  useLayoutEffect(() => {
    const el = container.current
    if (!el || targetRunId === null || located.current) return
    const target = el.querySelector<HTMLElement>(`[data-run-id="${targetRunId}"]`)
    if (!target) return
    located.current = true
    scrollTo(el, el.scrollTop + target.getBoundingClientRect().top - el.getBoundingClientRect().top - 24, lastScrollTop)
  }, [targetRunId, turns, inserts.length])

  // Size changes of the conversation area or its content don't always come with the updates above: the missing-session notice
  // appearing or going away at the top, images finishing loading, tool marks wrapping, status text changing.
  // If it was at the bottom, keep sticking to the bottom.
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
  // Turn numbering: the number of the first turn; null when unknown.
  const firstNumber = turnsBefore === undefined ? null : turnsBefore + 1

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
    const jumped = rulerJump.current !== null && el.scrollTop === rulerJump.current
    rulerJump.current = null
    // Back near the bottom means stick to the bottom; only a person scrolling up leaves the bottom. When the content suddenly grows
    // right after code stuck to the bottom, the position in the late scroll event hasn't decreased, so that can't be a reason to stop
    // sticking. When there are newer messages below, don't stick to the bottom; keep loading downward instead.
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80
    if (nearBottom && hasNewer) {
      if (!jumped) loadNewer()
    } else if (nearBottom) stickToBottom.current = true
    else if (el.scrollTop < lastScrollTop.current) stickToBottom.current = false
    lastScrollTop.current = el.scrollTop
    if (el.scrollTop < 80 && !jumped) loadOlder()
  }

  // The ruler jumps to a turn: bring the start of that turn to the upper part of the conversation area (the same position as locating)
  // and stop sticking to the bottom, so new content doesn't pull the position back to the bottom.
  // Jump instantly, without smooth scrolling.
  const jumpTo = (runId: string) => {
    const el = container.current
    const target = Array.from(content.current?.children ?? []).find((child) => child instanceof HTMLElement && child.dataset.runId === runId)
    if (!el || !target) return
    stickToBottom.current = false
    scrollTo(el, el.scrollTop + target.getBoundingClientRect().top - el.getBoundingClientRect().top - 24, lastScrollTop)
    rulerJump.current = lastScrollTop.current
  }

  return (
    // To the right of the conversation area is the ruler in the stub; it doesn't scroll with the messages and replaces the native
    // scrollbar (scrolling by wheel, trackpad and keyboard is unchanged);
    // with only one turn or no conversation the stub has no ticks, and on narrow screens where the sidebar folds into a drawer the stub
    // isn't shown: both cases keep the native scrollbar.
    <div className="flex h-full min-h-0">
      {/* The scroll area itself is the positioning container: absolutely positioned elements inside it (unread dots, the sr-only
          caption of the footnote heading, etc.) scroll with the content and get clipped; otherwise they'd be laid out in the whole
          page at their unscrolled positions, making the page taller, so a large blank area could be dragged out below the input box. */}
      <div
        ref={container}
        onScroll={onScroll}
        className={cn(
          // On wide screens the left padding is a bit narrower (the number column got narrower too), moving the body left; narrow
          // screens are unchanged.
          'relative h-full min-w-0 flex-1 overflow-y-auto pt-[26px] pr-[clamp(14px,3vw,44px)] pb-[22px] pl-[clamp(12px,2vw,28px)] nav:pl-[clamp(12px,1.4vw,20px)]',
          // With a ruler (at least two turns, wide screen) hide the native scrollbar; without ticks keep it, so you can still tell how far
          // you've scrolled through one very long reply.
          turns.length > 1 && 'nav:no-scrollbar',
        )}
        role="log"
        aria-label={t.chat.messagesLabel}
      >
        {/* The right padding also leaves room for the print shadows of bubbles and code blocks, so the scroll area doesn't clip them. */}
        <div ref={content} className="flex min-w-0 w-full flex-col gap-7">
          {loadingOlder && <p className="text-center text-[12.5px] font-bold text-ink-500">{t.chat.loadingOlder}</p>}
          {hasOlder && !loadingOlder && (
            <Button variant="ghost" size="sm" className="self-center" disabled={refreshing} onClick={loadOlder}>
              {t.chat.loadOlder}
            </Button>
          )}
          {turns.length === 0 && !hasQueued && placed.size === 0 && <p className="text-center text-[15px] text-ink-500">{t.chat.noMessages}</p>}
          {placed.get(-1)?.map((item) => <Insert key={item.key}>{item.node}</Insert>)}
          {turns.map((turn, index) => {
            const isTarget = turn.run.id === targetRunId
            return (
              <Fragment key={turn.key}>
                {index > 0 && turns[index - 1].sessionEpoch !== turn.sessionEpoch && <CutLine icon={ScissorsIcon} label={t.chat.sessionDivider} />}
                {index > 0 && interruptedBy(turns[index - 1], turn) && <CutLine icon={ZapIcon} label={t.chat.interruptDivider} />}
                <TurnChange previous={turns[index - 1]} turn={turn} />
                {/* One conversation turn: in the blank space on the left is the turn's hollow number (matching the number on the
                    ruler; left empty when the number is unknown), and on the right the user message and the reply.
                    The located turn is highlighted with a light yellow fill. */}
                <div data-run-id={turn.run.id} data-located={isTarget || undefined} className={cn('flex gap-3', isTarget && 'rounded-panel bg-tint-yellow p-4')}>
                  <HollowNumber data-turn-number className={cn(turnNumberClass, numberSize(firstNumber === null ? 0 : firstNumber + index))}>
                    {firstNumber === null ? '' : firstNumber + index}
                  </HollowNumber>
                  <div className="flex min-w-0 flex-1 flex-col gap-[22px]">
                    {isTarget && <span className="sr-only">{t.timeline.locate.target}</span>}
                    {turn.user &&
                      ((turn.user.origin === 'inbox_reminder' || turn.user.origin === 'sleep_wake') ? (
                        <PlatformReminder agentId={agent.id} message={turn.user} run={turn.run} slept={sleptBefore(turns[index - 1], turn)} />
                      ) : (
                        <UserBubble message={turn.user} run={turn.run} />
                      ))}
                    {/* The reply body follows the conversation column, up to 1100px on wide screens: no wasted space in a wide window,
                        and code blocks, tables and images widen with it. Narrow screens keep 760px. */}
                    <div className="max-w-[760px] min-w-0 nav:max-w-[1100px]">
                      <ReplyBubble turn={turn} agent={agent} located={isTarget} />
                    </div>
                  </div>
                </div>
                {placed.get(index)?.map((item) => <Insert key={item.key}>{item.node}</Insert>)}
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
      {turns.length > 1 ? (
        <MessageRuler
          turns={turns.map((turn) => ({ key: turn.run.id, label: turnPreview(turn) }))}
          visible={visibleTurns}
          onJump={jumpTo}
          firstNumber={firstNumber}
          total={firstNumber === null ? null : (turnsTotal ?? null)}
        />
      ) : (
        <Stub data-turn-stub aria-hidden="true" label={t.chat.ruler.title} className="w-[74px]" />
      )}
    </div>
  )
}
