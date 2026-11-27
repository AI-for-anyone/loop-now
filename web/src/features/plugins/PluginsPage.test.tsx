import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { fakePlugins, makeComponents, makeDetail, makeHost, makeManifest, makePlugin, makeResolvedSource } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, currentSidebarLinks } from '@/test/shell'

const playwright = makeDetail(
  makePlugin({ id: 'playwright', displayName: 'Dedicated browser', version: '2.0.0', hasPlatform: false, enabledProjects: 1, grantedAgents: 2 }),
  makeManifest({ id: 'playwright', displayName: 'Dedicated browser', version: '2.0.0', events: [], permissions: [], configSchema: {} as never, components: makeComponents({ platform: false, agentExtensions: 1, mcpServers: ['browser'], skills: ['skills/browse'] }) }),
)
const lark = makeDetail(
  makePlugin({ id: 'lark-notify', displayName: 'Lark notifications', version: '0.3.1', source: makeResolvedSource({ kind: 'git', location: 'https://github.com/foo/lark' }), status: 'error', lastError: 'Subscribed to run.ended, which the manifest does not declare.' }),
  makeManifest({ id: 'lark-notify', displayName: 'Lark notifications', version: '0.3.1', events: ['inbox.received', 'question.asked', 'run.ended'] }),
)

function renderMarket(initial: Parameters<typeof fakePlugins>[0] = {}, path = '/plugins') {
  const fake = fakePlugins({ details: [makeDetail(makePlugin({ enabledProjects: 3 })), playwright, lark], ...initial })
  server.use(...fake.handlers, ...baseHandlers())
  return { ...renderRoutes(routes, path), fake }
}

