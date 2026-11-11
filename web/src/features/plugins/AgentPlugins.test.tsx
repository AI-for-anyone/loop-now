import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { http, HttpResponse } from 'msw'
import { apiUrl, defaultAgentPath, defaultProject, makeAgent, platformAgentsUrl, toListItem } from '@/test/api'
import { fakePlugins, larkAccountKind, makeAccount, makeComponents, makeDetail, makeManifest, makePlugin } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const agentPlugin = (overrides: Partial<Schemas['AgentPlugin']>): Schemas['AgentPlugin'] => ({ pluginId: 'webhook', displayName: 'Webhook 通知', available: true, granted: false, accountIds: [], accountKinds: [], ...overrides })
const bot = makeAccount({ projectId: defaultProject.id, pluginId: 'lark-notify' })
const personal = makeAccount({ id: '72000000-0000-7000-8000-000000000002', projectId: defaultProject.id, pluginId: 'lark-notify', name: '个人测试' })

function renderAgent(plugins: Schemas['AgentPlugin'][], agent = makeAgent(), path = defaultAgentPath(agent.id, 'config')) {
  const fake = fakePlugins({ agents: { [agent.id]: plugins }, accounts: [bot, personal] })
  server.use(...fake.handlers, ...baseHandlers([agent]), ...(agent.scope === 'platform' ? [http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(agent)] }))] : []))
  return { ...renderRoutes(routes, path), fake, agent }
}

const section = () => screen.findByRole('region', { name: '插件权限' })

