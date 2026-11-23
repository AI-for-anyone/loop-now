import { describe, expect, it } from 'vitest'
import { makeMessage, makeMessagePage, makeRun } from '@/test/api'
import type { MessagePages } from '@/api/queries'
import { initialLiveState, liveReducer, type LiveState } from './live-state'
import { buildTurns } from './turns'

const page = (messages: ReturnType<typeof makeMessage>[]): MessagePages => ({ pages: [makeMessagePage(messages)], pageParams: [null] })

describe('buildTurns', () => {
  it('orders processed turns by the time they started running; an interrupt comes before a normal queued message sent earlier', () => {
    const long = makeRun({ status: 'interrupted', endReason: 'user_interrupt', startedAt: '2026-10-12T13:33:47.352Z', queuedAt: '2026-10-12T13:33:47.351Z' })
    const a = makeRun({ status: 'completed', whenBusy: 'queue', queuedAt: '2026-10-12T13:33:50.089Z', startedAt: '2026-10-12T13:34:02.230Z' })
    const b = makeRun({ status: 'completed', whenBusy: 'interrupt', queuedAt: '2026-10-12T13:34:02.220Z', startedAt: '2026-10-12T13:34:02.225Z' })
    const { turns } = buildTurns(
      page([makeMessage({ text: 'Long task', run: long }), makeMessage({ text: 'A', run: a }), makeMessage({ text: 'B', run: b })]),
      initialLiveState,
    )
    expect(turns.map((turn) => turn.user?.text)).toEqual(['Long task', 'B', 'A'])
  })

  it('orders by time value: times with different fraction digits ending in Z are not compared as strings', () => {
    // Go's RFC3339Nano drops trailing zeros: 05.120000 is printed as 05.12Z, and 06.000000 as 06Z.
    const r1 = makeRun({ startedAt: '2026-10-12T10:00:05.12Z' })
    const r2 = makeRun({ startedAt: '2026-10-12T10:00:05.123456Z' })
    const r3 = makeRun({ startedAt: '2026-10-12T10:00:06Z' })
    const r4 = makeRun({ startedAt: '2026-10-12T10:00:06.000001Z' })
    const r5 = makeRun({ startedAt: '2026-10-12T10:00:06.0000011Z' })
    const { turns } = buildTurns(
      page([r5, r4, r3, r2, r1].map((run, i) => makeMessage({ text: `Q${5 - i}`, run }))),
      initialLiveState,
    )
    expect(turns.map((turn) => turn.user?.text)).toEqual(['Q1', 'Q2', 'Q3', 'Q4', 'Q5'])
  })

  it('turns with the same start time keep their order from the history', () => {
    const at = '2026-10-12T10:00:05.5Z'
    const messages = ['Alpha', 'Beta', 'Gamma'].map((text) => makeMessage({ text, run: makeRun({ startedAt: at }) }))
    const { turns } = buildTurns(page(messages), initialLiveState)
    expect(turns.map((turn) => turn.user?.text)).toEqual(['Alpha', 'Beta', 'Gamma'])
  })

  it('when the run in the history is newer than the live copy the history wins and the status does not go back', () => {
    // The history request reads only after the run has ended, but the end event
    // has not arrived yet: the live part still thinks the run is replying.
    const run = makeRun({ status: 'running', endedAt: null })
    const ended = { ...run, status: 'completed' as const }
    const live: LiveState = { ...initialLiveState, current: { run, text: 'Half written' } }
    const { turns } = buildTurns(page([makeMessage({ text: 'Question', run: ended }), makeMessage({ role: 'assistant', text: 'Final reply', run: ended })]), live)
    expect(turns).toHaveLength(1)
    expect(turns[0].run.status).toBe('completed')
    expect(turns[0].reply).toEqual({ text: 'Final reply', live: false })
  })

  it('merges messages and runs from events and send results on top of the history; stale state read by the history request does not overwrite them', () => {
    const run = makeRun({ status: 'running', endedAt: null })
    const question = makeMessage({ text: 'Question', run })
    const ended = { ...run, status: 'completed' as const }
    const reply = makeMessage({ role: 'assistant', text: 'Final reply', run: ended })
    const queuedRun = makeRun({ status: 'queued', whenBusy: 'queue', startedAt: null })
    const sent = makeMessage({ text: 'Just sent', run: queuedRun })
    let live = liveReducer(initialLiveState, { type: 'event', event: { type: 'run.ended', id: '1-0', data: { run: ended, message: reply } } })
    live = liveReducer(live, { type: 'sent', result: { message: sent, run: queuedRun } })
    // The history request read before the run ended: the run is still replying, with no reply and without the message that was just sent.
    const { turns, queued } = buildTurns(page([question]), live)
    expect(turns).toHaveLength(1)
    expect(turns[0].user?.text).toBe('Question')
    expect(turns[0].run.status).toBe('completed')
    expect(turns[0].reply).toEqual({ text: 'Final reply', live: false })
    expect(queued.map((turn) => turn.user?.text)).toEqual(['Just sent'])
  })

  it('shows a message only once when it is in both the history and the events', () => {
    const run = makeRun({ status: 'completed' })
    const question = makeMessage({ text: 'Question', run })
    const reply = makeMessage({ role: 'assistant', text: 'Reply', run })
    const live = liveReducer(initialLiveState, { type: 'event', event: { type: 'run.ended', id: '1-0', data: { run, message: reply } } })
    const { turns } = buildTurns(page([question, reply]), live)
    expect(turns).toHaveLength(1)
    expect(turns[0].reply?.text).toBe('Reply')
  })

  // While a manual compaction (/compact) is in progress, the current run in the status is this
  // compaction (run.compaction is not null). It has no conversation messages and is not a turn.
  it('a manual compaction in progress is not a turn: it stays out of the conversation flow and does not count as “new messages” when locating', () => {
    const asked = makeRun({ startedAt: '2026-10-12T10:00:00Z' })
    const compacting = makeRun({ status: 'running', startedAt: '2026-10-12T10:05:00Z', endedAt: null, compaction: { instructions: '', outcome: null, skipReason: null, tokensBefore: null, estimatedTokensAfter: null } })
    const live: LiveState = { ...initialLiveState, current: { run: compacting, text: '' } }
    const history = page([makeMessage({ text: 'Question', run: asked }), makeMessage({ role: 'assistant', text: 'Answer', run: asked })])
    const latest = buildTurns(history, live)
    expect(latest.turns.map((turn) => turn.run.id)).toEqual([asked.id])
    expect(latest.queued).toEqual([])
    const located = buildTurns(history, live, false)
    expect(located.turns.map((turn) => turn.run.id)).toEqual([asked.id])
    expect(located.hiddenNew).toBe(0)
  })

  it('a normal run in progress (without compaction) is the last turn as usual', () => {
    const replying = makeRun({ status: 'running', startedAt: '2026-10-12T10:05:00Z', endedAt: null })
    const { turns } = buildTurns(page([]), { ...initialLiveState, current: { run: replying, text: 'Writing' } })
    expect(turns.map((turn) => turn.run.id)).toEqual([replying.id])
  })
})
