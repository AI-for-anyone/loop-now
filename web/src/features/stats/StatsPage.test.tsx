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
import { currentSidebarLinks, baseHandlers } from '@/test/shell'
import { emptyStatsFixture, statsFixture, type StatsAgentName, type StatsAgentRef } from '@/test/stats-fixture'

const NAMES: StatsAgentName[] = ['web', 'server', 'manager', 'test', 'worker', 'cli']
const region = (name: string) => screen.getByRole('region', { name })
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
  it('“Stats” in the sidebar is a link and becomes the current item when opened; defaults to the last 7 days, all projects, daily buckets', async () => {
    const { requests } = renderStats()
    expect(await screen.findByRole('heading', { name: 'Stats', level: 1 })).toBeInTheDocument()
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Stats'])
    await screen.findByRole('region', { name: 'Key metrics' })
    const { from } = statsWindow('week')
    expect(requests[0].get('bucket')).toBe('day')
    expect(requests[0].get('from')).toBe(localTime(from))
    expect(Date.parse(requests[0].get('to')!)).toBeGreaterThan(from.getTime())
    expect(requests[0].has('projectId')).toBe(false)
    expect(requests[0].has('platform')).toBe(false)
    expect(screen.getByRole('radio', { name: 'Last 7 days' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('combobox', { name: 'Scope' })).toHaveDisplayValue('All projects')
  })

  it('six key metrics: values and notes; total tokens comes with a cost estimated at API prices', async () => {
    renderStats()
    const kpis = await screen.findByRole('region', { name: 'Key metrics' })
    const tile = (label: string) => within(kpis).getByText(label).parentElement!
    expect(kpis.children).toHaveLength(6)
    expect(tile('Spend nobody asked for')).toHaveTextContent('57%84 scheduled wakes, 37.2M')
    expect(tile('Avg context per reply')).toHaveTextContent('91K55 compactions, 21 by web')
    expect(tile('Time to first reply, p50')).toHaveTextContent('6 sp90 2.4 min')
    expect(tile('Failed + interrupted')).toHaveTextContent('7625 failed, 51 interrupted')
    expect(tile('Waiting for your answer')).toHaveTextContent('14You reply in 6 min on average, 2 waiting now')
    expect(tile('Total tokens')).toHaveTextContent(/^Total tokens65\.1MEst\. \$[\d,]+, 79% cache hits$/)
    expect(within(region('Models and processes')).getByText('At API list prices; for reference only')).toBeInTheDocument()
  })

  it('charts with several series have a legend; moving to a column with the keyboard shows a tooltip listing each series and the total', async () => {
    renderStats()
    const origins = await screen.findByRole('region', { name: 'Daily usage by trigger' })
    expect(within(origins).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Your messages', 'Scheduled wakes', 'Agent-to-Agent / tasks', 'Other'])
    expect(within(region('Reply outcomes')).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Completed', 'Interrupted / stopped', 'Failed'])
    expect(within(region('Time to first reply')).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Half within (p50)', '90% within (p90)'])

    const chart = within(origins).getByRole('group')
    fireEvent.keyDown(chart, { key: 'End' })
    const tip = within(origins).getByRole('tooltip')
    expect(chart).toHaveAttribute('aria-describedby', tip.id)
    expect(tip).toHaveTextContent('Today2.1MYour messages4.3MScheduled wakes800KAgent-to-Agent / tasks30KOther7.2MTotal')
    fireEvent.keyDown(chart, { key: 'ArrowLeft' })
    expect(within(origins).getByRole('tooltip')).toHaveTextContent('3.8MYour messages')
    fireEvent.keyDown(chart, { key: 'Escape' })
    expect(within(origins).queryByRole('tooltip')).toBeNull()
  })

  it('an Agent with an average context above 120K is marked “New session due”', async () => {
    renderStats()
    const card = await screen.findByRole('region', { name: 'Context per reply' })
    const row = (name: string) => within(card).getByText(name).closest('li')!
    expect(row('web')).toHaveTextContent('142K21 compactionsNew session due')
    expect(row('test')).toHaveTextContent('98K9 compactions')
    expect(within(card).getAllByText('New session due')).toHaveLength(1)
  })

  it('a question waiting for your answer opens the chat of that Agent; “View its prompt” goes to the prompt block of the Agent whose wakes cost the most', async () => {
    const { refs } = renderStats()
    const questions = await screen.findByRole('region', { name: 'Waiting for your answer' })
    const link = within(questions).getByRole('link', { name: /Sidebar width 244 or 260/ })
    expect(link).toHaveAttribute('href', `${project}/agents/${refs.web.agentId}/chat`)
    expect(link).toHaveTextContent('Waited 5 min')
    const wakes = region('Who burns tokens after waking')
    expect(wakes).toHaveTextContent('84 scheduled wakes, 37.2M in total')
    expect(wakes).toHaveTextContent('Wakes every 4 h, 850K each time')
    expect(wakes).toHaveTextContent('web spends 850K per wake on average, 16× other Agents.')
    expect(within(wakes).getByRole('link', { name: 'View its prompt' })).toHaveAttribute('href', `${project}/agents/${refs.web.agentId}/config#config-prompt`)
  })

  it('reasons for unfinished replies are named by end reason and error code in words; unknown codes show as is', async () => {
    renderStats()
    const card = await screen.findByRole('region', { name: 'Why replies didn\'t finish' })
    const rows = within(card).getAllByRole('listitem').map((item) => item.textContent)
    expect(rows).toEqual(['InterruptedYou sent an interrupt30', 'StoppedYou pressed Stop16', 'FailedThe model service kept failing after retries12', 'FailedPi process exited unexpectedly7', 'Failedrate_limited3', 'FailedWorkspace sync failed3'])
    expect(card).toHaveTextContent('76 total')
    expect(card).toHaveTextContent('12 of 25 failures: The model service kept failing after retries.')
  })

  it('task-turn codes and sleep end reasons are named too', async () => {
    renderStats({
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
    const card = await screen.findByRole('region', { name: 'Why replies didn\'t finish' })
    expect(within(card).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      "InterruptedThe node or its conditions changed, so the message didn't run4",
      "InterruptedServer restarted; the turn wasn't replayed3",
      'InterruptedScheduled wake taken over by new work2',
    ])
  })

  it('the estimated cost counts only models with a price; those without one are listed in the note', async () => {
    renderStats({
      report: (refs) => {
        const report = statsFixture(refs)
        return { ...report, tokens: { ...report.tokens, byModel: [...report.tokens.byModel, { provider: 'p', model: 'house-model-1', total: 1000 }] } }
      },
    })
    const card = await screen.findByRole('region', { name: 'Models and processes' })
    expect(card).toHaveTextContent('Excludes models without a price: house-model-1')
    expect(card).toHaveTextContent('Claude 71%')
  })

  it('switching to “Today” buckets by hour from local midnight; with a project chosen as scope the project ID is sent', async () => {
    const user = userEvent.setup()
    const { requests } = renderStats({ path: `/stats?project=${encodeURIComponent(defaultProject.name)}` })
    await screen.findByRole('region', { name: 'Key metrics' })
    expect(requests.at(-1)!.get('projectId')).toBe(defaultProject.id)
    await user.click(screen.getByRole('radio', { name: 'Today' }))
    await waitFor(() => expect(requests.at(-1)!.get('bucket')).toBe('hour'))
    expect(requests.at(-1)!.get('from')).toBe(localTime(statsWindow('today').from))
    expect(requests.at(-1)!.get('projectId')).toBe(defaultProject.id)
    await user.selectOptions(screen.getByRole('combobox', { name: 'Scope' }), 'Platform')
    await waitFor(() => expect(requests.at(-1)!.get('platform')).toBe('true'))
  })

  it('with nothing recorded in the period it shows only a one-line note and draws no empty charts', async () => {
    renderStats({ report: () => emptyStatsFixture() })
    expect(await screen.findByText('Nothing recorded in this period')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Key metrics' })).toBeNull()
    expect(screen.queryByRole('region', { name: 'Daily usage by trigger' })).toBeNull()
    expect(screen.getByRole('main').querySelector('svg[width]')).toBeNull()
  })
})

// "Printed sheet" redesign, step 4: stats sit on a sheet, numbered 01, 02, ...;
// key metrics are white cards whose shadow color tells their meanings apart.
describe('Stats on a sheet', () => {
  it('the header is the page title: breadcrumb “Global›Stats”, next to it the segmented time range and the scope select', async () => {
    renderStats()
    const header = (await screen.findByRole('heading', { level: 1, name: 'Stats' })).closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Stats')
    expect(within(header).getByRole('radiogroup', { name: 'Time range' })).toHaveClass(
      'rounded-ticket',
      'border',
      'border-hairline-strong',
      '[[data-skin=plate]_&]:border-2',
      '[[data-skin=plate]_&]:border-ink',
      '[[data-skin=plate]_&]:rounded-full',
    )
    expect(within(header).getByRole('combobox', { name: 'Scope' }).parentElement).toHaveClass(
      'border-hairline-strong',
      'bg-surface-control',
      '[[data-skin=plate]_&]:border-dashed',
    )
  })

  it('key metrics are 01, and each chart below is a numbered section placed directly on the sheet, no longer a white card', async () => {
    renderStats()
    const kpis = await screen.findByRole('region', { name: 'Key metrics' })
    const sheet = kpis.closest('[data-sheet]')!
    expect(kpis.parentElement!.querySelector('[data-section-number]')).toHaveTextContent('01')
    const origins = region('Daily usage by trigger')
    expect(sheet).toContainElement(origins)
    expect(origins.querySelector('[data-section-number]')).toHaveTextContent('02')
    expect(origins).not.toHaveClass('bg-white')
    expect(origins).toHaveClass('border-t', 'border-hairline', '[[data-skin=plate]_&]:border-t-2', '[[data-skin=plate]_&]:border-dashed')
    expect(within(origins).getByRole('heading', { name: 'Daily usage by trigger' })).toHaveClass('riso-marker', 'font-display-cn')
    const numbers = [...sheet.querySelectorAll('[data-section-number]')].map((node) => node.textContent)
    expect(numbers).toEqual(numbers.map((_, index) => String(index + 1).padStart(2, '0')))
  })

  it('key metric cards: crisp surface-control cards in mono skin and colored shadow cards in Plate skin; the number is in large Bricolage', async () => {
    renderStats()
    const kpis = await screen.findByRole('region', { name: 'Key metrics' })
    const tile = (label: string) => within(kpis).getByText(label).parentElement!
    for (const label of ['Spend nobody asked for', 'Avg context per reply', 'Time to first reply, p50']) {
      expect(tile(label)).toHaveClass(
        'border',
        'border-hairline',
        'bg-surface-control',
        'shadow-surface',
        '[[data-skin=plate]_&]:border-2',
        '[[data-skin=plate]_&]:border-ink',
        '[[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)]',
      )
    }
    expect(tile('Failed + interrupted')).toHaveClass('[[data-skin=plate]_&]:shadow-[4px_4px_0_var(--riso-pink)]')
    expect(tile('Waiting for your answer')).toHaveClass('[[data-skin=plate]_&]:shadow-[4px_4px_0_var(--riso-yellow)]')
    expect(tile('Total tokens')).toHaveClass('[[data-skin=plate]_&]:shadow-[4px_4px_0_var(--riso-blue)]')
    expect(tile('Total tokens').children[1]).toHaveClass('font-display-latin')
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
