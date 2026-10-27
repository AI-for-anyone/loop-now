import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { fakePlugins, makeComponents, makeDetail, makeHost, makeManifest, makePlugin, makeResolvedSource } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, currentSidebarLinks } from '@/test/shell'

const playwright = makeDetail(
  makePlugin({ id: 'playwright', displayName: '专用浏览器', version: '2.0.0', hasPlatform: false, enabledProjects: 1, grantedAgents: 2 }),
  makeManifest({ id: 'playwright', displayName: '专用浏览器', version: '2.0.0', events: [], permissions: [], configSchema: {} as never, components: makeComponents({ platform: false, agentExtensions: 1, mcpServers: ['browser'], skills: ['skills/browse'] }) }),
)
const lark = makeDetail(
  makePlugin({ id: 'lark-notify', displayName: '飞书通知', version: '0.3.1', source: makeResolvedSource({ kind: 'git', location: 'https://github.com/foo/lark' }), status: 'error', lastError: '订阅了清单里没有声明的事件 run.ended。' }),
  makeManifest({ id: 'lark-notify', displayName: '飞书通知', version: '0.3.1', events: ['inbox.received', 'question.asked', 'run.ended'] }),
)

function renderMarket(initial: Parameters<typeof fakePlugins>[0] = {}) {
  const fake = fakePlugins({ details: [makeDetail(makePlugin({ enabledProjects: 3 })), playwright, lark], ...initial })
  server.use(...fake.handlers, ...baseHandlers())
  return { ...renderRoutes(routes, '/plugins'), fake }
}

describe('插件市场', () => {
  it('左侧栏“插件市场”为当前项；已安装的插件列出来源、两半各有什么、启用项目数与授权 Agent 数和状态', async () => {
    renderMarket({ host: makeHost({ loaded: ['webhook', 'playwright'], today: { delivered: 412, failed: 1 } }) })
    const webhook = (await screen.findByRole('link', { name: 'Webhook 通知' })).closest('li')!
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['插件市场'])
    await waitFor(() => expect(webhook).toHaveTextContent('平台半边'))
    expect(webhook).toHaveTextContent('webhook · v1.2.0官方')
    expect(webhook).toHaveTextContent('订阅 1 个事件权限 2 项')
    expect(webhook).toHaveTextContent('3 个项目启用不需要 Agent 授权')
    expect(within(webhook).getByText('运行中')).toBeInTheDocument()
    const browser = screen.getByRole('link', { name: '专用浏览器' }).closest('li')!
    await waitFor(() => expect(browser).toHaveTextContent('Agent 半边MCP 服务器 1Skill 1'))
    expect(browser).toHaveTextContent('1 个项目启用2 个 Agent 已授权')
    const failing = screen.getByRole('link', { name: '飞书通知' }).closest('li')!
    expect(failing).toHaveTextContent('git · 未审核')
    expect(failing).toHaveTextContent('订阅了清单里没有声明的事件 run.ended。')
    expect(within(failing).getByRole('link', { name: '查看日志' })).toHaveAttribute('href', '/plugins/lark-notify/logs')
    const host = screen.getByRole('region', { name: '插件宿主' })
    expect(host).toHaveTextContent('宿主运行中')
    expect(host).toHaveTextContent('2 个插件已加载')
    expect(host).toHaveTextContent('今天处理 412 个事件，失败 1 个')
  })

  it('全部停用：保存全局设置（保留市场列表）', async () => {
    const user = userEvent.setup()
    const { fake } = renderMarket({ settings: { disabled: false, markets: ['https://example.com/market.git'] } })
    const toggle = await screen.findByRole('switch', { name: '全部停用' })
    await waitFor(() => expect(toggle).toBeEnabled())
    await user.click(toggle)
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: '/plugin-settings', query: {}, body: { disabled: true, markets: ['https://example.com/market.git'] } }]))
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'true'))
  })

  it('宿主停了：写出崩溃时在处理什么，可以重新启动', async () => {
    const user = userEvent.setup()
    const { fake } = renderMarket({
      host: makeHost({ state: 'stopped', pid: null, lastCrash: { at: '2026-10-27T04:00:00Z', error: 'exit code 1', inFlight: [], last: { type: 'event', plugin: 'lark-notify', eventType: 'run.ended', since: '2026-10-27T03:59:59Z' }, stderr: [] } }),
    })
    const host = await screen.findByRole('region', { name: '插件宿主' })
    await waitFor(() => expect(host).toHaveTextContent('宿主已停止'))
    expect(host).toHaveTextContent('exit code 1')
    expect(host).toHaveTextContent('退出前在处理 lark-notify 的 run.ended')
    await user.click(within(host).getByRole('button', { name: '重新启动宿主' }))
    await waitFor(() => expect(fake.writes().map((r) => `${r.method} ${r.path}`)).toEqual(['POST /plugin-host/restart']))
    await waitFor(() => expect(host).toHaveTextContent('宿主启动中'))
  })

  it('还没装的官方插件单独列出；点安装按 builtin 来源取预览，确认后打开它的详情', async () => {
    const user = userEvent.setup()
    const { fake, router } = renderMarket({ builtin: [{ id: 'hello', displayName: 'hello 示例', description: '两半各一个最小例子', version: '1.0.0' }, { id: 'webhook', displayName: 'Webhook 通知', description: 'x', version: '1.2.0' }] })
    const section = await screen.findByRole('region', { name: '随平台发布的官方插件' })
    expect(within(section).queryByText('Webhook 通知')).toBeNull()
    await user.click(within(section).getByRole('button', { name: '安装' }))
    const dialog = await screen.findByRole('dialog', { name: /安装/ })
    expect(fake.requests.find((r) => r.path === '/plugin-previews')?.body).toEqual({ source: { kind: 'builtin', location: 'hello' } })
    await user.click(within(dialog).getByRole('button', { name: '安装' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/plugins/hello'))
    expect(fake.writes().map((r) => `${r.method} ${r.path}`)).toEqual(['POST /plugin-previews', 'POST /plugins'])
  })

  it('没有安装任何插件时说明从“安装插件”开始', async () => {
    renderMarket({ details: [] })
    expect(await screen.findByText('还没有安装插件。从右上角的“安装插件”开始。')).toBeInTheDocument()
  })
})
