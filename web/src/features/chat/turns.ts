import type { Schemas } from '@/api/client'
import { newerRun, type MessagePages } from '@/api/queries'
import type { LiveState } from './live-state'
import { isVoidedRun } from './run-status-text'
import { compareTimes, runStartedAt } from './time'

type Message = Schemas['Message']
type Run = Schemas['Run']

export interface Turn {
  key: string
  user: Message | null
  run: Run
  reply: { text: string; live: boolean } | null
  sessionEpoch: number
}

// 把历史与实时状态合成一轮轮对话：用户消息、它的执行和回复。
// 排队中的消息放在末尾，按服务端的处理顺序排列。
// 定位到较早的回复、历史还没有加载到最新时（atLatest 为 false），历史之外已开始的轮次不放进对话流，
// 只计入 hiddenNew，由对话页提示有新消息；排队中的消息照常进入排队列表。
export function buildTurns(
  pages: MessagePages | undefined,
  live: LiveState,
  atLatest = true,
): { turns: Turn[]; queued: Turn[]; hiddenNew: number } {
  const history = pages ? [...pages.pages].reverse().flatMap((page) => page.messages) : []
  // 事件与发送结果中的消息可能还不在历史里（请求在它们之前读取，或首次加载尚未完成），按 ID 补在后面。
  const known = new Set(history.map((message) => message.id))
  const historyRuns = new Set(history.map((message) => message.run.id))
  const messages = [...history, ...Object.values(live.messages).filter((message) => !known.has(message.id))]
  // 同一执行可能有历史、事件、发送结果与实时状态几份数据，到达顺序不定；合并时状态只前进不倒退，
  // 阶段相同时取实时部分的（其中有逐条收到的工具状态）。
  const effectiveRun = (run: Run): Run =>
    [live.runs[run.id], live.current?.run.id === run.id ? live.current.run : undefined, live.queued.find((r) => r.id === run.id)].reduce<Run>(
      (merged, other) => (other ? newerRun(merged, other) : merged),
      run,
    )

  const byRun = new Map<string, Turn>()
  const order: Turn[] = []
  for (const message of messages) {
    const run = effectiveRun(message.run)
    let turn = byRun.get(run.id)
    if (!turn) {
      turn = { key: run.id, user: null, run, reply: null, sessionEpoch: message.sessionEpoch }
      byRun.set(run.id, turn)
      order.push(turn)
    } else {
      turn.run = newerRun(turn.run, run)
    }
    if (message.role === 'user') turn.user = message
    else turn.reply = { text: message.text, live: false }
  }
  if (live.current && !byRun.has(live.current.run.id) && (atLatest || historyRuns.has(live.current.run.id))) {
    const run = effectiveRun(live.current.run)
    const turn: Turn = { key: run.id, user: null, run, reply: null, sessionEpoch: live.session?.epoch ?? 0 }
    byRun.set(turn.key, turn)
    order.push(turn)
  }
  if (live.current) {
    const turn = byRun.get(live.current.run.id)
    if (turn && !turn.reply) turn.reply = { text: live.current.text, live: true }
  }

  const queuedOrder = live.queued.map((r) => r.id)
  const rank = (turn: Turn) => {
    const index = queuedOrder.indexOf(turn.run.id)
    return index >= 0 ? index : queuedOrder.length + (turn.run.whenBusy === 'interrupt' ? 0 : 1)
  }
  const queued = order.filter((turn) => turn.run.status === 'queued').sort((a, b) => rank(a) - rank(b))
  // 已开始的轮次按实际开始执行的时间排列：打断消息虽然发得晚，却先于普通排队消息处理，
  // 历史顺序应与 Agent 实际看到的对话顺序一致。没有开始时间的按入队时间；时间相同的保持原有顺序。
  // 排队中即作废、没有执行的轮次不进入对话流。
  const started = order.filter((turn) => turn.run.status !== 'queued' && !isVoidedRun(turn.run))
  const hiddenRuns = new Set(atLatest ? [] : started.filter((turn) => !historyRuns.has(turn.run.id)).map((turn) => turn.run.id))
  if (!atLatest && live.current && !historyRuns.has(live.current.run.id)) hiddenRuns.add(live.current.run.id)
  const turns = started
    .filter((turn) => !hiddenRuns.has(turn.run.id))
    .map((turn, index) => ({ turn, index }))
    .sort((a, b) => compareTimes(runStartedAt(a.turn.run), runStartedAt(b.turn.run)) || a.index - b.index)
    .map(({ turn }) => turn)
  return { turns, queued, hiddenNew: hiddenRuns.size }
}
