import { describe, expect, it } from 'vitest'
import { makeMessage, makeRun } from '@/test/api'
import type { MessagePages } from '@/api/queries'
import { initialLiveState, liveReducer, type LiveState } from './live-state'
import { buildTurns } from './turns'

const page = (messages: ReturnType<typeof makeMessage>[]): MessagePages => ({ pages: [{ messages, hasMore: false, hasMoreAfter: false }], pageParams: [null] })

describe('buildTurns', () => {
  it('已处理的轮次按开始执行的时间排列，打断消息排在比它早发出的普通排队消息之前', () => {
    const long = makeRun({ status: 'interrupted', endReason: 'user_interrupt', startedAt: '2026-10-12T13:33:47.352Z', queuedAt: '2026-10-12T13:33:47.351Z' })
    const a = makeRun({ status: 'completed', whenBusy: 'queue', queuedAt: '2026-10-12T13:33:50.089Z', startedAt: '2026-10-12T13:34:02.230Z' })
    const b = makeRun({ status: 'completed', whenBusy: 'interrupt', queuedAt: '2026-10-12T13:34:02.220Z', startedAt: '2026-10-12T13:34:02.225Z' })
    const { turns } = buildTurns(
      page([makeMessage({ text: '长任务', run: long }), makeMessage({ text: 'A', run: a }), makeMessage({ text: 'B', run: b })]),
      initialLiveState,
    )
    expect(turns.map((turn) => turn.user?.text)).toEqual(['长任务', 'B', 'A'])
  })

  it('按时间数值排列：小数位数不同、以 Z 结尾的时间不按字符串比较', () => {
    // Go 的 RFC3339Nano 去掉末尾的 0：05.120000 输出为 05.12Z，06.000000 输出为 06Z。
    const r1 = makeRun({ startedAt: '2026-10-12T10:00:05.12Z' })
    const r2 = makeRun({ startedAt: '2026-10-12T10:00:05.123456Z' })
    const r3 = makeRun({ startedAt: '2026-10-12T10:00:06Z' })
    const r4 = makeRun({ startedAt: '2026-10-12T10:00:06.000001Z' })
    const r5 = makeRun({ startedAt: '2026-10-12T10:00:06.0000011Z' })
    const { turns } = buildTurns(
      page([r5, r4, r3, r2, r1].map((run, i) => makeMessage({ text: `问${5 - i}`, run }))),
      initialLiveState,
    )
    expect(turns.map((turn) => turn.user?.text)).toEqual(['问1', '问2', '问3', '问4', '问5'])
  })

  it('开始时间相同的轮次保持历史中的原有顺序', () => {
    const at = '2026-10-12T10:00:05.5Z'
    const messages = ['甲', '乙', '丙'].map((text) => makeMessage({ text, run: makeRun({ startedAt: at }) }))
    const { turns } = buildTurns(page(messages), initialLiveState)
    expect(turns.map((turn) => turn.user?.text)).toEqual(['甲', '乙', '丙'])
  })

  it('历史中的执行比实时副本更新时以历史为准，状态不倒退', () => {
    // 历史请求在执行结束后才读取，结束事件尚未到达：实时部分仍认为它在回复中。
    const run = makeRun({ status: 'running', endedAt: null })
    const ended = { ...run, status: 'completed' as const }
    const live: LiveState = { ...initialLiveState, current: { run, text: '写到一半' } }
    const { turns } = buildTurns(page([makeMessage({ text: '问题', run: ended }), makeMessage({ role: 'assistant', text: '最终回复', run: ended })]), live)
    expect(turns).toHaveLength(1)
    expect(turns[0].run.status).toBe('completed')
    expect(turns[0].reply).toEqual({ text: '最终回复', live: false })
  })

  it('事件与发送结果中的消息和执行合并到历史之上，历史请求读到的旧状态不覆盖它们', () => {
    const run = makeRun({ status: 'running', endedAt: null })
    const question = makeMessage({ text: '问题', run })
    const ended = { ...run, status: 'completed' as const }
    const reply = makeMessage({ role: 'assistant', text: '最终回复', run: ended })
    const queuedRun = makeRun({ status: 'queued', whenBusy: 'queue', startedAt: null })
    const sent = makeMessage({ text: '刚发出的', run: queuedRun })
    let live = liveReducer(initialLiveState, { type: 'event', event: { type: 'run.ended', id: '1-0', data: { run: ended, message: reply } } })
    live = liveReducer(live, { type: 'sent', result: { message: sent, run: queuedRun } })
    // 历史请求在执行结束之前读取：执行仍在回复中，没有回复，也没有刚发出的消息。
    const { turns, queued } = buildTurns(page([question]), live)
    expect(turns).toHaveLength(1)
    expect(turns[0].user?.text).toBe('问题')
    expect(turns[0].run.status).toBe('completed')
    expect(turns[0].reply).toEqual({ text: '最终回复', live: false })
    expect(queued.map((turn) => turn.user?.text)).toEqual(['刚发出的'])
  })

  it('同一条消息同时出现在历史与事件中时只显示一次', () => {
    const run = makeRun({ status: 'completed' })
    const question = makeMessage({ text: '问题', run })
    const reply = makeMessage({ role: 'assistant', text: '回复', run })
    const live = liveReducer(initialLiveState, { type: 'event', event: { type: 'run.ended', id: '1-0', data: { run, message: reply } } })
    const { turns } = buildTurns(page([question, reply]), live)
    expect(turns).toHaveLength(1)
    expect(turns[0].reply?.text).toBe('回复')
  })
})
