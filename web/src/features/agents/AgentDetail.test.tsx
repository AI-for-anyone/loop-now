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

describe('Runtime badge in the header', () => {
  it('a Claude Code Agent shows its runtime next to the name; a Pi Agent does not', async () => {
    const claude = makeAgent({ name: 'Local assistant', model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code' })
    const pi = makeAgent({ name: 'Pi assistant' })
    server.use(...baseHandlers([claude, pi]))
    const { router } = renderRoutes(routes, defaultAgentPath(claude.id, 'config'))
    const header = (await heading('Local assistant')).closest('header')!
    expect(within(header).getByTitle('Runtime: Claude Code on this computer')).toHaveTextContent(/^Claude Code$/)
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

// "Printed sheet" redesign: page header, index tabs and sheet.
describe('Header and tab layout', () => {
  async function open(agent: Schemas['Agent'], tab: 'chat' | 'config' = 'config') {
    server.use(...baseHandlers([agent]))
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentPath(agent.id, tab))
    const title = await heading(agent.name)
    return { user, title, header: title.closest('header')! }
  }

  it('the title uses crisp mono typography by default and keeps the display font and offset shadow in Plate skin, with the Manager badge and the status label next to it', async () => {
    const { title, header } = await open(makeAgent({ name: 'manager', isManager: true }))
    expect(title).toHaveClass('font-sans', 'text-[22px]', 'font-semibold', 'text-ink')
    expect(title).toHaveClass(
      '[[data-skin=plate]_&]:font-display-cn',
      '[[data-skin=plate]_&]:text-[clamp(38px,6vw,60px)]',
      '[[data-skin=plate]_&]:text-riso-blue',
      '[[data-skin=plate]_&]:[text-shadow:4px_4px_0_var(--riso-pink)]',
    )
    expect(title).toHaveClass(
      '[[data-skin=plate]_&]:nav:text-[40px]',
      '[[data-skin=plate]_&]:nav:leading-none',
      '[[data-skin=plate]_&]:nav:[text-shadow:3px_3px_0_var(--riso-pink)]',
    )
    expect(within(header).getByTitle('Project Manager')).toHaveTextContent('Manager')
    await waitFor(() => expect(header.querySelector('[data-agent-status]')).toHaveTextContent('Idle'))
  })

  it('an Agent on a command a person added shows the command\'s name in the model tag, not its runtime id and "default"', async () => {
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json({ runtimes: [...runtimeDefinitions().runtimes, acpCommandDefinition()] })))
    const { header } = await open(makeAgent({ name: 'Agent H', model: { provider: acpRuntimeId, modelId: 'default' }, runtime: acpRuntimeId }))
    const tag = await within(header).findByText('Hermes', { selector: '.font-mono' })
    expect(tag).toHaveClass('border-hairline', 'bg-surface-subtle', '[[data-skin=plate]_&]:bg-tint-blue')
    expect(header).not.toHaveTextContent(acpRuntimeId)
    expect(header).not.toHaveTextContent('default')
  })

  it('the model sits in a monospace subtle tag (light-blue in Plate); the address sits in a dashed box, and the button next to it copies the address and says it was copied', async () => {
    const { user, header } = await open(makeAgent({ name: 'Agent A', address: 'test-org/test-project/Agent A' }))
    expect(within(header).getByText('anthropic · claude-sonnet-5-5')).toHaveClass('font-mono', 'border-hairline', 'bg-surface-subtle', '[[data-skin=plate]_&]:bg-tint-blue')
    const address = within(header).getByText('test-org/test-project/Agent A')
    expect(address).toHaveClass('font-mono')
    expect(address.parentElement).toHaveClass('border-dashed')
    // English UI issue: the copy button's label glues t.common.copy and t.agents.address.label together without a space ("CopyAddress").
    const copy = within(header).getByRole('button', { name: 'CopyAddress' })
    await user.click(copy)
    expect(await navigator.clipboard.readText()).toBe('test-org/test-project/Agent A')
    expect(await within(header).findByText('Copied')).toBeInTheDocument()
    expect(copy).toHaveAccessibleName('CopyAddress')
  })

  it('without an address, explains why and shows no copy button', async () => {
    const { header } = await open(makeAgent({ name: 'old/bot', address: null }))
    expect(within(header).getByText(/This Agent has no address yet/)).toBeInTheDocument()
    expect(within(header).queryByRole('button', { name: 'CopyAddress' })).toBeNull()
  })

  it('the overprint decoration in the top-right corner is hidden from screen readers; it shows only when the header is wide enough, and then the text makes room for it in Plate skin instead of running under it', async () => {
    const { header, title } = await open(makeAgent({ name: 'Agent A' }))
    const deco = header.querySelector<HTMLElement>('[data-overprint-deco]')!
    expect(deco).toHaveAttribute('aria-hidden', 'true')
    expect(deco).toHaveClass('pointer-events-none', 'hidden', '@[820px]:block')
    // In the compact header on wide screens, the decoration shrinks to the
    // header's height (scaled to 0.6 as a whole, pinned to the top-right corner).
    expect(deco).toHaveClass('nav:top-2', 'nav:scale-[0.6]', 'origin-top-right')
    // The header is a container: the layout follows its own width, not the window width.
    expect(header).toHaveClass('@container')
    // In Plate skin, the block holding the title, badges, model and address leaves room for the decoration: 316px
    // on narrow screens as before, and 196px on wide screens once the decoration has shrunk.
    const row = title.closest('[data-agent-title-row]')!
    expect(row).toHaveClass('[[data-skin=plate]_&]:@[820px]:pr-[316px]', '[[data-skin=plate]_&]:nav:@[820px]:pr-[196px]')
    expect(row).toContainElement(within(header).getByText('anthropic · claude-sonnet-5-5'))
  })

  it('on wide screens the header is compact: right of the title, one row holds identity, status, runtime, model and address and wraps when it doesn\'t fit; the breadcrumb is tighter; the sheet\'s top edge is closer to the top of the window', async () => {
    const { title, header } = await open(makeAgent({ name: 'manager', isManager: true, address: 'test-org/test-project/manager' }))
    expect(header).toHaveClass('nav:gap-1.5', 'nav:pt-3', 'nav:pb-2.5')
    const row = title.closest('[data-agent-title-row]')!
    // Narrow screens keep two rows as before (title and badges in one, model and address in the other);
    // on wide screens the content of both rows goes into a single row that wraps when it doesn't fit.
    expect(row).toHaveClass('flex-col', 'nav:flex-row', 'nav:flex-wrap', 'nav:items-center')
    const rows = [...row.children]
    expect(rows).toHaveLength(2)
    for (const inner of rows) expect(inner).toHaveClass('nav:contents')
    expect(rows[0]).toContainElement(title)
    expect(rows[1]).toContainElement(within(header).getByText('test-org/test-project/manager'))
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' }).querySelector('ol')).toHaveClass('nav:leading-4')
  })

  it('the four tabs use hairline underline tabs in mono skin and preserve yellow/beige index tabs in Plate skin', async () => {
    const agent = makeAgent({ name: 'Agent A' })
    await open(agent)
    const tabs = screen.getByRole('navigation', { name: 'Agent A' })
    expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['Chat', 'Config', 'Folder', 'Inbox'])
    const current = within(tabs).getByRole('link', { name: 'Config' })
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(current).toHaveClass('border-b-2', 'border-ink', 'font-semibold', 'text-ink')
    expect(current).toHaveClass('[[data-skin=plate]_&]:h-10', '[[data-skin=plate]_&]:bg-riso-yellow', '[[data-skin=plate]_&]:border-b-0')
    const chat = within(tabs).getByRole('link', { name: 'Chat' })
    expect(chat).toHaveClass('border-transparent', 'text-ink-500', '[[data-skin=plate]_&]:h-[34px]', '[[data-skin=plate]_&]:bg-paper-tab')
    expect(chat).toHaveAttribute('href', defaultAgentPath(agent.id, 'chat'))
  })

  it('the tab content sits on a sheet; the header and the tabs are outside the sheet', async () => {
    const { header } = await open(makeAgent({ name: 'Agent A' }))
    const tabs = screen.getByRole('navigation', { name: 'Agent A' })
    const sheet = tabs.nextElementSibling as HTMLElement
    expect(sheet).toHaveAttribute('data-sheet')
    expect(sheet).toContainElement(await screen.findByRole('heading', { name: 'Basics' }))
    expect(sheet).not.toContainElement(header)
    expect(header.closest('[data-sheet]')).toBeNull()
  })
})
