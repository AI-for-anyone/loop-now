import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { makeRun, makeStatus } from '@/test/api'
import { makeTaskDetail } from '@/test/tasks'
import { makeSleep, sleepEvent } from '@/test/sleep'
import { sleepProjections } from './projection'
import type { Schemas } from '@/api/client'

describe('formal sleep scope projection', () => {
  it('keeps console participants only with their authoritative task version; newer shared SSE invalidates them until HTTP', () => {
    const store = sleepProjections(new QueryClient()), base = makeTaskDetail('project'), node = base.nodes[1]
    const incoming: Schemas['ConsoleTaskDetail'] = { ...base, larkSwitchParticipants: [{ agentId: node.agentId, runtimeId: node.runtimeId, baseSnapshotSHA: 'a'.repeat(64), larkAllowed: true, bindingRevision: 2, prepared: true, available: true, unavailableReason: null, stepIds: [node.stepId] }] }
    expect(store.task(incoming).larkSwitchParticipants).toEqual(incoming.larkSwitchParticipants)
    const snapshot = { ...base, task: { ...base.task, version: base.task.version + 1 } }
    expect(store.task(snapshot).larkSwitchParticipants).toBeUndefined()
    expect(store.task(incoming).larkSwitchParticipants).toBeUndefined()
    expect(store.task({ ...incoming, task: snapshot.task }).larkSwitchParticipants).toEqual(incoming.larkSwitchParticipants)
  })
  it('a new sleep is not affected by duplicates, an old null, an old terminal state or old HTTP; a real busy can still update', () => {
    const store = sleepProjections(new QueryClient()), sleep = makeSleep()
    expect(store.update(sleepEvent(sleep), { normalAgent: sleep.agentId })).toBe(true)
    expect(store.update(sleepEvent(sleep), { normalAgent: sleep.agentId })).toBe(false)
    expect(store.update(sleepEvent(sleep, { sleep: null, sleepRevision: 1, endedSleepId: 'previous' }), { normalAgent: sleep.agentId })).toBe(false)
    const stale = store.status(makeStatus(sleep.agentId, { busy: true, currentRun: makeRun({ status: 'running' }) }))
    expect(stale).toMatchObject({ busy: false, currentRun: null, sleep, sleepRevision: 2 })
    const newest = makeSleep({ id: 'sleep-b', revision: 4 })
    store.update(sleepEvent(newest), { normalAgent: sleep.agentId })
    expect(store.update(sleepEvent(sleep, { sleep: null, sleepRevision: 3, endedSleepId: sleep.id }), { normalAgent: sleep.agentId })).toBe(false)
    expect(store.status(makeStatus(sleep.agentId, { sleep, sleepRevision: 2 })).sleep).toEqual(newest)
    store.update(sleepEvent(newest, { sleep: null, sleepRevision: 5, endedSleepId: newest.id }), { normalAgent: sleep.agentId })
    expect(store.status(makeStatus(sleep.agentId, { sleepRevision: 5, busy: true, currentRun: makeRun({ status: 'running' }) })).busy).toBe(true)
  })

  it('normal sleep of the same Agent and different tasks/runtimes do not mix state; a blocked normal conversation is not mapped to a node', () => {
    const store = sleepProjections(new QueryClient()), detail = makeTaskDetail('project-a')
    const node = detail.nodes[1]
    const sleep = makeSleep({ scopeKind: 'task', taskId: detail.task.id, agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id })
    detail.sleepingInstances = [sleep]
    const projected = store.task(detail)
    expect(projected.task.status).toBe('blocked')
    expect(projected.nodes[1].sleep).toBeNull()
    expect(projected.sleepingInstances).toEqual([sleep])
    expect(store.normal(node.agentId, { sleep: null, sleepRevision: 0 }).sleep).toBeNull()
    expect(store.update(sleepEvent(sleep), { normalAgent: node.agentId })).toBe(false)
    expect(store.update(sleepEvent(sleep), { task: 'another-task' })).toBe(false)
    expect(store.task({ ...detail, task: { ...detail.task, id: 'another-task' }, sleepingInstances: [] }).sleepingInstances).toEqual([])
  })

  it('taskVersion blocks an old empty HTTP array; once a new snapshot after reconnecting clears it, old events cannot bring it back', () => {
    const store = sleepProjections(new QueryClient()), detail = makeTaskDetail('project-a')
    const node = detail.nodes[1], sleep = makeSleep({ scopeKind: 'task', taskId: detail.task.id, agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id })
    store.task(detail)
    expect(store.update(sleepEvent(sleep, { taskVersion: 3 }), { task: detail.task.id })).toBe(true)
    expect(store.task(detail).sleepingInstances).toEqual([sleep])
    const next = makeSleep({ ...sleep, id: 'next', revision: 4 })
    store.update(sleepEvent(next, { taskVersion: 5 }), { task: detail.task.id })
    expect(store.update(sleepEvent(sleep, { taskVersion: 4, sleepRevision: 3, sleep: null, endedSleepId: sleep.id }), { task: detail.task.id })).toBe(false)
    expect(store.task(detail).sleepingInstances).toEqual([next])
    const fresh = store.task({ ...detail, task: { ...detail.task, version: 6 } })
    expect(fresh.sleepingInstances).toEqual([])
    expect(store.update(sleepEvent(next, { taskVersion: 5 }), { task: detail.task.id })).toBe(false)
    expect(store.task(detail).sleepingInstances).toEqual([])
  })

  it('when events arrive before the first HTTP response, no stale empty projection is published; it waits for a new snapshot that can be checked', () => {
    const store = sleepProjections(new QueryClient()), detail = makeTaskDetail('project-a'), node = detail.nodes[1]
    const sleep = makeSleep({ scopeKind: 'task', taskId: detail.task.id, runtimeId: node.runtimeId, agentId: node.agentId })
    store.update(sleepEvent(sleep, { taskVersion: 3 }), { task: detail.task.id })
    expect(() => store.task(detail)).toThrow(/snapshot/)
    expect(store.currentTask(detail.task.id)).toBeUndefined()
    expect(store.task({ ...detail, task: { ...detail.task, version: 3 }, sleepingInstances: [sleep] }).sleepingInstances).toEqual([sleep])
  })
})
