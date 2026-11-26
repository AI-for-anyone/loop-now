import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { apiUrl, defaultProject, makeProject } from '@/test/api'
import { fakePlugins, larkAccountKind, makeAccount, makeComponents, makeConfig, makeDelivery, makeDetail, makeManifest, makeMarket, makeMarketEntry, makePlugin, makeProjectPlugin, makeResolvedSource } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const other = makeProject({ name: 'loop now scalable' })

function renderDetail(path = '/plugins/webhook', initial: Parameters<typeof fakePlugins>[0] = {}) {
  const fake = fakePlugins({
    configs: { webhook: makeConfig({ values: { url: 'https://hooks.example.com/loop-now', retries: 5 } as never, secretsSet: ['secret'], revision: 2 }) },
    projects: { [defaultProject.id]: { webhook: makeProjectPlugin({ enabled: true, config: makeConfig({ values: { url: 'https://hooks.example.com/propanda' } as never, revision: 3 }) }) } },
    deliveries: {
      webhook: [
        makeDelivery({ eventId: 'e4', at: '2026-10-27T04:41:00Z', projectId: defaultProject.id, durationMs: 120 }),
        makeDelivery({ eventId: 'e3', at: '2026-10-27T04:12:00Z', projectId: other.id, status: 'failed', attempts: 3, lastError: 'timeout after 15000ms', durationMs: null, ackedAt: null }),
        ...Array.from({ length: 60 }, (_, i) => makeDelivery({ eventId: `old-${i}`, at: '2026-10-26T01:00:00Z', projectId: defaultProject.id })),
      ],
    },
    state: {
      webhook: [
        { key: 'deliveries/01A', value: { id: '01A', at: '2026-10-27T04:41:00Z', eventId: 'e4', eventType: 'inbox.received', target: 'https://hooks.example.com/loop-now', status: 'delivered', attempts: 1, nextAttemptAt: null, lastStatusCode: 200, lastError: null, updatedAt: '2026-10-27T04:41:01Z' } },
        { key: 'deliveries/01B', value: { id: '01B', at: '2026-10-26T22:10:00Z', eventId: 'e1', eventType: 'inbox.received', target: 'https://old.example.com/hook', status: 'abandoned', attempts: 5, nextAttemptAt: null, lastStatusCode: 404, lastError: 'Not Found', updatedAt: '2026-10-27T01:00:00Z' } },
        { key: 'deliveries/01C', value: { id: '01C', at: '2026-10-27T04:12:00Z', eventId: 'e3', eventType: 'inbox.received', target: 'https://hooks.example.com/loop-now', status: 'failed', attempts: 3, nextAttemptAt: '2026-10-27T04:17:00Z', lastStatusCode: 503, lastError: '503', updatedAt: '2026-10-27T04:12:30Z' } },
        { key: 'deliveries/legacy', value: { note: 'written by an older version' } },
      ],
    },
    logs: { webhook: Array.from({ length: 120 }, (_, i) => ({ id: 200 - i, at: '2026-10-27T04:41:02Z', level: i === 1 ? 'warn' : 'info', line: i === 0 ? 'delivered evt_01JB → hooks.example.com 200 in 118ms' : `line ${i}` })) },
    ...initial,
  })
  server.use(...fake.handlers, ...baseHandlers(), http.get(apiUrl('/projects'), () => HttpResponse.json({ projects: [defaultProject, other] })))
  return { ...renderRoutes(routes, path), fake }
}

