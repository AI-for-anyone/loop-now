import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { agentsUrl, apiUrl, defaultAgentPath, defaultAgentsPath, errorBody, makeAgent, makeRun, makeStatus, platformAgentsUrl, toListItem } from '@/test/api'
import { renderChat } from '@/test/chat'
import { makeSleep } from '@/test/sleep'
import { providers } from '@/test/config'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { acpCommandDefinition, acpRuntimeId, runtimeDefinitions } from '@/test/runtimes'
import { baseHandlers } from '@/test/shell'

const heading = (name: string) => screen.findByRole('heading', { level: 1, name })

describe('Switching Agents', () => {
  // A project Agent's model accounts are managed in the project; the dialog
  // for entering an API Key directly exists only on platform Agents.
  it('going back to a cached Agent drops the previous Agent\'s credential dialog and never submits it to this Agent', async () => {
    const a = makeAgent({ name: 'Agent A', scope: 'platform', projectId: null })
    const b = makeAgent({ name: 'Agent B', scope: 'platform', projectId: null })
    const configPath = (agent: Schemas['Agent']) => `/platform/agents/${agent.id}/config`
    const puts: string[] = []
    server.use(
      http.put(apiUrl('/agents/:agentId/model-credential'), ({ params }) => {
        puts.push(String(params.agentId))
        return HttpResponse.json(errorBody('internal_error'), { status: 500 })
      }),
      http.get(apiUrl('/model-providers'), () => HttpResponse.json(providers)),
      http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [a, b].map((agent) => toListItem(agent)) })),
      http.get(agentsUrl, () => HttpResponse.json({ agents: [] })),
      ...baseHandlers([a, b]),
    )
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, configPath(b))
    await heading('Agent B')
    await act(() => router.navigate(configPath(a)))
    await heading('Agent A')
    const section = await screen.findByRole('region', { name: 'Model account credential' })
    await user.click(await within(section).findByRole('button', { name: 'Enter API key' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('API Key'), 'sk-for-a')

    // Browser back (POP) to B, which was viewed earlier and is already in the cache.
    await act(() => router.navigate(-1))
    await heading('Agent B')
    expect(router.state.location.pathname).toBe(configPath(b))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.body.innerHTML).not.toContain('sk-for-a')
    expect(puts).toEqual([])
  })

  it('switching to a cached Agent does not carry over the previous Agent\'s chat draft', async () => {
    const a = makeAgent({ name: 'Agent A' })
    const b = makeAgent({ name: 'Agent B' })
    const posts: string[] = []
    server.use(
      http.post(apiUrl('/agents/:agentId/messages'), ({ params }) => {
        posts.push(String(params.agentId))
        return HttpResponse.json(errorBody('internal_error'), { status: 500 })
      }),
      ...baseHandlers([a, b]),
    )
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentPath(b.id, 'chat'))
    await heading('Agent B')
    await act(() => router.navigate(defaultAgentPath(a.id, 'chat')))
    await heading('Agent A')
    await user.type(await screen.findByRole('textbox', { name: 'Message' }), 'Draft for A')

    await act(() => router.navigate(-1))
    await heading('Agent B')
    const input = await screen.findByRole('textbox', { name: 'Message' })
    expect(input).toHaveValue('')
    await user.type(input, '{Enter}')
    expect(posts).toEqual([])
  })
})

describe('Background refresh failure', () => {
  it('with data already loaded, keeps the tab content and unsaved edits and shows the reason above', async () => {
    const agent = makeAgent({ name: 'Agent A' })
    let fail = false
    server.use(
      http.get(apiUrl(`/agents/${agent.id}`), () => (fail ? HttpResponse.json(errorBody('internal_error'), { status: 500 }) : HttpResponse.json(agent))),
      ...baseHandlers([agent]),
    )
    const user = userEvent.setup()
    const { client } = renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const prompt = await screen.findByLabelText('Agent prompt')
    await user.type(prompt, 'Unsaved prompt')

    fail = true
    await act(() => client.refetchQueries({ queryKey: keys.agent(agent.id) }))
    expect(await screen.findByText('Internal server error')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: 'Agent A' })).toBeInTheDocument()
    expect(screen.getByLabelText('Agent prompt')).toHaveValue('Unsaved prompt')
  })

  it('without data, shows a full-page error', async () => {
    const agent = makeAgent()
    server.use(http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(errorBody('internal_error'), { status: 500 })), ...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    expect(await screen.findByText('Internal server error')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('heading', { level: 1 })).toBeNull())
  })
})

