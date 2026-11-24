import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeAgent, makeRun } from '@/test/api'
import { renderConfig, withClaudeCode } from '@/test/config'
import { chooseModel, chooseRunner, runnerTab } from '@/test/model-picker'
import { runtimeList } from '@/test/runtimes'
import { thinkingOptions } from '@/test/thinking'

// The runtime is determined by the model provider: choosing the local Claude Code means the Claude Code runtime; anything else is Pi.
const region = (name: string) => screen.getByRole('region', { name })
const saveButton = () => within(region('Basics')).getByRole('button', { name: 'Save' })
const providerNote = 'Uses the Claude Code installed on this computer and your login there. The platform doesn\'t store login details.'
const differencesTitle = 'Running on Claude Code'
const claudeAgent = (overrides: Partial<Schemas['Agent']> = {}) => makeAgent({ model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code', ...overrides })
const catalog = http.get(apiUrl('/model-providers'), () => HttpResponse.json(withClaudeCode))

describe('Choosing Claude Code', () => {
  it('a Pi Agent switching to Claude Code: notes it uses the local login, lists the differences from Pi, takes thinking levels from the catalog and shows no speed tier', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({ agent, handlers: [catalog] })
    const basic = region('Basics')
    expect(await runnerTab(basic, 'Claude Code')).toHaveAccessibleName('Claude Code, Ready')
    expect(within(basic).queryByText(providerNote)).toBeNull()
    expect(within(basic).queryByRole('region', { name: differencesTitle })).toBeNull()

    // Looking at Claude Code says whose login it runs on; the differences show once one of its models is chosen.
    await chooseRunner(user, basic, 'Claude Code')
    expect(await within(basic).findByText(providerNote)).toBeInTheDocument()
    expect(within(basic).queryByRole('region', { name: differencesTitle })).toBeNull()
    await chooseModel(user, basic, 'opus')
    const differences = within(basic).getByRole('region', { name: differencesTitle })
    expect(within(differences).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      'It runs with your home directory and logins on this computer: gh, git and other commands it runs use your own logins.',
      'It runs commands without asking first, the same as Pi Agents.',
      'Plugins that only contain Pi extensions don\'t apply to it; plugin skills and MCP tools work.',
      'There are no /pi: commands.',
    ])
    expect(thinkingOptions(basic)).toEqual(['Default (Medium)', 'Max', 'Extra high', 'High', 'Medium', 'Low'])
    expect(within(basic).queryByLabelText('Codex speed')).toBeNull()

    // Choosing a Pi model again takes the differences away.
    await chooseModel(user, basic, 'claude-haiku-5-5', { runtime: 'Pi', provider: 'anthropic' })
    expect(within(basic).queryByRole('region', { name: differencesTitle })).toBeNull()
  })

  it('a change of runtime saves straight away in either direction, with no confirmation; the conversation goes with it', async () => {
    const agent = makeAgent()
    const bodies: Schemas['AgentUpdate'][] = []
    let current = agent
    const { user } = await renderConfig({
      agent,
      handlers: [
        catalog,
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          const body = (await request.json()) as Schemas['AgentUpdate']
          bodies.push(body)
          current = body.model?.provider === 'claude-code' ? claudeAgent({ id: agent.id, configVersion: current.configVersion + 1 }) : { ...agent, model: body.model ?? agent.model, configVersion: current.configVersion + 1 }
          return HttpResponse.json(current)
        }),
      ],
    })
    const basic = region('Basics')
    await chooseModel(user, basic, 'sonnet', { runtime: 'Claude Code' })
    await user.click(saveButton())
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'sonnet' } }]))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(await within(basic).findByText('Saved')).toBeInTheDocument()

    await chooseModel(user, basic, 'claude-haiku-5-5', { runtime: 'Pi', provider: 'anthropic' })
    await user.click(saveButton())
    await waitFor(() => expect(bodies).toHaveLength(2))
    expect(bodies[1]).toEqual({ model: { provider: 'anthropic', modelId: 'claude-haiku-5-5' } })
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('a change of runtime while the Agent is replying is saved like any other change and applies from the next message', async () => {
    const agent = makeAgent()
    const bodies: unknown[] = []
    let current = agent
    const { user } = await renderConfig({
      agent,
      status: { busy: true, currentRun: makeRun({ status: 'running', configVersion: 1 }) },
      handlers: [
        catalog,
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          bodies.push(await request.json())
          current = claudeAgent({ id: agent.id, configVersion: 2 })
          return HttpResponse.json(current)
        }),
      ],
    })
    const basic = region('Basics')
    await chooseModel(user, basic, 'sonnet', { runtime: 'Claude Code' })
    await user.click(saveButton())
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'sonnet' } }]))
    expect(await within(basic).findByText('Saved. The current reply is not affected; changes take effect from the next message.')).toBeInTheDocument()
    expect(within(basic).queryByRole('alert')).toBeNull()
  })
})

