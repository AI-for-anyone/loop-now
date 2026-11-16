import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { makeRun, makeStatus } from '@/test/api'
import { makeSleep, sleepEvent } from '@/test/sleep'
import { makeTaskDetail } from '@/test/tasks'
import { initialLiveState, isBusy, liveReducer } from '@/features/chat/live-state'
import { buildSteps } from '@/features/chat/timeline-steps'
import { TaskInstanceSleeps } from './TaskInstanceSleeps'

it('a new normal live sleep is not affected by an old null, a reconnect or a task scope; whether a registered sleep is still busy follows the original status', () => {
  const sleep = makeSleep()
  let state = liveReducer(initialLiveState, { type: 'reset', agentId: sleep.agentId })
  const event = (id: string, data: ReturnType<typeof sleepEvent>) => ({ type: 'event' as const, event: { type: 'sleep.updated' as const, id, data } })
  state = liveReducer(state, event('1-0', sleepEvent(sleep)))
  expect(isBusy(state)).toBe(false)
  state = liveReducer(state, event('2-0', sleepEvent(sleep, { sleep: null, sleepRevision: 1, endedSleepId: 'old' })))
  state = liveReducer(state, { type: 'event', event: { type: 'snapshot', id: '3-0', data: { status: makeStatus(sleep.agentId), currentText: '', queuedRuns: [], queueItems: [] } } })
  expect(state.sleep).toEqual(sleep)
  state = liveReducer(state, event('4-0', sleepEvent(makeSleep({ scopeKind: 'task', taskId: 'task', runtimeId: 'runtime', revision: 8 }))))
  expect(state.sleepRevision).toBe(2)
  state = liveReducer(state, event('5-0', sleepEvent(makeSleep({ id: 'new', revision: 4 }))))
  state = liveReducer(state, event('6-0', sleepEvent(sleep, { sleep: null, sleepRevision: 3, endedSleepId: sleep.id })))
  expect(state.sleep?.id).toBe('new')
})

it('a blocked normal conversation that keeps a historical nodeRunId is still shown on its own; the node projection is not shown twice', () => {
  const detail = makeTaskDetail('project'), node = detail.nodes[1]
  const sleep = makeSleep({ scopeKind: 'task', agentId: node.agentId, taskId: detail.task.id, runtimeId: node.runtimeId, nodeRunId: node.id })
  detail.sleepingInstances = [sleep]
  const view = render(<TaskInstanceSleeps detail={detail} />)
  expect(screen.getByRole('region', { name: 'Sleeping task conversations' })).toBeVisible()
  expect(screen.getByText(`Instance ${node.runtimeId}`)).toBeVisible()
  detail.nodes[1] = { ...node, sleep, sleepRevision: sleep.revision }
  view.rerender(<TaskInstanceSleeps detail={detail} />)
  expect(screen.queryByRole('region', { name: 'Sleeping task conversations' })).not.toBeInTheDocument()
})

it('the sleep Timeline shows the real lifecycle; an invalidated timer that never ran is not shown as a manual interrupt', () => {
  const sleep = makeSleep({ state: 'woken', endReason: 'deadline', wakeRunId: 'wake-run' })
  const steps = buildSteps(makeRun(), [
    { type: 'sleep', at: sleep.requestedAt, sleep },
    { type: 'ended', at: sleep.requestedAt, status: 'interrupted', endReason: 'sleep_superseded', errorCode: null },
  ])
  expect(steps[1].lines).toContain('Woke on schedule')
  expect(steps[1].field).toContain(sleep.id)
  expect(steps[2].tone).toBe('neutral')
  expect(steps[2].label).not.toContain('Interrupted')
})