describe('Plugin market', () => {
  it('the sidebar marks "Plugin market" as current; installed plugins list source, concise side/project/Agent summaries and status without N+1 detail fetches', async () => {
    const { fake } = renderMarket({ host: makeHost({ loaded: ['webhook', 'playwright'], today: { delivered: 412, failed: 1 } }) })
    const webhook = (await screen.findByRole('link', { name: 'Webhook notifications' })).closest('li')!
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Plugin market'])
    expect(webhook).toHaveTextContent('webhook · v1.2.0Official')
    expect(webhook).toHaveTextContent('Platform side')
    expect(webhook).toHaveTextContent('Enabled in 3 projects')
    expect(webhook).toHaveTextContent('No Agent grant needed')
    expect(within(webhook).getByText('Running')).toBeInTheDocument()

    const browser = screen.getByRole('link', { name: 'Dedicated browser' }).closest('li')!
    expect(browser).toHaveTextContent('Agent side')
    expect(browser).toHaveTextContent('Enabled in 1 project')
    expect(browser).toHaveTextContent('Granted to 2 Agents')

    const failing = screen.getByRole('link', { name: 'Lark notifications' }).closest('li')!
    expect(failing).toHaveTextContent('git · unreviewed')
    expect(failing).toHaveTextContent('Subscribed to run.ended, which the manifest does not declare.')
    expect(within(failing).getByRole('link', { name: 'View logs' })).toHaveAttribute('href', '/plugins/lark-notify/activity?view=logs')

    const host = screen.getByRole('region', { name: 'Plugin host' })
    expect(host).toHaveTextContent('Host running')
    expect(host).toHaveTextContent('2 plugins loaded')

    // Zero N+1 GET /plugins/:pluginId fetches while rendering the Installed list.
    expect(fake.requests.filter((r) => /^\/plugins\/[^/]+$/.test(r.path))).toEqual([])
  })

  it('disable all saves the global settings (keeping the market list)', async () => {
    const user = userEvent.setup()
    const { fake } = renderMarket({ settings: { disabled: false, markets: ['https://example.com/market.git'] } })
    const toggle = await screen.findByRole('switch', { name: 'Disable all' })
    await waitFor(() => expect(toggle).toBeEnabled())
    await user.click(toggle)
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: '/plugin-settings', query: {}, body: { disabled: true, markets: ['https://example.com/market.git'] } }]))
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'true'))
  })

  it('host stopped: shows the crash banner below the header and allows restarting the host', async () => {
    const user = userEvent.setup()
    const { fake } = renderMarket({
      host: makeHost({ state: 'stopped', pid: null, lastCrash: { at: '2026-10-27T04:00:00Z', error: 'exit code 1', inFlight: [], last: { type: 'event', plugin: 'lark-notify', eventType: 'run.ended', since: '2026-10-27T03:59:59Z' }, stderr: [] } }),
    })
    const host = await screen.findByRole('region', { name: 'Plugin host' })
    await waitFor(() => expect(host).toHaveTextContent('Host stopped'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('exit code 1')
    expect(alert).toHaveTextContent("Was handling lark-notify's run.ended before it exited")
    await user.click(within(alert).getByRole('button', { name: 'Restart host' }))
    await waitFor(() => expect(fake.writes().map((r) => `${r.method} ${r.path}`)).toEqual(['POST /plugin-host/restart']))
    await waitFor(() => expect(host).toHaveTextContent('Host starting'))
  })

  it('built-in official plugins live in Discover (not Installed); Install previews from the builtin source and opens its details after confirming', async () => {
    const user = userEvent.setup()
    const { fake, router } = renderMarket(
      {
        builtin: [
          { id: 'hello', displayName: 'hello example', description: 'A minimal example on each side', version: '1.0.0' },
          { id: 'webhook', displayName: 'Webhook notifications', description: 'x', version: '1.2.0' },
        ],
      },
      '/plugins',
    )
    await screen.findByRole('link', { name: 'Webhook notifications' })
    expect(screen.queryByRole('region', { name: 'Official plugins that ship with the platform' })).toBeNull()

    await user.click(screen.getByRole('link', { name: 'Discover' }))
    const section = await screen.findByRole('region', { name: 'Official plugins that ship with the platform' })
    const helloCard = within(section).getByRole('heading', { name: 'hello example' }).closest('li')!
    await user.click(within(helloCard).getByRole('button', { name: 'Install' }))
    const dialog = await screen.findByRole('dialog', { name: /Install/ })
    expect(fake.requests.find((r) => r.path === '/plugin-previews')?.body).toEqual({ source: { kind: 'builtin', location: 'hello' } })
    await user.click(within(dialog).getByRole('button', { name: 'Install' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/plugins/hello'))
    expect(fake.writes().map((r) => `${r.method} ${r.path}`)).toEqual(['POST /plugin-previews', 'POST /plugins'])
  })

  it('with no plugins installed, shows the empty state with a CTA link to Discover', async () => {
    renderMarket({ details: [] })
    expect(await screen.findByText('No plugins installed yet. Start with "Install plugin" at the top right.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Browse Discover' })).toHaveAttribute('href', '/plugins?tab=discover')
  })
})

describe('Plugin market layout and Plate skin', () => {
  const builtin = (id: string, name: string): import('@/api/client').Schemas['BuiltinPluginSource'] => ({ id, displayName: name, description: `Description of ${name}`, version: '1.0.0' })

  it('places the compact HostStrip and primary "+ Install plugin" button in the right-aligned PageHeader actions slot', async () => {
    renderMarket()
    const title = await screen.findByRole('heading', { level: 1, name: 'Plugin market' })
    const header = title.closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Plugin market')
    const actions = header.querySelector('[data-page-actions]')!
    expect(actions).not.toBeNull()
    expect(header.querySelector('[data-page-meta]')).toBeNull()
    expect(within(actions as HTMLElement).getByRole('region', { name: 'Plugin host' })).toHaveClass('border', 'border-hairline-strong')
    expect(within(actions as HTMLElement).getByRole('button', { name: 'Install plugin' })).toHaveClass('bg-cta-bg', 'text-cta-fg')
  })

  it('uses two crisp tabs (Installed and Discover) above a Sheet + PageBody with a single-surface divide-y list', async () => {
    renderMarket()
    const tabs = await screen.findByRole('navigation', { name: 'Plugin market' })
    await waitFor(() => expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['Installed3', 'Discover']))
    expect(within(tabs).getByRole('link', { name: /Installed/ })).toHaveClass('border-b-2', 'border-ink')
    expect(within(tabs).getByRole('link', { name: 'Discover' })).toHaveClass('border-b-2', 'border-transparent')
    const list = (await screen.findByRole('link', { name: 'Webhook notifications' })).closest('ul')!
    expect(list.closest('[data-sheet]')).not.toBeNull()
    expect(list.closest('[data-page-body]')).not.toBeNull()
    expect(list).toHaveClass('divide-y', 'border', 'border-hairline-strong', 'bg-surface-control')
  })

  it('built-in plugins in Discover keep halftone color bands cycling blue, pink, yellow and Official badge tokens', async () => {
    renderMarket(
      { builtin: [builtin('hello', 'Hello example'), builtin('browser', 'Browser'), builtin('hook', 'Webhook inbox'), builtin('extra', 'Fourth one')] },
      '/plugins?tab=discover',
    )
    const heading = await screen.findByRole('heading', { name: 'Official plugins that ship with the platform' })
    expect(heading).toHaveClass('riso-marker')
    const cards = ['Hello example', 'Browser', 'Webhook inbox', 'Fourth one'].map((name) => screen.getByRole('heading', { name }).closest('li')!)
    expect(cards.map((card) => card.querySelector('[data-band]')!.getAttribute('data-band'))).toEqual(['blue', 'pink', 'yellow', 'blue'])
    expect(within(cards[0]).getByRole('heading', { name: 'Hello example' })).toHaveClass('font-display-cn')
    expect(within(cards[0]).getByText('Official')).toHaveClass('bg-cta-bg', 'text-cta-fg')
  })
})
