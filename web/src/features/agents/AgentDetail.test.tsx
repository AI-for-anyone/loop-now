import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { agentsUrl, apiUrl, defaultAgentPath, errorBody, makeAgent, makeRun, makeStatus, platformAgentsUrl, toListItem } from '@/test/api'
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

describe('Workspace header identity', () => {
  it('renders the translucent two-circle avatar and omits redundant runtime and model tags on the Chat tab', async () => {
    const claude = makeAgent({ name: 'Local assistant', model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code' })
    server.use(...baseHandlers([claude]))
    const { router } = renderRoutes(routes, defaultAgentPath(claude.id, 'chat'))
    const header = (await heading('Local assistant')).closest('header')!
    expect(header.querySelector('[data-avatar-palette]')).toBeInTheDocument()
    expect(within(header).queryByTitle('Runtime: Claude Code on this computer')).toBeNull()
    expect(within(header).queryByText('claude-code · sonnet')).toBeNull()

    await act(() => router.navigate(defaultAgentPath(claude.id, 'config')))
    const configHeader = (await heading('Local assistant')).closest('header')!
    expect(within(configHeader).getByText('claude-code · sonnet')).toHaveClass('font-mono', 'border-hairline', 'bg-surface-subtle')
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

// Single-row 48px workspace header, segmented view switcher and sheet.
describe('Header and tab layout', () => {
  async function open(agent: Schemas['Agent'], tab: 'chat' | 'config' = 'config') {
    server.use(...baseHandlers([agent]))
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentPath(agent.id, tab))
    const title = await heading(agent.name)
    return { user, title, header: title.closest('header')! }
  }

  it('the title uses crisp mono typography by default and preserves the display font and offset shadow in Plate skin, with the avatar, Manager badge and status next to it', async () => {
    const { title, header } = await open(makeAgent({ name: 'manager', isManager: true }))
    expect(title).toHaveClass('text-[14px]', 'font-semibold', 'text-ink')
    expect(title).toHaveClass(
      '[[data-skin=plate]_&]:font-display-cn',
      '[[data-skin=plate]_&]:text-[32px]',
      '[[data-skin=plate]_&]:text-riso-blue',
      '[[data-skin=plate]_&]:[text-shadow:3px_3px_0_var(--riso-pink)]',
    )
    expect(header.querySelector('[data-avatar-palette]')).toBeInTheDocument()
    expect(within(header).getByTitle('Project Manager')).toHaveTextContent('Manager')
    await waitFor(() => expect(header.querySelector('[data-agent-status]')).toHaveTextContent('Idle'))
  })

  it('an Agent on a command a person added shows the command\'s name in the model tag on non-Chat tabs, not its runtime id and "default"', async () => {
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json({ runtimes: [...runtimeDefinitions().runtimes, acpCommandDefinition()] })))
    const { header } = await open(makeAgent({ name: 'Agent H', model: { provider: acpRuntimeId, modelId: 'default' }, runtime: acpRuntimeId }), 'config')
    const tag = await within(header).findByText('Hermes', { selector: '.font-mono' })
    expect(tag).toHaveClass('border-hairline', 'bg-surface-subtle', '[[data-skin=plate]_&]:bg-tint-blue')
    expect(header).not.toHaveTextContent(acpRuntimeId)
    expect(header).not.toHaveTextContent('default')
  })

  it('compact address opens a popover with the full monospace address and copy button that copies and announces Copied', async () => {
    const { user, header } = await open(makeAgent({ name: 'Agent A', address: 'test-org/test-project/Agent A' }), 'chat')
    const trigger = within(header).getByRole('button', { name: 'Address' })
    expect(trigger).toHaveAttribute('title', 'test-org/test-project/Agent A')
    await user.click(trigger)
    const copy = await screen.findByRole('button', { name: 'CopyAddress' })
    expect(screen.getByText('test-org/test-project/Agent A', { selector: '.font-mono' })).toBeInTheDocument()
    await user.click(copy)
    expect(await navigator.clipboard.readText()).toBe('test-org/test-project/Agent A')
    expect(await screen.findByText('Copied')).toBeInTheDocument()
    expect(copy).toHaveAccessibleName('CopyAddress')
  })

  it('without an address, shows a warning indicator whose popover explains why and shows no copy button', async () => {
    const { user, header } = await open(makeAgent({ name: 'old/bot', address: null }), 'chat')
    const trigger = within(header).getByRole('button', { name: 'Address' })
    expect(trigger).toHaveAttribute('data-address-missing')
    expect(trigger).toHaveAttribute('title', expect.stringMatching(/This Agent has no address yet/))
    await user.click(trigger)
    expect(await screen.findByText(/This Agent has no address yet/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'CopyAddress' })).toBeNull()
  })

  it('the overprint decoration in the top-right corner is hidden from screen readers and reserves Plate space without affecting mono skin', async () => {
    const { header, title } = await open(makeAgent({ name: 'Agent A' }))
    const deco = header.querySelector<HTMLElement>('[data-overprint-deco]')!
    expect(deco).toHaveAttribute('aria-hidden', 'true')
    expect(deco).toHaveClass('pointer-events-none', 'hidden', '@[820px]:block')
    expect(deco).toHaveClass('nav:top-2', 'nav:scale-[0.6]', 'origin-top-right')
    expect(header).toHaveClass('@container')
    const row = title.closest('[data-agent-title-row]')!
    expect(row).toHaveClass('[[data-skin=plate]_&]:@[820px]:pr-[316px]', '[[data-skin=plate]_&]:nav:@[820px]:pr-[196px]')
  })

  it('collapses into a single 48px top bar holding breadcrumb, avatar, title, status, segmented views and address', async () => {
    const { title } = await open(makeAgent({ name: 'manager', isManager: true, address: 'test-org/test-project/manager' }), 'chat')
    const row = title.closest<HTMLElement>('[data-agent-title-row]')!
    expect(row).toHaveClass('min-h-12', 'sm:h-12', 'border-b', 'border-hairline', 'bg-surface-canvas')
    expect(within(row).getByRole('navigation', { name: 'Breadcrumb' })).toBeInTheDocument()
    expect(within(row).getByRole('navigation', { name: 'manager' })).toBeInTheDocument()
    expect(within(row).getByRole('button', { name: 'Address' })).toBeInTheDocument()
  })

  it('the four views sit in a compact segmented switcher in mono skin and preserve yellow/beige tab styling in Plate skin', async () => {
    const agent = makeAgent({ name: 'Agent A' })
    await open(agent)
    const tabs = screen.getByRole('navigation', { name: 'Agent A' })
    expect(tabs).toHaveClass('h-8', 'rounded-input', 'border', 'border-hairline', 'bg-surface-subtle')
    expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['Chat', 'Config', 'Folder', 'Inbox'])
    const current = within(tabs).getByRole('link', { name: 'Config' })
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(current).toHaveClass('bg-surface-elevated', 'font-semibold', 'text-ink', 'shadow-xs')
    expect(current).toHaveClass('[[data-skin=plate]_&]:bg-riso-yellow')
    const chat = within(tabs).getByRole('link', { name: 'Chat' })
    expect(chat).toHaveClass('text-ink-500', '[[data-skin=plate]_&]:bg-paper-tab')
    expect(chat).toHaveAttribute('href', defaultAgentPath(agent.id, 'chat'))
  })

  it('the tab content sits on a sheet; the header and the segmented switcher are outside the sheet', async () => {
    const { header } = await open(makeAgent({ name: 'Agent A' }))
    const tabs = screen.getByRole('navigation', { name: 'Agent A' })
    const sheet = header.nextElementSibling as HTMLElement
    expect(sheet).toHaveAttribute('data-sheet')
    expect(sheet).toContainElement(await screen.findByRole('heading', { name: 'Basics' }))
    expect(sheet).not.toContainElement(header)
    expect(sheet).not.toContainElement(tabs)
    expect(header.closest('[data-sheet]')).toBeNull()
  })
})
