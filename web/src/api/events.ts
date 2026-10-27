import { config } from '@/config'
import type { ScopeQuery } from '@/lib/scope'
import type { Schemas } from './client'

// 事件名与 OpenAPI 中 x-sse-events 的对应。scripts 中的契约测试会核对两者一致。
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

// 概览事件流：所有 Agent 的运行状态。事件名与 OpenAPI 中 /overview/events 的 x-sse-events 对应。
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

// 收件箱事件流：覆盖全部 Inbox 消息。事件名与 OpenAPI 中 /inbox/events 的 x-sse-events 对应。
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
  // 事件流被浏览器永久关闭（例如服务端返回错误状态）时调用；返回 false 表示不再重连。
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

// 概览事件流；不传范围参数时包含所有未删除项目与平台中的 Agent，按项目或平台筛选时只含该范围。
export function overviewEventsUrl(scope: ScopeQuery = {}): string {
  const query = new URLSearchParams()
  if (scope.projectId) query.set('projectId', scope.projectId)
  if (scope.platform) query.set('platform', 'true')
  const search = query.toString()
  return `${config.apiBase}/overview/events${search ? `?${search}` : ''}`
}

// 打开某个 Agent 的事件流。返回值用于关闭连接。
export function connectAgentEvents({ agentId, ...options }: AgentEventsOptions): () => void {
  return connectEventStream(agentEventsUrl(agentId), AGENT_EVENT_NAMES, options)
}

// 打开概览事件流。返回值用于关闭连接。
export function connectOverviewEvents({ scope, ...options }: OverviewEventsOptions & { scope?: ScopeQuery }): () => void {
  return connectEventStream(overviewEventsUrl(scope), OVERVIEW_EVENT_NAMES, options)
}

export function inboxEventsUrl(): string {
  return `${config.apiBase}/inbox/events`
}

// 打开收件箱事件流。返回值用于关闭连接。
export function connectInboxEvents(options: InboxEventsOptions): () => void {
  return connectEventStream(inboxEventsUrl(), INBOX_EVENT_NAMES, options)
}

// 浏览器的自动重连会带上 Last-Event-ID；永久关闭时按退避间隔重建连接，新连接由服务端先推送快照。
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

// 比较两个 Redis Stream 条目 ID（形如 1700000000000-0）。格式不符时按字符串比较。
export function compareEventIds(a: string, b: string): number {
  const pa = /^(\d+)-(\d+)$/.exec(a)
  const pb = /^(\d+)-(\d+)$/.exec(b)
  if (!pa || !pb) return a === b ? 0 : a < b ? -1 : 1
  const ms = BigInt(pa[1]) - BigInt(pb[1])
  if (ms !== 0n) return ms < 0n ? -1 : 1
  const seq = BigInt(pa[2]) - BigInt(pb[2])
  return seq === 0n ? 0 : seq < 0n ? -1 : 1
}
