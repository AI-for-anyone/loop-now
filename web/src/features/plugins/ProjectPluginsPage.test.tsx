import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { defaultProject } from '@/test/api'
import { fakePlugins, larkAccountKind, makeAccount, makeComponents, makeConfig, makeDelivery, makeDetail, makeManifest, makePlugin, makeProjectPlugin, makeResolvedSource } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const path = `/p/${encodeURIComponent(defaultProject.name)}/plugins`
const lark = makeDetail(
  makePlugin({ id: 'lark-notify', displayName: '飞书通知', version: '0.3.1', source: makeResolvedSource({ kind: 'git', location: 'https://github.com/foo/lark' }) }),
  makeManifest({ id: 'lark-notify', displayName: '飞书通知', version: '0.3.1', configSchema: {} as never, accounts: [larkAccountKind] }),
)
const browser = makeDetail(
  makePlugin({ id: 'playwright', displayName: '专用浏览器', version: '2.0.0', hasPlatform: false }),
  makeManifest({ id: 'playwright', displayName: '专用浏览器', version: '2.0.0', events: [], permissions: [], configSchema: {} as never, components: makeComponents({ platform: false, agentExtensions: 1 }) }),
)

function renderProject() {
  const fake = fakePlugins({
    details: [makeDetail(), lark, browser],
    configs: { webhook: makeConfig({ values: { url: 'https://hooks.example.com/loop-now' } as never, revision: 2 }) },
    projects: {
      [defaultProject.id]: {
        webhook: makeProjectPlugin({ enabled: true, config: makeConfig({ values: {} as never, revision: 1 }) }),
        'lark-notify': makeProjectPlugin({ pluginId: 'lark-notify', displayName: '飞书通知', version: '0.3.1', enabled: true, projectFields: [] }),
      },
    },
    accounts: [makeAccount({ projectId: defaultProject.id }), makeAccount({ id: '72000000-0000-7000-8000-000000000002', projectId: defaultProject.id, name: '个人测试', values: { appId: 'cli_9f8e7d6c5b4a' } as never })],
    deliveries: { webhook: [makeDelivery({ eventId: 'mine', projectId: defaultProject.id }), makeDelivery({ eventId: 'theirs', projectId: '00000000-0000-7000-8000-00000000ffff', eventType: 'run.ended' })] },
  })
  server.use(...fake.handlers, ...baseHandlers())
  return { ...renderRoutes(routes, path), fake }
}

const card = async (name: string) => (await screen.findByRole('link', { name })).closest('li')!

describe('项目插件页', () => {
  it('每个已安装插件一张卡片：启用开关；只有 Agent 半边的说明去 Agent 配置里授权', async () => {
    renderProject()
    const webhook = await card('Webhook 通知')
    expect(within(webhook).getByRole('switch', { name: '在这个项目启用 Webhook 通知' })).toHaveAttribute('aria-checked', 'true')
    const playwright = await card('专用浏览器')
    expect(within(playwright).getByRole('switch', { name: '在这个项目启用 专用浏览器' })).toHaveAttribute('aria-checked', 'false')
    await waitFor(() => expect(playwright).toHaveTextContent('只有 Agent 半边。启用后去 Agent 配置里授权。'))
  })

  it('启用：带上空的项目级配置与版本 0', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const playwright = await card('专用浏览器')
    await user.click(within(playwright).getByRole('switch', { name: '在这个项目启用 专用浏览器' }))
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: `/projects/${defaultProject.id}/plugins/playwright`, query: {}, body: { enabled: true, values: {}, expectedRevision: 0 } }]))
    await waitFor(() => expect(within(playwright).getByRole('switch', { name: '在这个项目启用 专用浏览器' })).toHaveAttribute('aria-checked', 'true'))
  })

  it('项目级配置只有 x-scope: project 的项，写出全局值；保存时保持启用并带版本', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const webhook = await card('Webhook 通知')
    const url = await within(webhook).findByLabelText('回调地址')
    expect(within(webhook).queryByLabelText('重试上限')).toBeNull()
    expect(webhook).toHaveTextContent('全局值：https://hooks.example.com/loop-now')
    await user.type(url, 'https://hooks.example.com/propanda')
    await user.click(within(webhook).getByRole('button', { name: '保存' }))
    await waitFor(() =>
      expect(fake.writes()).toEqual([{ method: 'PUT', path: `/projects/${defaultProject.id}/plugins/webhook`, query: {}, body: { enabled: true, values: { url: 'https://hooks.example.com/propanda' }, expectedRevision: 1 } }]),
    )
  })

  it('在这个项目里只列这个项目的事件', async () => {
    const { fake } = renderProject()
    const webhook = await card('Webhook 通知')
    await waitFor(() => expect(within(webhook).getAllByText('inbox.received')).toHaveLength(1))
    expect(within(webhook).queryByText('run.ended')).toBeNull()
    expect(fake.requests.find((r) => r.path === '/plugins/webhook/deliveries')?.query).toEqual({ limit: '3', projectId: defaultProject.id })
  })

  it('账号：列出名称与 App ID 的开头；添加时密钥只写；修改带版本；删除前确认', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const larkCard = await card('飞书通知')
    const accounts = await within(larkCard).findByRole('region', { name: '账号 · 飞书应用' })
    await waitFor(() => expect(within(accounts).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['研发群机器人cli_a1b2编辑删除', '个人测试cli_9f8e7d…编辑删除']))

    await user.click(within(accounts).getByRole('button', { name: '添加账号' }))
    await user.type(within(accounts).getByLabelText('名称'), '值班机器人')
    await user.type(within(accounts).getByLabelText('App ID'), 'cli_new')
    await user.type(within(accounts).getByLabelText('App Secret'), 's3cret')
    await user.click(within(accounts).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(within(accounts).getAllByRole('listitem')).toHaveLength(3))

    await user.click(within(accounts).getByRole('button', { name: '编辑 个人测试' }))
    expect(within(accounts).getByText('已设置')).toBeInTheDocument()
    await user.clear(within(accounts).getByLabelText('名称'))
    await user.type(within(accounts).getByLabelText('名称'), '个人测试 2')
    await user.click(within(accounts).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(within(accounts).getByText('个人测试 2')).toBeInTheDocument())

    await user.click(within(accounts).getByRole('button', { name: '删除 研发群机器人' }))
    await user.click(within(await screen.findByRole('alertdialog', { name: '删除账号 研发群机器人？' })).getByRole('button', { name: '删除' }))
    await waitFor(() => expect(within(accounts).queryByText('研发群机器人')).toBeNull())

    const base = `/projects/${defaultProject.id}/plugins/lark-notify/accounts`
    expect(fake.writes().map((r) => [r.method, r.path, r.body])).toEqual([
      ['POST', base, { kind: 'lark', name: '值班机器人', values: { appId: 'cli_new' }, secrets: { appSecret: 's3cret' } }],
      ['PUT', `${base}/72000000-0000-7000-8000-000000000002`, { name: '个人测试 2', values: { appId: 'cli_9f8e7d6c5b4a' }, expectedRevision: 1 }],
      ['DELETE', `${base}/72000000-0000-7000-8000-000000000001`, undefined],
    ])
  })
})
