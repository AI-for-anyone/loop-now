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

// 对话页的实时部分：当前执行、排队执行、会话状态与连接状态。历史部分在查询缓存中。
export interface LiveState {
  agentId: string | null
  sleep: Schemas['SleepInfo'] | null
  sleepRevision: number
  connection: ConnectionState
  lastEventId: string | null
  // 收到快照的次数；变化时对话页重新获取历史，消除断线期间的差异。
  snapshots: number
  current: LiveRun | null
  queued: Run[]
  // 逐条排队列表（输入框上方的排队托盘），按实际处理顺序；快照与 queue.updated 整体替换。
  queueItems: Schemas['QueueItem'][]
  // 收到快照或 queue.updated 的次数。排队动作开始后又收到过时，动作回执里的列表已经过时，不再采用。
  queueSeq: number
  session: Schemas['SessionInfo'] | null
  // 事件与发送结果中出现过的消息和执行，按 ID 合并，执行只保留最新的状态。历史请求进行中时写入缓存的内容
  // 会被请求结果覆盖，所以构建对话轮次时再把它们合并到历史之上；快照不清除它们。
  // 执行的已知状态也用来识别乱序到达的旧更新。
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

// 按服务端的处理顺序插入排队执行：打断消息排在已有打断消息之后、所有普通排队消息之前。
function insertQueued(queued: Run[], run: Run): Run[] {
  if (queued.some((item) => item.id === run.id)) return queued.map((item) => (item.id === run.id ? run : item))
  if (run.whenBusy !== 'interrupt') return [...queued, run]
  const firstNormal = queued.findIndex((item) => item.whenBusy !== 'interrupt')
  if (firstNormal === -1) return [...queued, run]
  return [...queued.slice(0, firstNormal), run, ...queued.slice(firstNormal)]
}

// 记下执行的最新状态。同一执行的更新可能乱序到达（发送请求与驱动循环分别发出事件），
// 比已知状态更早的更新返回 null，执行状态只前进不倒退。
function recordRun(runs: Record<string, Run>, run: Run): Record<string, Run> | null {
  const known = runs[run.id]
  if (known && newerRun(known, run) !== run) return null
  return { ...runs, [run.id]: run }
}

// queue.updated 是完整列表：按服务端给出的出队顺序整体替换排队执行，已知已经开始或结束的不放回队列。
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
    // 同一 Agent 的执行依次进行：当前执行开始得更晚时，这是另一个执行晚到的旧更新，不替换当前执行。
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
  }
}

export function liveReducer(state: LiveState, action: LiveAction): LiveState {
  switch (action.type) {
    case 'reset':
      return { ...initialLiveState, agentId: action.agentId ?? null }
    case 'connection':
      return state.connection === action.state ? state : { ...state, connection: action.state }
    case 'sent': {
      // 发送结果只补进历史部分；忙闲与队列以事件为准。结果晚于事件到达时执行状态不倒退。
      const { message, run } = action.result
      return recordMessage({ ...state, runs: recordRun(state.runs, run) ?? state.runs }, message)
    }
    case 'queueAction': {
      // 回执里的执行照常记入（状态只前进不倒退）；回执里的列表只在动作开始后没有收到新列表时采用。
      const { receipt, seq } = action
      const next = receipt.run ? applyRun(state, receipt.run) : state
      return seq === state.queueSeq ? { ...next, queueItems: receipt.queue } : next
    }
    case 'batch':
      return action.events.reduce((next, event) => liveReducer(next, { type: 'event', event }), state)
    case 'event': {
      const { event } = action
      // 快照总是整体替换；其他事件按事件 ID 去重，只处理比已处理位置更新的事件。
      if (event.type !== 'snapshot' && event.id && state.lastEventId && compareEventIds(event.id, state.lastEventId) <= 0) {
        return state
      }
      const next = applyEvent(state, event)
      return { ...next, lastEventId: event.id || state.lastEventId }
    }
  }
}
