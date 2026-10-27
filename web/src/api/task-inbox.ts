import { useSyncExternalStore } from 'react'
import { useInfiniteQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { api, ApiError, call, type Schemas } from './client'
import { taskInboxText as copy } from '@/i18n/task-inbox'

export const taskInboxKeys = {
  records: (taskId: string, nodeId?: string) => nodeId ? ['taskInbox', taskId, nodeId] as const : ['taskInbox', taskId] as const,
  timeline: (taskId: string, nodeId?: string, runId?: string) => ['taskRunTimeline', taskId, ...(nodeId ? [nodeId] : []), ...(runId ? [runId] : [])] as const,
}
type Record = Schemas['TaskNodeInboxRecord']
type Entry = { record: Record; observed: number; eventId: string | null }
class InboxProjection {
  private items = new Map<string, Entry>()
  private scopes = new Map<string, { agentId: string; runtimeId: string }>()
  private clock = 0
  private version = 0
  private scheduled = false
  private listeners = new Set<() => void>()
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener) } }
  snapshot = () => this.version
  private notify() {
    if (this.scheduled) return
    this.scheduled = true
    const flush = () => { this.scheduled = false; this.version++; this.listeners.forEach(fn => fn()) }
    if (typeof requestAnimationFrame === 'function') requestAnimationFrame(flush); else setTimeout(flush, 16)
  }
  private key(task: string, node: string, id: string) { return `${task}:${node}:${id}` }
  scope(taskId: string, nodeRunId: string, agentId: string, runtimeId: string) {
    this.scopes.set(`${taskId}:${nodeRunId}`, { agentId, runtimeId })
    for (const [key, entry] of this.items) if (entry.record.taskId === taskId && entry.record.nodeRunId === nodeRunId && (entry.record.agentId !== agentId || entry.record.runtimeId !== runtimeId)) this.items.delete(key)
  }
  begin() { return ++this.clock }
  private merge(previous: Record, next: Record): Record {
    const links = new Map([...previous.runLinks, ...next.runLinks].map(link => [`${link.runId}:${link.role}`, link]))
    return { ...next, message: { ...next.message, images: next.message.images ?? previous.message.images, readAt: previous.message.readAt ?? next.message.readAt }, runLinks: [...links.values()], notExecuted: previous.notExecuted ?? next.notExecuted }
  }
  read(started: number, next: Record) {
    const key = this.key(next.taskId, next.nodeRunId, next.message.id), previous = this.items.get(key)
    if (previous && (previous.observed > started || previous.record.agentId !== next.agentId || previous.record.runtimeId !== next.runtimeId || previous.record.message.threadId !== next.message.threadId || previous.record.message.inReplyTo !== next.message.inReplyTo)) return
    this.items.set(key, { record: previous ? this.merge(previous.record, next) : next, observed: started, eventId: previous?.eventId ?? null }); this.notify()
  }
  event(taskId: string, id: string, record: Record) {
    if (!/^\d+$/.test(id) || !record || record.taskId !== taskId || typeof record.nodeRunId !== 'string' || typeof record.agentId !== 'string' || typeof record.runtimeId !== 'string' || typeof record.message?.id !== 'string' || typeof record.message.text !== 'string' || !record.message.from || !record.message.to || !Array.isArray(record.runLinks)) return false
    const scope = this.scopes.get(`${taskId}:${record.nodeRunId}`)
    if (scope && (record.agentId !== scope.agentId || record.runtimeId !== scope.runtimeId)) return false
    const key = this.key(taskId, record.nodeRunId, record.message.id), previous = this.items.get(key)
    if (previous && (previous.eventId !== null && BigInt(id) <= BigInt(previous.eventId) || previous.record.agentId !== record.agentId || previous.record.runtimeId !== record.runtimeId || previous.record.message.threadId !== record.message.threadId || previous.record.message.inReplyTo !== record.message.inReplyTo)) return false
    this.items.set(key, { record: previous ? this.merge(previous.record, record) : record, observed: ++this.clock, eventId: id }); this.notify(); return true
  }
  connection(task: string) { const barrier = ++this.clock; for (const entry of this.items.values()) if (entry.record.taskId === task) entry.observed = barrier }
  rows(task: string, node: string) { return [...this.items.values()].map(entry => entry.record).filter(item => item.taskId === task && item.nodeRunId === node).sort((a, b) => a.message.createdAt.localeCompare(b.message.createdAt) || a.message.id.localeCompare(b.message.id)) }
}
const stores = new WeakMap<QueryClient, InboxProjection>()
export function taskInboxProjection(client: QueryClient) { let store = stores.get(client); if (!store) { store = new InboxProjection(); stores.set(client, store) } return store }
export function useTaskInboxMessages(taskId: string, nodeRunId: string, agentId: string, runtimeId: string) {
  const client = useQueryClient(), store = taskInboxProjection(client)
  store.scope(taskId, nodeRunId, agentId, runtimeId)
  useSyncExternalStore(store.subscribe, store.snapshot)
  const query = useInfiniteQuery({ queryKey: taskInboxKeys.records(taskId, nodeRunId), retry: false,
    queryFn: async ({ pageParam, signal }) => {
      const started = store.begin(), page = await call(api.GET('/tasks/{taskId}/nodes/{nodeRunId}/inbox-messages', { signal, params: { path: { taskId, nodeRunId }, query: { limit: 50, ...(pageParam ? { cursor: pageParam } : {}) } } }))
      for (const item of page.items) {
        if (item.taskId !== taskId || item.nodeRunId !== nodeRunId || item.agentId !== agentId || item.runtimeId !== runtimeId) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.scopeMismatch })
        store.read(started, item)
      }
      return page
    }, initialPageParam: null as string | null, getNextPageParam: page => page.nextCursor ?? undefined,
  })
  const items = store.rows(taskId, nodeRunId).filter(item => item.agentId === agentId && item.runtimeId === runtimeId)
  return { ...query, data: query.data || items.length ? { ...(query.data ?? { pages: [], pageParams: [] }), items } : undefined }
}
export async function readTaskInboxAction(taskId: string, nodeRunId: string, actionId: string) {
  return call(api.GET('/tasks/{taskId}/nodes/{nodeRunId}/inbox-actions/{actionId}', { params: { path: { taskId, nodeRunId, actionId } } }))
}
export function validateTaskInboxReceipt(receipt: Schemas['TaskNodeInboxReceipt'], taskId: string, nodeRunId: string, body: Schemas['TaskNodeInboxInput']) {
  if (receipt.actionId !== body.actionId || receipt.taskId !== taskId || receipt.nodeRunId !== nodeRunId || receipt.agentId !== body.expectedAgentId || receipt.runtimeId !== body.expectedRuntimeId || receipt.status !== 'accepted' || receipt.message?.from?.kind !== 'console' || receipt.message?.to?.kind !== 'agent' || receipt.message?.to?.id !== body.expectedAgentId || receipt.message.inReplyTo !== null || receipt.message.threadId !== receipt.message.id || receipt.message.text !== body.text || JSON.stringify((receipt.message.images ?? []).map(image => image.id)) !== JSON.stringify(body.imageIds ?? [])) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.scopeMismatch })
  return receipt
}
export function useTaskRunTimeline(taskId: string, nodeRunId: string, runId: string, agentId: string, runtimeId: string) {
  return useInfiniteQuery({ queryKey: taskInboxKeys.timeline(taskId, nodeRunId, runId), retry: false,
    queryFn: async ({ pageParam, signal }) => {
      const page = await call(api.GET('/tasks/{taskId}/nodes/{nodeRunId}/runs/{runId}/timeline', { signal, params: { path: { taskId, nodeRunId, runId }, query: { limit: 100, ...(pageParam ? { cursor: pageParam } : {}) } } }))
      if (page.taskId !== taskId || page.nodeRunId !== nodeRunId || page.run.id !== runId || page.agentId !== agentId || page.runtimeId !== runtimeId) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.scopeMismatch })
      return page
    }, initialPageParam: null as string | null, getNextPageParam: page => page.nextCursor ?? undefined,
    refetchInterval: query => { const run = query.state.data?.pages.at(-1)?.run; return run && !['completed', 'failed', 'interrupted'].includes(run.status) ? 1500 : false },
  })
}
