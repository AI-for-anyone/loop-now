import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { makeRun, makeStatus } from '@/test/api'
import { makeTaskDetail } from '@/test/tasks'
import { makeSleep, sleepEvent } from '@/test/sleep'
import { sleepProjections } from './projection'
import type { Schemas } from '@/api/client'

describe('正式 sleep scope 投影', () => {
  it('keeps console participants only with their authoritative task version; newer shared SSE invalidates them until HTTP', () => {
    const store = sleepProjections(new QueryClient()), base = makeTaskDetail('project'), node = base.nodes[1]
    const incoming: Schemas['ConsoleTaskDetail'] = { ...base, larkSwitchParticipants: [{ agentId: node.agentId, runtimeId: node.runtimeId, baseSnapshotSHA: 'a'.repeat(64), larkAllowed: true, bindingRevision: 2, prepared: true, available: true, unavailableReason: null, stepIds: [node.stepId] }] }
    expect(store.task(incoming).larkSwitchParticipants).toEqual(incoming.larkSwitchParticipants)
    const snapshot = { ...base, task: { ...base.task, version: base.task.version + 1 } }
    expect(store.task(snapshot).larkSwitchParticipants).toBeUndefined()
    expect(store.task(incoming).larkSwitchParticipants).toBeUndefined()
    expect(store.task({ ...incoming, task: snapshot.task }).larkSwitchParticipants).toEqual(incoming.larkSwitchParticipants)
  })
  it('新 sleep 不受重复、旧 null、旧终态和旧 HTTP 影响；真实 busy 仍可更新', () => {
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

  it('同 Agent normal 与不同 task/runtime 不串状态；blocked 普通会话不映射到节点', () => {
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

  it('taskVersion 拦截旧 HTTP 空数组，重连新快照清除后旧事件不能复活', () => {
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

  it('事件早于首次 HTTP 时不能发布旧空投影，等待可核对的新快照', () => {
    const store = sleepProjections(new QueryClient()), detail = makeTaskDetail('project-a'), node = detail.nodes[1]
    const sleep = makeSleep({ scopeKind: 'task', taskId: detail.task.id, runtimeId: node.runtimeId, agentId: node.agentId })
    store.update(sleepEvent(sleep, { taskVersion: 3 }), { task: detail.task.id })
    expect(() => store.task(detail)).toThrow(/snapshot/)
    expect(store.currentTask(detail.task.id)).toBeUndefined()
    expect(store.task({ ...detail, task: { ...detail.task, version: 3 }, sleepingInstances: [sleep] }).sleepingInstances).toEqual([sleep])
  })
})
