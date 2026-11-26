import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { localTime, statsWindow } from '@/api/stats'
import { routes } from '@/app/routes'
import { apiUrl, defaultProject, makeAgent } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, currentSidebarLinks } from '@/test/shell'
import { emptyStatsFixture, statsFixture, type StatsAgentName, type StatsAgentRef } from '@/test/stats-fixture'

const NAMES: StatsAgentName[] = ['web', 'server', 'manager', 'test', 'worker', 'cli']
const region = (name: string) => screen.getByRole('region', { name })
const tab = (name: string | RegExp) => screen.getByRole('tab', { name })
const project = `/p/${encodeURIComponent(defaultProject.name)}`

function renderStats({ path = '/stats', report }: { path?: string; report?: (refs: Record<StatsAgentName, StatsAgentRef>) => Schemas['StatsReport'] } = {}) {
  const agents = NAMES.map((name) => makeAgent({ name }))
  const refs = Object.fromEntries(agents.map((a) => [a.name, { agentId: a.id, projectId: a.projectId }])) as Record<StatsAgentName, StatsAgentRef>
  const requests: URLSearchParams[] = []
  server.use(
    http.get(apiUrl('/stats'), ({ request }) => {
      requests.push(new URL(request.url).searchParams)
      return HttpResponse.json(report ? report(refs) : statsFixture(refs))
    }),
    ...baseHandlers(agents),
  )
  return { ...renderRoutes(routes, path), agents, refs, requests }
}