describe('Runtime mark in the header', () => {
  it('a Claude Code Agent shows the sidebar runtime mark next to the name with no text badge; a Pi Agent shows no mark', async () => {
    const claude = makeAgent({ name: 'Local assistant', model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code' })
    const pi = makeAgent({ name: 'Pi assistant' })
    server.use(...baseHandlers([claude, pi]))
    const { router } = renderRoutes(routes, defaultAgentPath(claude.id, 'config'))
    const header = (await heading('Local assistant')).closest('header')!
    const mark = within(header).getByTitle('Runtime: Claude Code on this computer')
    expect(mark.querySelector('img')).toHaveAttribute('data-logo', 'claude')
    expect(mark).not.toHaveAttribute('data-slot', 'badge')
    expect(within(header).queryByText('Claude Code')).toBeNull()
    await act(() => router.navigate(defaultAgentPath(pi.id, 'config')))
    const piHeader = (await heading('Pi assistant')).closest('header')!
    expect(within(piHeader).queryByTitle('Runtime: Claude Code on this computer')).toBeNull()
  })
})

describe('Status label in the header', () => {
  const sticker = () => document.querySelector<HTMLElement>('[data-agent-status]')

  // Open the Agent on the Config tab; the status label comes from the periodically polled status endpoint.
  async function renderConfigWith(status: Partial<Schemas['AgentStatus']>) {
    const agent = makeAgent({ name: 'Agent A' })
    if (status.sleep) status = { ...status, sleep: { ...status.sleep, agentId: agent.id } }
    server.use(http.get(apiUrl(`/agents/${agent.id}/status`), () => HttpResponse.json(makeStatus(agent.id, status))), ...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    await heading('Agent A')
  }

  it('when busy, adds the current phase', async () => {
    await renderConfigWith({ busy: true, currentRun: makeRun({ status: 'starting', phase: 'preparing' }) })
    await waitFor(() => expect(sticker()?.textContent).toBe('BusyPreparing the environment'))
    expect(sticker()).toHaveAttribute('data-tone', 'busy')
  })

  it('shows idle when idle', async () => {
    await renderConfigWith({})
    await waitFor(() => expect(sticker()?.textContent).toBe('Idle'))
    expect(sticker()).toHaveAttribute('data-tone', 'success')
  })

  it('idle sleep keeps the wake time, countdown and reason', async () => {
    const wakeAt = new Date(Date.now() + 120_000).toISOString()
    await renderConfigWith({ sleepRevision: 2, sleep: makeSleep({ wakeAt, reason: 'Continue after external confirmation' }) })
    await screen.findByText('Continue after external confirmation')
    expect(document.querySelector('time[datetime="' + wakeAt + '"]')).toBeInTheDocument()
    expect(sticker()?.textContent).toBe('Idle')
    expect(document.querySelector('time[datetime="' + wakeAt + '"]')?.parentElement?.textContent).toMatch(/\d+ min \d+ s left/)
  })

  it('on the Chat tab the live status wins once a snapshot arrives; after leaving the Chat tab, the polled status applies again', async () => {
    const agent = makeAgent({ name: 'Agent A' })
    // The status endpoint keeps reporting idle (e.g. the next poll hasn't come around yet).
    const { router, emit } = await renderChat({ agent, handlers: baseHandlers([agent]) })
    await waitFor(() => expect(sticker()?.textContent).toBe('Idle'))

    const run = makeRun({ status: 'running', endedAt: null })
    await emit('snapshot', { status: makeStatus(agent.id, { busy: true, currentRun: run }), currentText: '', queuedRuns: [], queueItems: [] })
    await waitFor(() => expect(sticker()?.textContent).toBe('BusyReplying'))
    expect(sticker()).toHaveAttribute('data-tone', 'busy')
    await emit('run.updated', { run: { ...run, status: 'aborting' } })
    await waitFor(() => expect(sticker()?.textContent).toBe('BusyInterrupting'))

    await act(() => router.navigate(defaultAgentPath(agent.id, 'config')))
    await screen.findByLabelText('Agent prompt')
    await waitFor(() => expect(sticker()?.textContent).toBe('Idle'))
  })

  it('when the live status is idle, does not keep a stale busy from the status endpoint', async () => {
    const agent = makeAgent()
    const { emit } = await renderChat({ agent, status: { busy: true, currentRun: makeRun({ status: 'running' }) } })
    await waitFor(() => expect(sticker()?.textContent).toBe('BusyReplying'))
    await emit('snapshot', { status: makeStatus(agent.id), currentText: null, queuedRuns: [], queueItems: [] })
    await waitFor(() => expect(sticker()?.textContent).toBe('Idle'))
  })
})

// Thin Agent header bar: one ~48px bar on wide screens shared by all four tabs, with the address, runtime/model details and
// back-to-project link tucked into the "More" ("⋯") menu.
describe('Header bar and tabs', () => {
  async function open(agent: Schemas['Agent'], tab: 'chat' | 'config' = 'config') {
    server.use(
      http.get(apiUrl(`/agents/${agent.id}/inbox`), () => HttpResponse.json({ messages: [], nextCursor: null })),
      http.get(apiUrl(`/agents/${agent.id}/folder/entries`), () => HttpResponse.json({ path: '', entries: [], nextCursor: null })),
      http.get(apiUrl('/usage'), () => HttpResponse.json({ from: null, to: null, rows: [] })),
      http.get(apiUrl('/anomalies'), () => HttpResponse.json({ items: [], total: 0, nextCursor: null })),
      ...baseHandlers([agent]),
    )
    const user = userEvent.setup()
    const path = agent.scope === 'platform' ? `/platform/agents/${agent.id}/${tab}` : defaultAgentPath(agent.id, tab)
    const { router } = renderRoutes(routes, path)
    const title = await heading(agent.name)
    return { user, router, title, header: title.closest('header')! }
  }

  it('shows the Agent name in normal bold text (no display font, no shadow), Manager chip, status dot and four compact tabs, with no breadcrumb, model tag, runtime badge or overprint deco', async () => {
    const agent = makeAgent({ name: 'manager', isManager: true, runtime: 'claude-code', model: { provider: 'claude-code', modelId: 'opus' }, address: 'test-org/test-project/manager' })
    const { title, header } = await open(agent)
    expect(header).toHaveClass('nav:flex', 'nav:h-12', 'nav:items-center')
    expect(title).toHaveClass('text-[15px]', 'font-black', 'text-ink')
    expect(title).not.toHaveClass('font-display-cn')
    expect(title.className).not.toMatch(/text-shadow/)
    expect(within(header).getByTitle('Project Manager')).toHaveTextContent('Manager')
    await waitFor(() => expect(header.querySelector('[data-agent-status]')).toHaveTextContent('Idle'))
    // Removed from the bar: breadcrumb, runtime text badge, model tag, address, overprint decoration.
    expect(screen.queryByRole('navigation', { name: 'Breadcrumb' })).toBeNull()
    expect(header.querySelector('[data-overprint-deco]')).toBeNull()
    expect(within(header).queryByText('claude-code · opus')).toBeNull()
    expect(within(header).queryByText('Claude Code')).toBeNull()
    expect(within(header).queryByText('test-org/test-project/manager')).toBeNull()
    // Four compact text tabs inside the bar (not large folder index tabs).
    const tabs = within(header).getByRole('navigation', { name: 'manager' })
    expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['Chat', 'Config', 'Folder', 'Inbox'])
    const current = within(tabs).getByRole('link', { name: 'Config' })
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(current).toHaveClass('bg-riso-yellow')
    expect(current).not.toHaveClass('h-10', 'border-b-0')
    expect(within(tabs).getByRole('link', { name: 'Chat' })).not.toHaveClass('bg-paper-tab')
  })

  it('a normal Agent has no Manager chip', async () => {
    const { header } = await open(makeAgent({ name: 'worker', isManager: false }))
    expect(within(header).queryByTitle('Project Manager')).toBeNull()
  })

  it('each tab routes to its page and updates aria-current', async () => {
    const agent = makeAgent({ name: 'Agent A' })
    const { user, router, header } = await open(agent, 'config')
    const tabs = within(header).getByRole('navigation', { name: 'Agent A' })

    await user.click(within(tabs).getByRole('link', { name: 'Folder' }))
    expect(router.state.location.pathname).toBe(`${defaultAgentsPath}/${agent.id}/folder`)
    expect(within(tabs).getByRole('link', { name: 'Folder' })).toHaveAttribute('aria-current', 'page')

    await user.click(within(tabs).getByRole('link', { name: 'Inbox' }))
    expect(router.state.location.pathname).toBe(`${defaultAgentsPath}/${agent.id}/inbox`)
    expect(within(tabs).getByRole('link', { name: 'Inbox' })).toHaveAttribute('aria-current', 'page')

    await user.click(within(tabs).getByRole('link', { name: 'Chat' }))
    expect(router.state.location.pathname).toBe(defaultAgentPath(agent.id, 'chat'))
    expect(within(tabs).getByRole('link', { name: 'Chat' })).toHaveAttribute('aria-current', 'page')

    await user.click(within(tabs).getByRole('link', { name: 'Config' }))
    expect(router.state.location.pathname).toBe(defaultAgentPath(agent.id, 'config'))
    expect(within(tabs).getByRole('link', { name: 'Config' })).toHaveAttribute('aria-current', 'page')
  })

  it('keyboard focus order is name area, tabs, then the More button', async () => {
    const { user, title, header } = await open(makeAgent({ name: 'Agent A' }))
    const identity = title.closest('[data-agent-identity]')!
    const tabs = within(header).getByRole('navigation', { name: 'Agent A' })
    const links = within(tabs).getAllByRole('link')
    const more = within(header).getByRole('button', { name: 'More' })

    expect(identity.compareDocumentPosition(tabs) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(tabs.compareDocumentPosition(more) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

    links[0].focus()
    expect(links[0]).toHaveFocus()
    for (const next of [...links.slice(1), more]) {
      await user.tab()
      expect(next).toHaveFocus()
    }
  })

  it('phone layout: two short lines with identity + More on row 1 and horizontally scrollable tabs on row 2', async () => {
    const { title, header } = await open(makeAgent({ name: 'Agent A' }))
    const identity = title.closest('[data-agent-identity]')!
    const tabs = within(header).getByRole('navigation', { name: 'Agent A' })
    const more = within(header).getByRole('button', { name: 'More' })

    expect(header).toHaveClass('grid', 'grid-cols-[minmax(0,1fr)_auto]', 'nav:flex', 'nav:h-12')
    expect(identity).toHaveClass('col-start-1', 'row-start-1')
    expect(tabs).toHaveClass('col-span-2', 'row-start-2', 'max-nav:overflow-x-auto')
    expect(more).toHaveClass('col-start-2', 'row-start-1')
  })

  it('the More menu shows the address visibly with Copy, the runtime and model in words, and a link back to the project', async () => {
    const agent = makeAgent({ name: 'Agent A', runtime: 'claude-code', model: { provider: 'claude-code', modelId: 'opus' }, address: 'test-org/test-project/Agent A' })
    const { user, router, header } = await open(agent)

    await user.click(within(header).getByRole('button', { name: 'More' }))
    const menu = await screen.findByRole('dialog', { name: 'Agent info' })

    // Address stays visible in the dashed box and can be copied.
    const address = within(menu).getByText('test-org/test-project/Agent A')
    expect(address).toHaveClass('font-mono')
    expect(address.parentElement).toHaveClass('border-dashed')
    const copy = within(menu).getByRole('button', { name: 'Copy address' })
    await user.click(copy)
    expect(await navigator.clipboard.readText()).toBe('test-org/test-project/Agent A')
    expect(await within(menu).findByText('Copied')).toBeInTheDocument()
    expect(address).toBeVisible()

    // Runtime and model in words.
    expect(within(menu).getByText('Claude Code (this computer)')).toBeInTheDocument()
    expect(within(menu).getByText('claude-code · opus')).toBeInTheDocument()

    // Way back to the project.
    const back = within(menu).getByRole('link', { name: 'Back to test-project' })
    expect(back).toHaveAttribute('href', '/?project=test-project')
    await user.click(back)
    expect(router.state.location.pathname).toBe('/')
    expect(router.state.location.search).toBe('?project=test-project')
  })

  it('for a platform Agent, the More menu links back to Global', async () => {
    const platform = makeAgent({ name: 'Inspector', scope: 'platform', projectId: null, address: 'Inspector' })
    server.use(http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(platform)] })))
    const { user, header } = await open(platform)

    await user.click(within(header).getByRole('button', { name: 'More' }))
    const menu = await screen.findByRole('dialog', { name: 'Agent info' })
    expect(within(menu).getByText('Pi (built in)')).toBeInTheDocument()
    expect(within(menu).getByText('anthropic · claude-sonnet-5-5')).toBeInTheDocument()
    expect(within(menu).getByRole('link', { name: 'Back to Global' })).toHaveAttribute('href', '/')
  })

  it('an Agent on a command a person added shows the command name in the More menu, not its runtime id or "default"', async () => {
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json({ runtimes: [...runtimeDefinitions().runtimes, acpCommandDefinition()] })))
    const { user, header } = await open(makeAgent({ name: 'Agent H', model: { provider: acpRuntimeId, modelId: 'default' }, runtime: acpRuntimeId }))

    await user.click(within(header).getByRole('button', { name: 'More' }))
    const menu = await screen.findByRole('dialog', { name: 'Agent info' })
    expect(within(menu).getByText('Hermes (this computer)')).toBeInTheDocument()
    expect(menu).not.toHaveTextContent(acpRuntimeId)
    expect(menu).not.toHaveTextContent('default')
  })

  it('without an address, the More menu explains why and shows no copy button', async () => {
    const { user, header } = await open(makeAgent({ name: 'old/bot', address: null }))
    await user.click(within(header).getByRole('button', { name: 'More' }))
    const menu = await screen.findByRole('dialog', { name: 'Agent info' })
    expect(within(menu).getByText(/This Agent has no address yet/)).toBeInTheDocument()
    expect(within(menu).queryByRole('button', { name: 'Copy address' })).toBeNull()
  })

  it('the sheet starts directly under the bar; the bar is outside the sheet', async () => {
    const { header } = await open(makeAgent({ name: 'Agent A' }))
    const sheet = header.nextElementSibling as HTMLElement
    expect(sheet).toHaveAttribute('data-sheet')
    expect(sheet).toContainElement(await screen.findByRole('heading', { name: 'Basics' }))
    expect(sheet).not.toContainElement(header)
    expect(header.closest('[data-sheet]')).toBeNull()
  })

  it('the conversation panel under the bar uses tighter top padding above the first message', async () => {
    const { header } = await open(makeAgent({ name: 'Agent A' }), 'chat')
    const sheet = header.nextElementSibling as HTMLElement
    const log = await within(sheet).findByRole('log', { name: 'Conversation' })
    expect(log).toHaveClass('pt-3.5')
    expect(log).not.toHaveClass('pt-[26px]')
  })
})
