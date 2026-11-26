import type { Schemas } from '@/api/client'
import { compareEventIds, type AgentEvent, type ConnectionState } from '@/api/events'
import { newerRun } from '@/api/queries'
import { compareTimes, runStartedAt } from './time'
import { activeNormalSleep, newerSleep } from '@/features/sleep/projection'

type Message = Schemas['Message']
type Run = Schemas['Run']

export interface LiveRun {
  run: Run
  text: string
}

// The live part of the chat page: current run, queued runs, session state and connection state. The history part lives in the query cache.
export interface LiveState {
  agentId: string | null
  sleep: Schemas['SleepInfo'] | null
  sleepRevision: number
  connection: ConnectionState
  lastEventId: string | null
  // Number of snapshots received; when it changes the chat page refetches the history to clear up differences from while disconnected.
  snapshots: number
  current: LiveRun | null
  queued: Run[]
  // Per-item queue list (the queue tray above the input box), in actual processing order; replaced wholesale by snapshots and queue.updated.
  queueItems: Schemas['QueueItem'][]
  // Number of snapshots or queue.updated events received. If one arrived after a queue action started, the list in the action's
  // receipt is already stale and is not used.
  queueSeq: number
  session: Schemas['SessionInfo'] | null
  // The Agent's running background tasks, oldest first: from the snapshot's status, then replaced whole by background_tasks.updated.
  backgroundTasks: Schemas['BackgroundTask'][]
  // Permission requests waiting for an answer, oldest first: from the snapshot's status, then replaced whole by permission_requests.updated.
  permissionRequests: Schemas['PermissionRequest'][]
  // Messages and runs seen in events and send results, merged by ID; for runs only the latest state is kept. Anything written to the
  // cache while a history request is in flight gets overwritten by the request's result, so they are merged on top of the history again
  // when building conversation turns; snapshots don't clear them.
  // The known state of runs is also used to recognize stale updates that arrive out of order.
  messages: Record<string, Message>
  runs: Record<string, Run>
}

export type LiveAction =
  | { type: 'event'; event: AgentEvent }
  | { type: 'batch'; events: AgentEvent[] }
  | { type: 'connection'; state: ConnectionState }
  | { type: 'sent'; result: Schemas['SendMessageResult'] }
  | { type: 'queueAction'; receipt: Schemas['AgentQueueActionReceipt']; seq: number }
  | { type: 'reset'; agentId?: string }

export const initialLiveState: LiveState = {
  agentId: null, sleep: null, sleepRevision: 0,
  connection: 'connecting',
  lastEventId: null,
  snapshots: 0,
  current: null,
  queued: [],
  queueItems: [],
  queueSeq: 0,
  session: null,
  backgroundTasks: [],
  permissionRequests: [],
  messages: {},
  runs: {},
}

const IN_PROGRESS: ReadonlySet<Run['status']> = new Set(['starting', 'running', 'aborting'])

export function isInProgress(run: Run): boolean {
  return IN_PROGRESS.has(run.status)
}

export function isBusy(state: LiveState): boolean {
  return state.current !== null || state.queued.length > 0
}

export function queuedCounts(state: LiveState): Schemas['QueueCounts'] {
  const interrupt = state.queued.filter((run) => run.whenBusy === 'interrupt').length
  return { interrupt, normal: state.queued.length - interrupt }
}

// Insert a queued run in the server's processing order: interrupt messages go after existing interrupt messages and before all normal
// queued messages.
function insertQueued(queued: Run[], run: Run): Run[] {
  if (queued.some((item) => item.id === run.id)) return queued.map((item) => (item.id === run.id ? run : item))
  if (run.whenBusy !== 'interrupt') return [...queued, run]
  const firstNormal = queued.findIndex((item) => item.whenBusy !== 'interrupt')
  if (firstNormal === -1) return [...queued, run]
  return [...queued.slice(0, firstNormal), run, ...queued.slice(firstNormal)]
}

// Record the latest state of a run. Updates for the same run may arrive out of order (the send request and the driver loop emit events
// separately); an update older than the known state returns null, so a run's state only moves forward, never back.
function recordRun(runs: Record<string, Run>, run: Run): Record<string, Run> | null {
  const known = runs[run.id]
  if (known && newerRun(known, run) !== run) return null
  return { ...runs, [run.id]: run }
}

// queue.updated is the complete list: replace the queued runs wholesale in the dequeue order the server gives; runs already known to
// have started or ended are not put back into the queue.
function applyQueue(state: LiveState, queuedRuns: Run[]): LiveState {
  let runs = state.runs
  const queued: Run[] = []
  for (const run of queuedRuns) {
    const next = recordRun(runs, run)
    if (!next) continue
    runs = next
    queued.push(run)
  }
  return { ...state, runs, queued }
}

function recordMessage(state: LiveState, message: Message): LiveState {
  return { ...state, messages: { ...state.messages, [message.id]: message } }
}

