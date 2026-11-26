import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, defaultProject, errorBody, makeAgent, makeRun, makeStatus } from '@/test/api'
import { FakeEventSource } from '@/test/fake-event-source'
import { anomalyPage, makeAgentOverview, makeAnomaly, makeUsageRow, renderOverview, usagePage } from '@/test/overview'
import { baseHandlers } from '@/test/shell'

const section = (name: string) => screen.getByRole('region', { name })
const tab = (name: string | RegExp) => screen.getByRole('tab', { name })

describe('Overview page entry', () => {
  it('opening the root address shows the overview: KPI strip, SubNav tabs, the sidebar in the global scope, and “Overview” is current', async () => {
    const { snapshot, user } = await renderOverview()
    await snapshot([makeAgentOverview()])
    expect(screen.getByRole('list', { name: 'Run status summary' })).toBeInTheDocument()
    expect(section('Run status')).toBeInTheDocument()
    expect(tab(/^Agents/)).toHaveAttribute('aria-selected', 'true')
    expect(tab(/^Issues 24h/)).toBeInTheDocument()
    expect(tab(/^Usage/)).toBeInTheDocument()

    await user.click(tab(/^Usage/))
    expect(section('Usage')).toBeInTheDocument()
    await user.click(tab(/^Issues 24h/))
    expect(section('Issues and failures')).toBeInTheDocument()

    expect(screen.getByRole('button', { name: 'Switch scope, current: Global' })).toBeInTheDocument()
    const links = within(screen.getByRole('navigation', { name: 'Main navigation' })).getAllByRole('link')
    expect(links[1]).toHaveTextContent('Overview')
    expect(links[1]).toHaveAttribute('aria-current', 'page')
    expect(links.filter((link) => link.getAttribute('aria-current') === 'page')).toHaveLength(1)
  })

  it('the overview content area does not show service health; the sidebar connection status shows as usual', async () => {
    const { snapshot } = await renderOverview({
      handlers: [
        http.get(apiUrl('/health'), () =>
          HttpResponse.json({ ok: false, database: { ok: false, error: 'unable to open database file' } }, { status: 503 }),
        ),
      ],
    })
    await snapshot([makeAgentOverview()])
    const status = screen.getByRole('status', { name: 'Connection' })
    await waitFor(() => expect(status).toHaveTextContent('Database unavailable'))
    const main = screen.getByRole('main')
    expect(main).not.toHaveTextContent('unable to open database file')
    expect(main).not.toHaveTextContent('Database unavailable')
  })

  it('with no Agents (once the import page was skipped) it shows the empty state and a way to create an Agent, without the dashboard tabs', async () => {
    window.localStorage.setItem('loop-now.importSkipped', '1')
    const { snapshot, user } = await renderOverview()
    await snapshot([])
    const empty = screen.getByRole('region', { name: 'No Agents yet.' })
    expect(empty).toHaveTextContent('Once you create an Agent, its run status, usage, and issues show up here.')
    expect(screen.queryByRole('tablist', { name: 'Overview sections' })).toBeNull()
    await user.click(within(empty).getByRole('button', { name: 'New Agent' }))
    expect(await screen.findByRole('dialog', { name: 'New Agent' })).toBeInTheDocument()
  })

  it('when only usage fails to load, only the usage tab shows the error, request ID and retry; the rest shows as usual', async () => {
    let fail = true
    const { snapshot, user } = await renderOverview({
      anomalies: anomalyPage([makeAnomaly({ agentName: 'Test helper' })]),
      handlers: [
        http.get(apiUrl('/usage'), () =>
          fail
            ? HttpResponse.json(errorBody('internal_error', 'Internal server error'), {
                status: 500,
                headers: { 'Loop-Now-Request-Id': '01890a5d-ac96-774b-bcce-b302099a8057' },
              })
            : HttpResponse.json(usagePage([makeUsageRow({ input: 1200, output: 34 })])),
        ),
      ],
    })
    await snapshot([makeAgentOverview({ name: 'dev-helper' })])
    expect(within(section('Run status')).getByRole('table')).toHaveTextContent('dev-helper')

    await user.click(tab(/^Issues 24h/))
    expect(await within(section('Issues and failures')).findByText('Test helper')).toBeInTheDocument()
    expect(within(section('Issues and failures')).queryByRole('alert')).toBeNull()

    await user.click(tab(/^Usage/))
    const usage = section('Usage')
    const alert = await within(usage).findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load usage")
    expect(alert).toHaveTextContent('01890a5d-ac96-774b-bcce-b302099a8057')
    fail = false
    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await within(usage).findByText('1,234', { selector: 'span.riso-misreg' })).toBeInTheDocument()
    expect(within(usage).queryByRole('alert')).toBeNull()
  })
})

