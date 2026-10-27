import { describe, expect, it } from 'vitest'
import type { AgentEvent } from '@/api/events'
import type { Schemas } from '@/api/client'
import { makeMessage, makeQueueItem, makeRun, makeStatus } from '@/test/api'
import { initialLiveState, isBusy, liveReducer, queuedCounts, type LiveState } from './live-state'

const apply = (state: LiveState, ...events: AgentEvent[]) => events.reduce((s, event) => liveReducer(s, { type: 'event', event }), state)

describe('liveReducer', () => {
  it('快照整体替换实时部分，并以已收文字为准', () => {
    const run = makeRun({ status: 'running' })
    const queued = makeRun({ status: 'queued', whenBusy: 'queue' })
    const state = apply(
      { ...initialLiveState, current: { run: makeRun({ status: 'running' }), text: '旧的' } },
      { type: 'snapshot', id: '5-0', data: { status: makeStatus('a', { busy: true, currentRun: run }), currentText: '已经产生的', queuedRuns: [queued], queueItems: [] } },
    )
    expect(state.current).toEqual({ run, text: '已经产生的' })
    expect(state.queued).toEqual([queued])
    expect(state.snapshots).toBe(1)
    expect(state.lastEventId).toBe('5-0')
    expect(isBusy(state)).toBe(true)
  })

  it('追加文字增量，忽略重复投递的事件', () => {
    const run = makeRun({ status: 'running' })
    const state = apply(
      initialLiveState,
      { type: 'run.updated', id: '1-0', data: { run } },
      { type: 'run.text', id: '2-0', data: { runId: run.id, delta: '你' } },
      { type: 'run.text', id: '3-0', data: { runId: run.id, delta: '好' } },
      { type: 'run.text', id: '3-0', data: { runId: run.id, delta: '好' } },
      { type: 'run.text', id: '2-0', data: { runId: run.id, delta: '你' } },
    )
    expect(state.current?.text).toBe('你好')
  })

  it('按 toolCallId 更新工具状态', () => {
    const run = makeRun({ status: 'running' })
    const at = '2026-10-12T00:00:00Z'
    const state = apply(
      initialLiveState,
      { type: 'run.updated', id: '1-0', data: { run } },
      { type: 'run.tool', id: '2-0', data: { runId: run.id, toolCallId: 't1', toolName: 'bash', state: 'started', at } },
      { type: 'run.tool', id: '3-0', data: { runId: run.id, toolCallId: 't1', toolName: 'bash', state: 'succeeded', at } },
    )
    expect(state.current?.run.tools).toEqual([{ toolCallId: 't1', toolName: 'bash', state: 'succeeded', at }])
  })

  it('打断消息排在已有打断消息之后、所有普通排队消息之前', () => {
    const normal1 = makeRun({ status: 'queued', whenBusy: 'queue' })
    const normal2 = makeRun({ status: 'queued', whenBusy: 'queue' })
    const interrupt1 = makeRun({ status: 'queued', whenBusy: 'interrupt' })
    const interrupt2 = makeRun({ status: 'queued', whenBusy: 'interrupt' })
    const state = apply(
      initialLiveState,
      { type: 'message.created', id: '1-0', data: { message: makeMessage({ run: normal1 }) } },
      { type: 'message.created', id: '2-0', data: { message: makeMessage({ run: normal2 }) } },
      { type: 'message.created', id: '3-0', data: { message: makeMessage({ run: interrupt1 }) } },
      { type: 'message.created', id: '4-0', data: { message: makeMessage({ run: interrupt2 }) } },
    )
    expect(state.queued.map((r) => r.id)).toEqual([interrupt1.id, interrupt2.id, normal1.id, normal2.id])
    expect(queuedCounts(state)).toEqual({ interrupt: 2, normal: 2 })
  })

  it('排队执行开始后离开队列，结束后清除当前执行', () => {
    const run = makeRun({ status: 'queued', whenBusy: 'queue' })
    let state = apply(initialLiveState, { type: 'message.created', id: '1-0', data: { message: makeMessage({ run }) } })
    state = apply(state, { type: 'run.updated', id: '2-0', data: { run: { ...run, status: 'starting', phase: 'waiting_slot' } } })
    expect(state.queued).toEqual([])
    expect(state.current?.run.phase).toBe('waiting_slot')
    state = apply(state, {
      type: 'run.ended',
      id: '3-0',
      data: { run: { ...run, status: 'completed' }, message: makeMessage({ role: 'assistant', run: { ...run, status: 'completed' } }) },
    })
    expect(state.current).toBeNull()
    expect(isBusy(state)).toBe(false)
  })

  it('执行状态只前进不倒退：结束后晚到的打断中更新不让它重新成为当前执行', () => {
    // 打断并发送：结束事件与下一条执行的开始由驱动循环发出，打断中的更新由发送请求发出，可能晚到。
    const a = makeRun({ status: 'running', startedAt: '2026-10-12T08:00:00Z', endedAt: null })
    const b = makeRun({ status: 'queued', whenBusy: 'interrupt', startedAt: null })
    const bStarting = { ...b, status: 'starting' as const, phase: 'launching' as const, startedAt: '2026-10-12T08:00:10Z' }
    let state = apply(
      { ...initialLiveState, current: { run: a, text: '' } },
      { type: 'message.created', id: '9-0', data: { message: makeMessage({ run: b }) } },
      { type: 'run.ended', id: '10-0', data: { run: { ...a, status: 'interrupted', endReason: 'user_interrupt' }, message: makeMessage({ role: 'assistant', run: a }) } },
      { type: 'run.updated', id: '11-0', data: { run: bStarting } },
      { type: 'run.updated', id: '12-0', data: { run: { ...a, status: 'aborting' } } },
      { type: 'run.text', id: '13-0', data: { runId: b.id, delta: 'B 的回复' } },
    )
    expect(state.current).toEqual({ run: bStarting, text: 'B 的回复' })
    expect(state.queued).toEqual([])
    state = apply(state, {
      type: 'run.ended',
      id: '14-0',
      data: { run: { ...bStarting, status: 'completed' }, message: makeMessage({ role: 'assistant', run: { ...bStarting, status: 'completed' } }) },
    })
    expect(state.current).toBeNull()
    expect(isBusy(state)).toBe(false)
  })

  it('开始执行后晚到的排队更新不让它回到队列', () => {
    const run = makeRun({ status: 'queued', whenBusy: 'queue', startedAt: null })
    const starting = { ...run, status: 'starting' as const, phase: 'waiting_slot' as const, startedAt: '2026-10-12T08:00:01Z' }
    const state = apply(
      initialLiveState,
      { type: 'message.created', id: '1-0', data: { message: makeMessage({ run }) } },
      { type: 'run.updated', id: '2-0', data: { run: starting } },
      { type: 'run.updated', id: '3-0', data: { run } },
    )
    expect(state.current?.run).toEqual(starting)
    expect(state.queued).toEqual([])
    expect(queuedCounts(state)).toEqual({ interrupt: 0, normal: 0 })
  })

  it('快照之后晚到的旧执行更新不替换快照中的当前执行', () => {
    const old = makeRun({ status: 'running', startedAt: '2026-10-12T08:00:00Z', endedAt: null })
    const current = makeRun({ status: 'starting', phase: 'launching', startedAt: '2026-10-12T08:00:10Z', endedAt: null })
    const state = apply(
      initialLiveState,
      { type: 'snapshot', id: '20-0', data: { status: makeStatus('a', { busy: true, currentRun: current }), currentText: '', queuedRuns: [], queueItems: [] } },
      { type: 'run.updated', id: '21-0', data: { run: { ...old, status: 'aborting' } } },
    )
    expect(state.current?.run.id).toBe(current.id)
  })

  it('不认识的旧执行先成为当前执行时，较晚开始的执行仍能接替它', () => {
    // 快照时已结束的执行，其打断中更新晚于快照到达；它的结束事件在快照之前，不会再来。
    const old = makeRun({ status: 'aborting', startedAt: '2026-10-12T08:00:00Z', endedAt: null })
    const next = makeRun({ status: 'queued', whenBusy: 'interrupt', startedAt: null })
    const nextStarting = { ...next, status: 'starting' as const, phase: 'launching' as const, startedAt: '2026-10-12T08:00:10Z' }
    const state = apply(
      initialLiveState,
      { type: 'snapshot', id: '20-0', data: { status: makeStatus('a', { busy: true }), currentText: null, queuedRuns: [next], queueItems: [] } },
      { type: 'run.updated', id: '21-0', data: { run: old } },
      { type: 'run.updated', id: '22-0', data: { run: nextStarting } },
    )
    expect(state.current?.run).toEqual(nextStarting)
    expect(state.queued).toEqual([])
  })

  it('记下事件与发送结果中的消息和执行，快照不清除它们', () => {
    const run = makeRun({ status: 'running', endedAt: null })
    const question = makeMessage({ run })
    const ended = { ...run, status: 'completed' as const }
    const reply = makeMessage({ role: 'assistant', run: ended })
    const queuedRun = makeRun({ status: 'queued', whenBusy: 'queue', startedAt: null })
    const sent = makeMessage({ run: queuedRun })
    let state = apply(
      initialLiveState,
      { type: 'message.created', id: '1-0', data: { message: question } },
      { type: 'run.ended', id: '2-0', data: { run: ended, message: reply } },
    )
    state = liveReducer(state, { type: 'sent', result: { message: sent, run: queuedRun } })
    // 发送结果晚于结束事件到达时，不让执行倒退。
    state = liveReducer(state, { type: 'sent', result: { message: question, run } })
    state = apply(state, { type: 'snapshot', id: '3-0', data: { status: makeStatus('a'), currentText: null, queuedRuns: [], queueItems: [] } })
    expect(Object.keys(state.messages)).toEqual([question.id, reply.id, sent.id])
    expect(state.runs[run.id]).toEqual(ended)
    expect(state.runs[queuedRun.id]).toEqual(queuedRun)
    // 发送结果只记入历史部分，忙闲与队列仍以事件为准。
    expect(state.queued).toEqual([])
  })

  it('更新会话状态', () => {
    const state = apply(initialLiveState, { type: 'session.updated', id: '1-0', data: { state: 'missing', epoch: 1 } })
    expect(state.session).toEqual({ state: 'missing', epoch: 1 })
  })

  it('queue.updated 整体替换排队列表，执行的已知状态随之更新', () => {
    const a = makeRun({ status: 'queued', whenBusy: 'queue' })
    const b = makeRun({ status: 'queued', whenBusy: 'queue' })
    const promoted = { ...b, whenBusy: 'interrupt' as const }
    const state = apply(
      initialLiveState,
      { type: 'run.updated', id: '1-0', data: { run: a } },
      { type: 'run.updated', id: '2-0', data: { run: b } },
      { type: 'queue.updated', id: '3-0', data: { queuedRuns: [promoted, a], items: [] } },
    )
    expect(state.queued).toEqual([promoted, a])
    expect(state.runs[b.id]).toEqual(promoted)
    expect(queuedCounts(state)).toEqual({ interrupt: 1, normal: 1 })
  })

  it('快照与 queue.updated 整体替换逐条排队列表；动作回执只在之后没有收到新列表时采用', () => {
    const a = makeRun({ status: 'queued', whenBusy: 'queue' })
    const b = makeRun({ status: 'queued', whenBusy: 'queue' })
    const itemA = makeQueueItem({ runId: a.id, text: '甲' }), itemB = makeQueueItem({ runId: b.id, text: '乙' })
    let state = apply(initialLiveState, { type: 'snapshot', id: '1-0', data: { status: makeStatus('a', { busy: true }), currentText: null, queuedRuns: [a, b], queueItems: [itemA, itemB] } })
    expect(state.queueItems).toEqual([itemA, itemB])
    const seq = state.queueSeq
    const promoted = { ...b, whenBusy: 'interrupt' as const }
    const receipt = (queue: typeof state.queueItems): Schemas['AgentQueueActionReceipt'] => ({ actionId: 'action', agentId: 'a', operation: 'promote_run', status: 'accepted', item: itemB, run: promoted, queue })
    // 回执先到：采用回执里的列表，执行状态随之更新。
    const next = liveReducer(state, { type: 'queueAction', receipt: receipt([{ ...itemB, whenBusy: 'interrupt' }, itemA]), seq })
    expect(next.queueItems.map((item) => item.text)).toEqual(['乙', '甲'])
    expect(next.runs[b.id]).toEqual(promoted)
    // 回执之前已收到更新的列表（a 已开始）：回执里的旧列表不覆盖它。
    state = apply(state, { type: 'queue.updated', id: '2-0', data: { queuedRuns: [promoted], items: [{ ...itemB, whenBusy: 'interrupt' }] } })
    state = liveReducer(state, { type: 'queueAction', receipt: receipt([{ ...itemB, whenBusy: 'interrupt' }, itemA]), seq })
    expect(state.queueItems.map((item) => item.text)).toEqual(['乙'])
  })
})

