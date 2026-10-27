import { act, render, screen, within } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { makeRun, makeStatus } from '@/test/api'
import { makeSleep } from '@/test/sleep'
import { makeAgentOverview, makeUsageRow } from '@/test/overview'
import { initialOverviewState, overviewReducer, summarize, type OverviewState } from './overview-state'
import { RunStatusSection } from './RunStatusSection'
import { aggregateUsage, sumUsage, usageRangeStart } from './usage'

describe('运行时长与本轮已用时间在页面上走动', () => {
  afterEach(() => vi.useRealTimers())

  it('Pi 进程已运行 3 分钟，停留 1 分钟后显示约 4 分钟；本轮已用时间同样走动', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-13T10:03:00Z'))
    const agent = makeAgentOverview({ name: '开发助手' })
    agent.status = makeStatus(agent.agentId, {
      busy: true,
      processState: 'busy',
      processPid: 48213,
      processStartedAt: '2026-10-13T10:00:00Z',
      currentRun: makeRun({ status: 'running', startedAt: '2026-10-13T10:02:30Z', endedAt: null }),
    })
    const live: OverviewState = { ...initialOverviewState, synced: true, connection: 'open', agents: [agent] }
    const router = createMemoryRouter([{ path: '/', element: <RunStatusSection live={live} /> }])
    render(<RouterProvider router={router} />)
    const row = within(screen.getByRole('table')).getByText('开发助手').closest('tr')!
    expect(row).toHaveTextContent('已运行 3 分 0 秒')
    expect(row).toHaveTextContent('已用 30 秒')
    await act(async () => vi.advanceTimersByTime(60_000))
    expect(row).toHaveTextContent('已运行 4 分 0 秒')
    expect(row).toHaveTextContent('已用 1 分 30 秒')
  })
})

describe('用量时间段的边界（浏览器本地时区）', () => {
  const tz = process.env.TZ
  beforeAll(() => {
    process.env.TZ = 'Asia/Shanghai'
  })
  afterAll(() => {
    process.env.TZ = tz
  })

  it('今天从本地零点开始', () => {
    expect(usageRangeStart('today', new Date('2026-10-14T02:30:00Z')).toISOString()).toBe('2026-10-13T16:00:00.000Z')
  })

  it('本周从周一零点开始：周三、周日、周一零点之后都回到同一个周一', () => {
    const monday = '2026-10-11T16:00:00.000Z' // 2026-10-12（周一）00:00 +08:00
    expect(usageRangeStart('week', new Date('2026-10-14T02:30:00Z')).toISOString()).toBe(monday) // 周三 10:30
    expect(usageRangeStart('week', new Date('2026-10-18T15:59:59Z')).toISOString()).toBe(monday) // 周日 23:59:59
    expect(usageRangeStart('week', new Date('2026-10-11T16:00:01Z')).toISOString()).toBe(monday) // 周一 00:00:01
    expect(usageRangeStart('week', new Date('2026-10-11T15:59:59Z')).toISOString()).toBe('2026-10-04T16:00:00.000Z') // 上周日 23:59:59
  })

  it('近 30 天为当前时刻往前 30×24 小时', () => {
    expect(usageRangeStart('month', new Date('2026-10-14T02:30:00Z')).toISOString()).toBe('2026-09-14T02:30:00.000Z')
  })
})

