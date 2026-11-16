import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { fakePlugins, makeDetail, makeMarket, makeMarketEntry, makePlugin, makeResolvedSource } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

// In the Chinese UI the current language (zh-CN) differs from the en-US fallback, so these tests show that the author's zh-CN text is
// picked first. Moved from src/features/plugins/Markets.test.tsx, which keeps the same scenarios in English.

const OFFICIAL = 'https://market.loop-now.dev/loop-now-market.json'
const fromMarket = makeDetail(makePlugin({ id: 'git-commit', displayName: 'Git 自动提交', version: '1.1.2', source: makeResolvedSource({ kind: 'git', location: 'https://github.com/loop-now/git-commit', market: OFFICIAL }) }))

function renderMarket(path: string, initial: Parameters<typeof fakePlugins>[0] = {}) {
  const fake = fakePlugins({
    details: [makeDetail(), fromMarket],
    markets: [
      makeMarket({
        plugins: [
          makeMarketEntry(),
          makeMarketEntry({ id: 'git-commit', displayName: 'Git 自动提交', description: '回合结束后自动提交', version: '1.2.0', installedVersion: '1.1.2', updateAvailable: true }),
          makeMarketEntry({ id: 'webhook', displayName: 'Webhook 通知', version: '1.2.0', installedVersion: '1.2.0' }),
        ],
      }),
      makeMarket({ url: 'https://plugins.example.com/market.json', name: '社区市场', official: false, plugins: [makeMarketEntry({ id: 'lark-notify', displayName: '飞书通知', verified: false, version: '0.3.1' })] }),
      makeMarket({ url: 'https://broken.example.com/market.json', name: null, official: false, error: '404 Not Found', plugins: [] }),
    ],
    settings: { disabled: false, markets: ['https://plugins.example.com/market.json', 'https://broken.example.com/market.json'] },
    ...initial,
  })
  server.use(...fake.handlers, ...baseHandlers())
  return { ...renderRoutes(routes, path), fake }
}

describe('Plugin names and descriptions the author gives per language', () => {
  it('a market entry uses the current UI language, falling back to en-US and then to the first one the author wrote', async () => {
    renderMarket('/plugins?tab=official', {
      markets: [
        makeMarket({
          plugins: [
            makeMarketEntry({ id: 'lark-notify', displayName: { 'en-US': 'Lark notify', 'zh-CN': '飞书通知' }, description: { 'en-US': 'Push to Lark', 'zh-CN': '推送到飞书' } }),
            makeMarketEntry({ id: 'weekly', displayName: { 'en-US': 'Weekly report' }, description: { ja: '週報を送る', 'en-US': 'Send a weekly report' } }),
            makeMarketEntry({ id: 'ja-only', displayName: { ja: '週報' }, description: '只有一种写法' }),
          ],
        }),
      ],
    })
    const card = (name: string) => screen.getByRole('heading', { name }).closest('li')!
    await screen.findByRole('heading', { name: '飞书通知' })
    expect(card('飞书通知')).toHaveTextContent('推送到飞书')
    expect(card('Weekly report')).toHaveTextContent('Send a weekly report')
    expect(card('週報')).toHaveTextContent('只有一种写法')
  })

  it('installed cards and the plugin detail header also pick the name and description by language', async () => {
    const localized = makeDetail(makePlugin({ id: 'lark-notify', displayName: { 'en-US': 'Lark notify', 'zh-CN': '飞书通知' }, description: { 'en-US': 'Push to Lark', 'zh-CN': '推送到飞书' } }))
    const { router } = renderMarket('/plugins', { details: [localized] })
    expect(await screen.findByRole('link', { name: '飞书通知' })).toBeInTheDocument()
    await router.navigate('/plugins/lark-notify')
    expect(await screen.findByRole('heading', { name: '飞书通知', level: 1 })).toBeInTheDocument()
    expect(screen.getByText(/^推送到飞书 · /)).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('[object Object]')
  })
})
