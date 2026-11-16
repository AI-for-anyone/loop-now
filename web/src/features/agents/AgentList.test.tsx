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
    const reviewer = makeAgent({ name: 'Review', address: '测试组织/测试项目/Review' })
    const legacy = makeAgent({ name: 'old/bot', address: null })
    const { user } = setup([reviewer, legacy, makeAgent({ name: 'Writing assistant', address: '测试组织/测试项目/Writing assistant' })])
    await waitFor(() => expect(names()).toHaveLength(3))
    const search = within(sidebar()).getByRole('searchbox', { name: 'Search Agents' })
    await user.type(search, '测试项目/Rev')
    expect(names()).toEqual([expect.stringContaining('Review')])
    await user.clear(search)
    await user.type(search, '测试组织')
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
  it('each row is the name and one status label (Replying, Manager); idle shows no label; the create button is “+”', async () => {
    const busy = makeAgent({ name: 'A', busy: true })
    const idle = makeAgent({ name: 'B' })
    const manager = makeAgent({ name: 'C', isManager: true })
    setup([busy, idle, manager])
    const list = await screen.findByRole('list', { name: 'Agents' })
    const itemA = await within(list).findByRole('link', { name: /^A/ })
    expect(itemA).toHaveTextContent(/^AReplying$/)
    expect(within(list).getByRole('link', { name: /^B/ })).toHaveTextContent(/^B$/)
    expect(within(within(list).getByRole('link', { name: /^C/ })).getByText('Manager')).toHaveAttribute('title', 'Project Manager')
    const create = within(sidebar()).getByRole('button', { name: 'New Agent' })
    expect(create).toHaveTextContent('')
    expect(create).toBeEnabled()
  })

  it('a Claude Code Agent has a small icon after its name marking the runtime; a Pi Agent does not', async () => {
    setup([makeAgent({ name: 'Pi assistant' }), makeAgent({ name: 'Local assistant', model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code', busy: true })])
    const list = await screen.findByRole('list', { name: 'Agents' })
    const claude = await within(list).findByRole('link', { name: /^Local assistant/ })
    expect(claude).toHaveTextContent(/^Local assistantRuntime: Claude Code on this computerReplying$/)
    expect(within(claude).getByTitle('Runtime: Claude Code on this computer')).toBeInTheDocument()
    expect(within(list).getByRole('link', { name: /^Pi assistant/ })).toHaveTextContent(/^Pi assistant$/)
  })

  it('while sleeping shows “zZ sleep”, with the wake time and the reason in the tooltip', async () => {
    const agent = makeAgent({ name: 'Waiting assistant' })
    const reason = 'Waiting for external confirmation; keeping the current results.'
    const sleep = makeSleep({ agentId: agent.id, reason, wakeAt: new Date(Date.now() + 120_000).toISOString() })
    server.use(http.get(agentsUrl, () => HttpResponse.json({ agents: [{ ...toListItem(agent), sleep, sleepRevision: sleep.revision }] })), ...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    const chip = await within(list).findByText('zZ sleep')
    expect(chip.getAttribute('title')).toMatch(/^Sleeping until .+ · Waiting for external confirmation; keeping the current results\.$/)
    expect(chip.closest('a')).toHaveTextContent(/^Waiting assistantzZ sleep$/)
  })

  it('questions and messages sent to you and not yet handled show a “Needs your reply” sticker on the row', async () => {
    const agent = makeAgent({ name: '开发助手' })
    server.use(
      http.get(apiUrl('/inbox/pending'), () => HttpResponse.json({ questions: [], unread: [], byAgent: [{ agentId: agent.id, projectId: null, questions: 1, unread: 2 }], byTask: [] })),
      ...baseHandlers([agent]),
    )
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    expect(await within(list).findByRole('link', { name: /^开发助手\s*Needs your reply: 1 question, 2 new messages/ })).toHaveTextContent('3')
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
// and shadow style; the Manager badge in the selected row; the search box and the decoration.
describe('Sidebar styling', () => {
  const manager = makeAgent({ name: 'manager', isManager: true })
  const other = makeAgent({ name: 'Researcher', isManager: true })

  it('the project card and the selected Agent row both have a 2px navy border and a 4px solid navy shadow', async () => {
    setup([manager, other], defaultAgentPath(manager.id, 'config'))
    const row = await within(sidebar()).findByRole('link', { name: /manager/ })
    await waitFor(() => expect(row).toHaveAttribute('aria-current', 'page'))
    expect(row).toHaveClass('border-2', 'border-ink', 'bg-riso-yellow', 'shadow-[4px_4px_0_var(--ink)]')
    const card = within(sidebar()).getByRole('button', { name: /^Switch scope/ })
    expect(card).toHaveClass('border-2', 'border-ink', 'bg-paper-raised', 'shadow-[4px_4px_0_var(--ink)]')
    const unselected = within(sidebar()).getByRole('link', { name: /Researcher/ })
    expect(unselected).not.toHaveClass('bg-riso-yellow')
    expect(unselected).not.toHaveClass('shadow-[4px_4px_0_var(--ink)]')
  })

  it('the Manager badge in the selected row is white on solid navy; unselected rows keep the semi-transparent navy', async () => {
    setup([manager, other], defaultAgentPath(manager.id, 'config'))
    const row = await within(sidebar()).findByRole('link', { name: /manager/ })
    await waitFor(() => expect(row).toHaveAttribute('aria-current', 'page'))
    expect(within(row).getByText('Manager')).toHaveClass('bg-ink', 'text-white', 'rounded-full')
    const tag = within(within(sidebar()).getByRole('link', { name: /Researcher/ })).getByText('Manager')
    expect(tag).toHaveClass('bg-ink/45', 'text-white')
    expect(tag).not.toHaveClass('bg-ink')
  })

  // Redesign step 3: the selected Manager row used to leave only 66px for the name
  // (55px when the sidebar shows a scrollbar), so "manager" was cut to "man…".
  it('a name that fits is shown in full: the name fills the first line, and the runtime, status and Manager badge on the right wrap to the next line when they do not fit; only names that are too long are truncated, with the full name on hover', async () => {
    setup([manager, other], defaultAgentPath(manager.id, 'config'))
    const row = await within(sidebar()).findByRole('link', { name: /manager/ })
    await waitFor(() => expect(row).toHaveAttribute('aria-current', 'page'))
    // The selected row no longer has a fixed height; it grows with its content when it wraps.
    expect(row).toHaveClass('min-h-10')
    expect(row).not.toHaveClass('h-10')
    const name = within(row).getByText('manager')
    expect(name).toHaveAttribute('title', 'manager')
    expect(name).toHaveClass('max-w-full', 'truncate')
    expect(name.parentElement).toHaveClass('flex-wrap', 'min-w-0', 'flex-1')
    const tags = within(row).getByText('Manager').parentElement!
    expect(tags).toHaveClass('ml-auto', 'shrink-0')
    expect(tags).not.toContainElement(name)
    // Unselected rows use the same layout.
    const unselected = within(sidebar()).getByRole('link', { name: /Researcher/ })
    expect(unselected).toHaveClass('min-h-9')
    expect(within(unselected).getByText('Researcher')).toHaveAttribute('title', 'Researcher')
  })

  it('the sidebar scroll area uses a thin semi-transparent white scrollbar on the blue background', async () => {
    setup([manager])
    const list = await within(sidebar()).findByRole('list', { name: 'Agents' })
    const scroller = list.closest('.overflow-y-auto')!
    expect(scroller).toHaveClass('[scrollbar-width:thin]', '[scrollbar-color:rgb(255_255_255/.45)_transparent]')
  })

  it('the search box is 34px tall; the shortcut hint has a thin border', async () => {
    setup([manager])
    const search = await within(sidebar()).findByRole('searchbox', { name: 'Search Agents' })
    expect(search.closest('label')).toHaveClass('h-[34px]', 'bg-ink/[0.42]')
    expect(search.closest('label')!.querySelector('kbd')).toHaveClass('border', 'border-white/45')
  })

  it('the halftone circle and the two color dots in the lower middle are decoration: hidden from screen readers, not blocking clicks, placed in a slot shown depending on the height', async () => {
    setup([manager])
    await within(sidebar()).findByRole('link', { name: /manager/ })
    const deco = sidebar().querySelector<HTMLElement>('[data-sidebar-deco]')!
    expect(deco).toHaveAttribute('aria-hidden', 'true')
    // Not shown in the drawer on narrow screens.
    expect(deco).toHaveClass('pointer-events-none', 'sidebar-deco', 'max-nav:hidden')
    expect(deco.parentElement).toHaveClass('sidebar-deco-slot')
    expect(deco.querySelectorAll('span')).toHaveLength(3)
  })
})
