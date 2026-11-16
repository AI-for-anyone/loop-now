import type { Schemas } from '@/api/client'
import { compareEventIds, type ConnectionState, type OverviewEvent } from '@/api/events'
import { newerSleep } from '@/features/sleep/projection'

type AgentOverview = Schemas['AgentOverview']

// The overview's run status: each Agent's latest status, in snapshot order, with Agents that appear later added at the end.
export interface OverviewState {
  connection: ConnectionState
  lastEventId: string | null
  // Whether the snapshot has arrived; until then the run status shows loading.
  synced: boolean
  agents: AgentOverview[]
  // When the latest status change was received (browser time, ISO string).
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
      // A snapshot always replaces everything; other events are deduplicated by event ID, and only events newer than the
      // position already processed are handled.
      if (event.type !== 'snapshot' && event.id && state.lastEventId && compareEventIds(event.id, state.lastEventId) <= 0) return state
      const next = applyEvent(state, event, action.at)
      return { ...next, lastEventId: event.id || state.lastEventId }
    }
  }
}

// The four summary counts: replying (busy), idle (not busy and the session is fine), session lost, and queued (the sum of
// interrupt and normal queue counts across all Agents).
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

// Each row's state, with the same priority as the Agent header sticker: session lost, replying, idle.
export type AgentRowState = 'sessionMissing' | 'busy' | 'idle'

export function rowState(agent: AgentOverview): AgentRowState {
  if (agent.status.session.state === 'missing') return 'sessionMissing'
  return agent.status.busy ? 'busy' : 'idle'
}