describe('Run status', () => {
  it('renders a matching 5-cell KPI skeleton while syncing before the initial snapshot arrives', async () => {
    await renderOverview()
    const busyContainers = screen.getByRole('main').querySelectorAll('[aria-busy="true"]')
    expect(busyContainers[0].children).toHaveLength(5)
  })

  it('an Agent starts replying: without reloading, it shows replying and the current phase, and the “Replying” count goes up by one', async () => {
    const agent = makeAgentOverview({ name: 'dev-helper' })
    const { snapshot, emit } = await renderOverview()
    await snapshot([agent, makeAgentOverview({ name: 'Test helper' })])
    const tiles = screen.getByRole('list', { name: 'Run status summary' })
    const busyTile = tiles.querySelector('[data-tile="busy"]')!
    expect(busyTile).toHaveTextContent('0')
    expect(tiles.querySelector('[data-tile="idle"]')).toHaveTextContent('2')

    const run = makeRun({ status: 'starting', phase: 'preparing', endedAt: null, runningAt: null, configVersion: 3 })
    await emit('agent.status', { agent: { ...agent, status: makeStatus(agent.agentId, { busy: true, currentRun: run, processState: 'starting' }) } })
    expect(busyTile).toHaveTextContent('1')
    expect(busyTile).toHaveTextContent('dev-helper')
    const row = within(screen.getByRole('table')).getByText('dev-helper').closest('tr')!
    expect(row).toHaveTextContent('Replying')
    expect(row).toHaveTextContent('Preparing the environment')
    expect(row).toHaveTextContent('config v3')
  })

  it('queue counts, 24h issues, and today tokens appear in the single-row KPI strip with a Stats link', async () => {
    const { snapshot } = await renderOverview({
      usage: usagePage([makeUsageRow({ input: 5000, output: 1000 })]),
      anomalies: anomalyPage([makeAnomaly()], 2),
    })
    const busy = makeAgentOverview({ name: 'A' })
    busy.status = makeStatus(busy.agentId, { busy: true, currentRun: makeRun({ status: 'running', endedAt: null }), queued: { interrupt: 1, normal: 2 } })
    const idle = makeAgentOverview({ name: 'B' })
    await snapshot([busy, idle])
    const tiles = screen.getByRole('list', { name: 'Run status summary' })
    const items = within(tiles).getAllByRole('listitem')
    expect(items).toHaveLength(5)
    expect(tiles.querySelector('[data-tile="idle"]')).toHaveTextContent(/Idle\s*1\s*B/)
    expect(tiles.querySelector('[data-tile="queued"]')).toHaveTextContent('31 interrupt · 2 normal')
    await waitFor(() => expect(tiles.querySelector('[data-tile="issues"]')).toHaveTextContent('2'))
    await waitFor(() => expect(tiles.querySelector('[data-tile="tokens"]')).toHaveTextContent('6,000'))
    expect(within(tiles).getByRole('link', { name: 'Stats' })).toHaveAttribute('href', '/stats')
    const rowA = within(screen.getByRole('table')).getByText('A').closest('tr')!
    expect(rowA).toHaveTextContent('3 messages queued: 1 interrupt, 2 normal')
  })

  it('after the Pi process exits on idle it shows not running, with the time and reason of the exit folded into the state cell', async () => {
    const agent = makeAgentOverview({ name: 'dev-helper' })
    agent.status = makeStatus(agent.agentId, { processState: 'idle', processPid: 4321, processStartedAt: new Date(Date.now() - 60_000).toISOString() })
    const { snapshot, emit } = await renderOverview()
    await snapshot([agent])
    const row = () => within(screen.getByRole('table')).getByText('dev-helper').closest('tr')!
    expect(row()).toHaveTextContent('Ready · PID 4321')
    const exitAt = new Date()
    exitAt.setHours(10, 20, 0, 0)
    await emit('agent.status', {
      agent: { ...agent, status: makeStatus(agent.agentId, { processState: 'stopped' }), lastProcessExit: { at: exitAt.toISOString(), reason: 'idle_timeout' } },
    })
    expect(row()).toHaveTextContent('Stopped')
    expect(row()).not.toHaveTextContent('PID')
    expect(row()).toHaveTextContent('10:20 · Exited after idling')
  })

  it('shows the result and end time of the last reply', async () => {
    const failed = makeAgentOverview({ name: 'A', lastRun: makeRun({ status: 'failed', errorCode: 'session_missing' }) })
    const done = makeAgentOverview({ name: 'B', lastRun: makeRun({ status: 'completed' }) })
    const { snapshot } = await renderOverview()
    await snapshot([failed, done])
    const table = screen.getByRole('table')
    expect(within(table).getByText('A').closest('tr')).toHaveTextContent('Last reply failed: Session not found')
    expect(within(table).getByText('A').closest('tr')).toHaveTextContent('errorCode=session_missing')
    expect(within(table).getByText('B').closest('tr')).toHaveTextContent('Last reply completed')
  })

  it('clicking an Agent opens its chat page', async () => {
    const agent = makeAgent({ name: 'dev-helper' })
    const { snapshot, user } = await renderOverview({ handlers: baseHandlers([agent]) })
    await snapshot([makeAgentOverview({ agentId: agent.id, name: agent.name })])
    await user.click(within(screen.getByRole('table')).getByRole('link', { name: /dev-helper/ }))
    expect(await screen.findByRole('log', { name: 'Conversation' })).toBeInTheDocument()
  })

  it('a deleted Agent leaves the table; a new Agent appears in the table', async () => {
    const a = makeAgentOverview({ name: 'A' })
    const { snapshot, emit } = await renderOverview()
    await snapshot([a])
    await emit('agent.status', { agent: makeAgentOverview({ name: 'C' }) })
    expect(within(screen.getByRole('table')).getByText('C')).toBeInTheDocument()
    await emit('agent.removed', { agentId: a.agentId, scope: 'project', projectId: null })
    expect(within(screen.getByRole('table')).queryByText('A')).toBeNull()
  })

  it('when the event stream drops it shows reconnecting, and after recovery the latest state', async () => {
    const agent = makeAgentOverview({ name: 'dev-helper' })
    const { snapshot, source, emit } = await renderOverview()
    await snapshot([agent])
    expect(screen.getByText('Run status is updating live')).toBeInTheDocument()
    await act(async () => source().fail())
    expect(await screen.findByText('Connection lost, reconnecting…')).toBeInTheDocument()
    await waitFor(() => expect(FakeEventSource.instances.filter((s) => s.url.endsWith('/overview/events'))).toHaveLength(2), { timeout: 3000 })
    await act(async () => source().open())
    await emit('snapshot', { agents: [{ ...agent, name: 'Renamed helper' }] })
    expect(screen.getByText('Run status is updating live')).toBeInTheDocument()
    expect(within(screen.getByRole('table')).getByText('Renamed helper')).toBeInTheDocument()
  })
})