describe('用量的汇总', () => {
  const rows = [
    makeUsageRow({ agentId: 'a', agentName: 'A', model: 'x', input: 100, output: 10, cacheRead: 5, cacheWrite: 1 }),
    makeUsageRow({ agentId: 'b', agentName: 'B', model: 'x', input: 300, output: 30 }),
    makeUsageRow({ agentId: 'a', agentName: 'A', model: 'y', input: 1, output: 1 }),
    makeUsageRow({ agentId: 'b', agentName: 'B', provider: null, model: null, input: 2, output: 0 }),
  ]

  it('合计为四项之和', () => {
    expect(sumUsage(rows)).toEqual({ input: 403, output: 41, cacheRead: 5, cacheWrite: 1, total: 450 })
  })

  it('按 Agent 汇总并按合计从大到小排列', () => {
    expect(aggregateUsage(rows, 'agent').map((line) => [line.agentName, line.total])).toEqual([
      ['B', 332],
      ['A', 118],
    ])
  })

  it('按模型汇总，提供方与模型都为空的归为未知模型', () => {
    const lines = aggregateUsage(rows, 'model')
    expect(lines.map((line) => [line.provider, line.model, line.total])).toEqual([
      ['anthropic', 'x', 446],
      ['anthropic', 'y', 2],
      [null, null, 2],
    ])
  })
})

describe('概览状态', () => {
  const a = makeAgentOverview({ name: 'A' })
  const b = makeAgentOverview({ name: 'B' })
  const at = '2026-10-13T10:00:00Z'

  it('快照整体替换，状态事件按 Agent 替换或追加，移除事件删除，旧事件忽略', () => {
    let state = overviewReducer(initialOverviewState, { type: 'event', at, event: { type: 'snapshot', id: '10-0', data: { agents: [a] } } })
    expect(state.synced).toBe(true)
    state = overviewReducer(state, { type: 'event', at, event: { type: 'agent.status', id: '11-0', data: { agent: b } } })
    state = overviewReducer(state, { type: 'event', at, event: { type: 'agent.status', id: '12-0', data: { agent: { ...a, name: 'A2' } } } })
    expect(state.agents.map((agent) => agent.name)).toEqual(['A2', 'B'])
    state = overviewReducer(state, { type: 'event', at, event: { type: 'agent.status', id: '11-0', data: { agent: { ...a, name: '旧' } } } })
    expect(state.agents[0].name).toBe('A2')
    state = overviewReducer(state, { type: 'event', at, event: { type: 'agent.removed', id: '13-0', data: { agentId: b.agentId, scope: 'project', projectId: null } } })
    expect(state.agents.map((agent) => agent.name)).toEqual(['A2'])
  })

  it('汇总：会话丢失不计入空闲，排队数相加', () => {
    const missing = makeAgentOverview()
    missing.status = makeStatus(missing.agentId, { session: { state: 'missing', epoch: 1 }, queued: { interrupt: 0, normal: 1 } })
    const busy = makeAgentOverview()
    busy.status = makeStatus(busy.agentId, { busy: true, queued: { interrupt: 2, normal: 0 } })
    const summary = summarize([missing, busy, a])
    expect([summary.busy.length, summary.idle.length, summary.sessionMissing.length]).toEqual([1, 1, 1])
    expect(summary.queued).toEqual({ interrupt: 2, normal: 1 })
  })
})

it('概览 sleep 属空闲且旧快照/null 不覆盖新 scope revision', () => {
  const agent = makeAgentOverview(), sleep = makeSleep({ agentId: agent.agentId, revision: 4 })
  agent.status = makeStatus(agent.agentId, { sleep, sleepRevision: 4 })
  let state = overviewReducer(initialOverviewState, { type: 'event', at: sleep.requestedAt, event: { type: 'snapshot', id: '1-0', data: { agents: [agent] } } })
  expect(summarize(state.agents).idle).toHaveLength(1)
  const stale = { ...agent, status: makeStatus(agent.agentId, { sleepRevision: 3, busy: true }) }
  state = overviewReducer(state, { type: 'event', at: sleep.requestedAt, event: { type: 'snapshot', id: '2-0', data: { agents: [stale] } } })
  expect(state.agents[0].status).toMatchObject({ busy: false, sleep, sleepRevision: 4 })
  state = overviewReducer(state, { type: 'event', at: sleep.requestedAt, event: { type: 'agent.status', id: '3-0', data: { agent: stale } } })
  expect(state.agents[0].status.sleep?.id).toBe(sleep.id)
})