describe('Agent 配置页的插件段', () => {
  it('每个可授权的插件一行，摘要写授权与选用的账号；项目未启用的收在末尾，附项目插件页入口', async () => {
    renderAgent([
      agentPlugin({ pluginId: 'lark-notify', displayName: '飞书通知', granted: true, accountIds: [bot.id], accountKinds: [larkAccountKind] }),
      agentPlugin({ pluginId: 'playwright', displayName: '专用浏览器' }),
      agentPlugin({ pluginId: 'git-commit', displayName: 'Git 自动提交', available: false }),
    ])
    const plugins = await section()
    const lark = await within(plugins).findByRole('button', { name: /飞书通知/ })
    await waitFor(() => expect(lark).toHaveTextContent('飞书通知已授权· 账号：研发群机器人'))
    expect(within(plugins).getByRole('button', { name: /专用浏览器/ })).toHaveTextContent('专用浏览器未授权')
    const group = within(plugins).getByRole('button', { name: '项目未启用（1）' })
    expect(within(plugins).getByRole('link', { name: '去项目插件页启用' })).toHaveAttribute('href', `/p/${encodeURIComponent(defaultProject.name)}/plugins`)
    await userEvent.setup().click(group)
    expect(within(plugins).getByRole('listitem', { name: 'Git 自动提交' })).toHaveTextContent('项目未启用')
  })

  it('展开一行：勾选授权并选账号后保存，这一行标出已保存', async () => {
    const user = userEvent.setup()
    const { fake, agent } = renderAgent([agentPlugin({ pluginId: 'lark-notify', displayName: '飞书通知', accountKinds: [larkAccountKind] })])
    const plugins = await section()
    await user.click(await within(plugins).findByRole('button', { name: /飞书通知/ }))
    const form = within(plugins).getByRole('region', { name: '飞书通知' })
    await user.click(within(form).getByRole('checkbox', { name: '授权 飞书通知' }))
    await user.click(await within(form).findByRole('checkbox', { name: '个人测试' }))
    await user.click(within(form).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: `/agents/${agent.id}/plugins/lark-notify`, query: {}, body: { granted: true, accountIds: [personal.id] } }]))
    expect(await within(plugins).findByText('已保存', { selector: '[data-slot="badge"]' })).toBeInTheDocument()
  })

  it('授权保存后，模型选择用的目录（带 agentId）重新读取，插件提供方能不能选随之更新', async () => {
    const user = userEvent.setup()
    const { agent } = renderAgent([agentPlugin({ pluginId: 'lark-notify', displayName: '飞书通知', accountKinds: [larkAccountKind] })])
    const reads: string[] = []
    server.use(http.get(apiUrl('/model-providers'), ({ request }) => {
      reads.push(new URL(request.url).searchParams.get('agentId') ?? '')
      return HttpResponse.json({ piVersion: '1.1.0', catalogGeneratedAt: null, providers: [] })
    }))
    const plugins = await section()
    await user.click(await within(plugins).findByRole('button', { name: /飞书通知/ }))
    const form = within(plugins).getByRole('region', { name: '飞书通知' })
    await user.click(within(form).getByRole('checkbox', { name: '授权 飞书通知' }))
    const before = reads.length
    await user.click(within(form).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(reads.length).toBeGreaterThan(before))
    expect(reads.at(-1)).toBe(agent.id)
  })

  it('有未保存的修改时切到别的插件先确认', async () => {
    const user = userEvent.setup()
    renderAgent([agentPlugin({}), agentPlugin({ pluginId: 'playwright', displayName: '专用浏览器' })])
    const plugins = await section()
    await user.click(await within(plugins).findByRole('button', { name: /Webhook 通知/ }))
    await user.click(within(plugins).getByRole('checkbox', { name: '授权 Webhook 通知' }))
    await user.click(within(plugins).getByRole('button', { name: /专用浏览器/ }))
    const dialog = await screen.findByRole('alertdialog', { name: '放弃未保存的授权修改？' })
    await user.click(within(dialog).getByRole('button', { name: '放弃修改' }))
    expect(within(plugins).getByRole('checkbox', { name: '授权 专用浏览器' })).not.toBeChecked()
  })

  it('保存时项目已停用这个插件（409 plugin_not_enabled）：说明要先在项目里启用', async () => {
    const user = userEvent.setup()
    const { fake, agent } = renderAgent([agentPlugin({})])
    const plugins = await section()
    await user.click(await within(plugins).findByRole('button', { name: /Webhook 通知/ }))
    fake.state.agents[agent.id] = [agentPlugin({ available: false })]
    await user.click(within(plugins).getByRole('checkbox', { name: '授权 Webhook 通知' }))
    await user.click(within(plugins).getByRole('button', { name: '保存' }))
    expect(await within(plugins).findByText('这个插件还没在项目里启用，启用后才能授权。')).toBeInTheDocument()
  })

  it('Claude Code 的 Agent：说明下次启动生效（不提 Pi）；Agent 侧只有 Pi 扩展的插件标出对 Claude Code 不起作用', async () => {
    const withParts = (id: string, components: Partial<Schemas['PluginComponents']>) => {
      const plugin = makePlugin({ id, displayName: id })
      return makeDetail(plugin, makeManifest({ id, displayName: id, components: makeComponents(components) }))
    }
    const plugins = [
      agentPlugin({ pluginId: 'pi-only', displayName: '只有扩展' }),
      agentPlugin({ pluginId: 'with-skills', displayName: '带技能' }),
      agentPlugin({ pluginId: 'with-mcp', displayName: '带 MCP' }),
      agentPlugin({ pluginId: 'with-bin', displayName: '带命令' }),
      agentPlugin({ pluginId: 'platform-only', displayName: '只有平台侧' }),
    ]
    const details = [
      withParts('pi-only', { agentExtensions: 2 }),
      withParts('with-skills', { agentExtensions: 1, skills: ['skills/review'] }),
      withParts('with-mcp', { agentExtensions: 1, mcpServers: ['browser'] }),
      withParts('with-bin', { agentExtensions: 1, bin: ['bin'] }),
      withParts('platform-only', { agentExtensions: 0 }),
    ]
    const render = (agent: Schemas['Agent']) => {
      const fake = fakePlugins({ agents: { [agent.id]: plugins }, details })
      server.use(...fake.handlers, ...baseHandlers([agent]))
      return renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    }
    const hint = 'Agent 侧只有 Pi 扩展，对 Claude Code 不起作用'

    const claude = render(makeAgent({ model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code' }))
    let region = await section()
    expect(region).toHaveTextContent('授权后下次启动生效，不打断当前回合。')
    await waitFor(() => expect(within(region).getByRole('button', { name: /只有扩展/ })).toHaveTextContent(`只有扩展未授权· ${hint}`))
    for (const name of [/带技能/, /带 MCP/, /带命令/, /只有平台侧/]) expect(within(region).getByRole('button', { name })).not.toHaveTextContent(hint)
    claude.unmount()

    render(makeAgent())
    region = await section()
    expect(region).toHaveTextContent('授权后下次启动 Pi 生效，不打断当前回合。')
    expect(await within(region).findByRole('button', { name: /只有扩展/ })).toHaveTextContent(/^只有扩展未授权$/)
  })

  it('平台 Agent 没有账号可选，只勾选授权', async () => {
    const user = userEvent.setup()
    const agent = makeAgent({ scope: 'platform', projectId: null })
    const { fake } = renderAgent([agentPlugin({ pluginId: 'lark-notify', displayName: '飞书通知', accountKinds: [larkAccountKind] })], agent, `/platform/agents/${agent.id}/config`)
    const plugins = await section()
    await user.click(await within(plugins).findByRole('button', { name: /飞书通知/ }))
    const form = within(plugins).getByRole('region', { name: '飞书通知' })
    expect(within(form).queryByRole('group', { name: '账号 · 飞书应用' })).toBeNull()
    await user.click(within(form).getByRole('checkbox', { name: '授权 飞书通知' }))
    await user.click(within(form).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(fake.writes().map((r) => r.body)).toEqual([{ granted: true, accountIds: [] }]))
    expect(fake.requests.some((r) => r.path.includes('/accounts'))).toBe(false)
  })
})