describe('Plugin details', () => {
  it('uses 3 crisp tabs (Overview, Configuration, Activity & Logs), drops SectionToc and RecentActivity KPIs, and shows a 2-column Overview grid with expandable manifest details', async () => {
    const user = userEvent.setup()
    renderDetail()
    expect(await screen.findByRole('heading', { level: 1, name: 'Webhook notifications' })).toBeInTheDocument()
    const tabs = screen.getByRole('navigation', { name: 'Webhook notifications' })
    expect(within(tabs).getAllByRole('link').map((link) => [link.textContent, link.getAttribute('aria-current')])).toEqual([
      ['Overview', 'page'],
      ['Configuration', null],
      ['Activity & Logs', null],
    ])
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Plugin market›webhook')
    expect(screen.queryByRole('navigation', { name: 'Contents' })).toBeNull()
    expect(screen.queryByRole('region', { name: 'Recent activity' })).toBeNull()

    const enabled = screen.getByRole('region', { name: 'Enabled in projects' })
    await waitFor(() => expect(within(enabled).getByRole('switch', { name: `Enable Webhook notifications in ${defaultProject.name}` })).toHaveAttribute('aria-checked', 'true'))
    const defaultRow = within(enabled).getByText(defaultProject.name).closest('li')!
    expect(defaultRow).toHaveTextContent('Callback URL → https://hooks.example.com/propanda')
    expect(within(defaultRow).getByRole('link', { name: 'Configure ›' })).toHaveAttribute('href', '/plugins/webhook/config')

    const capabilities = screen.getByRole('region', { name: 'Capabilities & security' })
    expect(capabilities).toHaveTextContent('Platform side')
    expect(capabilities).toHaveTextContent('1 event subscribed')
    expect(capabilities).toHaveTextContent('2 permissions')
    await user.click(within(capabilities).getByText('Inspect manifest & permissions'))
    expect(within(capabilities).getByText('inbox.read').parentElement).toHaveTextContent('Read Inbox message bodies')
  })

  it('enabling in another project sends its existing project config and revision', async () => {
    const user = userEvent.setup()
    const { fake } = renderDetail()
    const toggle = await screen.findByRole('switch', { name: `Enable Webhook notifications in ${other.name}` })
    await waitFor(() => expect(toggle).toBeEnabled())
    await user.click(toggle)
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: `/projects/${other.id}/plugins/webhook`, query: {}, body: { enabled: true, values: {}, expectedRevision: 0 } }]))
  })

  it('configuration tab: edits global config, expands per-project overrides in place, and collapses runtime environment details', async () => {
    const user = userEvent.setup()
    const { fake } = renderDetail('/plugins/webhook/config')
    expect(await screen.findByLabelText('Callback URL')).toHaveValue('https://hooks.example.com/loop-now')
    expect(screen.getByText('Set')).toBeInTheDocument()
    await user.clear(screen.getByLabelText('Retry limit'))
    await user.type(screen.getByLabelText('Retry limit'), '3')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: '/plugins/webhook/config', query: {}, body: { values: { url: 'https://hooks.example.com/loop-now', retries: 3 }, expectedRevision: 2 } }]))

    const overrides = screen.getByRole('region', { name: 'Project overrides & accounts' })
    await waitFor(() => expect(overrides).toHaveTextContent(`${defaultProject.name}1 overrideCallback URL → https://hooks.example.com/propanda`))
    await user.click(within(overrides).getByRole('button', { name: 'Configure' }))
    const projectUrl = await within(overrides).findByLabelText('Callback URL')
    expect(projectUrl).toHaveValue('https://hooks.example.com/propanda')
    await user.clear(projectUrl)
    await user.type(projectUrl, 'https://hooks.example.com/updated')
    await user.click(within(overrides).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(fake.writes().at(-1)).toEqual({
        method: 'PUT',
        path: `/projects/${defaultProject.id}/plugins/webhook`,
        query: {},
        body: { enabled: true, values: { url: 'https://hooks.example.com/updated' }, expectedRevision: 3 },
      }),
    )

    await user.click(screen.getByText('Environment for the plugin process'))
    const env = screen.getByRole('region', { name: 'Environment for the plugin process' })
    expect(env).toHaveTextContent('LOOP_NOW_PLUGIN_DATA=/work/platform/plugins/webhook/data')
    expect(env).toHaveTextContent('Only inbox.read, state')
  })

  it('configuration tab: shows notEnabledProjects empty state when no project enables the plugin, and aliases /accounts to expanded project accounts', async () => {
    const lark = makeDetail(
      makePlugin({ id: 'lark-notify', displayName: 'Lark notifications' }),
      makeManifest({ id: 'lark-notify', displayName: 'Lark notifications', configSchema: {} as never, accounts: [larkAccountKind] }),
    )
    const first = renderDetail('/plugins/lark-notify/config', { details: [lark], projects: {} })
    expect(await screen.findByText('This plugin has no config.')).toBeInTheDocument()
    expect(await screen.findByText('No project has enabled this plugin yet.')).toBeInTheDocument()
    first.unmount()

    renderDetail('/plugins/lark-notify/accounts', {
      details: [lark],
      projects: { [defaultProject.id]: { 'lark-notify': makeProjectPlugin({ pluginId: 'lark-notify', displayName: 'Lark notifications', enabled: true }) } },
      accounts: [makeAccount({ projectId: defaultProject.id })],
    })
    const tabs = await screen.findByRole('navigation', { name: 'Lark notifications' })
    expect(within(tabs).getByRole('link', { name: 'Configuration' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByText('Dev group bot')).toBeInTheDocument()
  })

  it('activity & logs tab (and /events alias): filters events by status and project dropdown, and loads earlier entries', async () => {
    const user = userEvent.setup()
    const { fake } = renderDetail('/plugins/webhook/events')
    const tabs = await screen.findByRole('navigation', { name: 'Webhook notifications' })
    expect(within(tabs).getByRole('link', { name: 'Activity & Logs' })).toHaveAttribute('aria-current', 'page')
    const eventRows = () => within(screen.getByRole('list', { name: 'Events' })).getAllByRole('listitem')
    await waitFor(() => expect(eventRows()).toHaveLength(50))
    await user.click(screen.getByRole('button', { name: 'Load earlier' }))
    await waitFor(() => expect(eventRows()).toHaveLength(62))
    await user.click(screen.getByRole('radio', { name: 'Failed' }))
    await waitFor(() => expect(eventRows()).toHaveLength(1))
    await user.click(screen.getByRole('radio', { name: 'All' }))
    await user.selectOptions(screen.getByRole('combobox', { name: 'Filter by project' }), other.id)
    await waitFor(() => expect(eventRows()).toHaveLength(1))
    expect(fake.requests.filter((r) => r.path === '/plugins/webhook/deliveries').map((r) => r.query)).toContainEqual({ limit: '50', projectId: other.id })
  })

  it('activity & logs tab (/records alias): lists outbound delivery records newest first and renders raw JSON for unknown shapes', async () => {
    renderDetail('/plugins/webhook/records')
    const tabs = await screen.findByRole('navigation', { name: 'Webhook notifications' })
    expect(within(tabs).getByRole('link', { name: 'Activity & Logs' })).toHaveAttribute('aria-current', 'page')
    const rows = await screen.findAllByRole('row')
    expect(rows.slice(1).map((row) => row.textContent)).toEqual([
      expect.stringMatching(/hooks\.example\.com\/loop-now2001$/),
      expect.stringMatching(/hooks\.example\.com\/loop-nowRetryingNext .*3$/),
      expect.stringMatching(/old\.example\.com\/hookAbandoned · 4045$/),
      expect.stringContaining('deliveries/legacy'),
    ])
    expect(within(rows[4]).getByLabelText('Raw record')).toHaveTextContent('"note": "written by an older version"')
  })

  it('activity & logs tab (/logs alias): pages host logs 100 at a time with nextBefore', async () => {
    const user = userEvent.setup()
    renderDetail('/plugins/webhook/logs')
    const tabs = await screen.findByRole('navigation', { name: 'Webhook notifications' })
    expect(within(tabs).getByRole('link', { name: 'Activity & Logs' })).toHaveAttribute('aria-current', 'page')
    const logList = await screen.findByRole('list', { name: 'Logs' })
    await waitFor(() => expect(within(logList).getAllByRole('listitem')).toHaveLength(100))
    expect(within(logList).getAllByRole('listitem')[0]).toHaveTextContent('info delivered evt_01JB → hooks.example.com 200 in 118ms')
    await user.click(screen.getByRole('button', { name: 'Load earlier logs' }))
    await waitFor(() => expect(within(logList).getAllByRole('listitem')).toHaveLength(120))
    expect(screen.queryByRole('button', { name: 'Load earlier logs' })).toBeNull()
  })

  it('check for updates: previews from the installed source and updates to the new version after confirming', async () => {
    const user = userEvent.setup()
    const { fake } = renderDetail()
    await user.click(await screen.findByRole('button', { name: 'Check for updates' }))
    const dialog = await screen.findByRole('dialog', { name: /Update Webhook notifications/ })
    expect(dialog).toHaveTextContent('webhook · v1.2.0 → v1.3.0')
    await user.click(within(dialog).getByRole('button', { name: 'Update' }))
    await waitFor(() => expect(fake.writes().map((r) => [r.method, r.path, r.body])).toEqual([
      ['POST', '/plugin-previews', { pluginId: 'webhook' }],
      ['POST', '/plugins/webhook/update', { previewId: '71000000-0000-7000-8000-000000000001' }],
    ]))
  })

  it('uninstall: can also delete the data folder, and goes back to the plugin market afterwards', async () => {
    const user = userEvent.setup()
    const { fake, router } = renderDetail()
    await user.click(await screen.findByRole('button', { name: 'Uninstall' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Uninstall Webhook notifications?' })
    await user.click(within(dialog).getByRole('checkbox', { name: 'Also delete the data folder /work/platform/plugins/webhook/data' }))
    await user.click(within(dialog).getByRole('button', { name: 'Uninstall' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/plugins'))
    expect(fake.writes()).toEqual([{ method: 'DELETE', path: '/plugins/webhook', query: { deleteData: 'true' }, body: undefined }])
  })

  it('a missing plugin is explained with a link back to the plugin market', async () => {
    renderDetail('/plugins/missing')
    expect(await screen.findByText('This plugin doesn\'t exist; it may have been uninstalled.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to the plugin market' })).toHaveAttribute('href', '/plugins')
  })
})

describe('Plugin details: README and repository', () => {
  const readme = [
    '# Webhook notifications',
    '',
    '## Config',
    '',
    '- `url`: the callback URL',
    '- `retries`: the retry limit',
    '',
    '```json',
    '{ "url": "https://hooks.example.com" }',
    '```',
    '',
    'See the [docs](https://docs.example.com/webhook), the [example](./examples/basic.md) and [email](mailto:dev@example.com).',
    '',
    '![Architecture](./docs/arch.png)![Remote image](https://img.example.com/x.png)',
    '',
    '<script>alert(1)</script>',
  ].join('\n')

  it('the overview About card reads the README in the console language; renders headings, lists, code blocks, inline code and links; shows raw HTML as text; shows relative links and images as text only', async () => {
    const { fake } = renderDetail('/plugins/webhook', { readmes: { webhook: { file: 'README.en-US.md', markdown: readme, truncated: false } } })
    const card = await screen.findByRole('region', { name: 'About' })
    expect(fake.requests.find((r) => r.path === '/plugins/webhook/readme')?.query).toEqual({ locale: 'en-US' })
    expect(card).toHaveTextContent('README.en-US.md')
    expect(within(card).getByRole('heading', { level: 2, name: 'Config' })).toBeInTheDocument()
    expect(within(card).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['url: the callback URL', 'retries: the retry limit'])
    expect(within(card).getByText('url').tagName).toBe('CODE')
    expect(card.querySelector('pre code')).toHaveTextContent('{ "url": "https://hooks.example.com" }')
    const docs = within(card).getByRole('link', { name: 'docs' })
    expect(docs).toHaveAttribute('href', 'https://docs.example.com/webhook')
    expect(docs).toHaveAttribute('target', '_blank')
    expect(docs.getAttribute('rel')).toContain('noopener')
    expect(within(card).getByRole('link', { name: 'email' })).toHaveAttribute('href', 'mailto:dev@example.com')
    expect(within(card).queryByRole('link', { name: 'example' })).toBeNull()
    expect(within(card).getByText('example')).toBeInTheDocument()
    expect(card.querySelector('img')).toBeNull()
    expect(card).toHaveTextContent('ArchitectureRemote image')
    expect(card.querySelector('script')).toBeNull()
    expect(card).toHaveTextContent('<script>alert(1)</script>')
    expect(screen.queryByText('This README is too long; only the beginning is shown.')).toBeNull()
  })

  it('a truncated README is noted at the bottom of the card', async () => {
    renderDetail('/plugins/webhook', { readmes: { webhook: { file: 'README.md', markdown: '# A very long README', truncated: true } } })
    const card = await screen.findByRole('region', { name: 'About' })
    expect(card.lastElementChild).toHaveTextContent('This README is too long; only the beginning is shown.')
  })

  it('no About card without a README', async () => {
    const { client } = renderDetail()
    await screen.findByRole('region', { name: 'Capabilities & security' })
    await waitFor(() => expect(client.getQueryState(['plugins', 'readme', 'webhook'])?.status).toBe('success'))
    expect(screen.queryByRole('region', { name: 'About' })).toBeNull()
  })

  it('git source: the header links to the repository page, to /tree/<ref> on GitHub with a ref; an SSH address is shown as text only', async () => {
    const git = (location: string, ref: string | null) => makeDetail(makePlugin({ id: 'lark-notify', displayName: 'Lark notifications', source: makeResolvedSource({ kind: 'git', location, ref, commit: 'abc1234def' }) }))
    const { unmount } = renderDetail('/plugins/lark-notify', { details: [git('https://github.com/foo/loop-now-lark-notify.git', 'release/0.3')] })
    const link = await screen.findByRole('link', { name: 'Repository' })
    expect(link).toHaveAttribute('href', 'https://github.com/foo/loop-now-lark-notify/tree/release/0.3')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link.getAttribute('rel')).toContain('noopener')
    unmount()
    renderDetail('/plugins/lark-notify', { details: [git('git@github.com:foo/loop-now-lark-notify.git', null)] })
    await screen.findByRole('heading', { level: 1, name: 'Lark notifications' })
    expect(screen.queryByRole('link', { name: 'Repository' })).toBeNull()
    expect(screen.getByText('git@github.com:foo/loop-now-lark-notify.git')).toBeInTheDocument()
  })

  it('official plugins have no repository link', async () => {
    renderDetail()
    await screen.findByRole('heading', { level: 1, name: 'Webhook notifications' })
    expect(screen.queryByRole('link', { name: 'Repository' })).toBeNull()
  })
})

describe('Plugin details: model providers', () => {
  const grok = makePlugin({ id: 'grok', displayName: 'Grok subscription' })
  const withProviders = (overrides: Partial<ReturnType<typeof makeDetail>>, agentExtensions = 1) => ({
    ...makeDetail(grok, makeManifest({ id: 'grok', displayName: 'Grok subscription', components: makeComponents({ agentExtensions }) })),
    ...overrides,
  })
  const provider = (id: string, overrides: Partial<ReturnType<typeof makeDetail>['providers'][number]> = {}) => ({
    id, name: id, extension: 'extensions/grok.ts', oauth: true, overridesBuiltin: false, inCatalog: false, error: null, modelCount: 2, ...overrides,
  })

  it('lists the providers registered by Pi extensions inside the manifest details (auto-opened when extension errors exist)', async () => {
    renderDetail('/plugins/grok', {
      details: [
        withProviders({
          providers: [
            provider('grok-cli', { name: 'Grok CLI', inCatalog: true, modelCount: 3 }),
            provider('anthropic', { overridesBuiltin: true }),
            provider('grok-key', { oauth: false }),
            provider('grok-bad', { name: '<b>bad</b>', error: 'TypeError: <script>x</script>' }),
            provider('grok-dup'),
            provider('grok-bare', { name: 'Grok Bare', inCatalog: true }),
          ],
          providerErrors: [{ path: 'extensions/broken.ts', error: 'SyntaxError: Unexpected token' }],
          providerState: { 'grok-cli': { paths: ['.grok/auth.json', '.config/grok'] }, 'grok-key': { paths: ['.grok-key'] } },
        }),
      ],
    })
    const panel = await screen.findByRole('region', { name: 'Model providers' })
    expect(panel).toHaveTextContent('Read from its Pi extensions when installed or updated')
    const items = within(within(panel).getAllByRole('list')[0]).getAllByRole('listitem')
    expect(items.map((item) => item.textContent)).toEqual([
      "Grok CLIgrok-cliOAuth loginIn the model catalogLogin state saved with project accounts and platform Agents' logins: .grok/auth.json, .config/grok3 models · From extensions/grok.ts",
      "anthropicOAuth loginSame id as a built-in Pi provider: it only overrides that provider's settings and isn't added to the model catalog2 models · From extensions/grok.ts",
      "grok-keyDoesn't register an OAuth login, so it isn't added to the model catalogLogin state saved with project accounts and platform Agents' logins: .grok-key2 models · From extensions/grok.ts",
      '<b>bad</b>grok-badOAuth loginRegistration failed: TypeError: <script>x</script>2 models · From extensions/grok.ts',
      'grok-dupOAuth loginNot in the model catalog (for example, a provider in models.json has the same id)2 models · From extensions/grok.ts',
      // In the model catalog but no state path declared: warn that the login may not be saved with the account.
      'Grok Baregrok-bareOAuth loginIn the model catalogNo login state paths declared, so the login may not be saved with the account2 models · From extensions/grok.ts',
    ])
    expect(panel.querySelector('b, script')).toBeNull()
    const failed = within(panel).getByRole('heading', { name: 'Extensions that failed to load' }).parentElement!
    expect(failed).toHaveTextContent('extensions/broken.tsSyntaxError: Unexpected token')
    expect(within(panel).queryByText('No model providers were found.')).toBeNull()
  })

  it('when the whole read fails, explains why instead of saying none were found', async () => {
    renderDetail('/plugins/grok', { details: [withProviders({ providersError: 'Pi unavailable: timed out' })] })
    const panel = await screen.findByRole('region', { name: 'Model providers' })
    expect(within(panel).getByRole('alert')).toHaveTextContent("Couldn't read them: Pi unavailable: timed out")
    expect(within(panel).queryByText('No model providers were found.')).toBeNull()
  })

  it('uninstalling a plugin that registers model providers explains that subscription accounts are kept by default and deleted along with the data; plugins without providers do not say this', async () => {
    const user = userEvent.setup()
    const { unmount } = renderDetail('/plugins/grok', { details: [withProviders({ providers: [provider('grok-cli', { inCatalog: true })] })] })
    await user.click(await screen.findByRole('button', { name: 'Uninstall' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Uninstall Grok subscription?' })
    expect(dialog).toHaveTextContent('The project subscription accounts and platform Agents\' own logins for the model providers it registers are kept by default and come back if you reinstall it. With this checked they\'re deleted too (accounts in use by an unfinished task stay until the task ends).')
    unmount()
    renderDetail()
    await user.click(await screen.findByRole('button', { name: 'Uninstall' }))
    expect(await screen.findByRole('alertdialog', { name: 'Uninstall Webhook notifications?' })).not.toHaveTextContent('subscription accounts')
  })

  it('Pi extensions without registered providers: says none were found when expanded; a plugin with no extensions and nothing read omits the providers section', async () => {
    const user = userEvent.setup()
    const { unmount } = renderDetail('/plugins/grok', { details: [withProviders({})] })
    await user.click(await screen.findByText('Inspect manifest & permissions'))
    const panel = await screen.findByRole('region', { name: 'Model providers' })
    expect(panel).toHaveTextContent('No model providers were found.')
    unmount()
    renderDetail()
    await user.click(await screen.findByText('Inspect manifest & permissions'))
    expect(screen.queryByRole('region', { name: 'Model providers' })).toBeNull()
  })
})

describe('Plugin details: source label and market update button', () => {
  const official = makeMarket({ plugins: [makeMarketEntry({ id: 'grok', displayName: 'Grok CLI', installedVersion: '1.0.0' })] })
  const other = makeMarket({ url: 'https://example.com/market.json', name: 'Community market', official: false, plugins: [makeMarketEntry({ id: 'grok', verified: true, installedVersion: '1.0.0' }), makeMarketEntry({ id: 'raw', verified: false, installedVersion: '1.0.0' })] })
  const fromMarket = (id: string, market: string | null, kind: 'npm' | 'git' = 'npm') =>
    makeDetail(makePlugin({ id, displayName: id, source: makeResolvedSource({ kind, location: `pi-${id}`, npmVersion: '1.0.0', market }) }))
  const badge = async (id: string, detail: ReturnType<typeof makeDetail>) => {
    const { unmount } = renderDetail(`/plugins/${id}`, { details: [detail], markets: [official, other] })
    const heading = await screen.findByRole('heading', { level: 1, name: id })
    const text = await waitFor(() => {
      const found = heading.closest('header')!.querySelector('[data-page-meta] [data-source-badge]')?.textContent
      if (!found) throw new Error('The source label is not shown yet')
      return found
    })
    unmount()
    return text
  }

  it('header update button reflects market status: enabled Update button when newer exists, disabled Up to date otherwise', async () => {
    const newer = makeMarket({ plugins: [makeMarketEntry({ id: 'grok', displayName: 'Grok CLI', installedVersion: '1.0.0', version: '1.1.0', updateAvailable: true })] })
    const first = renderDetail('/plugins/grok', { details: [fromMarket('grok', newer.url)], markets: [newer] })
    expect(await screen.findByRole('button', { name: 'Update to v1.1.0' })).toBeEnabled()
    first.unmount()
    renderDetail('/plugins/grok', { details: [fromMarket('grok', official.url)], markets: [official] })
    expect(await screen.findByRole('button', { name: 'Up to date' })).toBeDisabled()
  })

  it('follows the install rules: installed from the official market counts as official; verified entries in other markets say verified; unverified ones and ones not installed from a market say unreviewed', async () => {
    expect(await badge('grok', fromMarket('grok', official.url))).toBe('Official')
    expect(await badge('grok', fromMarket('grok', other.url))).toBe('npm · verified')
    expect(await badge('raw', fromMarket('raw', other.url))).toBe('npm · unreviewed')
    expect(await badge('direct', fromMarket('direct', null, 'git'))).toBe('git · unreviewed')
    // The market is no longer in the list: there is no way to tell, so treat it as unreviewed.
    expect(await badge('gone', fromMarket('gone', 'https://removed.example/market.json'))).toBe('npm · unreviewed')
  })
})