describe('Claude Code needs no account', () => {
  const account = 'This Agent uses the Claude Code on this computer and your login there. It needs no account on the platform, and the platform doesn\'t store login details.'

  it('platform Agent: the model account section only has a note, with no login or entry button, and the login status is not read', async () => {
    const agent = claudeAgent({ scope: 'platform', projectId: null })
    let authReads = 0
    await renderConfig({ agent, handlers: [catalog, http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => { authReads += 1; return HttpResponse.json({ credential: null, operation: null }) })] })
    const section = region('Model account credential')
    expect(section).toHaveTextContent(account)
    expect(within(section).queryAllByRole('button')).toEqual([])
    expect(within(section).queryByText('Not set')).toBeNull()
    expect(authReads).toBe(0)
  })

  it('project Agent: shows no Claude Code account source and no link to the project settings', async () => {
    // The default account sources include a claude-code entry (with no candidate accounts), matching the server.
    await renderConfig({ agent: claudeAgent(), handlers: [catalog] })
    const section = region('Model account credential')
    expect(section).toHaveTextContent(account)
    expect(section).not.toHaveTextContent('Model accounts are managed by the project')
    await waitFor(() => expect(within(section).queryByRole('status')).toBeNull())
    expect(within(section).queryByRole('combobox')).toBeNull()
    expect(within(section).queryByRole('link')).toBeNull()
  })
})

describe('model account section when the runtime definitions cannot be read', () => {
  const failing = http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 }))

  it('says why instead of guessing whether the Agent needs an account, in both scopes', async () => {
    for (const agent of [claudeAgent({ scope: 'platform', projectId: null }), claudeAgent()]) {
      const view = await renderConfig({ agent, handlers: [catalog, failing] })
      const section = await screen.findByRole('region', { name: 'Model account credential' })
      await waitFor(() => expect(within(section).getByRole('alert')).toHaveTextContent('Internal server error'))
      expect(section).not.toHaveTextContent(/needs no account|Model accounts are managed by the project/)
      view.unmount()
    }
  })
})

describe('Local Claude Code unavailable or not logged in', () => {
  it('not installed: the rail says so, looking at it shows the install command, and none of its models can be chosen', async () => {
    const agent = makeAgent()
    await renderConfig({ agent, handlers: [catalog, http.get(apiUrl('/runtimes'), () => HttpResponse.json(runtimeList({ available: false, version: null, binaryPath: null, unavailableReason: 'not_found', login: null })))] })
    const basic = region('Basics')
    const tab = await runnerTab(basic, 'Claude Code')
    await waitFor(() => expect(tab).toHaveAccessibleName('Claude Code, Not installed'))
    await userEvent.click(tab)
    const panel = within(basic).getByRole('tabpanel')
    expect(within(panel).getByText("claude wasn't found on this computer: install Claude Code and make sure claude runs in a terminal.")).toBeInTheDocument()
    expect(within(panel).getByText('curl -fsSL https://claude.ai/install.sh | bash')).toBeInTheDocument()
    expect(within(panel).getByText("Claude Code can't be chosen until it's set up.")).toBeInTheDocument()
    expect(within(panel).queryByRole('treeitem')).toBeNull()
    expect(saveButton()).toBeDisabled()
  })

  it('an Agent on a logged-out Claude Code: its runtime is marked as in use but logged out, with the login command; logged in or unknown lists the models', async () => {
    const loggedOut = http.get(apiUrl('/runtimes'), () => HttpResponse.json(runtimeList({ login: { state: 'logged_out', authMethod: null, subscriptionType: null } })))
    const first = await renderConfig({ agent: claudeAgent(), handlers: [catalog, loggedOut] })
    const tab = await runnerTab(region('Basics'), 'Claude Code')
    await waitFor(() => expect(tab).toHaveAccessibleName('Claude Code, Logged out, Current'))
    expect(within(region('Basics')).getByText("Claude Code isn't logged in on this computer: run claude in a terminal there and log in.")).toBeInTheDocument()
    expect(within(within(region('Basics')).getByRole('tabpanel')).getByText('claude')).toBeInTheDocument()
    first.unmount()

    for (const login of [{ state: 'logged_in', authMethod: 'claude.ai', subscriptionType: 'max' }, { state: 'unknown', authMethod: null, subscriptionType: null }] as const) {
      const view = await renderConfig({ agent: claudeAgent(), handlers: [catalog, http.get(apiUrl('/runtimes'), () => HttpResponse.json(runtimeList({ login })))] })
      const basic = region('Basics')
      await waitFor(() => expect(within(basic).getByRole('treeitem', { name: 'Claude Sonnet (latest) (sonnet)' })).toHaveAttribute('aria-selected', 'true'))
      expect(within(basic).queryByText(/isn't logged in/)).toBeNull()
      view.unmount()
    }
  })
})