function applyRun(state: LiveState, run: Run): LiveState {
  const runs = recordRun(state.runs, run)
  if (!runs) return state
  if (run.status === 'queued') return { ...state, runs, queued: insertQueued(state.queued, run) }
  const queued = state.queued.filter((item) => item.id !== run.id)
  if (isInProgress(run)) {
    const current = state.current
    // An Agent's runs happen one after another: if the current run started later, this is a late, stale update for another run and
    // does not replace the current run.
    if (current && current.run.id !== run.id && compareTimes(runStartedAt(run), runStartedAt(current.run)) <= 0) {
      return { ...state, runs, queued }
    }
    const text = current?.run.id === run.id ? current.text : ''
    return { ...state, runs, queued, current: { run, text } }
  }
  const current = state.current?.run.id === run.id ? null : state.current
  return { ...state, runs, queued, current }
}

function applyEvent(state: LiveState, event: AgentEvent): LiveState {
  switch (event.type) {
    case 'snapshot': {
      const { status, currentText, queuedRuns, queueItems } = event.data
      const sleep = newerSleep(state, status)
      const stale = status.sleepRevision < state.sleepRevision
      let runs = state.runs
      for (const run of status.currentRun ? [status.currentRun, ...queuedRuns] : queuedRuns) runs = recordRun(runs, run) ?? runs
      return {
        ...state,
        agentId: status.agentId,
        sleep: sleep.sleep, sleepRevision: sleep.sleepRevision,
        snapshots: state.snapshots + 1,
        current: stale ? state.current : status.currentRun ? { run: status.currentRun, text: currentText ?? '' } : null,
        queued: stale ? state.queued : queuedRuns,
        queueItems: stale ? state.queueItems : queueItems,
        queueSeq: state.queueSeq + 1,
        session: status.session,
        backgroundTasks: status.backgroundTasks,
        permissionRequests: status.permissionRequests,
        runs,
      }
    }
    case 'message.created':
      return recordMessage(applyRun(state, event.data.message.run), event.data.message)
    case 'run.updated':
      return applyRun(state, event.data.run)
    case 'run.text': {
      if (state.current?.run.id !== event.data.runId) return state
      return { ...state, current: { ...state.current, text: state.current.text + event.data.delta } }
    }
    case 'run.tool': {
      const current = state.current
      if (current?.run.id !== event.data.runId) return state
      const { toolCallId, toolName, state: toolState, at } = event.data
      const tool = { toolCallId, toolName, state: toolState, at }
      const exists = current.run.tools.some((item) => item.toolCallId === toolCallId)
      const tools = exists
        ? current.run.tools.map((item) => (item.toolCallId === toolCallId ? tool : item))
        : [...current.run.tools, tool]
      return { ...state, current: { ...current, run: { ...current.run, tools } } }
    }
    case 'run.ended': {
      const next = applyRun(state, event.data.run)
      return event.data.message ? recordMessage(next, event.data.message) : next
    }
    case 'session.updated':
      return { ...state, session: { state: event.data.state, epoch: event.data.epoch } }
    case 'sleep.updated': {
      const value = event.data
      if (value.scopeKind !== 'normal' || value.taskId !== null || value.runtimeId !== null || value.agentId !== state.agentId || value.sleepRevision <= state.sleepRevision) return state
      const sleeping = activeNormalSleep(value.sleep, value.agentId)
      return { ...state, sleep: value.sleep, sleepRevision: value.sleepRevision, ...(sleeping ? { current: null, queued: [], queueItems: [] } : {}) }
    }
    case 'queue.updated':
      return { ...applyQueue(state, event.data.queuedRuns), queueItems: event.data.items, queueSeq: state.queueSeq + 1 }
    case 'background_tasks.updated':
      return { ...state, backgroundTasks: event.data.tasks }
    case 'permission_requests.updated':
      return { ...state, permissionRequests: event.data.requests }
  }
}

export function liveReducer(state: LiveState, action: LiveAction): LiveState {
  switch (action.type) {
    case 'reset':
      return { ...initialLiveState, agentId: action.agentId ?? null }
    case 'connection':
      return state.connection === action.state ? state : { ...state, connection: action.state }
    case 'sent': {
      // The send result only fills in the history part; busy/idle and the queue follow the events. If the result arrives after the
      // events, the run's state doesn't move back.
      const { message, run } = action.result
      return recordMessage({ ...state, runs: recordRun(state.runs, run) ?? state.runs }, message)
    }
    case 'queueAction': {
      // The run in the receipt is recorded as usual (its state only moves forward, never back); the list in the receipt is used only if
      // no new list has arrived since the action started.
      const { receipt, seq } = action
      const next = receipt.run ? applyRun(state, receipt.run) : state
      return seq === state.queueSeq ? { ...next, queueItems: receipt.queue } : next
    }
    case 'batch':
      return action.events.reduce((next, event) => liveReducer(next, { type: 'event', event }), state)
    case 'event': {
      const { event } = action
      // A snapshot always replaces everything; other events are deduplicated by event ID, and only events newer than the position
      // already processed are handled.
      if (event.type !== 'snapshot' && event.id && state.lastEventId && compareEventIds(event.id, state.lastEventId) <= 0) {
        return state
      }
      const next = applyEvent(state, event)
      return { ...next, lastEventId: event.id || state.lastEventId }
    }
  }
}
