import type { Schemas } from '@/api/client'
import { compareEventIds, type ConnectionState, type OverviewEvent } from '@/api/events'
import { newerSleep } from '@/features/sleep/projection'

type AgentOverview = Schemas['AgentOverview']

// 概览的运行状态：各 Agent 的最新状态，按快照中的顺序排列，之后新出现的排在末尾。
export interface OverviewState {
  connection: ConnectionState
  lastEventId: string | null
  // 是否已收到快照；收到之前运行状态显示加载中。
  synced: boolean
  agents: AgentOverview[]
  // 最近一次收到状态变化的时刻（浏览器时间，ISO 字符串）。
  lastChangeAt: string | null
}

export type OverviewAction =
  | { type: 'event'; event: OverviewEvent; at: string }
  | { type: 'connection'; state: ConnectionState }

export const initialOverviewState: OverviewState = {
  connection: 'connecting',
  lastEventId: null,
  synced: false,
  agents: [],
  lastChangeAt: null,
}

function applyEvent(state: OverviewState, event: OverviewEvent, at: string): OverviewState {
  const merge = (incoming: AgentOverview): AgentOverview => {
    const known = state.agents.find(agent => agent.agentId === incoming.agentId)
    if (!known) return incoming
    const { sleep, sleepRevision } = newerSleep(known.status, incoming.status)
    return { ...incoming, status: incoming.status.sleepRevision < known.status.sleepRevision ? known.status : { ...incoming.status, sleep, sleepRevision } }
  }
  switch (event.type) {
    case 'snapshot':
      return { ...state, synced: true, agents: event.data.agents.map(merge), lastChangeAt: at }
    case 'agent.status': {
      const next = merge(event.data.agent)
      const exists = state.agents.some((agent) => agent.agentId === next.agentId)
      const agents = exists ? state.agents.map((agent) => (agent.agentId === next.agentId ? next : agent)) : [...state.agents, next]
      return { ...state, agents, lastChangeAt: at }
    }
    case 'agent.removed':
      return { ...state, agents: state.agents.filter((agent) => agent.agentId !== event.data.agentId), lastChangeAt: at }
  }
}

export function overviewReducer(state: OverviewState, action: OverviewAction): OverviewState {
  switch (action.type) {
    case 'connection':
      return state.connection === action.state ? state : { ...state, connection: action.state }
    case 'event': {
      const { event } = action
      // 快照总是整体替换；其他事件按事件 ID 去重，只处理比已处理位置更新的事件。
      if (event.type !== 'snapshot' && event.id && state.lastEventId && compareEventIds(event.id, state.lastEventId) <= 0) return state
      const next = applyEvent(state, event, action.at)
      return { ...next, lastEventId: event.id || state.lastEventId }
    }
  }
}

// 四个汇总数：回复中（busy）、空闲（不忙且会话正常）、会话丢失、排队中（所有 Agent 的打断与普通排队数之和）。
export interface OverviewSummary {
  busy: AgentOverview[]
  idle: AgentOverview[]
  sessionMissing: AgentOverview[]
  queued: { interrupt: number; normal: number }
}

export function summarize(agents: AgentOverview[]): OverviewSummary {
  const missing = (agent: AgentOverview) => agent.status.session.state === 'missing'
  return {
    busy: agents.filter((agent) => agent.status.busy),
    idle: agents.filter((agent) => !agent.status.busy && !missing(agent)),
    sessionMissing: agents.filter(missing),
    queued: agents.reduce(
      (sum, agent) => ({ interrupt: sum.interrupt + agent.status.queued.interrupt, normal: sum.normal + agent.status.queued.normal }),
      { interrupt: 0, normal: 0 },
    ),
  }
}

// 每行的状态，与 Agent 头部贴纸的优先级一致：会话丢失、回复中、空闲。
export type AgentRowState = 'sessionMissing' | 'busy' | 'idle'

export function rowState(agent: AgentOverview): AgentRowState {
  if (agent.status.session.state === 'missing') return 'sessionMissing'
  return agent.status.busy ? 'busy' : 'idle'
}
