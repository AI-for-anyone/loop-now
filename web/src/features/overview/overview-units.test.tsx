import { act, render, screen, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { makeRun, makeStatus } from '@/test/api'
import { createTestQueryClient } from '@/test/render'
import { makeSleep } from '@/test/sleep'
import { makeAgentOverview, makeUsageRow } from '@/test/overview'
import { initialOverviewState, overviewReducer, summarize, type OverviewState } from './overview-state'
import { RunStatusSection } from './RunStatusSection'
import { aggregateUsage, sumUsage, usageRangeStart } from './usage'

describe('Uptime and time used in this reply keep counting on the page', () => {
  afterEach(() => vi.useRealTimers())

  it('the Pi process has been up 3 minutes; after staying 1 minute it shows about 4 minutes; the time used in this reply counts up the same way', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-13T10:03:00Z'))
    const agent = makeAgentOverview({ name: 'dev-helper' })
    agent.status = makeStatus(agent.agentId, {
      busy: true,
      processState: 'busy',
      processPid: 48213,
      processStartedAt: '2026-10-13T10:00:00Z',
      currentRun: makeRun({ status: 'running', startedAt: '2026-10-13T10:02:30Z', endedAt: null }),
    })
    const live: OverviewState = { ...initialOverviewState, synced: true, connection: 'open', agents: [agent] }
    const router = createMemoryRouter([{ path: '/', element: <RunStatusSection live={live} /> }])
    // The section names a failed turn's runtime through the runtime lookup, which reads the runtime definitions.
    render(<QueryClientProvider client={createTestQueryClient()}><RouterProvider router={router} /></QueryClientProvider>)
    const row = within(screen.getByRole('table')).getByText('dev-helper').closest('tr')!
    expect(row).toHaveTextContent('up 3 min 0 s')
    expect(row).toHaveTextContent('30 s elapsed')
    await act(async () => vi.advanceTimersByTime(60_000))
    expect(row).toHaveTextContent('up 4 min 0 s')
    expect(row).toHaveTextContent('1 min 30 s elapsed')
  })
})

describe('Usage period boundaries (browser local time zone)', () => {
  const tz = process.env.TZ
  beforeAll(() => {
    process.env.TZ = 'Asia/Shanghai'
  })
  afterAll(() => {
    process.env.TZ = tz
  })

  it('today starts at local midnight', () => {
    expect(usageRangeStart('today', new Date('2026-10-14T02:30:00Z')).toISOString()).toBe('2026-10-13T16:00:00.000Z')
  })

  it('this week starts at Monday midnight: Wednesday, Sunday and just after Monday midnight all go back to the same Monday', () => {
    const monday = '2026-10-11T16:00:00.000Z' // 2026-10-12 (Monday) 00:00 +08:00
    expect(usageRangeStart('week', new Date('2026-10-14T02:30:00Z')).toISOString()).toBe(monday) // Wednesday 10:30
    expect(usageRangeStart('week', new Date('2026-10-18T15:59:59Z')).toISOString()).toBe(monday) // Sunday 23:59:59
    expect(usageRangeStart('week', new Date('2026-10-11T16:00:01Z')).toISOString()).toBe(monday) // Monday 00:00:01
    expect(usageRangeStart('week', new Date('2026-10-11T15:59:59Z')).toISOString()).toBe('2026-10-04T16:00:00.000Z') // previous Sunday 23:59:59
  })

  it('the last 30 days are 30×24 hours back from now', () => {
    expect(usageRangeStart('month', new Date('2026-10-14T02:30:00Z')).toISOString()).toBe('2026-09-14T02:30:00.000Z')
  })
})

describe('Usage totals', () => {
  const rows = [
    makeUsageRow({ agentId: 'a', agentName: 'A', model: 'x', input: 100, output: 10, cacheRead: 5, cacheWrite: 1 }),
    makeUsageRow({ agentId: 'b', agentName: 'B', model: 'x', input: 300, output: 30 }),
    makeUsageRow({ agentId: 'a', agentName: 'A', model: 'y', input: 1, output: 1 }),
    makeUsageRow({ agentId: 'b', agentName: 'B', provider: null, model: null, input: 2, output: 0 }),
  ]

  it('the total is the sum of the four parts', () => {
    expect(sumUsage(rows)).toEqual({ input: 403, output: 41, cacheRead: 5, cacheWrite: 1, total: 450 })
  })

  it('sums by Agent and sorts by total, largest first', () => {
    expect(aggregateUsage(rows, 'agent').map((line) => [line.agentName, line.total])).toEqual([
      ['B', 332],
      ['A', 118],
    ])
  })

  it('sums by model; rows with neither provider nor model count as an unknown model', () => {
    const lines = aggregateUsage(rows, 'model')
    expect(lines.map((line) => [line.provider, line.model, line.total])).toEqual([
      ['anthropic', 'x', 446],
      ['anthropic', 'y', 2],
      [null, null, 2],
    ])
  })
})

describe('Overview state', () => {
  const a = makeAgentOverview({ name: 'A' })
  const b = makeAgentOverview({ name: 'B' })
  const at = '2026-10-13T10:00:00Z'

  it('a snapshot replaces everything, status events replace or append by Agent, removal events delete, stale events are ignored', () => {
    let state = overviewReducer(initialOverviewState, { type: 'event', at, event: { type: 'snapshot', id: '10-0', data: { agents: [a] } } })
    expect(state.synced).toBe(true)
    state = overviewReducer(state, { type: 'event', at, event: { type: 'agent.status', id: '11-0', data: { agent: b } } })
    state = overviewReducer(state, { type: 'event', at, event: { type: 'agent.status', id: '12-0', data: { agent: { ...a, name: 'A2' } } } })
    expect(state.agents.map((agent) => agent.name)).toEqual(['A2', 'B'])
    state = overviewReducer(state, { type: 'event', at, event: { type: 'agent.status', id: '11-0', data: { agent: { ...a, name: 'Old' } } } })
    expect(state.agents[0].name).toBe('A2')
    state = overviewReducer(state, { type: 'event', at, event: { type: 'agent.removed', id: '13-0', data: { agentId: b.agentId, scope: 'project', projectId: null } } })
    expect(state.agents.map((agent) => agent.name)).toEqual(['A2'])
  })

  it('summary: replying and idle split the Agents, queue counts add up', () => {
    const waiting = makeAgentOverview()
    waiting.status = makeStatus(waiting.agentId, { queued: { interrupt: 0, normal: 1 } })
    const busy = makeAgentOverview()
    busy.status = makeStatus(busy.agentId, { busy: true, queued: { interrupt: 2, normal: 0 } })
    const summary = summarize([waiting, busy, a])
    expect([summary.busy.length, summary.idle.length]).toEqual([1, 2])
    expect(summary.queued).toEqual({ interrupt: 2, normal: 1 })
  })
})

it('overview: sleep counts as idle, and a stale snapshot or null does not overwrite a newer scope revision', () => {
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
