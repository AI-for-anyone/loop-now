import type { QueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'
import { RevisionLedger } from './revision-ledger'

type Sleep = Schemas['SleepInfo'] | null
export type SleepProjection = Pick<Schemas['AgentStatus'], 'sleep' | 'sleepRevision'>
type Update = Schemas['SseSleepUpdated']
const normalKey = (id: string) => `normal/${id}`
const taskKey = (task: string, runtime: string) => `task/${task}/runtime/${runtime}`
const stores = new WeakMap<QueryClient, SleepProjections>()
export const sleepProjections = (client: QueryClient) => {
  let store = stores.get(client)
  if (!store) { store = new SleepProjections(); stores.set(client, store) }
  return store
}

export function newerSleep(known: SleepProjection, incoming: SleepProjection): SleepProjection {
  return incoming.sleepRevision <= known.sleepRevision ? known : incoming
}
export function activeNormalSleep(sleep: Sleep, agentId: string): sleep is Schemas['SleepInfo'] {
  return sleep?.state === 'sleeping' && sleep.scopeKind === 'normal' && sleep.agentId === agentId && sleep.taskId === null && sleep.runtimeId === null
}

// QueryClient-local UI fences survive page switches, but never persist to disk.
// No timers, requests or workflow transitions originate from this store.
class SleepProjections {
  private ledger = new RevisionLedger<Sleep>()
  private taskScopes = new Map<string, Set<string>>()
  private tasks = new Map<string, Schemas['ConsoleTaskDetail']>()

  normal<T extends SleepProjection>(agentId: string, incoming: T): T {
    const value = activeNormalSleep(incoming.sleep, agentId) ? incoming.sleep : null
    this.ledger.observe(normalKey(agentId), incoming.sleepRevision, value)
    const known = this.ledger.current(normalKey(agentId))!
    if (known.revision === incoming.sleepRevision && known.value === incoming.sleep) return incoming
    return { ...incoming, sleep: known.value, sleepRevision: known.revision }
  }

  status(incoming: Schemas['AgentStatus']): Schemas['AgentStatus'] {
    const next = this.normal(incoming.agentId, incoming)
    // A newer authoritative sleeping projection explicitly means no active run.
    if (next !== incoming && activeNormalSleep(next.sleep, incoming.agentId)) return { ...next, busy: false, currentRun: null, queued: { interrupt: 0, normal: 0 } }
    return next
  }

  list(incoming: Schemas['AgentList']): Schemas['AgentList'] {
    return { ...incoming, agents: incoming.agents.map(agent => {
      const next = this.normal(agent.id, agent)
      return next !== agent && activeNormalSleep(next.sleep, agent.id) ? { ...next, busy: false } : next
    }) }
  }

  taskVersion(id: string) { return this.ledger.ownerVersion(id) }

  task(incoming: Schemas['ConsoleTaskDetail']): Schemas['ConsoleTaskDetail'] {
    const id = incoming.task.id, known = this.tasks.get(id)
    if (!this.ledger.acceptOwner(id, incoming.task.version)) {
      if (known) return known
      // First-load events can outrun both initial HTTP reads. Leave the query
      // retryable instead of publishing an older empty projection as current.
      throw new Error('Task snapshot predates the observed sleep revision')
    }
    const scopes = this.taskScopes.get(id) ?? new Set<string>(), present = new Set<string>()
    // This array is authoritative per runtime. Never infer node mode from its
    // nodeRunId: blocked ordinary conversations can carry historical node IDs.
    for (const sleep of incoming.sleepingInstances) {
      if (sleep.scopeKind !== 'task' || sleep.taskId !== id || !sleep.runtimeId) continue
      const key = taskKey(id, sleep.runtimeId); present.add(key); scopes.add(key)
      this.ledger.observe(key, sleep.revision, sleep)
    }
    for (const key of scopes) if (!present.has(key)) this.ledger.clearFromSnapshot(id, incoming.task.version, key, null)
    this.taskScopes.set(id, scopes)
    const next = {
      ...incoming,
      sleepingInstances: [...scopes].map(key => this.ledger.current(key)?.value).filter((sleep): sleep is Schemas['SleepRecord'] => sleep?.state === 'sleeping'),
      nodes: incoming.nodes.map(node => {
        const previous = known?.nodes.find(old => old.id === node.id && old.runtimeId === node.runtimeId)
        const scoped = this.ledger.current(taskKey(id, node.runtimeId))
        if (scoped && node.sleepRevision < scoped.revision) return { ...node, sleep: previous?.sleepRevision === scoped.revision ? previous.sleep : null, sleepRevision: scoped.revision }
        return previous && node.sleepRevision < previous.sleepRevision ? { ...node, sleep: previous.sleep, sleepRevision: previous.sleepRevision } : node
      }),
    }
    this.tasks.set(id, next)
    return next
  }

  update(event: Update, expected: { normalAgent?: string; task?: string }): boolean {
    if (event.scopeKind === 'normal') {
      if (event.agentId !== expected.normalAgent || event.taskId !== null || event.runtimeId !== null || event.taskVersion !== null) return false
      if (event.sleep && !activeNormalSleep(event.sleep, event.agentId)) return false
      return this.ledger.observe(normalKey(event.agentId), event.sleepRevision, event.sleep)
    }
    if (!expected.task || event.taskId !== expected.task || !event.runtimeId || event.taskVersion === null) return false
    if (event.sleep && (event.sleep.scopeKind !== 'task' || event.sleep.taskId !== event.taskId || event.sleep.runtimeId !== event.runtimeId || event.sleep.agentId !== event.agentId)) return false
    if (event.taskVersion < this.ledger.ownerVersion(event.taskId)) return false
    const key = taskKey(event.taskId, event.runtimeId)
    if (!this.ledger.observe(key, event.sleepRevision, event.sleep)) return false
    this.ledger.acceptOwner(event.taskId, event.taskVersion)
    const scopes = this.taskScopes.get(event.taskId) ?? new Set<string>(); scopes.add(key); this.taskScopes.set(event.taskId, scopes)
    const known = this.tasks.get(event.taskId)
    if (known) this.tasks.set(event.taskId, {
      ...known,
      sleepingInstances: [...known.sleepingInstances.filter(sleep => sleep.runtimeId !== event.runtimeId), ...(event.sleep?.state === 'sleeping' ? [event.sleep] : [])],
      nodes: known.nodes.map(node => node.runtimeId === event.runtimeId && node.sleep && node.sleep.id !== event.sleep?.id
        ? { ...node, sleep: null, sleepRevision: event.sleepRevision } : node),
    })
    return true
  }

  currentTask(id: string) { return this.tasks.get(id) }
}

export function applyNormalSleep(client: QueryClient, agentId: string, event: Update): boolean {
  const store = sleepProjections(client)
  if (!store.update(event, { normalAgent: agentId })) return false
  client.setQueryData<Schemas['AgentStatus']>(['agentStatus', agentId], known => known ? store.status(known) : known)
  client.setQueriesData<Schemas['AgentList']>({ queryKey: ['agents'] }, known => known ? store.list(known) : known)
  return true
}
