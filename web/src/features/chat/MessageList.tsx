import { ArrowDownIcon, HistoryIcon, ScissorsIcon, ZapIcon } from 'lucide-react'
import { CutLine } from '@/components/CutLine'
import { MessageImages } from '@/features/images/MessageImages'
import { UserChatBubble, AgentChatReply, SleepWakeLine } from './ChatBubble'
import { Fragment, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import type { Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { config } from '@/config'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { handoffBefore, modelChangeBefore, type Turn } from './turns'
import { HandoffRow, ModelChangeLine } from './HandoffRow'
import { Cursor, Markdown } from './Markdown'
import { RunRecord } from './RunRecord'
import { PermissionRequestCard } from '@/features/permissions/PermissionRequestCard'
import { FailureNote, RunStatusBadge, ToolList } from './RunStatus'
import { unfinishedTool } from './tool-state'
import { runStartedAt } from './time'
import { InboxReminder } from './InboxReminder'
import { BackgroundFollowUpLine } from './BackgroundFollowUp'
import { BackgroundToolCards } from './BackgroundToolCard'
import { useRuntimeLookup } from '@/lib/runtime'
import { MessageRuler } from './MessageRuler'
import { turnPreview, useVisibleTurns } from './ruler'
import { getChatScrollTop, setChatScrollTop } from './draft-store'

function UserBubble({ message, run }: { message: Schemas['Message']; run: Schemas['Run'] }) {
  return <UserChatBubble wide images={<MessageImages images={message.images} />} text={message.text} time={t.chat.time(message.createdAt)} interrupted={run.whenBusy === 'interrupt'} wakeSleepId={run.wakeSleepId} />
}
// slept: how long the sleep lasted, when the turn before it is the one that went to sleep.
function PlatformReminder({ agentId, message, run, slept }: { agentId: string; message: Schemas['Message']; run: Schemas['Run']; slept?: number }) {
  if (message.origin === 'inbox_reminder') return <InboxReminder agentId={agentId} messageId={run.triggerInboxMessageId ?? message.run?.triggerInboxMessageId} at={message.createdAt} wakeSleepId={run.wakeSleepId ?? message.run?.wakeSleepId} />
  return <SleepWakeLine text={message.text} at={message.createdAt} sleepId={run.wakeSleepId ?? message.run?.wakeSleepId} slept={slept} />
}
// A reply the CLI wrote by itself after background work ended has no person's message above it: one line says which task it follows,
// named by the runtime that ran it.
function BackgroundFollowUp({ message, run, agent }: { message: Schemas['Message']; run: Schemas['Run']; agent: Partial<Pick<Schemas['Agent'], 'runtime'>> }) {
  const runtimes = useRuntimeLookup()
  const tasks = run.triggerBackgroundTasks ?? message.run?.triggerBackgroundTasks ?? []
  const runtime = tasks[0]?.runtime ?? run.selection?.runtime ?? agent.runtime
  return <BackgroundFollowUpLine tasks={tasks} runtimeName={runtime ? runtimes.name(runtime) : ''} at={message.createdAt} />
}
// A deadline wake follows straight on from the turn that went to sleep: sleep starts only once that turn
// completes, and any new work in between would have woken it early. So the gap between them is the sleep.
function sleptBefore(previous: Turn | undefined, turn: Turn) {
  if (turn.user?.origin !== 'sleep_wake' || !previous || previous.sessionEpoch !== turn.sessionEpoch || previous.run.status !== 'completed' || !previous.run.endedAt) return undefined
  const ms = Date.parse(turn.user.createdAt) - Date.parse(previous.run.endedAt)
  return Number.isFinite(ms) && ms >= 0 ? ms : undefined
}

// Agent replies have no card: a top line with the avatar, name, time (the phase while in progress), outcome stamp and inline turn tag,
// the body directly below, and the run record last.
// A reply that didn't complete normally keeps the text it produced, shown in the secondary text color.
function ReplyBubble({
  turn,
  agent,
  located,
  turnNumber,
  requests = [],
}: {
  turn: Turn
  agent: Pick<Schemas['Agent'], 'id' | 'name'> & Partial<Pick<Schemas['Agent'], 'runtime'>>
  located: boolean
  turnNumber: number | null
  requests?: Schemas['PermissionRequest'][]
}) {
  const { run, reply } = turn
  const ended = run.status === 'completed' || run.status === 'failed' || run.status === 'interrupted'
  const streaming = reply?.live === true && run.status === 'running'
  // /pi: an extension command handled directly by Pi and never passed to the model: the turn ends normally with no assistant reply.
  // The command's output is the messages the extension sent via notify, shown in order where the reply would be; only when there
  // are no messages (including old runs that finished before the upgrade) is a single line "Command ran; no reply" shown.
  const command = run.piCommand?.handled === true && run.status === 'completed' && (!reply || reply.text === '') ? run.piCommand : null
  const backgroundTools = new Set((run.backgroundTasks ?? []).flatMap((task) => (task.toolCallId ? [task.toolCallId] : [])))
  const toolNames = new Map(run.tools.map((tool) => [tool.toolCallId, tool.toolName]))
  return (
    <AgentChatReply
      agent={agent}
      turnNumber={turnNumber}
      showTurnNumber
      metadata={<>{ended && <span>{t.chat.time(run.endedAt ?? runStartedAt(run))}</span>}<RunStatusBadge run={run} /></>}
    >
      <FailureNote run={run} runtime={agent.runtime} />
      {reply && reply.text !== '' ? (
        <div className={cn('max-w-none', ended && run.status !== 'completed' && 'text-ink-500')}>
          <Markdown text={reply.text} cursor={streaming} />
        </div>
      ) : command ? (
        (command.notices ?? []).length > 0 ? (
          <PiCommandNotices notices={command.notices} truncated={command.truncated} />
        ) : (
          <p data-pi-command-handled className="text-[14px] text-ink-500">{t.chat.piCommandHandled}</p>
        )
      ) : (
        streaming && (
          <p>
            <Cursor />
          </p>
        )
      )}
      {/* Tools that went on in the background are cards of their own, with how they ended; the others stay chips. */}
      <ToolList tools={run.tools} unfinished={unfinishedTool(run)} exclude={backgroundTools} />
      <BackgroundToolCards tasks={run.backgroundTasks ?? []} toolNames={toolNames} />
      {/* What the reply waits for a person to approve, where it asked. */}
      {requests.map((request) => (
        <PermissionRequestCard key={request.id} request={request} agentName={agent.name} />
      ))}
      {/* An imported turn is a copy of the terminal conversation: it has no record of its own. */}
      {!imported(turn) && <RunRecord agentId={agent.id} run={run} defaultOpen={located || config.runRecordExpanded} />}
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

// A turn copied from the conversation an Agent was imported from (origin imported).
const imported = (turn: Turn) => (turn.user?.origin ?? turn.origin) === 'imported'

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
  agent: Pick<Schemas['Agent'], 'id' | 'name'> & Partial<Pick<Schemas['Agent'], 'runtime'>>
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
  // Permission requests waiting for an answer; each shows in the reply that asked.
  permissionRequests?: Schemas['PermissionRequest'][]
  // Turn numbering (contract MessagePage): how many turns come before the first loaded turn, taken from turnsBefore of the earliest
  // loaded page; numbering here continues from it.
  // Unknown, and not passed, while the history hasn't been fetched: then no numbers are shown, neither on the turns nor on the ruler,
  // only the ticks.
  turnsBefore?: number
  // The total turn count at the bottom of the ruler, computed by the chat page; not passed when unknown.
  turnsTotal?: number
}

// Content placed into the conversation flow lines up directly with the centered conversation column.
function Insert({ children }: { children: ReactNode }) {
  return <div className="flex min-w-0 flex-col">{children}</div>
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
  permissionRequests = [],
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
  // When returning to an Agent whose user had scrolled up, restore that scroll position instead of jumping to the bottom.
  const [initialScroll] = useState(() => (targetRunId === null ? getChatScrollTop(agent.id) : null))
  const [scrolledUp, setScrolledUp] = useState(initialScroll !== null)
  const pendingRestore = useRef<number | null>(initialScroll)
  const stickToBottom = useRef(targetRunId === null && initialScroll === null)
  const located = useRef(false)
  // Track when new turns or streaming output arrive while the user is scrolled up, so the floating button can announce "New output".
  const latestSig = lastKey === undefined ? '' : `${lastKey}:${lastText ?? ''}`
  const [seenLatest, setSeenLatest] = useState(latestSig)
  const [hasNewBelow, setHasNewBelow] = useState(false)
  if (latestSig !== seenLatest) {
    setSeenLatest(latestSig)
    if (seenLatest !== '' && scrolledUp) {
      setHasNewBelow(true)
    }
  }
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
    if (pendingRestore.current !== null) {
      if (turns.length > 0) {
        scrollTo(el, pendingRestore.current, lastScrollTop)
        pendingRestore.current = null
      }
    } else if (olderFrom.current !== null && wasLoadingOlder.current && !loadingOlder) {
      // Loading finished: the earlier messages were inserted at the top, so compensate by the added height.
      scrollTo(el, el.scrollTop + el.scrollHeight - olderFrom.current, lastScrollTop)
      olderFrom.current = null
    } else if (stickToBottom.current) {
      scrollTo(el, el.scrollHeight, lastScrollTop)
    }
    wasLoadingOlder.current = loadingOlder
  }, [firstKey, lastKey, lastText, loadingOlder, turns.length])

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
    } else if (nearBottom) {
      stickToBottom.current = true
      setScrolledUp(false)
      setHasNewBelow(false)
      if (targetRunId === null) setChatScrollTop(agent.id, null)
    } else if (el.scrollTop < lastScrollTop.current) {
      stickToBottom.current = false
      setScrolledUp(true)
      if (targetRunId === null) setChatScrollTop(agent.id, el.scrollTop)
    } else if (!stickToBottom.current && targetRunId === null) {
      setChatScrollTop(agent.id, el.scrollTop)
    }
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
    pendingRestore.current = null
    stickToBottom.current = false
    scrollTo(el, el.scrollTop + target.getBoundingClientRect().top - el.getBoundingClientRect().top - 24, lastScrollTop)
    rulerJump.current = lastScrollTop.current
    setScrolledUp(true)
    if (targetRunId === null) setChatScrollTop(agent.id, lastScrollTop.current)
  }

  // Return to the true bottom of the conversation and resume auto-following new content.
  const jumpToBottom = () => {
    const el = container.current
    if (!el) return
    pendingRestore.current = null
    stickToBottom.current = true
    scrollTo(el, el.scrollHeight, lastScrollTop)
    setScrolledUp(false)
    setHasNewBelow(false)
    if (targetRunId === null) setChatScrollTop(agent.id, null)
  }

  return (
    // Centered conversation stream with a zero-width-tax floating PrecisionRuler overlay on the right edge.
    <div className="relative flex h-full min-h-0 flex-1 flex-col">
      {/* The scroll area itself is the positioning container: absolutely positioned elements inside it (unread dots, the sr-only
          caption of the footnote heading, etc.) scroll with the content and get clipped; otherwise they'd be laid out in the whole
          page at their unscrolled positions, making the page taller, so a large blank area could be dragged out below the input box. */}
      <div
        ref={container}
        onScroll={onScroll}
        className="relative h-full min-w-0 flex-1 overflow-y-auto"
        role="log"
        aria-label={t.chat.messagesLabel}
      >
        <div ref={content} className="mx-auto flex w-full max-w-[800px] min-w-0 flex-col gap-5 px-4 pt-4 pb-6 sm:px-6">
          {loadingOlder && <p className="text-center text-[12.5px] font-bold text-ink-500">{t.chat.loadingOlder}</p>}
          {hasOlder && !loadingOlder && (
            <Button variant="ghost" size="sm" className="self-center" disabled={refreshing} onClick={loadOlder}>
              {t.chat.loadOlder}
            </Button>
          )}
          {turns.length === 0 && !hasQueued && placed.size === 0 && <p className="text-center text-[14px] text-ink-500">{t.chat.noMessages}</p>}
          {placed.get(-1)?.map((item) => <Insert key={item.key}>{item.node}</Insert>)}
          {turns.map((turn, index) => {
            const isTarget = turn.run.id === targetRunId
            const turnNumber = firstNumber === null ? null : firstNumber + index
            return (
              <Fragment key={turn.key}>
                {/* The turns copied from the terminal conversation come first, under their own divider; the Agent's own follow. */}
                {imported(turn) && (index === 0 ? !hasOlder : !imported(turns[index - 1])) && <CutLine icon={HistoryIcon} label={t.importing.history.divider} />}
                {index > 0 && imported(turns[index - 1]) && !imported(turn) && <CutLine icon={ArrowDownIcon} label={t.importing.history.continued} />}
                {index > 0 && turns[index - 1].sessionEpoch !== turn.sessionEpoch && !imported(turns[index - 1]) && <CutLine icon={ScissorsIcon} label={t.chat.sessionDivider} />}
                {index > 0 && interruptedBy(turns[index - 1], turn) && <CutLine icon={ZapIcon} label={t.chat.interruptDivider} />}
                <TurnChange previous={turns[index - 1]} turn={turn} />
                {/* One conversation turn: user message and agent reply stacked cleanly inside the centered 800px column.
                    The located turn is highlighted with a light yellow fill. */}
                <div data-run-id={turn.run.id} data-located={isTarget || undefined} className={cn('flex min-w-0 flex-col gap-3.5', isTarget && 'rounded-panel bg-tint-yellow p-4')}>
                  {isTarget && <span className="sr-only">{t.timeline.locate.target}</span>}
                  {turn.user &&
                    (turn.user.origin === 'background' ? (
                      <BackgroundFollowUp message={turn.user} run={turn.run} agent={agent} />
                    ) : (turn.user.origin === 'inbox_reminder' || turn.user.origin === 'sleep_wake') ? (
                      <PlatformReminder agentId={agent.id} message={turn.user} run={turn.run} slept={sleptBefore(turns[index - 1], turn)} />
                    ) : (
                      <UserBubble message={turn.user} run={turn.run} />
                    ))}
                  <div className="min-w-0 w-full">
                    <ReplyBubble
                      turn={turn}
                      agent={agent}
                      located={isTarget}
                      turnNumber={turnNumber}
                      requests={permissionRequests.filter((request) => request.runId === turn.run.id)}
                    />
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
      {scrolledUp && turns.length > 0 && (
        <div className="pointer-events-none absolute inset-x-0 bottom-3 z-10 flex justify-center px-4">
          <button
            type="button"
            data-jump-bottom
            onClick={jumpToBottom}
            className={cn(
              'pointer-events-auto inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-full border px-3.5 text-[12.5px] font-semibold shadow-floating outline-none focus-visible:shadow-focus',
              hasNewBelow
                ? 'border-transparent bg-cta-bg text-cta-fg hover:bg-cta-hover [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-print-ink'
                : 'border-hairline-strong bg-surface-elevated text-ink hover:bg-surface-hover [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:shadow-print-ink',
            )}
          >
            <ArrowDownIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.5} />
            <span>{hasNewBelow ? t.chat.newOutput : t.chat.backToLatest}</span>
          </button>
        </div>
      )}
      <MessageRuler
        turns={turns.map((turn) => ({ key: turn.run.id, label: turnPreview(turn) }))}
        visible={visibleTurns}
        onJump={jumpTo}
        onJumpBottom={jumpToBottom}
        firstNumber={firstNumber}
        total={firstNumber === null ? null : (turnsTotal ?? null)}
      />
    </div>
  )
}
