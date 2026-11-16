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
const personal = makeAccount({ id: '72000000-0000-7000-8000-000000000002', projectId: defaultProject.id, pluginId: 'lark-notify', name: 'Personal test' })

function renderAgent(plugins: Schemas['AgentPlugin'][], agent = makeAgent(), path = defaultAgentPath(agent.id, 'config')) {
  const fake = fakePlugins({ agents: { [agent.id]: plugins }, accounts: [bot, personal] })
  server.use(...fake.handlers, ...baseHandlers([agent]), ...(agent.scope === 'platform' ? [http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(agent)] }))] : []))
  return { ...renderRoutes(routes, path), fake, agent }
}

const section = () => screen.findByRole('region', { name: 'Plugin grants' })

describe('Plugin section of the Agent config page', () => {
  it('one row per grantable plugin with a summary of the grant and chosen accounts; plugins the project has not enabled are tucked at the end with a link to the project plugin page', async () => {
    renderAgent([
      agentPlugin({ pluginId: 'lark-notify', displayName: 'Lark notifications', granted: true, accountIds: [bot.id], accountKinds: [larkAccountKind] }),
      agentPlugin({ pluginId: 'playwright', displayName: 'Dedicated browser' }),
      agentPlugin({ pluginId: 'git-commit', displayName: 'Git auto-commit', available: false }),
    ])
    const plugins = await section()
    const lark = await within(plugins).findByRole('button', { name: /Lark notifications/ })
    await waitFor(() => expect(lark).toHaveTextContent('Lark notificationsGranted· Accounts: 研发群机器人'))
    expect(within(plugins).getByRole('button', { name: /Dedicated browser/ })).toHaveTextContent('Dedicated browserNot granted')
    const group = within(plugins).getByRole('button', { name: 'Not enabled in project (1)' })
    expect(within(plugins).getByRole('link', { name: 'Enable on the project\'s plugin page' })).toHaveAttribute('href', `/p/${encodeURIComponent(defaultProject.name)}/plugins`)
    await userEvent.setup().click(group)
    expect(within(plugins).getByRole('listitem', { name: 'Git auto-commit' })).toHaveTextContent('Not enabled in project')
  })

  it('expanding a row: check the grant, pick an account and save; the row is marked as saved', async () => {
    const user = userEvent.setup()
    const { fake, agent } = renderAgent([agentPlugin({ pluginId: 'lark-notify', displayName: 'Lark notifications', accountKinds: [larkAccountKind] })])
    const plugins = await section()
    await user.click(await within(plugins).findByRole('button', { name: /Lark notifications/ }))
    const form = within(plugins).getByRole('region', { name: 'Lark notifications' })
    await user.click(within(form).getByRole('checkbox', { name: 'Grant Lark notifications' }))
    await user.click(await within(form).findByRole('checkbox', { name: 'Personal test' }))
    await user.click(within(form).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: `/agents/${agent.id}/plugins/lark-notify`, query: {}, body: { granted: true, accountIds: [personal.id] } }]))
    expect(await within(plugins).findByText('Saved', { selector: '[data-slot="badge"]' })).toBeInTheDocument()
  })

  it('after a grant is saved, the model picker catalog (with agentId) is read again, so whether plugin providers can be chosen updates with it', async () => {
    const user = userEvent.setup()
    const { agent } = renderAgent([agentPlugin({ pluginId: 'lark-notify', displayName: 'Lark notifications', accountKinds: [larkAccountKind] })])
    const reads: string[] = []
    server.use(http.get(apiUrl('/model-providers'), ({ request }) => {
      reads.push(new URL(request.url).searchParams.get('agentId') ?? '')
      return HttpResponse.json({ piVersion: '1.1.0', catalogGeneratedAt: null, providers: [] })
    }))
    const plugins = await section()
    await user.click(await within(plugins).findByRole('button', { name: /Lark notifications/ }))
    const form = within(plugins).getByRole('region', { name: 'Lark notifications' })
    await user.click(within(form).getByRole('checkbox', { name: 'Grant Lark notifications' }))
    const before = reads.length
    await user.click(within(form).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(reads.length).toBeGreaterThan(before))
    expect(reads.at(-1)).toBe(agent.id)
  })

  it('switching to another plugin with unsaved changes asks for confirmation first', async () => {
    const user = userEvent.setup()
    renderAgent([agentPlugin({}), agentPlugin({ pluginId: 'playwright', displayName: 'Dedicated browser' })])
    const plugins = await section()
    await user.click(await within(plugins).findByRole('button', { name: /Webhook 通知/ }))
    await user.click(within(plugins).getByRole('checkbox', { name: 'Grant Webhook 通知' }))
    await user.click(within(plugins).getByRole('button', { name: /Dedicated browser/ }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Discard unsaved grant changes?' })
    await user.click(within(dialog).getByRole('button', { name: 'Discard changes' }))
    expect(within(plugins).getByRole('checkbox', { name: 'Grant Dedicated browser' })).not.toBeChecked()
  })

  it('the project disabled the plugin before saving (409 plugin_not_enabled): explains it must be enabled in the project first', async () => {
    const user = userEvent.setup()
    const { fake, agent } = renderAgent([agentPlugin({})])
    const plugins = await section()
    await user.click(await within(plugins).findByRole('button', { name: /Webhook 通知/ }))
    fake.state.agents[agent.id] = [agentPlugin({ available: false })]
    await user.click(within(plugins).getByRole('checkbox', { name: 'Grant Webhook 通知' }))
    await user.click(within(plugins).getByRole('button', { name: 'Save' }))
    expect(await within(plugins).findByText('This plugin isn\'t enabled in the project yet; enable it there before granting.')).toBeInTheDocument()
  })

  it('Claude Code Agent: says grants take effect on the next start (without mentioning Pi); plugins whose Agent side is only Pi extensions are marked as having no effect on Claude Code', async () => {
    const withParts = (id: string, components: Partial<Schemas['PluginComponents']>) => {
      const plugin = makePlugin({ id, displayName: id })
      return makeDetail(plugin, makeManifest({ id, displayName: id, components: makeComponents(components) }))
    }
    const plugins = [
      agentPlugin({ pluginId: 'pi-only', displayName: 'Extension only' }),
      agentPlugin({ pluginId: 'with-skills', displayName: 'With skills' }),
      agentPlugin({ pluginId: 'with-mcp', displayName: 'With MCP' }),
      agentPlugin({ pluginId: 'with-bin', displayName: 'With CLI' }),
      agentPlugin({ pluginId: 'platform-only', displayName: 'Platform only' }),
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
    const hint = 'Its Agent side is only Pi extensions, which have no effect on Claude Code'

    const claude = render(makeAgent({ model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code' }))
    let region = await section()
    expect(region).toHaveTextContent('Grants take effect the next time the Agent starts; the current turn isn\'t interrupted.')
    await waitFor(() => expect(within(region).getByRole('button', { name: /Extension only/ })).toHaveTextContent(`Extension onlyNot granted· ${hint}`))
    for (const name of [/With skills/, /With MCP/, /With CLI/, /Platform only/]) expect(within(region).getByRole('button', { name })).not.toHaveTextContent(hint)
    claude.unmount()

    render(makeAgent())
    region = await section()
    expect(region).toHaveTextContent('Grants take effect the next time Pi starts; the current turn isn\'t interrupted.')
    expect(await within(region).findByRole('button', { name: /Extension only/ })).toHaveTextContent(/^Extension onlyNot granted$/)
  })

  it('a platform Agent has no accounts to choose, only the grant checkbox', async () => {
    const user = userEvent.setup()
    const agent = makeAgent({ scope: 'platform', projectId: null })
    const { fake } = renderAgent([agentPlugin({ pluginId: 'lark-notify', displayName: 'Lark notifications', accountKinds: [larkAccountKind] })], agent, `/platform/agents/${agent.id}/config`)
    const plugins = await section()
    await user.click(await within(plugins).findByRole('button', { name: /Lark notifications/ }))
    const form = within(plugins).getByRole('region', { name: 'Lark notifications' })
    expect(within(form).queryByRole('group', { name: 'Accounts · 飞书应用' })).toBeNull()
    await user.click(within(form).getByRole('checkbox', { name: 'Grant Lark notifications' }))
    await user.click(within(form).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(fake.writes().map((r) => r.body)).toEqual([{ granted: true, accountIds: [] }]))
    expect(fake.requests.some((r) => r.path.includes('/accounts'))).toBe(false)
  })
})
