import { describe, expect, it } from 'vitest'
import type { AgentEvent } from '@/api/events'
import type { Schemas } from '@/api/client'
import { makeMessage, makeQueueItem, makeRun, makeStatus } from '@/test/api'
import { initialLiveState, isBusy, liveReducer, queuedCounts, type LiveState } from './live-state'

const apply = (state: LiveState, ...events: AgentEvent[]) => events.reduce((s, event) => liveReducer(s, { type: 'event', event }), state)

describe('liveReducer', () => {
  it('a snapshot replaces the live part as a whole and its received text wins', () => {
    const run = makeRun({ status: 'running' })
    const queued = makeRun({ status: 'queued', whenBusy: 'queue' })
    const state = apply(
      { ...initialLiveState, current: { run: makeRun({ status: 'running' }), text: 'old' } },
      { type: 'snapshot', id: '5-0', data: { status: makeStatus('a', { busy: true, currentRun: run }), currentText: 'produced so far', queuedRuns: [queued], queueItems: [] } },
    )
    expect(state.current).toEqual({ run, text: 'produced so far' })
    expect(state.queued).toEqual([queued])
    expect(state.snapshots).toBe(1)
    expect(state.lastEventId).toBe('5-0')
    expect(isBusy(state)).toBe(true)
  })

  it('appends text deltas and ignores events delivered twice', () => {
    const run = makeRun({ status: 'running' })
    const state = apply(
      initialLiveState,
      { type: 'run.updated', id: '1-0', data: { run } },
      { type: 'run.text', id: '2-0', data: { runId: run.id, delta: 'Hel' } },
      { type: 'run.text', id: '3-0', data: { runId: run.id, delta: 'lo' } },
      { type: 'run.text', id: '3-0', data: { runId: run.id, delta: 'lo' } },
      { type: 'run.text', id: '2-0', data: { runId: run.id, delta: 'Hel' } },
    )
    expect(state.current?.text).toBe('Hello')
  })

  it('updates tool state by toolCallId', () => {
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

  it('an interrupt message goes after existing interrupt messages and before all normal queued messages', () => {
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

  it('a queued run leaves the queue when it starts, and the current run is cleared when it ends', () => {
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

  it('run state only moves forward: a late aborting update after the end does not make it the current run again', () => {
    // Interrupt and send: the end event and the start of the next run are emitted by the driver
    // loop, while the aborting update is emitted by the send request and may arrive late.
    const a = makeRun({ status: 'running', startedAt: '2026-10-12T08:00:00Z', endedAt: null })
    const b = makeRun({ status: 'queued', whenBusy: 'interrupt', startedAt: null })
    const bStarting = { ...b, status: 'starting' as const, phase: 'launching' as const, startedAt: '2026-10-12T08:00:10Z' }
    let state = apply(
      { ...initialLiveState, current: { run: a, text: '' } },
      { type: 'message.created', id: '9-0', data: { message: makeMessage({ run: b }) } },
      { type: 'run.ended', id: '10-0', data: { run: { ...a, status: 'interrupted', endReason: 'user_interrupt' }, message: makeMessage({ role: 'assistant', run: a }) } },
      { type: 'run.updated', id: '11-0', data: { run: bStarting } },
      { type: 'run.updated', id: '12-0', data: { run: { ...a, status: 'aborting' } } },
      { type: 'run.text', id: '13-0', data: { runId: b.id, delta: 'Reply from B' } },
    )
    expect(state.current).toEqual({ run: bStarting, text: 'Reply from B' })
    expect(state.queued).toEqual([])
    state = apply(state, {
      type: 'run.ended',
      id: '14-0',
      data: { run: { ...bStarting, status: 'completed' }, message: makeMessage({ role: 'assistant', run: { ...bStarting, status: 'completed' } }) },
    })
    expect(state.current).toBeNull()
    expect(isBusy(state)).toBe(false)
  })

  it('a late queued update after the run started does not put it back in the queue', () => {
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

  it('a late update of an old run after a snapshot does not replace the current run from the snapshot', () => {
    const old = makeRun({ status: 'running', startedAt: '2026-10-12T08:00:00Z', endedAt: null })
    const current = makeRun({ status: 'starting', phase: 'launching', startedAt: '2026-10-12T08:00:10Z', endedAt: null })
    const state = apply(
      initialLiveState,
      { type: 'snapshot', id: '20-0', data: { status: makeStatus('a', { busy: true, currentRun: current }), currentText: '', queuedRuns: [], queueItems: [] } },
      { type: 'run.updated', id: '21-0', data: { run: { ...old, status: 'aborting' } } },
    )
    expect(state.current?.run.id).toBe(current.id)
  })

  it('when an unknown old run becomes the current run first, a run that started later can still take over', () => {
    // A run that had already ended at snapshot time: its aborting update arrives after the
    // snapshot, and its end event came before the snapshot, so it will not come again.
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

  it('records messages and runs from events and send results, and a snapshot does not clear them', () => {
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
    // When a send result arrives after the end event, it does not move the run backward.
    state = liveReducer(state, { type: 'sent', result: { message: question, run } })
    state = apply(state, { type: 'snapshot', id: '3-0', data: { status: makeStatus('a'), currentText: null, queuedRuns: [], queueItems: [] } })
    expect(Object.keys(state.messages)).toEqual([question.id, reply.id, sent.id])
    expect(state.runs[run.id]).toEqual(ended)
    expect(state.runs[queuedRun.id]).toEqual(queuedRun)
    // A send result is recorded only in the history part; busy/idle state and the queue still follow the events.
    expect(state.queued).toEqual([])
  })

  it('updates the session state', () => {
    const state = apply(initialLiveState, { type: 'session.updated', id: '1-0', data: { state: 'missing', epoch: 1 } })
    expect(state.session).toEqual({ state: 'missing', epoch: 1 })
  })

  it('queue.updated replaces the queue as a whole and updates the known run state with it', () => {
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

  it('a snapshot and queue.updated replace the queue item list as a whole; an action receipt is used only when no newer list arrived after it', () => {
    const a = makeRun({ status: 'queued', whenBusy: 'queue' })
    const b = makeRun({ status: 'queued', whenBusy: 'queue' })
    const itemA = makeQueueItem({ runId: a.id, text: 'Alpha' }), itemB = makeQueueItem({ runId: b.id, text: 'Beta' })
    let state = apply(initialLiveState, { type: 'snapshot', id: '1-0', data: { status: makeStatus('a', { busy: true }), currentText: null, queuedRuns: [a, b], queueItems: [itemA, itemB] } })
    expect(state.queueItems).toEqual([itemA, itemB])
    const seq = state.queueSeq
    const promoted = { ...b, whenBusy: 'interrupt' as const }
    const receipt = (queue: typeof state.queueItems): Schemas['AgentQueueActionReceipt'] => ({ actionId: 'action', agentId: 'a', operation: 'promote_run', status: 'accepted', item: itemB, run: promoted, queue })
    // The receipt arrives first: use the list in the receipt, and update the run status accordingly.
    const next = liveReducer(state, { type: 'queueAction', receipt: receipt([{ ...itemB, whenBusy: 'interrupt' }, itemA]), seq })
    expect(next.queueItems.map((item) => item.text)).toEqual(['Beta', 'Alpha'])
    expect(next.runs[b.id]).toEqual(promoted)
    // A newer list (a has started) was received before the receipt: the older list in the receipt does not overwrite it.
    state = apply(state, { type: 'queue.updated', id: '2-0', data: { queuedRuns: [promoted], items: [{ ...itemB, whenBusy: 'interrupt' }] } })
    state = liveReducer(state, { type: 'queueAction', receipt: receipt([{ ...itemB, whenBusy: 'interrupt' }, itemA]), seq })
    expect(state.queueItems.map((item) => item.text)).toEqual(['Beta'])
  })
})

