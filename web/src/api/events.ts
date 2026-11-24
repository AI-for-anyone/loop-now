import { config } from '@/config'
import { connectLocalEventStream } from './event-stream'
import { sharedStreams } from './shared-events/client'
import type { ScopeQuery } from '@/lib/scope'
import type { Schemas } from './client'

// Maps event names to x-sse-events in the OpenAPI spec. A contract test in scripts checks that the two agree.
export interface AgentEventDataMap {
  snapshot: Schemas['SseSnapshot']
  'message.created': Schemas['SseMessageCreated']
  'run.updated': Schemas['SseRunUpdated']
  'run.text': Schemas['SseRunText']
  'run.tool': Schemas['SseRunTool']
  'run.ended': Schemas['SseRunEnded']
  'session.updated': Schemas['SseSessionUpdated']
  'sleep.updated': Schemas['SseSleepUpdated']
  'queue.updated': Schemas['SseQueueUpdated']
}

export type AgentEventName = keyof AgentEventDataMap

export const AGENT_EVENT_NAMES = [
  'snapshot',
  'message.created',
  'run.updated',
  'run.text',
  'run.tool',
  'run.ended',
  'session.updated',
  'sleep.updated',
  'queue.updated',
] as const satisfies readonly AgentEventName[]

export type AgentEvent = {
  [K in AgentEventName]: { type: K; id: string; data: AgentEventDataMap[K] }
}[AgentEventName]

// Overview event stream: the run state of every Agent. Event names match x-sse-events of /overview/events in the OpenAPI spec.
export interface OverviewEventDataMap {
  snapshot: Schemas['SseOverviewSnapshot']
  'agent.status': Schemas['SseAgentStatus']
  'agent.removed': Schemas['SseAgentRemoved']
}

export type OverviewEventName = keyof OverviewEventDataMap

export const OVERVIEW_EVENT_NAMES = ['snapshot', 'agent.status', 'agent.removed'] as const satisfies readonly OverviewEventName[]

export type OverviewEvent = {
  [K in OverviewEventName]: { type: K; id: string; data: OverviewEventDataMap[K] }
}[OverviewEventName]

// Inbox event stream: covers all Inbox messages. Event names match x-sse-events of /inbox/events in the OpenAPI spec.
export interface InboxEventDataMap {
  snapshot: Schemas['SseInboxSnapshot']
  'inbox.message.created': Schemas['SseInboxMessageCreated']
  'inbox.message.read': Schemas['SseInboxMessageRead']
  'inbox.message.cancelled': Schemas['SseInboxMessageCancelled']
  'inbox.question.updated': Schemas['SseInboxMessageCreated']
}

export type InboxEventName = keyof InboxEventDataMap

export const INBOX_EVENT_NAMES = ['snapshot', 'inbox.message.created', 'inbox.message.read', 'inbox.message.cancelled', 'inbox.question.updated'] as const satisfies readonly InboxEventName[]

export type InboxEvent = {
  [K in InboxEventName]: { type: K; id: string; data: InboxEventDataMap[K] }
}[InboxEventName]

export type ConnectionState = 'connecting' | 'open' | 'reconnecting' | 'closed'

export interface StreamOptions<E> {
  onEvent: (event: E) => void
  onState: (state: ConnectionState) => void
  // Called when the browser closes the event stream for good (e.g. the server returned an error status); returning false means do not
  // reconnect.
  onClosed: () => Promise<boolean>
  createEventSource?: (url: string) => EventSource
  retryMinMs?: number
  retryMaxMs?: number
}

export interface AgentEventsOptions extends StreamOptions<AgentEvent> {
  agentId: string
}

export type OverviewEventsOptions = StreamOptions<OverviewEvent>

export type InboxEventsOptions = StreamOptions<InboxEvent>

export function agentEventsUrl(agentId: string): string {
  return `${config.apiBase}/agents/${encodeURIComponent(agentId)}/events`
}

// Overview event stream; without scope parameters it includes the Agents in every project that is not deleted and in the platform;
// filtered by project or platform it includes only that scope.
export function overviewEventsUrl(scope: ScopeQuery = {}): string {
  const query = new URLSearchParams()
  if (scope.projectId) query.set('projectId', scope.projectId)
  if (scope.platform) query.set('platform', 'true')
  const search = query.toString()
  return `${config.apiBase}/overview/events${search ? `?${search}` : ''}`
}

// A stream as its own endpoint serves it (url) and as GET /events names it (stream).
export interface StreamTarget {
  url: string
  stream: string
}

export function agentEventsTarget(agentId: string): StreamTarget {
  return { url: agentEventsUrl(agentId), stream: `agent:${agentId}` }
}

export function overviewEventsTarget(scope: ScopeQuery = {}): StreamTarget {
  return { url: overviewEventsUrl(scope), stream: scope.projectId ? `overview:project:${scope.projectId}` : scope.platform ? 'overview:platform' : 'overview' }
}

export function inboxEventsTarget(): StreamTarget {
  return { url: inboxEventsUrl(), stream: 'inbox' }
}

// Open an Agent's event stream. The return value closes the connection.
export function connectAgentEvents({ agentId, ...options }: AgentEventsOptions): () => void {
  return connectEventStream(agentEventsTarget(agentId), AGENT_EVENT_NAMES, options)
}

// Open the overview event stream. The return value closes the connection.
export function connectOverviewEvents({ scope, ...options }: OverviewEventsOptions & { scope?: ScopeQuery }): () => void {
  return connectEventStream(overviewEventsTarget(scope), OVERVIEW_EVENT_NAMES, options)
}

export function inboxEventsUrl(): string {
  return `${config.apiBase}/inbox/events`
}

// Open the inbox event stream. The return value closes the connection.
export function connectInboxEvents(options: InboxEventsOptions): () => void {
  return connectEventStream(inboxEventsTarget(), INBOX_EVENT_NAMES, options)
}

// Each stream on an EventSource of its own, at its own endpoint, instead of one GET /events for all: for the tests of the views,
// which drive those endpoints' events directly (the shared path has tests of its own in shared-events/).
let ownStreams = false
export function useOwnEventStreams(on = true) {
  ownStreams = on
}

// Open an event stream. Every stream of every console tab goes over one GET /events connection, held by a shared worker where the
// browser has one (shared-events/), otherwise by the tab; either way the caller gets the same events and states as from the
// stream's own endpoint. A caller that supplies its own EventSource gets the stream at its own endpoint. The return value closes it.
export function connectEventStream<E extends { type: string; id: string; data: unknown }>(
  target: StreamTarget,
  names: readonly E['type'][],
  options: StreamOptions<E>,
): () => void {
  const shared = options.createEventSource || ownStreams ? null : sharedStreams()
  if (shared) return shared.subscribe(target.stream, names, options)
  return connectLocalEventStream(target.url, names, options)
}

export { compareEventIds } from './event-ids'