describe('Stats page', () => {
  it('“Stats” in the sidebar is current; defaults to Usage & Cost tab, last 7 days, all projects, daily buckets', async () => {
    const { requests } = renderStats()
    expect(await screen.findByRole('heading', { name: 'Stats', level: 1 })).toBeInTheDocument()
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Stats'])
    await screen.findByRole('region', { name: 'Key metrics' })
    expect(tab('Usage & Cost')).toHaveAttribute('aria-selected', 'true')
    expect(tab(/^Reliability & Speed/)).toHaveTextContent('27')
    expect(tab('Diagnostics')).toBeInTheDocument()

    const { from } = statsWindow('week')
    expect(requests[0].get('bucket')).toBe('day')
    expect(requests[0].get('from')).toBe(localTime(from))
    expect(Date.parse(requests[0].get('to')!)).toBeGreaterThan(from.getTime())
    expect(requests[0].has('projectId')).toBe(false)
    expect(requests[0].has('platform')).toBe(false)
    expect(screen.getByRole('radio', { name: 'Last 7 days' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('combobox', { name: 'Scope' })).toHaveDisplayValue('All projects')
  })

  it('Usage & Cost tab: 4-cell MetricStrip + OriginsCard, WakesCard, ContextCard, and ModelCostCard', async () => {
    const { refs } = renderStats()
    const kpis = await screen.findByRole('region', { name: 'Key metrics' })
    const cell = (label: string) => within(kpis).getByText(label).parentElement!.parentElement!
    expect(kpis.children).toHaveLength(4)
    expect(cell('Total tokens')).toHaveTextContent(/^Total tokens65\.1MEst\. \$[\d,]+ at API list prices$/)
    expect(cell('Spend nobody asked for')).toHaveTextContent('57%84 scheduled wakes, 37.2M')
    expect(cell('Avg context per reply')).toHaveTextContent('91K55 compactions, 21 by web')
    expect(cell('Cache hit rate')).toHaveTextContent('79%')

    const wakes = region('Who burns tokens after waking')
    expect(wakes).toHaveTextContent('84 scheduled wakes, 37.2M in total')
    expect(wakes).toHaveTextContent('Wakes every 4 h, 850K each time')
    expect(wakes).toHaveTextContent('web spends 850K per wake on average, 16× other Agents.')
    expect(within(wakes).getByRole('link', { name: 'View its prompt' })).toHaveAttribute('href', `${project}/agents/${refs.web.agentId}/config#config-prompt`)

    const context = region('Context per reply')
    const row = (name: string) => within(context).getByText(name).closest('li')!
    expect(row('web')).toHaveTextContent('142K21 compactionsNew session due')
    expect(row('test')).toHaveTextContent('98K9 compactions')
    expect(within(context).getAllByText('New session due')).toHaveLength(1)

    const modelCost = region('Models & cost')
    expect(within(modelCost).getByText('At API list prices; for reference only')).toBeInTheDocument()
    expect(modelCost).toHaveTextContent('Claude 71%')
  })

  it('charts with several series have a legend; moving to a column with the keyboard shows a tooltip listing each series and the total', async () => {
    const user = userEvent.setup()
    renderStats()
    const origins = await screen.findByRole('region', { name: 'Daily usage by trigger' })
    expect(within(origins).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Your messages', 'Scheduled wakes', 'Agent-to-Agent / tasks', 'Other'])

    const chart = within(origins).getByRole('group')
    fireEvent.keyDown(chart, { key: 'End' })
    const tip = within(origins).getByRole('tooltip')
    expect(chart).toHaveAttribute('aria-describedby', tip.id)
    expect(tip).toHaveTextContent('Today2.1MYour messages4.3MScheduled wakes800KAgent-to-Agent / tasks30KOther7.2MTotal')
    fireEvent.keyDown(chart, { key: 'ArrowLeft' })
    expect(within(origins).getByRole('tooltip')).toHaveTextContent('3.8MYour messages')
    fireEvent.keyDown(chart, { key: 'Escape' })
    expect(within(origins).queryByRole('tooltip')).toBeNull()

    await user.click(tab(/^Reliability & Speed/))
    expect(within(region('Reply outcomes')).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Completed', 'Interrupted / stopped', 'Failed'])
    expect(within(region('Time to first reply')).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Half within (p50)', '90% within (p90)'])
  })

  it('Reliability & Speed tab: 4-cell MetricStrip, WaitCard, OutcomesCard, ReasonsCard, and trimmed QuestionsCard', async () => {
    const { refs } = renderStats({ path: '/stats?tab=reliability' })
    const kpis = await screen.findByRole('region', { name: 'Key metrics' })
    const cell = (label: string) => within(kpis).getByText(label).parentElement!.parentElement!
    expect(kpis.children).toHaveLength(4)
    expect(cell('Completion rate')).toHaveTextContent('%')
    expect(cell('Failed + interrupted')).toHaveTextContent('7625 failed, 51 interrupted')
    expect(cell('Time to first reply, p50')).toHaveTextContent('6 sp90 2.4 min')
    expect(cell('Waiting for your answer')).toHaveTextContent('14You reply in 6 min on average, 2 waiting now')

    const questions = region('Waiting for your answer')
    // Trimmed QuestionsCard omits the redundant 3-tile <dl>.
    expect(questions.querySelector('dl')).toBeNull()
    const link = within(questions).getByRole('link', { name: /Sidebar width 244 or 260/ })
    expect(link).toHaveAttribute('href', `${project}/agents/${refs.web.agentId}/chat`)
    expect(link).toHaveTextContent('Waited 5 min')

    const reasons = region("Why replies didn't finish")
    const rows = within(reasons).getAllByRole('listitem').map((item) => item.textContent)
    expect(rows).toEqual([
      'InterruptedYou sent an interrupt30',
      'StoppedYou pressed Stop16',
      'FailedThe model service kept failing after retries12',
      'FailedPi process exited unexpectedly7',
      'Failedrate_limited3',
      'FailedWorkspace sync failed3',
    ])
    expect(reasons).toHaveTextContent('76 total')
    expect(reasons).toHaveTextContent('12 of 25 failures: The model service kept failing after retries.')
  })

  it('task-turn codes and sleep end reasons are named too', async () => {
    renderStats({
      path: '/stats?tab=reliability',
      report: (refs) => {
        const report = statsFixture(refs)
        const reasons = [
          { kind: 'interrupted' as const, code: 'task_conflict', count: 4 },
          { kind: 'interrupted' as const, code: 'server_restarted', count: 3 },
          { kind: 'interrupted' as const, code: 'sleep_superseded', count: 2 },
        ]
        return { ...report, outcomes: { ...report.outcomes, reasons } }
      },
    })
    const card = await screen.findByRole('region', { name: "Why replies didn't finish" })
    expect(within(card).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      "InterruptedThe node or its conditions changed, so the message didn't run4",
      "InterruptedServer restarted; the turn wasn't replayed3",
      'InterruptedScheduled wake taken over by new work2',
    ])
  })

  it('Diagnostics tab: HealthCard links Agent names to chat alongside ToolsCard, trimmed ProcessCard, and BusyCard', async () => {
    const { refs } = renderStats({ path: '/stats?tab=diagnostics' })
    const health = await screen.findByRole('region', { name: 'Agent health' })
    const webLink = within(health).getByRole('link', { name: 'web' })
    expect(webLink).toHaveAttribute('href', `${project}/agents/${refs.web.agentId}/chat`)
    expect(region('Tool calls')).toBeInTheDocument()
    const processCard = region('Process & retries')
    expect(processCard).toHaveTextContent('Model retries')
    expect(processCard).not.toHaveTextContent('Estimated cost')
    expect(region('How busy the day gets')).toBeInTheDocument()
  })

  it('the estimated cost counts only models with a price; those without one are listed in the note', async () => {
    renderStats({
      report: (refs) => {
        const report = statsFixture(refs)
        return { ...report, tokens: { ...report.tokens, byModel: [...report.tokens.byModel, { provider: 'p', model: 'house-model-1', total: 1000 }] } }
      },
    })
    const card = await screen.findByRole('region', { name: 'Models & cost' })
    expect(card).toHaveTextContent('Excludes models without a price: house-model-1')
    expect(card).toHaveTextContent('Claude 71%')
  })

  it('switching scope preserves ?tab= and ?range= in the URL, and switching to Today buckets by hour', async () => {
    const user = userEvent.setup()
    const { requests, router } = renderStats({ path: `/stats?project=${encodeURIComponent(defaultProject.name)}&tab=reliability` })
    await screen.findByRole('region', { name: 'Key metrics' })
    expect(requests.at(-1)!.get('projectId')).toBe(defaultProject.id)
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(`${defaultProject.name}›Stats`)

    await user.click(screen.getByRole('radio', { name: 'Today' }))
    await waitFor(() => expect(requests.at(-1)!.get('bucket')).toBe('hour'))
    expect(requests.at(-1)!.get('from')).toBe(localTime(statsWindow('today').from))
    expect(requests.at(-1)!.get('projectId')).toBe(defaultProject.id)

    await user.selectOptions(screen.getByRole('combobox', { name: 'Scope' }), 'Platform')
    await waitFor(() => expect(requests.at(-1)!.get('platform')).toBe('true'))
    const nextParams = new URLSearchParams(router.state.location.search)
    expect(nextParams.get('platform')).toBe('1')
    expect(nextParams.get('tab')).toBe('reliability')
    expect(nextParams.get('range')).toBe('today')
    expect(nextParams.get('project')).toBeNull()
  })

  it('with nothing recorded in the period it shows only a one-line note and draws no empty charts', async () => {
    renderStats({ report: () => emptyStatsFixture() })
    expect(await screen.findByText('Nothing recorded in this period')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Key metrics' })).toBeNull()
    expect(screen.queryByRole('region', { name: 'Daily usage by trigger' })).toBeNull()
    expect(screen.getByRole('main').querySelector('svg[width]')).toBeNull()
  })
})

describe('Stats layout & styling', () => {
  it('the header shows dynamic scope breadcrumb, date window meta, and height-matched h-8 scope select + range toggle in actions', async () => {
    renderStats()
    const header = (await screen.findByRole('heading', { level: 1, name: 'Stats' })).closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Stats')
    const actions = header.querySelector('[data-page-actions]')!
    expect(within(actions as HTMLElement).getByRole('combobox', { name: 'Scope' }).parentElement).toHaveClass(
      'h-8',
      'border-hairline-strong',
      'bg-surface-control',
      '[[data-skin=plate]_&]:border-dashed',
    )
    expect(within(actions as HTMLElement).getByRole('radiogroup', { name: 'Time range' })).toHaveClass(
      'h-8',
      'rounded-[8px]',
      'border',
      'border-hairline',
      'bg-segment-track',
      '[[data-skin=plate]_&]:border-2',
      '[[data-skin=plate]_&]:border-ink',
      '[[data-skin=plate]_&]:rounded-full',
    )
  })

  it('cards use restrained hairline containers with no chapter numbers or broken HollowNumber headers', async () => {
    renderStats()
    const origins = await screen.findByRole('region', { name: 'Daily usage by trigger' })
    const sheet = origins.closest('[data-sheet]')!
    expect(sheet.querySelector('[data-section-number]')).toBeNull()
    expect(origins).toHaveClass('rounded-card', 'border', 'border-hairline', 'bg-surface-control', 'p-4')
    expect(within(origins).getByRole('heading', { name: 'Daily usage by trigger' })).toHaveClass('text-[13.5px]', 'font-semibold')
  })

  it('each bar in the bar chart uses the chart bar stroke token with a hairline baseline below; series colors use the chart variables', async () => {
    renderStats()
    const origins = await screen.findByRole('region', { name: 'Daily usage by trigger' })
    const svg = origins.querySelector('svg[width]')!
    const outlines = svg.querySelectorAll('rect[data-column-outline]')
    expect(outlines.length).toBeGreaterThan(0)
    for (const outline of outlines) {
      expect(outline).toHaveAttribute('stroke', 'var(--chart-bar-stroke)')
      expect(outline).toHaveAttribute('stroke-width', '2')
      expect(outline).toHaveAttribute('fill', 'none')
    }
    expect(svg.querySelector('line[data-baseline]')).toHaveAttribute('stroke', 'var(--hairline-strong)')
    expect([...svg.querySelectorAll('rect:not([data-column-outline])')].every((rect) => rect.getAttribute('fill')?.startsWith('var(--chart-'))).toBe(true)
  })
})