describe('Usage', () => {
  it('shows today by Agent by default; shows the total and its four parts without raw camelCase keys, a total per row with details, and a Stats link', async () => {
    const { snapshot, user } = await renderOverview({
      usage: usagePage([
        makeUsageRow({ agentName: 'dev-helper', input: 92140, output: 8420, cacheRead: 61440, cacheWrite: 4096 }),
        makeUsageRow({ agentName: 'Old helper', agentDeleted: true, input: 100, output: 10 }),
      ]),
    })
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Usage/))
    const usage = section('Usage')
    expect(within(usage).getByRole('radio', { name: 'Today' })).toBeChecked()
    expect(within(usage).getByRole('radio', { name: 'By Agent' })).toBeChecked()
    expect(await within(usage).findByText('Total · Today')).toBeInTheDocument()
    expect(within(usage).getByText('166,206', { selector: 'span.riso-misreg' })).toBeInTheDocument()
    expect(usage).not.toHaveTextContent('cacheRead')
    expect(usage).not.toHaveTextContent('cacheWrite')
    expect(within(usage).getByRole('link', { name: 'Full analytics in Stats' })).toHaveAttribute('href', '/stats')
    const rows = within(usage).getAllByRole('listitem')
    expect(rows[0]).toHaveTextContent('dev-helper')
    expect(rows[0]).toHaveAccessibleDescription('Input 92,140 · Output 8,420 · Cache read 61,440 · Cache write 4,096 · Total 166,096')
    expect(rows[1]).toHaveTextContent('Old helperDeleted')
    expect(screen.getByRole('main')).not.toHaveTextContent(/cost|fee|price|dollar|USD|\$/i)
  })

  it('aligns bar widths and the “Most” badge with the sorted total metric even when cacheRead dominates row #1', async () => {
    const { snapshot, user } = await renderOverview({
      usage: usagePage([
        makeUsageRow({ agentName: 'cache-heavy', input: 1000, output: 500, cacheRead: 98500, cacheWrite: 0 }),
        makeUsageRow({ agentName: 'input-heavy', input: 8000, output: 2000, cacheRead: 0, cacheWrite: 0 }),
      ]),
    })
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Usage/))
    const usage = section('Usage')
    const rows = await within(usage).findAllByRole('listitem')
    expect(rows[0]).toHaveTextContent('cache-heavy')
    expect(within(rows[0]).getByText('Most')).toBeInTheDocument()
    expect(within(rows[1]).queryByText('Most')).toBeNull()
    const totalPct = (row: HTMLElement) =>
      [...row.querySelectorAll<HTMLElement>('[data-usage-part]')].reduce((sum, el) => sum + Number.parseFloat(el.style.width), 0)
    expect(totalPct(rows[0])).toBeCloseTo(100, 1)
    expect(totalPct(rows[1])).toBeCloseTo(10, 1)
  })

  it('switching to this week counts from this Monday at midnight', async () => {
    const froms: string[] = []
    const { snapshot, user } = await renderOverview({
      handlers: [
        http.get(apiUrl('/usage'), ({ request }) => {
          froms.push(new URL(request.url).searchParams.get('from')!)
          return HttpResponse.json(usagePage([makeUsageRow()]))
        }),
      ],
    })
    await snapshot([makeAgentOverview()])
    await waitFor(() => expect(froms).toHaveLength(1))
    await user.click(tab(/^Usage/))
    await user.click(within(section('Usage')).getByRole('radio', { name: 'This week' }))
    await waitFor(() => expect(froms).toHaveLength(2))
    const monday = new Date(froms[1])
    expect(monday.getDay()).toBe(1)
    expect([monday.getHours(), monday.getMinutes(), monday.getSeconds()]).toEqual([0, 0, 0])
    expect(Date.now() - monday.getTime()).toBeLessThan(7 * 24 * 60 * 60 * 1000)
    expect(await within(section('Usage')).findByText('Total · This week')).toBeInTheDocument()
  })

  it('by model, the numbers of one model are the sum over Agents; rows without a model show as an unknown model', async () => {
    const { snapshot, user } = await renderOverview({
      usage: usagePage([
        makeUsageRow({ agentName: 'A', model: 'x', input: 100, output: 10 }),
        makeUsageRow({ agentName: 'B', model: 'x', input: 200, output: 20 }),
        makeUsageRow({ agentName: 'B', provider: null, model: null, input: 5, output: 0 }),
      ]),
    })
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Usage/))
    const usage = section('Usage')
    await within(usage).findByText('Total · Today')
    await user.click(within(usage).getByRole('radio', { name: 'By model' }))
    const rows = within(usage).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('anthropic / x')
    expect(rows[0]).toHaveTextContent('330')
    expect(rows[1]).toHaveTextContent('Unknown model')
  })

  it('shows a compact 1-line empty notice when the chosen period has no usage', async () => {
    const { snapshot, user } = await renderOverview()
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Usage/))
    const empty = await within(section('Usage')).findByRole('status')
    expect(empty).toHaveTextContent('No usage yet · Today')
    expect(empty.querySelector('.text-\\[56px\\]')).toBeNull()
  })
})

