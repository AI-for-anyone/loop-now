import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { makeRun, makeStatus } from '@/test/api'
import { makeSleep, sleepEvent } from '@/test/sleep'
import { makeTaskDetail } from '@/test/tasks'
import { initialLiveState, isBusy, liveReducer } from '@/features/chat/live-state'
import { buildSteps } from '@/features/chat/timeline-steps'
import { TaskInstanceSleeps } from './TaskInstanceSleeps'

it('normal live 新 sleep 不受旧 null/重连/任务 scope 影响，已登记仍 busy 由原状态决定', () => {
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

it('blocked 普通会话沿用历史 nodeRunId 仍独立展示；节点投影不重复显示', () => {
  const detail = makeTaskDetail('project'), node = detail.nodes[1]
  const sleep = makeSleep({ scopeKind: 'task', agentId: node.agentId, taskId: detail.task.id, runtimeId: node.runtimeId, nodeRunId: node.id })
  detail.sleepingInstances = [sleep]
  const view = render(<TaskInstanceSleeps detail={detail} />)
  expect(screen.getByRole('region', { name: '任务内普通对话 sleep' })).toBeVisible()
  expect(screen.getByText(`运行实例 ${node.runtimeId}`)).toBeVisible()
  detail.nodes[1] = { ...node, sleep, sleepRevision: sleep.revision }
  view.rerender(<TaskInstanceSleeps detail={detail} />)
  expect(screen.queryByRole('region', { name: '任务内普通对话 sleep' })).not.toBeInTheDocument()
})

it('sleep Timeline 展示真实生命周期，未运行的计时器失效不显示成人工打断', () => {
  const sleep = makeSleep({ state: 'woken', endReason: 'deadline', wakeRunId: 'wake-run' })
  const steps = buildSteps(makeRun(), [
    { type: 'sleep', at: sleep.requestedAt, sleep },
    { type: 'ended', at: sleep.requestedAt, status: 'interrupted', endReason: 'sleep_superseded', errorCode: null },
  ])
  expect(steps[1].lines).toContain('到期唤醒')
  expect(steps[1].field).toContain(sleep.id)
  expect(steps[2].tone).toBe('neutral')
  expect(steps[2].label).not.toContain('用户打断')
})
