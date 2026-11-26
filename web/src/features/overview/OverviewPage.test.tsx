import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, defaultProject, errorBody, makeAgent, makeRun, makeStatus } from '@/test/api'
import { FakeEventSource } from '@/test/fake-event-source'
import { anomalyPage, makeAgentOverview, makeAnomaly, makeUsageRow, renderOverview, usagePage } from '@/test/overview'
import { baseHandlers } from '@/test/shell'

const section = (name: string) => screen.getByRole('region', { name })

describe('Overview page entry', () => {
  it('opening the root address shows the overview: three blocks, the sidebar in the global scope, “Overview” is the first item and the current one', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    expect(section('Run status')).toBeInTheDocument()
    expect(section('Usage')).toBeInTheDocument()
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

  it('with no Agents (once the import page was skipped) it shows the empty state and a way to create an Agent, without the three blocks', async () => {
    window.localStorage.setItem('loop-now.importSkipped', '1')
    const { snapshot, user } = await renderOverview()
    await snapshot([])
    const empty = screen.getByRole('region', { name: 'No Agents yet.' })
    expect(empty).toHaveTextContent('Once you create an Agent, its run status, usage, and issues show up here.')
    expect(screen.queryByRole('region', { name: 'Usage' })).toBeNull()
    await user.click(within(empty).getByRole('button', { name: 'New Agent' }))
    expect(await screen.findByRole('dialog', { name: 'New Agent' })).toBeInTheDocument()
  })

  it('when only usage fails to load, only the usage block shows the error, request ID and retry; the rest shows as usual', async () => {
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
    const usage = section('Usage')
    const alert = await within(usage).findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't load usage")
    expect(alert).toHaveTextContent('01890a5d-ac96-774b-bcce-b302099a8057')
    expect(within(section('Run status')).getByRole('table')).toHaveTextContent('dev-helper')
    expect(await within(section('Issues and failures')).findByText('Test helper')).toBeInTheDocument()
    expect(within(section('Issues and failures')).queryByRole('alert')).toBeNull()
    fail = false
    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await within(usage).findByText('1,234', { selector: 'span.riso-misreg' })).toBeInTheDocument()
    expect(within(usage).queryByRole('alert')).toBeNull()
  })
})