describe('Issues and failures', () => {
  it('starts from the last 24 hours, shows the total count, lists newest first, and can load more', async () => {
    const requests: URLSearchParams[] = []
    const first = [makeAnomaly({ agentName: 'B', kind: 'run_interrupted', errorCode: null, errorMessage: null, endReason: 'user_interrupt' })]
    const second = [makeAnomaly({ agentName: 'A', errorCode: 'workspace_sync_failed', errorMessage: 'Clone failed: Authentication failed' })]
    const { snapshot, user } = await renderOverview({
      handlers: [
        http.get(apiUrl('/anomalies'), ({ request }) => {
          const params = new URL(request.url).searchParams
          requests.push(params)
          return HttpResponse.json(params.get('cursor') ? anomalyPage(second, 2) : anomalyPage(first, 2, 'c1'))
        }),
      ],
    })
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Issues 24h/))
    const list = section('Issues and failures')
    expect(await within(list).findByText('Last 24 hours · 2')).toBeInTheDocument()
    const from = Date.parse(requests[0].get('from')!)
    expect(Math.abs(Date.now() - 24 * 60 * 60 * 1000 - from)).toBeLessThan(60_000)
    await user.click(within(list).getByRole('button', { name: 'Load more' }))
    await waitFor(() => expect(within(list).getAllByRole('link')).toHaveLength(2))
    expect(requests[1].get('cursor')).toBe('c1')
    expect(requests[1].get('from')).toBe(requests[0].get('from'))
    const [b, a] = within(list).getAllByRole('link')
    expect(b).toHaveTextContent('Interrupted')
    expect(b).toHaveTextContent('endReason=user_interrupt')
    expect(a).toHaveTextContent('errorCode=workspace_sync_failed')
    expect(a).toHaveTextContent('Workspace sync failed: Clone failed: Authentication failed')
    expect(within(list).queryByRole('button', { name: 'Load more' })).toBeNull()
  })

  it("a runtime failure names the turn's runtime; with none known it says “the runtime”", async () => {
    const { snapshot, user } = await renderOverview({
      anomalies: anomalyPage([
        makeAnomaly({ agentName: 'A', runtime: 'claude-code', errorCode: 'runtime_unavailable', errorMessage: 'claude: command not found' }),
        makeAnomaly({ agentName: 'B', runtime: null, errorCode: 'runtime_start_failed', errorMessage: 'exit status 1' }),
      ]),
    })
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Issues 24h/))
    const [a, b] = await within(section('Issues and failures')).findAllByRole('link')
    await waitFor(() => expect(a).toHaveTextContent("Claude Code can't be used on this computer: it wasn't found, its version couldn't be read, or it's too old: claude: command not found"))
    expect(b).toHaveTextContent("The runtime couldn't start: exit status 1")
  })

  it('a failed CLI install shows as failed, with the CLI name in the reason', async () => {
    const { snapshot, user } = await renderOverview({
      anomalies: anomalyPage([makeAnomaly({ errorCode: 'cli_install_failed', errorMessage: 'gh 2.100.0: the downloaded file does not match the SHA-256 in the template' })]),
    })
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Issues 24h/))
    const item = await within(section('Issues and failures')).findByRole('link')
    expect(item).toHaveTextContent('Failed')
    expect(item).toHaveTextContent('CLI install failed: gh 2.100.0')
  })

  it('every item jumps to its reply, a missing session as well (nothing is left to confirm there)', async () => {
    const missing = makeAnomaly({ errorCode: 'session_missing', errorMessage: null })
    const other = makeAnomaly()
    const { snapshot, user } = await renderOverview({ anomalies: anomalyPage([missing, other]) })
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Issues 24h/))
    const [a, b] = await within(section('Issues and failures')).findAllByRole('link')
    expect(a).toHaveAttribute('href', `/agents/${missing.agentId}/chat#run-${missing.runId}`)
    expect(a).toHaveTextContent('View reply')
    expect(b).toHaveAttribute('href', `/agents/${other.agentId}/chat#run-${other.runId}`)
    expect(b).toHaveTextContent('View reply')
  })

  it('a failed manual compaction reads “Compaction failed” with the compaction instructions; “View compaction” jumps to that compaction', async () => {
    const failed = makeAnomaly({
      errorCode: 'pi_error',
      errorMessage: 'compaction failed',
      compaction: { instructions: 'Keep key decisions', outcome: null, skipReason: null, tokensBefore: null, estimatedTokensAfter: null },
    })
    const { snapshot, user } = await renderOverview({ anomalies: anomalyPage([failed]) })
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Issues 24h/))
    const item = await within(section('Issues and failures')).findByRole('link')
    expect(within(item).getByText('Compaction failed')).toBeInTheDocument()
    expect(within(item).queryByText('Failed', { exact: true })).toBeNull()
    expect(item).toHaveTextContent('/compact Keep key decisions')
    expect(item).toHaveTextContent('Agent reported an error: compaction failed')
    expect(item).toHaveTextContent('View compaction')
    expect(item).toHaveAttribute('href', `/agents/${failed.agentId}/chat#run-${failed.runId}`)
  })

  it('a manual compaction without instructions shows only /compact', async () => {
    const failed = makeAnomaly({ errorCode: 'pi_exited', errorMessage: null, compaction: { instructions: '', outcome: null, skipReason: null, tokensBefore: null, estimatedTokensAfter: null } })
    const { snapshot, user } = await renderOverview({ anomalies: anomalyPage([failed]) })
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Issues 24h/))
    const item = await within(section('Issues and failures')).findByRole('link')
    expect(item.querySelector('code')).toHaveTextContent(/^\/compact$/)
  })

  it('shows the empty state when there are no issues in the last 24 hours', async () => {
    const { snapshot, user } = await renderOverview()
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Issues 24h/))
    expect(await within(section('Issues and failures')).findByText('No issues in the last 24 hours.')).toBeInTheDocument()
  })
})

