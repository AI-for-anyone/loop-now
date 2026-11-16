import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { fakePlugins, makeDetail, makeMarket, makeMarketEntry, makePlugin, makeResolvedSource } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const OFFICIAL = 'https://market.loop-now.dev/loop-now-market.json'
const fromMarket = makeDetail(makePlugin({ id: 'git-commit', displayName: 'Git auto-commit', version: '1.1.2', source: makeResolvedSource({ kind: 'git', location: 'https://github.com/loop-now/git-commit', market: OFFICIAL }) }))

function renderMarket(path: string, initial: Parameters<typeof fakePlugins>[0] = {}) {
  const fake = fakePlugins({
    details: [makeDetail(), fromMarket],
    markets: [
      makeMarket({
        plugins: [
          makeMarketEntry(),
          makeMarketEntry({ id: 'git-commit', displayName: 'Git auto-commit', description: 'Commits automatically after each turn', version: '1.2.0', installedVersion: '1.1.2', updateAvailable: true }),
          makeMarketEntry({ id: 'webhook', displayName: 'Webhook 通知', version: '1.2.0', installedVersion: '1.2.0' }),
        ],
      }),
      makeMarket({ url: 'https://plugins.example.com/market.json', name: 'Community market', official: false, plugins: [makeMarketEntry({ id: 'lark-notify', displayName: 'Lark notifications', verified: false, version: '0.3.1' })] }),
      makeMarket({ url: 'https://broken.example.com/market.json', name: null, official: false, error: '404 Not Found', plugins: [] }),
    ],
    settings: { disabled: false, markets: ['https://plugins.example.com/market.json', 'https://broken.example.com/market.json'] },
    ...initial,
  })
  server.use(...fake.handlers, ...baseHandlers())
  return { ...renderRoutes(routes, path), fake }
}

