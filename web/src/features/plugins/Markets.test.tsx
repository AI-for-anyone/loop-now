import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { fakePlugins, makeDetail, makeMarket, makeMarketEntry, makePlugin, makeResolvedSource } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

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

describe('插件市场的页签', () => {
  it('三个页签写在地址里：已安装、官方市场、其他市场', async () => {
    const user = userEvent.setup()
    const { router } = renderMarket('/plugins')
    const tabs = await screen.findByRole('navigation', { name: '插件市场' })
    await waitFor(() => expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['已安装 2', '官方市场', '其他市场']))
    await user.click(within(tabs).getByRole('link', { name: '官方市场' }))
    expect(router.state.location.search).toBe('?tab=official')
    expect(within(tabs).getByRole('link', { name: '官方市场' })).toHaveAttribute('aria-current', 'page')
  })

  it('官方市场：没装的可安装（按市场来源取预览）；有新版本的可更新；已是最新的写出来', async () => {
    const user = userEvent.setup()
    const { fake, router } = renderMarket('/plugins?tab=official')
    const card = (name: string) => screen.getByRole('heading', { name }).closest('li')!
    await screen.findByRole('heading', { name: '用量周报' })
    expect(card('Webhook 通知')).toHaveTextContent('已装 v1.2.0已是最新')
    expect(within(card('Git 自动提交')).getByRole('button', { name: '更新到 v1.2.0' })).toBeInTheDocument()
    await user.click(within(card('用量周报')).getByRole('button', { name: '安装' }))
    const dialog = await screen.findByRole('dialog', { name: /安装/ })
    expect(fake.requests.find((r) => r.path === '/plugin-previews')?.body).toEqual({ source: { kind: 'market', market: OFFICIAL, id: 'usage-report', version: '0.9.0' } })
    await user.click(within(dialog).getByRole('button', { name: '安装' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/plugins/usage-report'))
  })

  it('其他市场：列出已验证与未验证；读取失败的市场写出原因；可以添加与移除市场地址', async () => {
    const user = userEvent.setup()
    const { fake } = renderMarket('/plugins?tab=other')
    const community = await screen.findByRole('region', { name: '社区市场' })
    expect(within(community).getByText('未验证')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('未命名的市场 读取失败：404 Not Found')
    const manage = screen.getByRole('region', { name: '市场地址' })
    await user.type(within(manage).getByLabelText('市场地址'), 'http://insecure.example.com/m.json')
    await user.click(within(manage).getByRole('button', { name: '添加' }))
    expect(within(manage).getByText('请填写 https:// 开头的地址')).toBeInTheDocument()
    await user.clear(within(manage).getByLabelText('市场地址'))
    await user.type(within(manage).getByLabelText('市场地址'), 'https://more.example.com/m.json')
    await user.click(within(manage).getByRole('button', { name: '添加' }))
    await waitFor(() => expect(within(manage).getByText('https://more.example.com/m.json')).toBeInTheDocument())
    await user.click(within(manage).getByRole('button', { name: '移除市场 https://broken.example.com/market.json' }))
    await waitFor(() =>
      expect(fake.writes().map((r) => r.body)).toEqual([
        { disabled: false, markets: ['https://plugins.example.com/market.json', 'https://broken.example.com/market.json', 'https://more.example.com/m.json'] },
        { disabled: false, markets: ['https://plugins.example.com/market.json', 'https://more.example.com/m.json'] },
      ]),
    )
  })

  it('已安装的卡片：从市场装的、市场里有新版本时可直接更新', async () => {
    const user = userEvent.setup()
    const { fake } = renderMarket('/plugins')
    const card = (await screen.findByRole('link', { name: 'Git 自动提交' })).closest('li')!
    const update = await within(card).findByRole('button', { name: '更新到 v1.2.0' })
    expect(within(screen.getByRole('link', { name: 'Webhook 通知' }).closest('li')!).queryByRole('button', { name: /更新到/ })).toBeNull()
    await user.click(update)
    await screen.findByRole('dialog', { name: /更新 Git 自动提交/ })
    expect(fake.requests.find((r) => r.path === '/plugin-previews')?.body).toEqual({ source: { kind: 'market', market: OFFICIAL, id: 'git-commit', version: '1.2.0' } })
  })

  it('详情页：从市场装的按市场判断能否更新；已是最新时按钮不可点', async () => {
    renderMarket('/plugins/git-commit')
    expect(await screen.findByRole('button', { name: '更新到 v1.2.0' })).toBeEnabled()
  })

  it('详情页：市场里没有新版本时写“已是最新”', async () => {
    renderMarket('/plugins/git-commit', {
      markets: [makeMarket({ plugins: [makeMarketEntry({ id: 'git-commit', displayName: 'Git 自动提交', version: '1.1.2', installedVersion: '1.1.2' })] })],
    })
    expect(await screen.findByRole('button', { name: '已是最新' })).toBeDisabled()
  })
})

describe('插件作者按语言给出的名称与说明', () => {
  it('市场条目取当前界面语言，缺当前语言时回退到 en-US，再到作者写的第一项', async () => {
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

  it('已安装的卡片与插件详情页头同样按语言取名称和说明', async () => {
    const localized = makeDetail(makePlugin({ id: 'lark-notify', displayName: { 'en-US': 'Lark notify', 'zh-CN': '飞书通知' }, description: { 'en-US': 'Push to Lark', 'zh-CN': '推送到飞书' } }))
    const { router } = renderMarket('/plugins', { details: [localized] })
    expect(await screen.findByRole('link', { name: '飞书通知' })).toBeInTheDocument()
    await router.navigate('/plugins/lark-notify')
    expect(await screen.findByRole('heading', { name: '飞书通知', level: 1 })).toBeInTheDocument()
    expect(screen.getByText(/^推送到飞书 · /)).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('[object Object]')
  })
})
