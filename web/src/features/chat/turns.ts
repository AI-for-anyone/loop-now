import type { Schemas } from '@/api/client'
import { newerRun, type MessagePages } from '@/api/queries'
import type { LiveState } from './live-state'
import { isVoidedRun } from './run-status-text'
import { latestBackgroundTasks } from './background-text'
import { compareTimes, runStartedAt } from './time'

type Message = Schemas['Message']
type Run = Schemas['Run']

export interface Turn {
  key: string
  // Where the turn came from (Message.origin, the same on the prompt and the reply): known from either, since a page of history can
  // start between them.
  origin?: Message['origin']
  user: Message | null
  run: Run
  reply: { text: string; live: boolean } | null
  sessionEpoch: number
}

// Combine the history and the live state into conversation turns: a user message, its run and its reply.
// Queued messages go at the end, in the server's processing order.
// When an earlier reply was located and the history isn't loaded up to the latest (atLatest is false), turns that started outside the
// history aren't put into the conversation flow;
// they only count toward hiddenNew, and the chat page announces new messages; queued messages go into the queue list as usual.
export function buildTurns(
  pages: MessagePages | undefined,
  live: LiveState,
  atLatest = true,
): { turns: Turn[]; queued: Turn[]; hiddenNew: number } {
  const history = pages ? [...pages.pages].reverse().flatMap((page) => page.messages) : []
  // Messages from events and send results may not be in the history yet (the request read before they existed, or the first load
  // hasn't finished), so they're appended by ID.
  const known = new Set(history.map((message) => message.id))
  const historyRuns = new Set(history.map((message) => message.run.id))
  const messages = [...history, ...Object.values(live.messages).filter((message) => !known.has(message.id))]
  // The same run may have several copies of its data (history, events, send result and live state), arriving in no fixed order; when
  // merging, the state only moves forward, never back,
  // and at the same phase the live copy wins (it has the tool states received one by one). Background tasks are merged task by task, since
  // an ended run's tasks still end later (a snapshot's history can be newer than the live copy of the same ended run).
  const mergeRuns = (a: Run, b: Run): Run => {
    const chosen = newerRun(a, b)
    const lists = [a.backgroundTasks ?? [], b.backgroundTasks ?? []]
    return lists.some((list) => list.length > 0) ? { ...chosen, backgroundTasks: latestBackgroundTasks(lists) } : chosen
  }
  const effectiveRun = (run: Run): Run =>
    [live.runs[run.id], live.current?.run.id === run.id ? live.current.run : undefined, live.queued.find((r) => r.id === run.id)].reduce<Run>(
      (merged, other) => (other ? mergeRuns(merged, other) : merged),
      run,
    )

  const byRun = new Map<string, Turn>()
  const order: Turn[] = []
  for (const message of messages) {
    const run = effectiveRun(message.run)
    let turn = byRun.get(run.id)
    if (!turn) {
      turn = { key: run.id, origin: message.origin, user: null, run, reply: null, sessionEpoch: message.sessionEpoch }
      byRun.set(run.id, turn)
      order.push(turn)
    } else {
      turn.run = mergeRuns(turn.run, run)
    }
    if (message.role === 'user') turn.user = message
    else turn.reply = { text: message.text, live: false }
  }
  // While a manual compaction (/compact) is in progress, the current run is that compaction (run.compaction is not null). It has no
  // conversation messages and is not a turn:
  // it doesn't enter the conversation flow, takes no number or tick, and doesn't count as a new message when locating; the chat page
  // shows the busy state as usual.
  const current = live.current && live.current.run.compaction == null ? live.current : null
  if (current && !byRun.has(current.run.id) && (atLatest || historyRuns.has(current.run.id))) {
    const run = effectiveRun(current.run)
    const turn: Turn = { key: run.id, user: null, run, reply: null, sessionEpoch: live.session?.epoch ?? 0 }
    byRun.set(turn.key, turn)
    order.push(turn)
  }
  if (current) {
    const turn = byRun.get(current.run.id)
    if (turn && !turn.reply) turn.reply = { text: current.text, live: true }
  }

  const queuedOrder = live.queued.map((r) => r.id)
  const rank = (turn: Turn) => {
    const index = queuedOrder.indexOf(turn.run.id)
    return index >= 0 ? index : queuedOrder.length + (turn.run.whenBusy === 'interrupt' ? 0 : 1)
  }
  const queued = order.filter((turn) => turn.run.status === 'queued').sort((a, b) => rank(a) - rank(b))
  // Started turns are ordered by when they actually started running: an interrupt message, though sent later, is handled before
  // normal queued messages,
  // and the history order should match the conversation order the Agent actually saw. Turns without a start time use the queued time;
  // equal times keep their original order.
  // Turns voided while still queued that never ran don't enter the conversation flow.
  const started = order.filter((turn) => turn.run.status !== 'queued' && !isVoidedRun(turn.run))
  const hiddenRuns = new Set(atLatest ? [] : started.filter((turn) => !historyRuns.has(turn.run.id)).map((turn) => turn.run.id))
  if (!atLatest && current && !historyRuns.has(current.run.id)) hiddenRuns.add(current.run.id)
  const turns = started
    .filter((turn) => !hiddenRuns.has(turn.run.id))
    .map((turn, index) => ({ turn, index }))
    .sort((a, b) => compareTimes(runStartedAt(a.turn.run), runStartedAt(b.turn.run)) || a.index - b.index)
    .map(({ turn }) => turn)
  return { turns, queued, hiddenNew: hiddenRuns.size }
}

// The handover of the conversation to another runtime to show before a turn: the turn's own record (Run.contextHandoff), unless the turn
// before carried the same one (the block goes again with each message until one reaches the new runtime, so a failed try and its retry
// both carry it; it is shown once, before the first).
export function handoffBefore(previous: Turn | undefined, turn: Turn): Schemas['ContextHandoff'] | null {
  const handoff = turn.run.contextHandoff
  if (!handoff) return null
  const earlier = previous?.run.contextHandoff
  return earlier && earlier.reason === handoff.reason && earlier.fromRuntime === handoff.fromRuntime && earlier.toRuntime === handoff.toRuntime ? null : handoff
}

// A change of model on the same runtime between two neighbouring turns (Run.selection): from and to. Nothing when either turn has no
// selection (it never began, or it was recorded before selections were kept), or when the runtime changed (the handover shows that).
export function modelChangeBefore(previous: Turn | undefined, turn: Turn): { from: Schemas['RunSelection']; to: Schemas['RunSelection'] } | null {
  const from = previous?.run.selection, to = turn.run.selection
  if (!from || !to || turn.run.contextHandoff || from.runtime !== to.runtime) return null
  return from.provider !== to.provider || from.modelId !== to.modelId ? { from, to } : null
}
