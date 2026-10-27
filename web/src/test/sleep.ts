import type { Schemas } from '@/api/client'

export function makeSleep(overrides: Partial<Schemas['SleepRecord']> = {}): Schemas['SleepRecord'] {
  return {
    id: 'sleep-a', scopeKind: 'normal', agentId: 'agent-a', taskId: null, runtimeId: null, nodeRunId: null,
    sourceRunId: 'source-a', state: 'sleeping', seconds: 120,
    requestedAt: '2026-10-15T08:00:00Z', enteredAt: '2026-10-15T08:00:01Z', wakeAt: '2026-10-15T08:02:01Z',
    endedAt: null, endReason: null, wakeRunId: null, reason: '稍后检查', revision: 2, ...overrides,
  }
}
export function sleepEvent(sleep: Schemas['SleepRecord'], overrides: Partial<Schemas['SseSleepUpdated']> = {}): Schemas['SseSleepUpdated'] {
  return {
    scopeKind: sleep.scopeKind, agentId: sleep.agentId, taskId: sleep.taskId, runtimeId: sleep.runtimeId,
    nodeRunId: sleep.nodeRunId, taskVersion: sleep.scopeKind === 'task' ? 3 : null,
    sleepRevision: sleep.revision, sleep, endedSleepId: null, ...overrides,
  }
}