describe('Run status', () => {
  it('an Agent starts replying: without reloading, it shows replying and the current phase, and the “Replying” count goes up by one', async () => {
    const agent = makeAgentOverview({ name: 'dev-helper' })
    const { snapshot, emit } = await renderOverview()
    await snapshot([agent, makeAgentOverview({ name: 'Test helper' })])
    const tiles = screen.getByRole('list', { name: 'Run status summary' })
    const busyTile = within(tiles).getByText('Replying').closest('li')!
    expect(busyTile).toHaveTextContent('0')
    expect(within(tiles).getByText('Idle').closest('li')).toHaveTextContent('2')

    const run = makeRun({ status: 'starting', phase: 'preparing', endedAt: null, runningAt: null, configVersion: 3 })
    await emit('agent.status', { agent: { ...agent, status: makeStatus(agent.agentId, { busy: true, currentRun: run, processState: 'starting' }) } })
    expect(busyTile).toHaveTextContent('1')
    expect(busyTile).toHaveTextContent('dev-helper')
    const row = within(screen.getByRole('table')).getByText('dev-helper').closest('tr')!
    expect(row).toHaveTextContent('Replying')
    expect(row).toHaveTextContent('Preparing the environment')
    expect(row).toHaveTextContent('config v3')
  })

  it('queue counts add up in the summary; a lost session is no state of its own any more (the server carries it into a new one)', async () => {
    const { snapshot } = await renderOverview()
    const busy = makeAgentOverview({ name: 'A' })
    busy.status = makeStatus(busy.agentId, { busy: true, currentRun: makeRun({ status: 'running', endedAt: null }), queued: { interrupt: 1, normal: 2 } })
    const idle = makeAgentOverview({ name: 'B' })
    await snapshot([busy, idle])
    const tiles = screen.getByRole('list', { name: 'Run status summary' })
    expect(within(tiles).getAllByRole('listitem').map((tile) => tile.firstElementChild?.textContent)).toEqual(['Replying', 'Idle', 'Queued'])
    expect(within(tiles).getByText('Idle').closest('li')).toHaveTextContent(/Idle\s*1\s*B/)
    expect(within(tiles).getByText('Queued').closest('li')).toHaveTextContent('31 interrupt · 2 normal')
    const rowA = within(screen.getByRole('table')).getByText('A').closest('tr')!
    expect(rowA).toHaveTextContent('3 messages queued: 1 interrupt, 2 normal')
  })

  it('after the Pi process exits on idle it shows not running, with the time and reason of the exit', async () => {
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
  it('shows today by Agent by default; shows the total and its four parts, a total per row with details, and no cost', async () => {
    const { snapshot } = await renderOverview({
      usage: usagePage([
        makeUsageRow({ agentName: 'dev-helper', input: 92140, output: 8420, cacheRead: 61440, cacheWrite: 4096 }),
        makeUsageRow({ agentName: 'Old helper', agentDeleted: true, input: 100, output: 10 }),
      ]),
    })
    await snapshot([makeAgentOverview()])
    const usage = section('Usage')
    expect(within(usage).getByRole('radio', { name: 'Today' })).toBeChecked()
    expect(within(usage).getByRole('radio', { name: 'By Agent' })).toBeChecked()
    expect(await within(usage).findByText('Total · Today')).toBeInTheDocument()
    expect(within(usage).getByText('166,206', { selector: 'span.riso-misreg' })).toBeInTheDocument()
    const rows = within(usage).getAllByRole('listitem')
    expect(rows[0]).toHaveTextContent('dev-helper')
    expect(rows[0]).toHaveAccessibleDescription('Input 92,140 · Output 8,420 · Cache read 61,440 · Cache write 4,096 · Total 166,096')
    expect(rows[1]).toHaveTextContent('Old helperDeleted')
    expect(screen.getByRole('main')).not.toHaveTextContent(/cost|fee|price|dollar|USD|\$/i)
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
    const usage = section('Usage')
    await within(usage).findByText('Total · Today')
    await user.click(within(usage).getByRole('radio', { name: 'By model' }))
    const rows = within(usage).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('anthropic / x')
    expect(rows[0]).toHaveTextContent('330')
    expect(rows[1]).toHaveTextContent('Unknown model')
  })

  it('shows the empty state when the chosen period has no usage', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    expect(await within(section('Usage')).findByText('No usage yet · Today')).toBeInTheDocument()
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
    const { snapshot } = await renderOverview({
      anomalies: anomalyPage([
        makeAnomaly({ agentName: 'A', runtime: 'claude-code', errorCode: 'runtime_unavailable', errorMessage: 'claude: command not found' }),
        makeAnomaly({ agentName: 'B', runtime: null, errorCode: 'runtime_start_failed', errorMessage: 'exit status 1' }),
      ]),
    })
    await snapshot([makeAgentOverview()])
    const [a, b] = await within(section('Issues and failures')).findAllByRole('link')
    await waitFor(() => expect(a).toHaveTextContent("Claude Code can't be used on this computer: it wasn't found, its version couldn't be read, or it's too old: claude: command not found"))
    expect(b).toHaveTextContent("The runtime couldn't start: exit status 1")
  })

  it('a failed CLI install shows as failed, with the CLI name in the reason', async () => {
    const { snapshot } = await renderOverview({
      anomalies: anomalyPage([makeAnomaly({ errorCode: 'cli_install_failed', errorMessage: 'gh 2.100.0: the downloaded file does not match the SHA-256 in the template' })]),
    })
    await snapshot([makeAgentOverview()])
    const item = await within(section('Issues and failures')).findByRole('link')
    expect(item).toHaveTextContent('Failed')
    expect(item).toHaveTextContent('CLI install failed: gh 2.100.0')
  })

  it('every item jumps to its reply, a missing session as well (nothing is left to confirm there)', async () => {
    const missing = makeAnomaly({ errorCode: 'session_missing', errorMessage: null })
    const other = makeAnomaly()
    const { snapshot } = await renderOverview({ anomalies: anomalyPage([missing, other]) })
    await snapshot([makeAgentOverview()])
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
    const { snapshot } = await renderOverview({ anomalies: anomalyPage([failed]) })
    await snapshot([makeAgentOverview()])
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
    const { snapshot } = await renderOverview({ anomalies: anomalyPage([failed]) })
    await snapshot([makeAgentOverview()])
    const item = await within(section('Issues and failures')).findByRole('link')
    expect(item.querySelector('code')).toHaveTextContent(/^\/compact$/)
  })

  it('shows the empty state when there are no issues in the last 24 hours', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    expect(await within(section('Issues and failures')).findByText('No issues in the last 24 hours.')).toBeInTheDocument()
  })
})