describe('Overview workspace layout', () => {
  it('in Global scope the header shows a 2-way All | Platform toggle, live status, and Import / New Agent actions', async () => {
    const { snapshot, user, router } = await renderOverview()
    await snapshot([makeAgentOverview()])
    const header = screen.getByRole('heading', { level: 1, name: 'Overview' }).closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Overview')
    const meta = header.querySelector('[data-page-meta]')!
    const toggle = within(meta as HTMLElement).getByRole('radiogroup', { name: 'Scope' })
    expect(within(toggle).getByRole('radio', { name: 'All' })).toBeChecked()
    expect(within(toggle).getByRole('radio', { name: 'Platform' })).not.toBeChecked()
    expect(within(meta as HTMLElement).getByText('Run status is updating live')).toBeInTheDocument()

    const actions = header.querySelector('[data-page-actions]')!
    expect(within(actions as HTMLElement).getByRole('link', { name: 'Import' })).toHaveAttribute('href', '/import')
    expect(within(actions as HTMLElement).getByRole('button', { name: 'New Agent' })).toBeInTheDocument()

    await user.click(within(toggle).getByRole('radio', { name: 'Platform' }))
    await waitFor(() => expect(router.state.location.search).toBe('?platform=1'))
  })

  it('inside a project scope the header omits the scope toggle while preserving the project breadcrumb and scoped Stats link', async () => {
    const { user } = await renderOverview({ path: `/?project=${encodeURIComponent(defaultProject.name)}` })
    const stream = () => FakeEventSource.instances.filter((s) => s.url.includes(`projectId=${defaultProject.id}`)).at(-1)
    await waitFor(() => expect(stream()).toBeDefined())
    await act(async () => stream()!.open())
    await act(async () => {
      stream()!.emit('snapshot', { agents: [makeAgentOverview({ projectId: defaultProject.id })] }, '1-0')
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
    const header = screen.getByRole('heading', { level: 1, name: 'Overview' }).closest('header')!
    await waitFor(() => expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(`${defaultProject.name}›Overview`))
    expect(within(header).queryByRole('radiogroup', { name: 'Scope' })).toBeNull()
    expect(within(header).queryByRole('combobox', { name: 'Scope' })).toBeNull()

    const expectedStats = `/stats?project=${encodeURIComponent(defaultProject.name)}`
    expect(within(screen.getByRole('list', { name: 'Run status summary' })).getByRole('link', { name: 'Stats' })).toHaveAttribute('href', expectedStats)
    await user.click(tab(/^Usage/))
    expect(within(section('Usage')).getByRole('link', { name: 'Full analytics in Stats' })).toHaveAttribute('href', expectedStats)
  })

  it('Agent table removes forced 860px overflow, uses 4 columns with hairline borders, and preserves mono/plate styling', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview({ name: 'dev-helper' })])
    const table = screen.getByRole('table')
    expect(table).not.toHaveClass('min-w-[860px]')
    expect(table.querySelectorAll('thead th')).toHaveLength(4)
    expect(table.querySelector('thead tr')).toHaveClass('border-b', 'border-hairline-strong', '[[data-skin=plate]_&]:border-b-2', '[[data-skin=plate]_&]:border-ink')
    const row = within(table).getByText('dev-helper').closest('tr')!
    expect(row).toHaveClass('border-b', 'border-hairline', '[[data-skin=plate]_&]:border-b-[1.5px]', '[[data-skin=plate]_&]:border-dashed')
    expect(within(row).getByText(/ \/ /)).toHaveClass('font-mono')
    const pill = within(row).getByText('Idle').closest('[data-state-pill]')!
    expect(pill).toHaveAttribute('data-state-pill', 'idle')
    expect(pill.querySelector('[aria-hidden="true"]')).toHaveClass('bg-tint-green', '[[data-skin=plate]_&]:rotate-[-3deg]')
  })

  it('usage bars use chart tokens in mono skin and halftone fills in Plate skin; period and grouping are segmented buttons', async () => {
    const { snapshot, user } = await renderOverview({
      usage: usagePage([makeUsageRow({ agentName: 'dev-helper', input: 9000, output: 1000 }), makeUsageRow({ agentName: 'Test helper', input: 2000, output: 500 })]),
    })
    await snapshot([makeAgentOverview()])
    await user.click(tab(/^Usage/))
    const usage = section('Usage')
    const lines = await within(usage).findAllByRole('listitem')
    expect(lines[0]).toHaveTextContent('dev-helper')
    expect(within(lines[0]).getByText('Most')).toHaveClass('bg-surface-active', '[[data-skin=plate]_&]:bg-riso-yellow')
    expect(within(lines[1]).queryByText('Most')).toBeNull()
    const track = lines[0].querySelector('[data-usage-track]')!
    expect(track).toHaveClass('border', 'border-hairline-strong', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-ink')
    expect(track.querySelector('[data-usage-part="input"]')).toHaveClass('bg-[var(--chart-blue)]', '[[data-skin=plate]_&]:bg-riso-blue', '[[data-skin=plate]_&]:bg-size-[5px_5px]')
    const range = within(usage).getByRole('radiogroup', { name: 'Period' })
    expect(range).toHaveClass('rounded-[8px]', 'border', 'border-hairline', 'bg-segment-track', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-ink', '[[data-skin=plate]_&]:rounded-full')
    expect(within(range).getByRole('radio', { name: 'Today' })).toHaveClass('data-[state=on]:bg-segment-active', '[[data-skin=plate]_&]:data-[state=on]:bg-riso-yellow')
  })
})
