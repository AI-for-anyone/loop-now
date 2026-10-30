import { act, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { t } from '@/i18n'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, makeRun, makeStatus } from './api'
import { FakeEventSource } from './fake-event-source'
import { renderRoutes } from './render'
import { baseHandlers } from './shell'
import { server } from './server'

let seq = 0
const id = () => `00000000-0000-7000-9000-${String(++seq).padStart(12, '0')}`

// 概览中的一个 Agent，默认空闲、Pi 进程未运行、没有回复过。
export function makeAgentOverview(overrides: Partial<Schemas['AgentOverview']> = {}): Schemas['AgentOverview'] {
  const agentId = overrides.agentId ?? id()
  return {
    agentId,
    scope: 'project',
    projectId: null,
    name: '开发助手',
    model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' },
    status: makeStatus(agentId, { processState: 'stopped' }),
    lastRun: null,
    lastProcessExit: null,
    ...overrides,
  }
}

export function makeUsageRow(overrides: Partial<Schemas['UsageRow']> = {}): Schemas['UsageRow'] {
  const row = {
    agentId: id(),
    scope: 'project' as const,
    projectId: null,
    agentName: '开发助手',
    agentDeleted: false,
    provider: 'anthropic',
    model: 'claude-sonnet-5-5',
    calls: 1,
    input: 100,
    output: 10,
    cacheRead: 0,
    cacheWrite: 0,
    ...overrides,
  }
  return { ...row, total: overrides.total ?? row.input + row.output + row.cacheRead + row.cacheWrite }
}

export function makeAnomaly(overrides: Partial<Schemas['Anomaly']> = {}): Schemas['Anomaly'] {
  return {
    runId: id(),
    scope: 'project',
    projectId: null,
    kind: 'run_failed',
    at: new Date().toISOString(),
    agentId: id(),
    agentName: '开发助手',
    errorCode: 'pi_exited',
    errorMessage: 'exit status 137',
    endReason: null,
    ...overrides,
  }
}

export const usagePage = (rows: Schemas['UsageRow'][]): Schemas['UsagePage'] => ({ from: null, to: null, rows })
export const anomalyPage = (items: Schemas['Anomaly'][], total = items.length, nextCursor: string | null = null): Schemas['AnomalyPage'] => ({
  items,
  total,
  nextCursor,
})

export { makeRun }

interface OverviewSetup {
  usage?: Schemas['UsagePage']
  anomalies?: Schemas['AnomalyPage']
  handlers?: HttpHandler[]
  path?: string
}

// 打开概览页：用量与异常接口默认为空，概览事件流由测试推送。
export async function renderOverview(setup: OverviewSetup = {}) {
  server.use(
    ...(setup.handlers ?? []),
    http.get(apiUrl('/usage'), () => HttpResponse.json(setup.usage ?? usagePage([]))),
    http.get(apiUrl('/anomalies'), () => HttpResponse.json(setup.anomalies ?? anomalyPage([]))),
    ...baseHandlers([]),
  )
  const user = userEvent.setup()
  const result = renderRoutes(routes, setup.path ?? '/')
  await screen.findByRole('heading', { name: t.overview.title, level: 1 })
  const source = () => FakeEventSource.instances.filter((s) => s.url.endsWith('/overview/events')).at(-1)!
  const emit = async (type: string, data: unknown, eventId?: string) => {
    await act(async () => {
      source().emit(type, data, eventId ?? nextId())
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
  }
  const snapshot = async (agents: Schemas['AgentOverview'][]) => {
    await act(async () => source().open())
    await emit('snapshot', { agents })
  }
  return { ...result, user, source, emit, snapshot }
}

let eventSeq = 5000
export const nextId = () => `${++eventSeq}-0`