describe('Plugin market tabs', () => {
  it('the three tabs are kept in the URL: installed, official market, other markets', async () => {
    const user = userEvent.setup()
    const { router } = renderMarket('/plugins')
    const tabs = await screen.findByRole('navigation', { name: 'Plugin market' })
    await waitFor(() => expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['Installed 2', 'Official market', 'Other markets']))
    await user.click(within(tabs).getByRole('link', { name: 'Official market' }))
    expect(router.state.location.search).toBe('?tab=official')
    expect(within(tabs).getByRole('link', { name: 'Official market' })).toHaveAttribute('aria-current', 'page')
  })

  it('official market: ones not installed can be installed (previewed from the market source); ones with a new version can be updated; up-to-date ones say so', async () => {
    const user = userEvent.setup()
    const { fake, router } = renderMarket('/plugins?tab=official')
    const card = (name: string) => screen.getByRole('heading', { name }).closest('li')!
    await screen.findByRole('heading', { name: '用量周报' })
    expect(card('Webhook 通知')).toHaveTextContent('v1.2.0 installedUp to date')
    expect(within(card('Git auto-commit')).getByRole('button', { name: 'Update to v1.2.0' })).toBeInTheDocument()
    await user.click(within(card('用量周报')).getByRole('button', { name: 'Install' }))
    const dialog = await screen.findByRole('dialog', { name: /Install/ })
    expect(fake.requests.find((r) => r.path === '/plugin-previews')?.body).toEqual({ source: { kind: 'market', market: OFFICIAL, id: 'usage-report', version: '0.9.0' } })
    await user.click(within(dialog).getByRole('button', { name: 'Install' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/plugins/usage-report'))
  })

  it('cards from git sources link to the repository page: the ref is split off by the server rules, and GitHub with a ref links to /tree/<ref>; npm and SSH addresses have no link', async () => {
    renderMarket('/plugins?tab=official', {
      markets: [
        makeMarket({
          plugins: [
            makeMarketEntry(),
            makeMarketEntry({ id: 'private-tool', displayName: 'URL with credentials', source: 'git:https://ci@github.com/foo/private-tool' }),
            makeMarketEntry({ id: 'gitlab-tool', displayName: 'GitLab source', source: 'git:https://gitlab.com/foo/gitlab-tool.git@main' }),
            makeMarketEntry({ id: 'ssh-tool', displayName: 'SSH source', source: 'git:git@github.com:foo/ssh-tool.git@v1.0.0' }),
            makeMarketEntry({ id: 'npm-tool', displayName: 'npm source', source: 'npm:@foo/npm-tool@1.0.0' }),
          ],
        }),
      ],
    })
    const card = (name: string) => screen.getByRole('heading', { name }).closest('li')!
    await screen.findByRole('heading', { name: '用量周报' })
    const link = within(card('用量周报')).getByRole('link', { name: 'Repository' })
    expect(link).toHaveAttribute('href', 'https://github.com/loop-now/usage-report/tree/v0.9.0')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link.getAttribute('rel')).toContain('noopener')
    expect(within(card('URL with credentials')).getByRole('link', { name: 'Repository' })).toHaveAttribute('href', 'https://github.com/foo/private-tool')
    expect(within(card('GitLab source')).getByRole('link', { name: 'Repository' })).toHaveAttribute('href', 'https://gitlab.com/foo/gitlab-tool.git')
    expect(within(card('SSH source')).queryByRole('link', { name: 'Repository' })).toBeNull()
    expect(within(card('npm source')).queryByRole('link', { name: 'Repository' })).toBeNull()
  })

  it('other markets: lists verified and unverified ones; a market that failed to load shows the reason; market URLs can be added and removed', async () => {
    const user = userEvent.setup()
    const { fake } = renderMarket('/plugins?tab=other')
    const community = await screen.findByRole('region', { name: 'Community market' })
    expect(within(community).getByText('Unverified')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent("Couldn't read Unnamed market: 404 Not Found")
    const manage = screen.getByRole('region', { name: 'Market URLs' })
    await user.type(within(manage).getByLabelText('Market URL'), 'http://insecure.example.com/m.json')
    await user.click(within(manage).getByRole('button', { name: 'Add' }))
    expect(within(manage).getByText('Enter a URL starting with https://')).toBeInTheDocument()
    await user.clear(within(manage).getByLabelText('Market URL'))
    await user.type(within(manage).getByLabelText('Market URL'), 'https://more.example.com/m.json')
    await user.click(within(manage).getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(within(manage).getByText('https://more.example.com/m.json')).toBeInTheDocument())
    await user.click(within(manage).getByRole('button', { name: 'Remove market https://broken.example.com/market.json' }))
    await waitFor(() =>
      expect(fake.writes().map((r) => r.body)).toEqual([
        { disabled: false, markets: ['https://plugins.example.com/market.json', 'https://broken.example.com/market.json', 'https://more.example.com/m.json'] },
        { disabled: false, markets: ['https://plugins.example.com/market.json', 'https://more.example.com/m.json'] },
      ]),
    )
  })

  it('installed cards: one installed from a market can be updated directly when the market has a new version', async () => {
    const user = userEvent.setup()
    const { fake } = renderMarket('/plugins')
    const card = (await screen.findByRole('link', { name: 'Git auto-commit' })).closest('li')!
    const update = await within(card).findByRole('button', { name: 'Update to v1.2.0' })
    expect(within(screen.getByRole('link', { name: 'Webhook 通知' }).closest('li')!).queryByRole('button', { name: /Update to/ })).toBeNull()
    await user.click(update)
    await screen.findByRole('dialog', { name: /Update Git auto-commit/ })
    expect(fake.requests.find((r) => r.path === '/plugin-previews')?.body).toEqual({ source: { kind: 'market', market: OFFICIAL, id: 'git-commit', version: '1.2.0' } })
  })

  it('detail page: one installed from a market uses the market to decide whether it can update; the button is disabled when up to date', async () => {
    renderMarket('/plugins/git-commit')
    expect(await screen.findByRole('button', { name: 'Update to v1.2.0' })).toBeEnabled()
  })

  it('detail page: says "Up to date" when the market has no new version', async () => {
    renderMarket('/plugins/git-commit', {
      markets: [makeMarket({ plugins: [makeMarketEntry({ id: 'git-commit', displayName: 'Git auto-commit', version: '1.1.2', installedVersion: '1.1.2' })] })],
    })
    expect(await screen.findByRole('button', { name: 'Up to date' })).toBeDisabled()
  })
})

describe('Plugin names and descriptions the author gives per language', () => {
  it('a market entry uses the current UI language, falling back to en-US and then to the first one the author wrote', async () => {
    renderMarket('/plugins?tab=official', {
      markets: [
        makeMarket({
          plugins: [
            makeMarketEntry({ id: 'lark-notify', displayName: { 'en-US': 'Lark notify', 'zh-CN': '飞书通知' }, description: { 'en-US': 'Push to Lark', 'zh-CN': '推送到飞书' } }),
            makeMarketEntry({ id: 'weekly', displayName: { 'en-US': 'Weekly report' }, description: { ja: '週報を送る', 'en-US': 'Send a weekly report' } }), // check-english: zh (Japanese text: a language other than the UI language)
            makeMarketEntry({ id: 'ja-only', displayName: { ja: '週報' }, description: 'Only one wording' }), // check-english: zh (Japanese text: an entry with neither the UI language nor en-US)
          ],
        }),
      ],
    })
    const card = (name: string) => screen.getByRole('heading', { name }).closest('li')!
    await screen.findByRole('heading', { name: 'Lark notify' })
    expect(card('Lark notify')).toHaveTextContent('Push to Lark')
    expect(card('Weekly report')).toHaveTextContent('Send a weekly report')
    expect(card('週報')).toHaveTextContent('Only one wording') // check-english: zh (Japanese text: an entry with neither the UI language nor en-US)
  })

  it('installed cards and the plugin detail header also pick the name and description by language', async () => {
    const localized = makeDetail(makePlugin({ id: 'lark-notify', displayName: { 'en-US': 'Lark notify', 'zh-CN': '飞书通知' }, description: { 'en-US': 'Push to Lark', 'zh-CN': '推送到飞书' } }))
    const { router } = renderMarket('/plugins', { details: [localized] })
    expect(await screen.findByRole('link', { name: 'Lark notify' })).toBeInTheDocument()
    await router.navigate('/plugins/lark-notify')
    expect(await screen.findByRole('heading', { name: 'Lark notify', level: 1 })).toBeInTheDocument()
    expect(screen.getByText(/^Push to Lark · /)).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('[object Object]')
  })
})
