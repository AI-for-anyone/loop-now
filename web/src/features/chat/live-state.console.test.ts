import { expect, it } from 'vitest'
import { makeMessage, makeRun } from '@/test/api'
import { initialLiveState, liveReducer } from './live-state'

it('compact 用终态 run.updated 结束执行，不伪造对话消息', () => {
  const running = makeRun({ status: 'running', endedAt: null })
  const existing = makeMessage({ run: running })
  let state = liveReducer(initialLiveState, { type: 'event', event: { type: 'run.updated', id: '1-0', data: { run: running } } })
  state = { ...state, messages: { [existing.id]: existing } }
  state = liveReducer(state, { type: 'event', event: { type: 'run.updated', id: '2-0', data: { run: { ...running, status: 'completed' } } } })
  expect(state.current).toBeNull()
  expect(state.messages).toEqual({ [existing.id]: existing })
  expect(state.runs[running.id]?.status).toBe('completed')
})

it('普通完成仍通过 run.ended 记录非空回复', () => {
  const running = makeRun({ status: 'running', endedAt: null })
  const completed = { ...running, status: 'completed' as const }
  const reply = makeMessage({ role: 'assistant', run: completed })
  let state = liveReducer(initialLiveState, { type: 'event', event: { type: 'run.updated', id: '1-0', data: { run: running } } })
  state = liveReducer(state, { type: 'event', event: { type: 'run.ended', id: '2-0', data: { run: completed, message: reply } } })
  expect(state.current).toBeNull()
  expect(state.messages[reply.id]).toEqual(reply)
})
