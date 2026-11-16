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

function renderMarket(initial: Parameters<typeof fakePlugins>[0] = {}) {
  const fake = fakePlugins({ details: [makeDetail(makePlugin({ enabledProjects: 3 })), playwright, lark], ...initial })
  server.use(...fake.handlers, ...baseHandlers())
  return { ...renderRoutes(routes, '/plugins'), fake }
}

describe('Plugin market', () => {
  it('the sidebar marks "Plugin market" as current; installed plugins list their source, what each side has, the number of enabled projects and granted Agents, and status', async () => {
    renderMarket({ host: makeHost({ loaded: ['webhook', 'playwright'], today: { delivered: 412, failed: 1 } }) })
    const webhook = (await screen.findByRole('link', { name: 'Webhook notifications' })).closest('li')!
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Plugin market'])
    await waitFor(() => expect(webhook).toHaveTextContent('Platform side'))
    expect(webhook).toHaveTextContent('webhook · v1.2.0Official')
    expect(webhook).toHaveTextContent('1 event subscribed2 permissions')
    expect(webhook).toHaveTextContent('Enabled in 3 projectsNo Agent grant needed')
    expect(within(webhook).getByText('Running')).toBeInTheDocument()
    const browser = screen.getByRole('link', { name: 'Dedicated browser' }).closest('li')!
    await waitFor(() => expect(browser).toHaveTextContent('Agent sideMCP servers 1Skills 1'))
    expect(browser).toHaveTextContent('Enabled in 1 projectGranted to 2 Agents')
    const failing = screen.getByRole('link', { name: 'Lark notifications' }).closest('li')!
    expect(failing).toHaveTextContent('git · unreviewed')
    expect(failing).toHaveTextContent('Subscribed to run.ended, which the manifest does not declare.')
    expect(within(failing).getByRole('link', { name: 'View logs' })).toHaveAttribute('href', '/plugins/lark-notify/logs')
    const host = screen.getByRole('region', { name: 'Plugin host' })
    expect(host).toHaveTextContent('Host running')
    expect(host).toHaveTextContent('2 plugins loaded')
    expect(host).toHaveTextContent('Today: 412 events handled, 1 failed')
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

  it('host stopped: shows what it was handling when it crashed, and it can be restarted', async () => {
    const user = userEvent.setup()
    const { fake } = renderMarket({
      host: makeHost({ state: 'stopped', pid: null, lastCrash: { at: '2026-10-27T04:00:00Z', error: 'exit code 1', inFlight: [], last: { type: 'event', plugin: 'lark-notify', eventType: 'run.ended', since: '2026-10-27T03:59:59Z' }, stderr: [] } }),
    })
    const host = await screen.findByRole('region', { name: 'Plugin host' })
    await waitFor(() => expect(host).toHaveTextContent('Host stopped'))
    expect(host).toHaveTextContent('exit code 1')
    expect(host).toHaveTextContent("Was handling lark-notify's run.ended before it exited")
    await user.click(within(host).getByRole('button', { name: 'Restart host' }))
    await waitFor(() => expect(fake.writes().map((r) => `${r.method} ${r.path}`)).toEqual(['POST /plugin-host/restart']))
    await waitFor(() => expect(host).toHaveTextContent('Host starting'))
  })

  it('official plugins not installed yet are listed separately; Install previews from the builtin source and opens its details after confirming', async () => {
    const user = userEvent.setup()
    const { fake, router } = renderMarket({ builtin: [{ id: 'hello', displayName: 'hello example', description: 'A minimal example on each side', version: '1.0.0' }, { id: 'webhook', displayName: 'Webhook notifications', description: 'x', version: '1.2.0' }] })
    const section = await screen.findByRole('region', { name: 'Official plugins that ship with the platform' })
    expect(within(section).queryByText('Webhook notifications')).toBeNull()
    await user.click(within(section).getByRole('button', { name: 'Install' }))
    const dialog = await screen.findByRole('dialog', { name: /Install/ })
    expect(fake.requests.find((r) => r.path === '/plugin-previews')?.body).toEqual({ source: { kind: 'builtin', location: 'hello' } })
    await user.click(within(dialog).getByRole('button', { name: 'Install' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/plugins/hello'))
    expect(fake.writes().map((r) => `${r.method} ${r.path}`)).toEqual(['POST /plugin-previews', 'POST /plugins'])
  })

  it('with no plugins installed, says to start with "Install plugin"', async () => {
    renderMarket({ details: [] })
    expect(await screen.findByText('No plugins installed yet. Start with "Install plugin" at the top right.')).toBeInTheDocument()
  })
})

// "Printed sheet" redesign, step 4: the plugin market's page header, index tabs,
// the installed card with a pink shadow, and plugin cards with a color band.
describe('Plugin market on the sheet', () => {
  const builtin = (id: string, name: string): import('@/api/client').Schemas['BuiltinPluginSource'] => ({ id, displayName: name, description: `Description of ${name}`, version: '1.0.0' })

  it('the header is the page title: breadcrumb "Global›Plugin market", next to it the host status in a dashed box and a fluorescent pink "Install plugin"', async () => {
    renderMarket()
    const title = await screen.findByRole('heading', { level: 1, name: 'Plugin market' })
    const header = title.closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Plugin market')
    expect(within(header).getByRole('region', { name: 'Plugin host' })).toHaveClass('border-dashed')
    expect(within(header).getByRole('button', { name: 'Install plugin' })).toHaveClass('bg-riso-pink', 'border-2', 'border-ink')
  })

  it('the three categories are index tabs on the top edge of the sheet: the current one is yellow and taller; installed plugins sit on the sheet as cards with a pink shadow', async () => {
    renderMarket()
    const tabs = await screen.findByRole('navigation', { name: 'Plugin market' })
    expect(within(tabs).getByRole('link', { name: /Installed/ })).toHaveClass('h-10', 'bg-riso-yellow')
    expect(within(tabs).getByRole('link', { name: 'Official market' })).toHaveClass('h-[34px]', 'bg-paper-tab')
    const card = (await screen.findByRole('link', { name: 'Webhook notifications' })).closest('li')!
    expect(card.closest('[data-sheet]')).not.toBeNull()
    expect(card).toHaveClass('border-2', 'border-ink', 'shadow-print-pink')
  })

  it('plugins that ship with the platform: a halftone color band on top of each card, cycling blue, pink, yellow; the name uses the display font and "Official" is a navy tag', async () => {
    renderMarket({ builtin: [builtin('hello', 'Hello example'), builtin('browser', 'Browser'), builtin('hook', 'Webhook inbox'), builtin('extra', 'Fourth one')] })
    const heading = await screen.findByRole('heading', { name: 'Official plugins that ship with the platform' })
    expect(heading).toHaveClass('riso-marker', 'font-display-cn')
    const cards = ['Hello example', 'Browser', 'Webhook inbox', 'Fourth one'].map((name) => screen.getByRole('heading', { name }).closest('li')!)
    expect(cards.map((card) => card.querySelector('[data-band]')!.getAttribute('data-band'))).toEqual(['blue', 'pink', 'yellow', 'blue'])
    expect(within(cards[0]).getByRole('heading', { name: 'Hello example' })).toHaveClass('font-display-cn')
    expect(within(cards[0]).getByText('Official')).toHaveClass('bg-ink', 'text-white')
  })
})
