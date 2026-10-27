import type { QueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'

type Message = Schemas['TaskMessage']
type Entry = { message: Message; observed: number; eventId: string | null }
export type MessageRead = { taskId: string; nodeRunId: string; started: number }
const terminal = (message: Message) => message.status === 'completed' || message.status === 'failed' || message.status === 'interrupted'
const decimal = (id: string) => /^\d+$/.test(id)
const belongs = (a: Message, b: Message) => a.nodeRunId === b.nodeRunId && a.runId === b.runId && a.role === b.role

// UI projection, not a wire DTO. Full-text events and HTTP reads share a local
// observation clock; neither text length nor a Redis cursor decides freshness.
class TaskMessageProjection {
  private tasks = new Map<string, Map<string, Map<string, Entry>>>()
  private clock = 0
  private version = 0
  private listeners = new Set<() => void>()
  private scheduled = false
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener) } }
  snapshot = () => this.version
  private notify() {
    if (this.scheduled) return
    this.scheduled = true
    // Coalesce the received full text at the next animation frame, without
    // waiting for another HTTP request or creating one read per text chunk.
    const flush = () => { this.scheduled = false; this.version++; this.listeners.forEach(listener => listener()) }
    if (typeof requestAnimationFrame === 'function') requestAnimationFrame(flush)
    else setTimeout(flush, 16)
  }
  private node(taskId: string, nodeRunId: string) {
    let task = this.tasks.get(taskId)
    if (!task) { task = new Map(); this.tasks.set(taskId, task) }
    let node = task.get(nodeRunId)
    if (!node) { node = new Map(); task.set(nodeRunId, node) }
    return node
  }
  beginRead(taskId: string, nodeRunId: string): MessageRead { return { taskId, nodeRunId, started: ++this.clock } }
  reconcile(read: MessageRead, messages: Message[]) {
    const node = this.node(read.taskId, read.nodeRunId)
    let changed = false
    const merged = messages.filter(message => message.nodeRunId === read.nodeRunId).map(message => {
      const previous = node.get(message.id)
      if (previous && (!belongs(previous.message, message) || previous.observed > read.started || terminal(previous.message) && !terminal(message))) return previous.message
      node.set(message.id, { message, observed: read.started, eventId: previous?.eventId ?? null }); changed = true
      return message
    })
    if (changed) this.notify()
    return merged
  }
  message(taskId: string, id: string, message: Message) {
    // SSE decoding does not validate JSON. Reject incomplete/foreign-shaped
    // entities instead of making a loading placeholder into a real message.
    if (!decimal(id) || !message || typeof message.id !== 'string' || typeof message.nodeRunId !== 'string' || typeof message.runId !== 'string' || typeof message.text !== 'string' || typeof message.createdAt !== 'string' || !['user', 'assistant'].includes(message.role) || !['queued', 'starting', 'running', 'aborting', 'completed', 'failed', 'interrupted'].includes(message.status)) return false
    const node = this.node(taskId, message.nodeRunId), previous = node.get(message.id)
    if (previous && (!belongs(previous.message, message) || previous.eventId !== null && BigInt(id) <= BigInt(previous.eventId) || terminal(previous.message) && !terminal(message))) return false
    node.set(message.id, { message, observed: ++this.clock, eventId: id }); this.notify()
    return true
  }
  connection(taskId: string) {
    // A new snapshot is not a complete message list. Protect the existing
    // projection from reads begun on the previous connection, then calibrate.
    const barrier = ++this.clock
    for (const node of this.tasks.get(taskId)?.values() ?? []) for (const entry of node.values()) entry.observed = barrier
  }
  messages(taskId: string, nodeRunId: string): Message[] { return [...(this.tasks.get(taskId)?.get(nodeRunId)?.values() ?? [])].map(entry => entry.message) }
}
const stores = new WeakMap<QueryClient, TaskMessageProjection>()
export function taskMessageProjection(client: QueryClient) {
  let store = stores.get(client)
  if (!store) { store = new TaskMessageProjection(); stores.set(client, store) }
  return store
}
export function taskMessageRows(history: Message[], live: Message[], nodeRunId: string) {
  const rows = new Map(history.filter(message => message.nodeRunId === nodeRunId).map(message => [message.id, message]))
  for (const message of live) if (message.nodeRunId === nodeRunId) rows.set(message.id, message)
  return [...rows.values()].sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id))
}
