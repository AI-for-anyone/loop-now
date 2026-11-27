import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { config } from '@/config'
import { agentsUrl, apiUrl, defaultAgentPath, defaultAgentsPath, makeAgent, toListItem } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, sidebar } from '@/test/shell'
import { makeSleep } from '@/test/sleep'

const names = () =>
  within(screen.getByRole('list', { name: 'Agents' }))
    .queryAllByRole('link')
    .map((link) => link.textContent ?? '')

function setup(agents: Schemas['Agent'][], path = defaultAgentsPath) {
  let listCalls = 0
  const listed = [...agents]
  server.use(
    http.get(agentsUrl, () => {
      listCalls += 1
      return HttpResponse.json({ agents: listed.map((a) => toListItem(a)) })
    }),
    ...baseHandlers(agents),
  )
  const user = userEvent.setup()
  return { user, listed, listCalls: () => listCalls, ...renderRoutes(routes, path) }
}

const review = makeAgent({ name: 'Code review assistant' })
const writer = makeAgent({ name: 'Writing assistant' })
const deploy = makeAgent({ name: 'Deploy Bot' })

describe('Sidebar search', () => {
  it('matches names case-insensitively as a substring, trimming leading and trailing whitespace; clearing restores all', async () => {
    const { user } = setup([review, writer, deploy])
    await waitFor(() => expect(names()).toHaveLength(3))
    const search = within(sidebar()).getByRole('searchbox', { name: 'Search Agents' })
    expect(search).toHaveAttribute('placeholder', 'Search')

    await user.type(search, '  deploy ')
    expect(names()).toEqual([expect.stringContaining('Deploy Bot')])

    await user.clear(search)
    await user.type(search, 'assistant')
    expect(names()).toHaveLength(2)

    await user.clear(search)
    expect(names()).toHaveLength(3)
  })

  it('matches by address: typing part of an address filters to that Agent; an Agent without an address matches by name only', async () => {
    const reviewer = makeAgent({ name: 'Review', address: 'test-org/test-project/Review' })
    const legacy = makeAgent({ name: 'old/bot', address: null })
    const { user } = setup([reviewer, legacy, makeAgent({ name: 'Writing assistant', address: 'test-org/test-project/Writing assistant' })])
    await waitFor(() => expect(names()).toHaveLength(3))
    const search = within(sidebar()).getByRole('searchbox', { name: 'Search Agents' })
    await user.type(search, 'test-project/Rev')
    expect(names()).toEqual([expect.stringContaining('Review')])
    await user.clear(search)
    await user.type(search, 'test-org')
    expect(names()).toHaveLength(2)
    expect(names().some((name) => name.includes('old/bot'))).toBe(false)
  })

  it('when no Agent matches, says there are no matches and keeps the Agent open on the right unchanged', async () => {
    const { user, router } = setup([review, writer, deploy], defaultAgentPath(review.id, 'config'))
    await waitFor(() => expect(names()).toHaveLength(3))
    expect(await screen.findByRole('link', { name: 'Config' })).toHaveAttribute('aria-current', 'page')

    await user.type(within(sidebar()).getByRole('searchbox', { name: 'Search Agents' }), 'No such name')
    expect(within(sidebar()).getByText('No matching Agents')).toBeInTheDocument()
    expect(names()).toHaveLength(0)
    expect(router.state.location.pathname).toBe(defaultAgentPath(review.id, 'config'))
    expect(screen.getByRole('link', { name: 'Config' })).toHaveAttribute('aria-current', 'page')
  })

  it('the periodic refresh keeps running during a search, and new matches show up', async () => {
    // Take over the timers before the list starts polling; only then can the list's periodic refresh be advanced.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const { user, listed, listCalls } = setup([review, deploy])
      await waitFor(() => expect(names()).toHaveLength(2))
      await user.type(within(sidebar()).getByRole('searchbox', { name: 'Search Agents' }), 'DEPLOY')
      expect(names()).toHaveLength(1)

      listed.push(makeAgent({ name: 'deploy staging' }))
      const before = listCalls()
      await act(async () => {
        await vi.advanceTimersByTimeAsync(config.agentListPollMs + 100)
      })
      await waitFor(() => expect(names()).toHaveLength(2))
      expect(listCalls()).toBeGreaterThan(before)
      expect(within(sidebar()).getByRole('searchbox', { name: 'Search Agents' })).toHaveValue('DEPLOY')
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('Look and status of the Agent rows in the sidebar', () => {
  it('each status renders a compact symbol with full words in the tooltip and screen-reader text; idle shows no symbol; the create button is “+”', async () => {
    const busy = makeAgent({ name: 'A', busy: true })
    const bg = makeAgent({ name: 'B', backgroundTaskCount: 2 })
    const sleeping = makeAgent({ name: 'C' })
    const manager = makeAgent({ name: 'D', isManager: true })
    const idle = makeAgent({ name: 'E' })
    const reason = 'Waiting for external confirmation; keeping the current results.'
    const sleep = makeSleep({ agentId: sleeping.id, reason, wakeAt: new Date(Date.now() + 120_000).toISOString() })
    server.use(
      http.get(agentsUrl, () =>
        HttpResponse.json({
          agents: [toListItem(busy), toListItem(bg), { ...toListItem(sleeping), sleep, sleepRevision: sleep.revision }, toListItem(manager), toListItem(idle)],
        }),
      ),
      ...baseHandlers([busy, bg, sleeping, manager, idle]),
    )
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })

    // Replying: pulsing busy dot with reduced-motion fallback.
    const itemA = await within(list).findByRole('link', { name: /^A/ })
    const busyMark = within(itemA).getByTitle('Replying')
    expect(busyMark).toHaveAttribute('data-status', 'busy')
    const dot = busyMark.querySelector('[data-status-dot]')!
    expect(dot).toHaveClass('bg-status-busy', 'rounded-full', 'animate-pulse', 'motion-reduce:animate-none')
    expect(dot).toHaveAttribute('aria-hidden', 'true')
    expect(within(busyMark).getByText('Replying')).toHaveClass('sr-only')
    expect(itemA).toHaveTextContent(/^AReplying$/)

    // Background tasks: high-contrast pill with shell glyph and count; full words in tooltip and sr-only.
    const itemB = within(list).getByRole('link', { name: /^B/ })
    const bgMark = within(itemB).getByTitle('Idle; 2 background tasks are still running')
    expect(bgMark).toHaveAttribute('data-status', 'background')
    expect(bgMark).toHaveClass('bg-sidebar-active-bg', 'text-sidebar-active-fg', 'rounded-full')
    expect(bgMark.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    expect(within(bgMark).getByText('2')).toHaveAttribute('aria-hidden', 'true')
    expect(within(bgMark).getByText('Idle; 2 background tasks are still running')).toHaveClass('sr-only')

    // Sleeping: moon glyph with wake time and reason in tooltip and sr-only.
    const itemC = within(list).getByRole('link', { name: /^C/ })
    const sleepMark = itemC.querySelector<HTMLElement>('[data-status="sleeping"]')!
    expect(sleepMark).toHaveClass('bg-sidebar-control-bg', 'text-sidebar-control-fg', 'rounded-full')
    expect(sleepMark.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    expect(sleepMark.getAttribute('title')).toMatch(/^Sleeping until .+ · Waiting for external confirmation; keeping the current results\.$/)
    expect(sleepMark.querySelector('.sr-only')?.textContent).toBe(sleepMark.getAttribute('title'))

    // Manager: same flag glyph as ManagerBadge, with full label in tooltip and sr-only.
    const itemD = within(list).getByRole('link', { name: /^D/ })
    const managerMark = within(itemD).getByTitle('Project Manager')
    expect(managerMark).toHaveAttribute('data-status', 'manager')
    expect(managerMark).toHaveClass('bg-sidebar-control-bg', 'text-sidebar-control-fg', 'rounded-full')
    expect(managerMark.querySelector('svg path')).toHaveAttribute('d', 'M5 21V4h11l-2 4 2 4H5')
    expect(within(managerMark).getByText('Project Manager')).toHaveClass('sr-only')

    // Idle: no status symbol at all.
    const itemE = within(list).getByRole('link', { name: /^E$/ })
    expect(itemE.querySelector('[data-status]')).toBeNull()
    expect(itemE).toHaveTextContent(/^E$/)

    const create = within(sidebar()).getByRole('button', { name: 'New Agent' })
    expect(create).toHaveTextContent('')
    expect(create).toBeEnabled()
  })

  it('shows at most one status symbol by priority: replying > background tasks > sleeping > Manager', async () => {
    const allFour = makeAgent({ name: 'AllFour', busy: true, backgroundTaskCount: 3, isManager: true })
    const bgAndSleep = makeAgent({ name: 'BgAndSleep', backgroundTaskCount: 2, isManager: true })
    const sleepAndMgr = makeAgent({ name: 'SleepAndMgr', isManager: true })
    const onlyMgr = makeAgent({ name: 'OnlyMgr', isManager: true })
    const wakeAt = new Date(Date.now() + 120_000).toISOString()
    const sleepingFor = (agent: Schemas['Agent']) => {
      const s = makeSleep({ agentId: agent.id, wakeAt })
      return { ...toListItem(agent), sleep: s, sleepRevision: s.revision }
    }
    server.use(
      http.get(agentsUrl, () =>
        HttpResponse.json({
          agents: [sleepingFor(allFour), sleepingFor(bgAndSleep), sleepingFor(sleepAndMgr), toListItem(onlyMgr)],
        }),
      ),
      ...baseHandlers([allFour, bgAndSleep, sleepAndMgr, onlyMgr]),
    )
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    const statusOf = async (name: string) => {
      const link = await within(list).findByRole('link', { name: new RegExp(`^${name}`) })
      return [...link.querySelectorAll('[data-status]')].map((el) => el.getAttribute('data-status'))
    }
    expect(await statusOf('AllFour')).toEqual(['busy'])
    expect(await statusOf('BgAndSleep')).toEqual(['background'])
    expect(await statusOf('SleepAndMgr')).toEqual(['sleeping'])
    expect(await statusOf('OnlyMgr')).toEqual(['manager'])
  })

  it('a Claude Code Agent has a 16px runtime icon after its name; a Pi Agent does not', async () => {
    setup([makeAgent({ name: 'Pi assistant' }), makeAgent({ name: 'Local assistant', model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code', busy: true })])
    const list = await screen.findByRole('list', { name: 'Agents' })
    const claude = await within(list).findByRole('link', { name: /^Local assistant/ })
    expect(claude).toHaveTextContent(/^Local assistantRuntime: Claude Code on this computerReplying$/)
    expect(within(claude).getByTitle('Runtime: Claude Code on this computer').querySelector('svg')).toHaveClass('size-4')
    expect(within(list).getByRole('link', { name: /^Pi assistant/ })).toHaveTextContent(/^Pi assistant$/)
  })

  it('questions and messages sent to you and not yet handled show a “Needs your reply” sticker on the row', async () => {
    const agent = makeAgent({ name: 'dev-helper' })
    server.use(
      http.get(apiUrl('/inbox/pending'), () => HttpResponse.json({ questions: [], unread: [], byAgent: [{ agentId: agent.id, projectId: null, questions: 1, unread: 2 }], byTask: [] })),
      ...baseHandlers([agent]),
    )
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    expect(await within(list).findByRole('link', { name: /^dev-helper\s*Needs your reply: 1 question, 2 new messages/ })).toHaveTextContent('3')
  })

  it('when the server is unreachable, shows the halftone skeleton and “Can\'t reach the server”, and the create button is disabled', async () => {
    server.use(http.all(`${config.apiBase}/*`, () => HttpResponse.error()))
    renderRoutes(routes, defaultAgentsPath)
    // A request still in flight from the previous test case may return here and briefly change
    // the connection status; wait for the sidebar to settle on the skeleton before checking.
    await waitFor(() => {
      expect(within(sidebar()).getByText('Can\'t reach the server')).toBeInTheDocument()
      expect(sidebar().querySelectorAll('.riso-skeleton')).toHaveLength(3)
    })
    expect(within(sidebar()).getByRole('button', { name: 'New Agent' })).toBeDisabled()
    expect(within(sidebar()).queryByRole('list', { name: 'Agents' })).toBeNull()
  })
})

// "Printed sheet" redesign, item 10: the project card and the selected row share one border
// and shadow style; selected-row colors for every status symbol; one-line truncation; search box and decoration.
describe('Sidebar styling', () => {
  const manager = makeAgent({ name: 'manager', isManager: true })
  const other = makeAgent({ name: 'Researcher', isManager: true })

  it('the project switcher and the selected Agent row use semantic sidebar tokens', async () => {
    setup([manager, other], defaultAgentPath(manager.id, 'config'))
    const row = await within(sidebar()).findByRole('link', { name: /manager/ })
    await waitFor(() => expect(row).toHaveAttribute('aria-current', 'page'))
    expect(row).toHaveClass('bg-sidebar-active-bg', 'font-semibold', 'text-sidebar-active-fg')
    const card = within(sidebar()).getByRole('button', { name: /^Switch scope/ })
    expect(card).toHaveClass('h-9', 'border-0', 'bg-transparent')
    const unselected = within(sidebar()).getByRole('link', { name: /Researcher/ })
    expect(unselected).not.toHaveClass('bg-sidebar-active-bg')
  })

  it('status symbols adapt to selected and unselected rows via semantic sidebar tokens', async () => {
    const busySel = makeAgent({ name: 'BusySelected', busy: true })
    const busyIdle = makeAgent({ name: 'BusyOther', busy: true })
    const bgSel = makeAgent({ name: 'BgSelected', backgroundTaskCount: 2 })
    const bgOther = makeAgent({ name: 'BgOther', backgroundTaskCount: 2 })
    const sleepSel = makeAgent({ name: 'SleepSelected' })
    const sleepOther = makeAgent({ name: 'SleepOther' })
    const mgrSel = makeAgent({ name: 'MgrSelected', isManager: true })
    const mgrOther = makeAgent({ name: 'MgrOther', isManager: true })
    const wakeAt = new Date(Date.now() + 120_000).toISOString()

    // Check Manager + Background + Replying + Sleeping selected vs unselected.
    const checkPair = async (selected: Schemas['Agent'], unselected: Schemas['Agent'], withSleep = false) => {
      const toItem = (a: Schemas['Agent']) => {
        if (!withSleep) return toListItem(a)
        const s = makeSleep({ agentId: a.id, wakeAt })
        return { ...toListItem(a), sleep: s, sleepRevision: s.revision }
      }
      server.use(
        http.get(agentsUrl, () => HttpResponse.json({ agents: [toItem(selected), toItem(unselected)] })),
        ...baseHandlers([selected, unselected]),
      )
      const view = renderRoutes(routes, defaultAgentPath(selected.id, 'config'))
      const selRow = await within(sidebar()).findByRole('link', { name: new RegExp(`^${selected.name}`) })
      await waitFor(() => expect(selRow).toHaveAttribute('aria-current', 'page'))
      const unselRow = within(sidebar()).getByRole('link', { name: new RegExp(`^${unselected.name}`) })
      return { selRow, unselRow, unmount: view.unmount }
    }

    const mgr = await checkPair(mgrSel, mgrOther)
    const selFlag = within(mgr.selRow).getByTitle('Project Manager')
    expect(selFlag).toHaveClass('bg-sidebar-active-fg', 'text-sidebar-active-bg', 'rounded-full')
    const unselFlag = within(mgr.unselRow).getByTitle('Project Manager')
    expect(unselFlag).toHaveClass('bg-sidebar-control-bg', 'text-sidebar-control-fg', 'rounded-full')
    expect(unselFlag).not.toHaveClass('bg-sidebar-active-fg')
    mgr.unmount()

    const slp = await checkPair(sleepSel, sleepOther, true)
    expect(slp.selRow.querySelector('[data-status="sleeping"]')).toHaveClass('bg-sidebar-active-fg', 'text-sidebar-active-bg', 'rounded-full')
    const unselMoon = slp.unselRow.querySelector('[data-status="sleeping"]')!
    expect(unselMoon).toHaveClass('bg-sidebar-control-bg', 'text-sidebar-control-fg', 'rounded-full')
    expect(unselMoon).not.toHaveClass('bg-sidebar-active-fg')
    slp.unmount()

    const bg = await checkPair(bgSel, bgOther)
    expect(bg.selRow.querySelector('[data-status="background"]')).toHaveClass('bg-sidebar-active-fg', 'text-sidebar-active-bg', 'rounded-full')
    expect(bg.unselRow.querySelector('[data-status="background"]')).toHaveClass('bg-sidebar-active-bg', 'text-sidebar-active-fg', 'rounded-full')
    bg.unmount()

    const bsy = await checkPair(busySel, busyIdle)
    expect(bsy.selRow.querySelector('[data-status-dot]')).toHaveClass('bg-status-busy', 'ring-1', 'ring-sidebar-active-fg/40')
    const unselDot = bsy.unselRow.querySelector('[data-status-dot]')!
    expect(unselDot).toHaveClass('bg-status-busy')
    expect(unselDot).not.toHaveClass('ring-1')
    bsy.unmount()
  })

  it('a long name with a busy status and runtime mark stays on one line: no wrapping, fixed row height, truncated name with full title', async () => {
    const longBusy = makeAgent({
      name: 'loop-now-publicity-and-release-announcements',
      runtime: 'claude-code',
      model: { provider: 'claude-code', modelId: 'sonnet' },
      busy: true,
    })
    const longIdle = makeAgent({ name: 'loop-now-very-long-secondary-agent-name' })
    setup([longBusy, longIdle], defaultAgentPath(longBusy.id, 'config'))
    const row = await within(sidebar()).findByRole('link', { name: /^loop-now-publicity/ })
    await waitFor(() => expect(row).toHaveAttribute('aria-current', 'page'))
    expect(row).toHaveClass('h-[38px]')
    expect(row).not.toHaveClass('flex-wrap')
    expect(row.querySelector('.flex-wrap')).toBeNull()
    const name = within(row).getByText(longBusy.name)
    expect(name).toHaveAttribute('title', longBusy.name)
    expect(name).toHaveClass('min-w-0', 'flex-1', 'truncate')
    const marks = within(row).getByTitle('Replying').parentElement!
    expect(marks).toHaveClass('shrink-0')
    expect(marks).not.toContainElement(name)
    // Unselected rows use the same single-line layout.
    const unselected = within(sidebar()).getByRole('link', { name: longIdle.name })
    expect(unselected).toHaveClass('h-[34px]')
    expect(unselected.querySelector('.flex-wrap')).toBeNull()
    expect(within(unselected).getByText(longIdle.name)).toHaveAttribute('title', longIdle.name)
    expect(within(unselected).getByText(longIdle.name)).toHaveClass('min-w-0', 'flex-1', 'truncate')
  })

  it('the sidebar scroll area uses a thin hairline scrollbar', async () => {
    setup([manager])
    const list = await within(sidebar()).findByRole('list', { name: 'Agents' })
    const scroller = list.closest('.overflow-y-auto')!
    expect(scroller).toHaveClass('[scrollbar-width:thin]', '[scrollbar-color:var(--hairline-strong)_transparent]')
  })

  it('the search box is 34px tall; the shortcut hint has a thin border', async () => {
    setup([manager])
    const search = await within(sidebar()).findByRole('searchbox', { name: 'Search Agents' })
    const box = search.parentElement!
    expect(box).toHaveClass('h-[34px]', 'border-sidebar-border', 'bg-sidebar-control-bg')
    const kbd = box.querySelector('kbd')!
    const shortcut = kbd.closest('button') ?? kbd
    expect(shortcut).toHaveClass('border', 'border-sidebar-border')
  })
})
