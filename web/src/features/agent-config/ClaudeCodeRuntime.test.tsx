import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { renderConfig, withClaudeCode } from '@/test/config'
import { chooseModel } from '@/test/model-picker'
import { runtimeList } from '@/test/runtimes'
import { thinkingOptions } from '@/test/thinking'

// The runtime is determined by the model provider: choosing the local Claude Code means the Claude Code runtime; anything else is Pi.
const region = (name: string) => screen.getByRole('region', { name })
const saveButton = () => within(region('Basics')).getByRole('button', { name: 'Save' })
const option = (select: HTMLElement, value: string) => within(select).queryAllByRole('option').find((item) => (item as HTMLOptionElement).value === value) as HTMLOptionElement | undefined
const providerNote = 'Uses the Claude Code installed on this computer and your login there. The platform doesn\'t store login details.'
const differencesTitle = 'Running on Claude Code'
const claudeAgent = (overrides: Partial<Schemas['Agent']> = {}) => makeAgent({ model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code', ...overrides })
const catalog = http.get(apiUrl('/model-providers'), () => HttpResponse.json(withClaudeCode))

describe('Choosing Claude Code', () => {
  it('a Pi Agent switching to Claude Code: notes it uses the local login, lists the differences from Pi, takes thinking levels from the catalog and shows no speed tier', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({ agent, handlers: [catalog] })
    const basic = region('Basics')
    const select = within(basic).getByLabelText('Model provider')
    await waitFor(() => expect(option(select, 'claude-code')).toBeDefined())
    expect(option(select, 'claude-code')).toHaveTextContent(/^Claude Code \(this computer, 2 models\)$/)
    expect(option(select, 'claude-code')).toBeEnabled()
    expect(within(basic).queryByText(providerNote)).toBeNull()
    expect(within(basic).queryByRole('region', { name: differencesTitle })).toBeNull()

    await user.selectOptions(select, 'claude-code')
    expect(within(basic).getByText(providerNote)).toHaveAttribute('role', 'note')
    const differences = within(basic).getByRole('region', { name: differencesTitle })
    expect(within(differences).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      'It runs with your home directory and logins on this computer: gh, git and other commands it runs use your own logins.',
      'It runs commands without asking first, the same as Pi Agents.',
      'Plugins that only contain Pi extensions don\'t apply to it; plugin skills and MCP tools work.',
      'There are no /pi: commands.',
    ])
    await chooseModel(user, basic, 'opus')
    expect(thinkingOptions(basic)).toEqual(['Default (Medium)', 'Low', 'Medium', 'High', 'Extra high', 'Max'])
    expect(within(basic).queryByLabelText('Codex speed')).toBeNull()

    // Switching back to a Pi provider collapses the notes again.
    await user.selectOptions(select, 'anthropic')
    expect(within(basic).queryByText(providerNote)).toBeNull()
    expect(within(basic).queryByRole('region', { name: differencesTitle })).toBeNull()
  })

  it('when saving changes the runtime: confirm first; cancelling does not save, confirming saves', async () => {
    const agent = makeAgent()
    const bodies: unknown[] = []
    let current = agent
    const { user } = await renderConfig({
      agent,
      handlers: [
        catalog,
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          bodies.push(await request.json())
          current = claudeAgent({ id: agent.id, configVersion: 2, session: { state: 'none', epoch: 2 } })
          return HttpResponse.json(current)
        }),
      ],
    })
    const basic = region('Basics')
    const select = within(basic).getByLabelText('Model provider')
    await waitFor(() => expect(option(select, 'claude-code')).toBeDefined())
    await user.selectOptions(select, 'claude-code')
    await chooseModel(user, basic, 'sonnet')

    await user.click(saveButton())
    const dialog = await screen.findByRole('alertdialog', { name: 'Switch runtime?' })
    expect(dialog).toHaveTextContent('The runtime changes from Pi to Claude Code. The Agent starts a new session: the current session is kept but not carried over, and pending sleeps in it are cancelled.')
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(bodies).toEqual([])

    await user.click(saveButton())
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Switch and save' }))
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'sonnet' } }]))
    expect(await within(basic).findByText('Saved')).toBeInTheDocument()
  })

  it('switching from Claude Code back to a Pi provider also needs confirming; changing models within the same runtime does not', async () => {
    const agent = claudeAgent()
    const bodies: unknown[] = []
    const { user } = await renderConfig({
      agent,
      handlers: [
        catalog,
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          const body = (await request.json()) as Schemas['AgentUpdate']
          bodies.push(body)
          return HttpResponse.json({ ...agent, model: body.model ?? agent.model, configVersion: agent.configVersion + 1 })
        }),
      ],
    })
    const basic = region('Basics')
    await chooseModel(user, basic, 'opus')
    await user.click(saveButton())
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'opus' } }]))
    expect(screen.queryByRole('alertdialog')).toBeNull()

    await user.selectOptions(within(basic).getByLabelText('Model provider'), 'anthropic')
    await chooseModel(user, basic, 'claude-haiku-5-5')
    await user.click(saveButton())
    expect(await screen.findByRole('alertdialog', { name: 'Switch runtime?' })).toHaveTextContent('The runtime changes from Claude Code to Pi.')
  })

  it('switching while the Agent is replying (409 agent_not_idle): says to switch after this reply finishes', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      handlers: [
        catalog,
        http.patch(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(errorBody('agent_not_idle', 'The Agent is replying; wait for the reply to finish before switching runtimes'), { status: 409 })),
      ],
    })
    const basic = region('Basics')
    const select = within(basic).getByLabelText('Model provider')
    await waitFor(() => expect(option(select, 'claude-code')).toBeDefined())
    await user.selectOptions(select, 'claude-code')
    await chooseModel(user, basic, 'sonnet')
    await user.click(saveButton())
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Switch and save' }))
    expect(await within(basic).findByRole('alert')).toHaveTextContent(/^The Agent is replying\. Switch after this reply finishes\.$/)
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(within(basic).getByText('Unsaved')).toBeInTheDocument()
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
  const runtimes = (list: Schemas['RuntimeList']) => {
    let reads = 0
    return { handler: http.get(apiUrl('/runtimes'), () => { reads += 1; return HttpResponse.json(list) }), reads: () => reads }
  }

  it('warns when chosen, saying how to fix it and that runs fail until then; it can still be chosen and saved', async () => {
    const agent = makeAgent()
    const status = runtimes(runtimeList({ available: false, version: null, binaryPath: null, unavailableReason: 'not_found', login: null }))
    const bodies: unknown[] = []
    const { user } = await renderConfig({
      agent,
      handlers: [catalog, status.handler, http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => { bodies.push(await request.json()); return HttpResponse.json(claudeAgent({ id: agent.id })) })],
    })
    const basic = region('Basics')
    const select = within(basic).getByLabelText('Model provider')
    await waitFor(() => expect(option(select, 'claude-code')).toBeDefined())
    // The runtime status is not read while Claude Code is not selected.
    expect(status.reads()).toBe(0)
    await user.selectOptions(select, 'claude-code')
    expect(await within(basic).findByText("claude wasn't found on this computer: install Claude Code and make sure claude runs in a terminal. Until then, Agents using it will fail to run.")).toHaveAttribute('role', 'note')
    await chooseModel(user, basic, 'sonnet')
    await user.click(saveButton())
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Switch and save' }))
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'sonnet' } }]))
  })

  it('when logged out, says to log in from a terminal on this computer; no warning when logged in or when the login state is unknown', async () => {
    const loggedOut = runtimes(runtimeList({ login: { state: 'logged_out', authMethod: null, subscriptionType: null } }))
    const first = await renderConfig({ agent: claudeAgent(), handlers: [catalog, loggedOut.handler] })
    expect(await within(region('Basics')).findByText("Claude Code isn't logged in on this computer: run claude in a terminal there and log in. Until then, Agents using it will fail to run.")).toBeInTheDocument()
    first.unmount()

    for (const login of [{ state: 'logged_in', authMethod: 'claude.ai', subscriptionType: 'max' }, { state: 'unknown', authMethod: null, subscriptionType: null }] as const) {
      const status = runtimes(runtimeList({ login }))
      const view = await renderConfig({ agent: claudeAgent(), handlers: [catalog, status.handler] })
      await waitFor(() => expect(status.reads()).toBe(1))
      await new Promise((resolve) => setTimeout(resolve, 20))
      expect(within(region('Basics')).queryByText(/Until then, Agents using it will fail to run/)).toBeNull()
      view.unmount()
    }
  })
})