// "Printed sheet" redesign, step 3: the overview's header is the page title, and the content sits on a sheet, numbered 01, 02, 03.
describe('Overview on a sheet', () => {
  const sectionNumber = (name: string) => section(name).querySelector('[data-section-number]')

  it('the header is the page title: the breadcrumb names the current scope, the title is “Overview”, and the line below has the scope select and live status', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    const header = screen.getByRole('heading', { level: 1, name: 'Overview' }).closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Overview')
    const meta = header.querySelector('[data-page-meta]')!
    expect(within(meta as HTMLElement).getByRole('combobox', { name: 'Scope' })).toBeInTheDocument()
    expect(within(meta as HTMLElement).getByText('Run status is updating live')).toBeInTheDocument()
  })

  it('with a project chosen the breadcrumb names the project; the scope select is “Scope name” in a dashed box', async () => {
    await renderOverview({ path: `/?project=${encodeURIComponent(defaultProject.name)}` })
    await waitFor(() => expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(`${defaultProject.name}›Overview`))
    const select = screen.getByRole('combobox', { name: 'Scope' })
    expect(select.parentElement).toHaveClass('border-dashed', 'border-[1.5px]', 'h-[26px]')
  })

  it('run status, usage, and issues and failures sit on one sheet, numbered 01, 02, 03, with a dashed line between blocks', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    const sheet = section('Run status').closest('[data-sheet]')!
    expect(sheet).toContainElement(section('Usage'))
    expect(sheet).toContainElement(section('Issues and failures'))
    expect(sectionNumber('Run status')).toHaveTextContent('01')
    expect(sectionNumber('Usage')).toHaveTextContent('02')
    expect(sectionNumber('Issues and failures')).toHaveTextContent('03')
    expect(within(section('Run status')).getByRole('heading', { level: 2, name: 'Run status' })).toHaveClass('riso-marker', 'font-display-cn')
    expect(section('Run status')).not.toHaveClass('border-t-2')
    expect(section('Usage')).toHaveClass('border-t-2', 'border-dashed')
  })

  it('status tiles: tinted background, 2px navy border, solid navy shadow, the number in large Bricolage, no longer tilted', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    const tiles = within(screen.getByRole('list', { name: 'Run status summary' })).getAllByRole('listitem')
    expect(tiles).toHaveLength(3)
    for (const tile of tiles) {
      expect(tile).toHaveClass('border-2', 'border-ink', 'rounded-card', 'shadow-[4px_4px_0_var(--ink)]')
      expect(tile).not.toHaveClass('riso-tilt')
    }
    expect(tiles.map((tile) => tile.className.match(/bg-tint-[a-z0-9-]+/)?.[0])).toEqual(['bg-tint-pink-2', 'bg-tint-green', 'bg-tint-blue'])
    expect(within(tiles[1]).getByText('1')).toHaveClass('font-display-latin', 'text-[46px]')
  })

  it('Agent table: a solid line under the header, dashed lines between rows; model names monospace; idle is a light green pill', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview({ name: 'dev-helper' })])
    const table = screen.getByRole('table')
    // Placed directly on the sheet, not wrapped in a white card.
    expect(table.parentElement).not.toHaveClass('bg-white', 'shadow-print-rest')
    expect(table.querySelector('thead tr')).toHaveClass('border-b-2', 'border-ink')
    const row = within(table).getByText('dev-helper').closest('tr')!
    expect(row).toHaveClass('border-b-[1.5px]', 'border-dashed')
    expect(within(row).getByText(/ \/ /)).toHaveClass('font-mono')
    const pill = within(row).getByText('Idle').closest('[data-state-pill]')!
    expect(pill).toHaveAttribute('data-state-pill', 'idle')
    expect(pill.querySelector('[aria-hidden="true"]')).toHaveClass('bg-tint-green', 'rotate-[-3deg]')
  })

  it('usage: bars have a halftone fill, the largest row has a small yellow “Most” tag; period and grouping are segmented buttons, the chosen one yellow', async () => {
    const { snapshot } = await renderOverview({
      usage: usagePage([makeUsageRow({ agentName: 'dev-helper', input: 9000, output: 1000 }), makeUsageRow({ agentName: 'Test helper', input: 2000, output: 500 })]),
    })
    await snapshot([makeAgentOverview()])
    const usage = section('Usage')
    const lines = await within(usage).findAllByRole('listitem')
    expect(lines[0]).toHaveTextContent('dev-helper')
    expect(within(lines[0]).getByText('Most')).toHaveClass('bg-riso-yellow')
    expect(within(lines[1]).queryByText('Most')).toBeNull()
    const track = lines[0].querySelector('[data-usage-track]')!
    expect(track).toHaveClass('border-2', 'border-ink')
    expect(track.querySelector('[data-usage-part="input"]')).toHaveClass('bg-riso-blue', 'bg-size-[5px_5px]')
    const range = within(usage).getByRole('radiogroup', { name: 'Period' })
    expect(range).toHaveClass('border-2', 'border-ink', 'rounded-full')
    expect(within(range).getByRole('radio', { name: 'Today' })).toHaveClass('data-[state=on]:bg-riso-yellow')
  })
})
