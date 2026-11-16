import { config } from '@/config'
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

// Open an Agent's event stream. The return value closes the connection.
export function connectAgentEvents({ agentId, ...options }: AgentEventsOptions): () => void {
  return connectEventStream(agentEventsUrl(agentId), AGENT_EVENT_NAMES, options)
}

// Open the overview event stream. The return value closes the connection.
export function connectOverviewEvents({ scope, ...options }: OverviewEventsOptions & { scope?: ScopeQuery }): () => void {
  return connectEventStream(overviewEventsUrl(scope), OVERVIEW_EVENT_NAMES, options)
}

export function inboxEventsUrl(): string {
  return `${config.apiBase}/inbox/events`
}

// Open the inbox event stream. The return value closes the connection.
export function connectInboxEvents(options: InboxEventsOptions): () => void {
  return connectEventStream(inboxEventsUrl(), INBOX_EVENT_NAMES, options)
}

// The browser's automatic reconnect sends Last-Event-ID; after a permanent close the connection is rebuilt after a backoff interval, and
// on the new connection the server pushes a snapshot first.
export function connectEventStream<E extends { type: string; id: string; data: unknown }>(
  url: string,
  names: readonly E['type'][],
  options: StreamOptions<E>,
): () => void {
  const create = options.createEventSource ?? ((target: string) => new EventSource(target))
  const retryMin = options.retryMinMs ?? config.eventRetryMinMs
  const retryMax = options.retryMaxMs ?? config.eventRetryMaxMs
  let source: EventSource | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let attempts = 0
  let stopped = false

  const open = () => {
    if (stopped) return
    options.onState(attempts === 0 ? 'connecting' : 'reconnecting')
    const es = create(url)
    source = es
    es.onopen = () => {
      attempts = 0
      options.onState('open')
    }
    es.onerror = () => {
      if (stopped || source !== es) return
      if (es.readyState !== EventSource.CLOSED) {
        options.onState('reconnecting')
        return
      }
      es.close()
      source = null
      options.onState('reconnecting')
      void options.onClosed().then((retry) => {
        if (stopped) return
        if (!retry) {
          options.onState('closed')
          return
        }
        const delay = Math.min(retryMax, retryMin * 2 ** attempts)
        attempts += 1
        timer = setTimeout(open, delay)
      })
    }
    for (const name of names) {
      es.addEventListener(name, (message) => {
        if (stopped || source !== es) return
        let data: unknown
        try {
          data = JSON.parse((message as MessageEvent<string>).data)
        } catch {
          return
        }
        options.onEvent({ type: name, id: (message as MessageEvent).lastEventId, data } as E)
      })
    }
  }

  open()
  return () => {
    stopped = true
    if (timer !== null) clearTimeout(timer)
    source?.close()
    source = null
  }
}

// Compare two event stream entry IDs (like 1700000000000-0). Compared as strings when the format does not match.
export function compareEventIds(a: string, b: string): number {
  const pa = /^(\d+)-(\d+)$/.exec(a)
  const pb = /^(\d+)-(\d+)$/.exec(b)
  if (!pa || !pb) return a === b ? 0 : a < b ? -1 : 1
  const ms = BigInt(pa[1]) - BigInt(pb[1])
  if (ms !== 0n) return ms < 0n ? -1 : 1
  const seq = BigInt(pa[2]) - BigInt(pb[2])
  return seq === 0n ? 0 : seq < 0n ? -1 : 1
}
