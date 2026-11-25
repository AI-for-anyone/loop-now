import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl } from '@/test/api'
import { chooseModel, chooseRunner, modelOptions, modelSearch, openProvider, runnerTab } from '@/test/model-picker'
import { renderWithQuery } from '@/test/render'
import { acpCommand, acpCommandDefinition, acpCommandStatus, acpDefaultModel, acpRuntimeId, codexStatus, runtimeDefinitions, runtimeList, withCodex } from '@/test/runtimes'
import { server } from '@/test/server'
import { chooseThinking, thinkingChoice, thinkingOptions } from '@/test/thinking'
import { RunnerPicker, type RunnerChoice, type RunnerLead } from './RunnerPicker'

type Provider = Schemas['ModelProvider']
type Model = Schemas['Model']

const model = (id: string, name: string, levels: Schemas['ThinkingLevel'][] = ['minimal', 'low', 'medium', 'high']): Model => ({ id, name, reasoning: levels.length > 0, thinkingLevels: levels, defaultThinkingLevel: levels.length ? 'medium' : undefined, contextWindow: 200000, maxTokens: 64000, input: ['text'] })
const provider = (id: string, overrides: Partial<Provider> = {}): Provider => ({ id, name: null, source: 'builtin', runtime: 'pi', pluginId: null, pluginDisplayName: null, unavailableReason: null, usable: null, apiKeyEnvVars: [], modelCount: 2, authMethods: ['api_key'], ...overrides })
const claudeLevels: Schemas['ThinkingLevel'][] = ['low', 'medium', 'high', 'xhigh', 'max']

const catalog: Record<string, Model[]> = {
  'claude-code': [model('opus', 'Claude Opus (latest)', claudeLevels), model('haiku', 'Claude Haiku (latest)', claudeLevels)],
  anthropic: [model('claude-sonnet-4-5', 'Claude Sonnet 4.5'), model('claude-haiku-4-5', 'Claude Haiku 4.5')],
  openrouter: [model('openai/gpt-5', 'OpenAI: GPT-5'), model('anthropic/claude-sonnet-4.5', 'Anthropic: Claude Sonnet 4.5')],
  'grok-cli': [model('grok-4', 'Grok 4')],
}

// The catalog as the server gives it: usable when asked for an Agent or a new Agent (anthropic has a key), models with include=models.
function useCatalog(statuses: Schemas['RuntimeList'] = runtimeList(), extra: Provider[] = []) {
  const asked: URLSearchParams[] = []
  server.use(
    http.get(apiUrl('/runtimes'), () => HttpResponse.json(statuses)),
    http.get(apiUrl('/model-providers'), ({ request }) => {
      const query = new URL(request.url).searchParams
      asked.push(query)
      const forSomeone = query.has('agentId') || query.has('projectId') || query.has('scope')
      const list = [provider('anthropic'), provider('grok-cli', { source: 'plugin', name: 'Grok CLI', pluginId: 'grok', pluginDisplayName: { 'en-US': 'Grok', 'zh-CN': 'Grok' }, authMethods: ['oauth'], modelCount: 1 }), provider('openrouter'), provider('claude-code', { source: 'local_cli', runtime: 'claude-code', authMethods: [] }), ...extra].map((p) => ({
        ...p,
        usable: forSomeone ? p.id === 'anthropic' || p.id === 'claude-code' : null,
        ...(query.get('include') === 'models' ? { models: catalog[p.id] } : {}),
      }))
      return HttpResponse.json({ piVersion: '1.4.0', catalogGeneratedAt: null, providers: list })
    }),
    http.get(apiUrl('/model-providers/:providerId/models'), ({ params }) => HttpResponse.json({ providerId: String(params.providerId), models: catalog[String(params.providerId)] ?? [] })),
  )
  return asked
}

function Picker({ initial = null, level = null, onChoose = () => {}, agentId, newIn, offers, lead }: { initial?: Schemas['ModelRef'] | null; level?: Schemas['ThinkingLevel'] | null; onChoose?: (choice: RunnerChoice) => void; agentId?: string; newIn?: string; offers?: (definition: Schemas['RuntimeDefinition']) => boolean; lead?: (choose: () => void) => RunnerLead }) {
  const [value, setValue] = useState(initial)
  const [thinking, setThinking] = useState(level)
  return (
    <div>
      <RunnerPicker
        value={value}
        thinkingLevel={thinking}
        agentId={agentId}
        newIn={newIn}
        offers={offers}
        lead={lead?.(() => {
          setValue(null)
          setThinking(null)
        })}
        onChoose={(choice) => {
          onChoose(choice)
          setValue(choice.model)
          setThinking(choice.thinkingLevel)
        }}
        onThinkingChange={setThinking}
      />
      <output data-testid="value">{value ? `${value.provider}/${value.modelId} ${thinking ?? 'default'}` : 'none'}</output>
    </div>
  )
}

const shown = () => screen.getByTestId('value').textContent

describe('runner picker', () => {
  it('lists the CLIs found on this computer first and the bundled runtime last, each with its state', async () => {
    useCatalog()
    renderWithQuery(<Picker />)
    const rail = await screen.findByRole('tablist', { name: 'Runtimes' })
    expect((await within(rail).findAllByRole('tab')).map((tab) => tab.getAttribute('aria-label'))).toEqual(['Claude Code, Ready', 'Pi, Built in, Uses an API key or another subscription'])
    // With nothing chosen yet, the first runtime is looked at and its models are listed directly.
    expect(await runnerTab(document.body, 'Claude Code')).toHaveAttribute('aria-selected', 'true')
    expect(await modelOptions(document.body)).toEqual(['Claude Opus (latest) (opus)', 'Claude Haiku (latest) (haiku)'])
  })

  it('a CLI that is not installed moves down, shows the install command, and offers no models to choose', async () => {
    useCatalog(runtimeList({ available: false, unavailableReason: 'not_found', login: null, version: null, binaryPath: null }))
    renderWithQuery(<Picker initial={{ provider: 'anthropic', modelId: 'claude-sonnet-4-5' }} />)
    await screen.findByText('Claude Sonnet 4.5')
    const tab = await runnerTab(document.body, 'Claude Code')
    expect(tab).toHaveAccessibleName('Claude Code, Not installed')
    expect(within(screen.getByRole('tablist')).getAllByRole('tab')[0]).toBe(tab)
    await userEvent.click(tab)
    const panel = screen.getByRole('tabpanel')
    expect(within(panel).getByRole('heading', { name: 'Set up Claude Code first' })).toBeInTheDocument()
    expect(within(panel).getByText('curl -fsSL https://claude.ai/install.sh | bash')).toBeInTheDocument()
    expect(within(panel).getByRole('link', { name: 'Setup guide from the vendor' })).toHaveAttribute('href', 'https://docs.anthropic.com/en/docs/claude-code/setup')
    expect(within(panel).queryByRole('tree')).toBeNull()
    expect(shown()).toBe('anthropic/claude-sonnet-4-5 default')
  })

  it('with nothing chosen yet, the first runtime that can run is looked at, not one that needs setting up', async () => {
    useCatalog(runtimeList({ available: false, unavailableReason: 'not_found', login: null, version: null, binaryPath: null }))
    renderWithQuery(<Picker />)
    expect(await runnerTab(document.body, 'Claude Code')).toHaveAccessibleName('Claude Code, Not installed')
    await vi.waitFor(async () => expect(await runnerTab(document.body, 'Pi')).toHaveAttribute('aria-selected', 'true'))
  })

  it('a CLI older than its runtime supports says which version it needs, offers the update command, and cannot be chosen', async () => {
    useCatalog({ runtimes: [...runtimeList().runtimes, codexStatus({ available: false, unavailableReason: 'version_unsupported', version: '0.150.0', login: null })] })
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(withCodex())))
    const user = userEvent.setup()
    renderWithQuery(<Picker />)
    const tab = await runnerTab(document.body, 'Codex')
    await vi.waitFor(() => expect(tab).toHaveAccessibleName('Codex, Too old'))
    await user.click(tab)
    const panel = screen.getByRole('tabpanel')
    expect(within(panel).getByText('Codex on this computer is version 0.150.0; it needs 0.167.0 or later. Update it, then check again.')).toBeInTheDocument()
    expect(within(panel).getByText('Update it: run this in a terminal on this computer')).toBeInTheDocument()
    expect(within(panel).getByText('npm install -g @openai/codex')).toBeInTheDocument()
    expect(within(panel).queryByRole('tree')).toBeNull()
  })

  it('a CLI that is logged out shows the login command; checking again reads the status without the cache', async () => {
    useCatalog(runtimeList({ login: { state: 'logged_out', authMethod: null, subscriptionType: null } }))
    const refreshed = vi.fn()
    server.use(http.get(apiUrl('/runtimes'), ({ request }) => {
      if (new URL(request.url).searchParams.get('refresh') === 'true') refreshed()
      return HttpResponse.json(runtimeList({ login: { state: 'logged_out', authMethod: null, subscriptionType: null } }))
    }))
    const user = userEvent.setup()
    renderWithQuery(<Picker />)
    await chooseRunner(user, document.body, 'Claude Code')
    expect(await runnerTab(document.body, 'Claude Code')).toHaveAccessibleName('Claude Code, Logged out')
    expect(within(screen.getByRole('tabpanel')).getByText('claude')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Check again' }))
    expect(refreshed).toHaveBeenCalledTimes(1)
  })

  it('choosing a CLI model chooses its runtime, and keeps the thinking level the new model has', async () => {
    useCatalog()
    const onChoose = vi.fn()
    const user = userEvent.setup()
    renderWithQuery(<Picker initial={{ provider: 'anthropic', modelId: 'claude-sonnet-4-5' }} level="high" onChoose={onChoose} agentId="agent-1" />)
    await chooseModel(user, document.body, 'haiku', { runtime: 'Claude Code' })
    expect(onChoose).toHaveBeenLastCalledWith({ model: { provider: 'claude-code', modelId: 'haiku' }, thinkingLevel: 'high' })
    // The rail now marks Claude Code as the one in use.
    expect(await runnerTab(document.body, 'Claude Code')).toHaveAccessibleName('Claude Code, Ready, Current')
    expect(thinkingOptions(document.body)).toEqual(['Default (Medium)', 'Max', 'Extra high', 'High', 'Medium', 'Low'])
    expect(thinkingChoice(document.body)).toBe('High')
  })

  it('a level the new model lacks goes back to the default', async () => {
    useCatalog()
    const onChoose = vi.fn()
    const user = userEvent.setup()
    renderWithQuery(<Picker initial={{ provider: 'claude-code', modelId: 'opus' }} level="max" onChoose={onChoose} />)
    await chooseModel(user, document.body, 'claude-haiku-4-5', { runtime: 'Pi', provider: 'anthropic' })
    expect(onChoose).toHaveBeenLastCalledWith({ model: { provider: 'anthropic', modelId: 'claude-haiku-4-5' }, thinkingLevel: null })
  })

  it('under Pi the providers the Agent can use now come first, under their own heading', async () => {
    const asked = useCatalog()
    renderWithQuery(<Picker initial={{ provider: 'openrouter', modelId: 'openai/gpt-5' }} agentId="agent-1" />)
    const tree = await screen.findByRole('tree')
    await within(tree).findByText('Ready to use')
    const providers = within(tree).getAllByRole('treeitem').filter((row) => row.hasAttribute('aria-expanded'))
    expect(providers.map((row) => row.getAttribute('aria-label')?.split(',')[0])).toEqual(['anthropic', 'openrouter', 'grok-cli'])
    expect(within(tree).getByText('Needs an account or key first')).toBeInTheDocument()
    // The provider in use is open; the others are closed until opened.
    expect(providers.map((row) => row.getAttribute('aria-expanded'))).toEqual(['false', 'true', 'false'])
    expect(asked.some((query) => query.get('agentId') === 'agent-1')).toBe(true)
  })

  it('a new Agent asks the catalog for its project or the platform', async () => {
    const asked = useCatalog()
    const { unmount } = renderWithQuery(<Picker newIn="project-1" />)
    await screen.findAllByRole('tab')
    await vi.waitFor(() => expect(asked.some((query) => query.get('projectId') === 'project-1')).toBe(true))
    unmount()
    renderWithQuery(<Picker newIn="platform" />)
    await vi.waitFor(() => expect(asked.some((query) => query.get('scope') === 'platform')).toBe(true))
  })

  it('a plugin provider cannot be chosen while creating an Agent', async () => {
    useCatalog()
    const user = userEvent.setup()
    renderWithQuery(<Picker initial={{ provider: 'anthropic', modelId: 'claude-sonnet-4-5' }} />)
    await openProvider(user, document.body, 'grok-cli')
    expect(screen.getByRole('treeitem', { name: 'grok-cli, Grok CLI · Grok, grant the plugin after creating, then choose' })).toBeInTheDocument()
    await user.click(await screen.findByRole('treeitem', { name: /grok-4/ }))
    expect(shown()).toBe('anthropic/claude-sonnet-4-5 default')
  })

  it('a search looks through every provider of Pi at once', async () => {
    useCatalog()
    const user = userEvent.setup()
    renderWithQuery(<Picker initial={{ provider: 'anthropic', modelId: 'claude-haiku-4-5' }} agentId="agent-1" />)
    await screen.findByText('Claude Haiku 4.5')
    await user.type(modelSearch(document.body), 'sonnet 4.5')
    expect(await modelOptions(document.body)).toEqual(['Claude Sonnet 4.5 (claude-sonnet-4-5)', 'Anthropic: Claude Sonnet 4.5 (anthropic/claude-sonnet-4.5)'])
    expect(screen.getAllByText('1 of 2 models')).toHaveLength(2)
  })

  it('works from the keyboard: arrows through the list, Enter chooses, left goes back to the runtimes, up and down move between them', async () => {
    useCatalog()
    const user = userEvent.setup()
    renderWithQuery(<Picker initial={{ provider: 'claude-code', modelId: 'opus' }} />)
    await screen.findByText('Claude Haiku (latest)')
    await user.click(modelSearch(document.body))
    await user.keyboard('{ArrowDown}{Enter}')
    expect(shown()).toBe('claude-code/haiku default')
    await user.keyboard('{ArrowLeft}')
    expect(await runnerTab(document.body, 'Claude Code')).toHaveFocus()
    await user.keyboard('{ArrowDown}')
    expect(await runnerTab(document.body, 'Pi')).toHaveFocus()
    expect(await runnerTab(document.body, 'Pi')).toHaveAttribute('aria-selected', 'true')
    // Right goes on to the models' search box.
    await user.keyboard('{ArrowRight}')
    expect(modelSearch(document.body)).toHaveFocus()
  })

  it('right at the end of the search goes to the chosen thinking level, not to Default', async () => {
    useCatalog()
    const user = userEvent.setup()
    renderWithQuery(<Picker initial={{ provider: 'claude-code', modelId: 'haiku' }} level="high" />)
    await screen.findByText('Claude Haiku (latest)')
    await vi.waitFor(() => expect(screen.getByRole('radio', { name: 'High' })).toBeEnabled())
    await user.click(modelSearch(document.body))
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('radio', { name: 'High' })).toHaveFocus()
  })

  it('Enter with nothing listed does nothing (it does not submit the form around the picker)', async () => {
    useCatalog()
    const submitted = vi.fn((event: React.FormEvent) => event.preventDefault())
    const user = userEvent.setup()
    renderWithQuery(<form onSubmit={submitted}><Picker initial={{ provider: 'claude-code', modelId: 'haiku' }} /><button type="submit">Create</button></form>)
    await screen.findByText('Claude Haiku (latest)')
    await user.type(modelSearch(document.body), 'no-such-model{Enter}')
    expect(submitted).not.toHaveBeenCalled()
    expect(shown()).toBe('claude-code/haiku default')
  })

  it('choosing a model leaves the providers where they were, so the row under the pointer stays the same', async () => {
    useCatalog()
    const user = userEvent.setup()
    renderWithQuery(<Picker initial={{ provider: 'anthropic', modelId: 'claude-sonnet-4-5' }} />)
    await screen.findByText('Claude Sonnet 4.5')
    const order = () => screen.getAllByRole('treeitem').filter((row) => row.hasAttribute('aria-expanded')).map((row) => row.getAttribute('aria-label')?.split(',')[0])
    const before = order()
    await chooseModel(user, document.body, 'openai/gpt-5', { provider: 'openrouter' })
    expect(shown()).toBe('openrouter/openai/gpt-5 default')
    expect(order()).toEqual(before)
  })

  it('a model in use whose provider left the catalog is named once, and nothing claims the catalog failed', async () => {
    useCatalog()
    server.use(http.get(apiUrl('/model-providers/gone/models'), () => HttpResponse.json({ error: { code: 'not_found', message: 'no such provider' } }, { status: 404 })))
    renderWithQuery(<Picker initial={{ provider: 'gone', modelId: 'old-model' }} />)
    expect(await screen.findByText("gone / old-model, used now, isn't in the model catalog")).toBeInTheDocument()
    // It runs on the default runtime, which is looked at first; its providers list fine, and so does Claude Code's.
    expect(await runnerTab(document.body, 'Pi')).toHaveAttribute('aria-selected', 'true')
    await screen.findByRole('treeitem', { name: 'anthropic' })
    expect(screen.queryByText(/Couldn't load the model catalog/)).toBeNull()
    await chooseRunner(userEvent.setup(), document.body, 'Claude Code')
    await screen.findByText('Claude Haiku (latest)')
    expect(screen.queryByText(/Couldn't load the model catalog/)).toBeNull()
    expect(screen.queryByText(/^In use:/)).toBeNull()
    expect(screen.queryByText('Pick a model first')).toBeNull()
  })

  it('a model whose level comes with it (Antigravity: one level, off) leaves the ruler empty with a note, and a context size of 0 (Codex) is not shown', async () => {
    useCatalog()
    catalog['claude-code'] = [...catalog['claude-code'], { ...model('fixed-high', 'Fixed High', ['off']), reasoning: true, defaultThinkingLevel: 'off', contextWindow: 0, maxTokens: 0 }]
    renderWithQuery(<Picker initial={{ provider: 'claude-code', modelId: 'fixed-high' }} />)
    const row = await screen.findByRole('treeitem', { name: 'Fixed High (fixed-high)' })
    expect(row).not.toHaveTextContent(/\b0\b/)
    expect(await screen.findByText("This model's thinking level is fixed: it comes with the model")).toBeInTheDocument()
    expect(screen.queryByRole('radiogroup', { name: 'Thinking level' })).toBeNull()
    catalog['claude-code'] = catalog['claude-code'].filter((entry) => entry.id !== 'fixed-high')
  })

  it('a local ACP command a person added is a runtime of its own: its mark is its initial, its one model has no level, and choosing it drops the level', async () => {
    const definitions = runtimeDefinitions()
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json({ runtimes: [...definitions.runtimes, acpCommandDefinition()] })))
    useCatalog({ runtimes: [...runtimeList().runtimes, acpCommandStatus()] }, [provider(acpRuntimeId, { source: 'local_cli', runtime: acpRuntimeId, authMethods: [], modelCount: 1 })])
    catalog[acpRuntimeId] = [acpDefaultModel]
    const user = userEvent.setup()
    renderWithQuery(<Picker initial={{ provider: 'anthropic', modelId: 'claude-sonnet-4-5' }} level="off" />)
    const tab = await runnerTab(document.body, 'Hermes')
    expect(tab).toHaveTextContent(/^H/)
    await chooseModel(user, document.body, 'default', { runtime: 'Hermes' })
    expect(shown()).toBe(`${acpRuntimeId}/default default`)
    expect(await screen.findByText('The program sets its own thinking level')).toBeInTheDocument()
    expect(screen.queryByRole('radiogroup', { name: 'Thinking level' })).toBeNull()
    delete catalog[acpRuntimeId]
  })

  it('an added command whose program is missing says what to check in its own words, links to where it is edited, and its foot line says it was added', async () => {
    const definitions = runtimeDefinitions()
    server.use(
      http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json({ runtimes: [...definitions.runtimes, acpCommandDefinition(), acpCommandDefinition('9b8f0c1d-2e3a-4b5c-8d6e-7f8091a2b3c4', 'Athena')] })),
      http.get(apiUrl('/acp-commands'), () => HttpResponse.json({ commands: [acpCommand()] })),
    )
    const found = acpCommandStatus('9b8f0c1d-2e3a-4b5c-8d6e-7f8091a2b3c4')
    useCatalog({ runtimes: [...runtimeList().runtimes, acpCommandStatus(undefined, { available: false, binaryPath: null, unavailableReason: 'not_found', login: null }), found] }, [provider(found.id, { source: 'local_cli', runtime: found.id, authMethods: [], modelCount: 1 })])
    const user = userEvent.setup()
    renderWithQuery(<Picker />)
    // Its stamp says the program can't be found, not that something isn't installed.
    expect(await runnerTab(document.body, 'Hermes')).toHaveAccessibleName(/^Hermes, Not found/)
    expect(await runnerTab(document.body, 'Hermes')).toHaveTextContent('Not found')
    await chooseRunner(user, document.body, 'Hermes')
    expect(await screen.findByText("hermes wasn't found on this computer: check the command is spelled right and runs in a terminal, or give its absolute path.")).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Edit this command in Global settings' })).toHaveAttribute('href', '/settings#settings-runtimes')
    await chooseRunner(user, document.body, 'Athena')
    expect(await screen.findByText('Athena is an ACP command you added in Global settings; it uses its own settings and login on this computer.')).toBeInTheDocument()
  })

  it('the thinking level is a ruler of the chosen model’s levels', async () => {
    useCatalog()
    const user = userEvent.setup()
    renderWithQuery(<Picker initial={{ provider: 'claude-code', modelId: 'haiku' }} />)
    await screen.findByText('Claude Haiku (latest)')
    await vi.waitFor(() => expect(thinkingOptions(document.body)).toEqual(['Default (Medium)', 'Max', 'Extra high', 'High', 'Medium', 'Low']))
    await chooseThinking(user, document.body, 'Extra high')
    expect(shown()).toBe('claude-code/haiku xhigh')
  })
  describe('a first entry that is not a runtime, and only some runtimes (who writes with AI)', () => {
    const local = (definition: Schemas['RuntimeDefinition']) => definition.capabilities.accounts === 'local'
    const automatic = (choose: () => void): RunnerLead => ({
      name: 'Automatic',
      mark: <span>A</span>,
      state: <span>Claude Code now</span>,
      stateText: 'Claude Code now',
      panel: (
        <button type="button" onClick={choose}>
          Use automatic
        </button>
      ),
      thinkingNote: 'Picked with the CLI',
    })

    it('offers only the runtimes asked for, with the first entry ahead of them; with nothing chosen it is in use and looked at', async () => {
      useCatalog()
      renderWithQuery(<Picker offers={local} lead={automatic} />)
      const rail = await screen.findByRole('tablist', { name: 'Runtimes' })
      await vi.waitFor(() => expect(within(rail).getAllByRole('tab').map((tab) => tab.getAttribute('aria-label'))).toEqual(['Automatic, Claude Code now, Current', 'Claude Code, Ready']))
      expect(within(rail).getAllByRole('tab')[0]).toHaveAttribute('aria-selected', 'true')
      const panel = screen.getByRole('tabpanel')
      expect(panel).toHaveAccessibleName('Automatic, Claude Code now, Current')
      expect(within(panel).getByRole('button', { name: 'Use automatic' })).toBeInTheDocument()
      expect(within(panel).queryByRole('tree')).toBeNull()
      expect(screen.getByText('Picked with the CLI')).toBeInTheDocument()
    })

    it('arrow keys go from the first entry to the runtimes; choosing a model leaves it, and its own button comes back to it', async () => {
      useCatalog()
      const user = userEvent.setup()
      renderWithQuery(<Picker offers={local} lead={automatic} />)
      const first = await runnerTab(document.body, 'Automatic')
      first.focus()
      await user.keyboard('{ArrowDown}')
      expect(await runnerTab(document.body, 'Claude Code')).toHaveFocus()
      await chooseModel(user, document.body, 'haiku')
      expect(shown()).toBe('claude-code/haiku default')
      expect(await runnerTab(document.body, 'Automatic')).toHaveAccessibleName('Automatic, Claude Code now')
      expect(await runnerTab(document.body, 'Claude Code')).toHaveAccessibleName('Claude Code, Ready, Current')
      await user.keyboard('{ArrowUp}')
      expect(await runnerTab(document.body, 'Automatic')).toHaveFocus()
      await user.click(screen.getByRole('button', { name: 'Use automatic' }))
      expect(shown()).toBe('none')
      expect(await runnerTab(document.body, 'Automatic')).toHaveAccessibleName('Automatic, Claude Code now, Current')
      expect(await runnerTab(document.body, 'Automatic')).toHaveAttribute('aria-selected', 'true')
    })

    it('Right arrow on the first entry goes into its panel', async () => {
      useCatalog()
      const user = userEvent.setup()
      renderWithQuery(<Picker offers={local} lead={automatic} initial={{ provider: 'claude-code', modelId: 'opus' }} />)
      await vi.waitFor(async () => expect(await runnerTab(document.body, 'Claude Code')).toHaveAttribute('aria-selected', 'true'))
      const first = await runnerTab(document.body, 'Automatic')
      await user.click(first)
      await user.keyboard('{ArrowRight}')
      expect(screen.getByRole('button', { name: 'Use automatic' })).toHaveFocus()
    })

    it('with a model chosen, its runtime is looked at first, not the first entry', async () => {
      useCatalog()
      renderWithQuery(<Picker offers={local} lead={automatic} initial={{ provider: 'claude-code', modelId: 'opus' }} />)
      // The first entry is there before the runtimes have been read.
      await vi.waitFor(async () => expect(await runnerTab(document.body, 'Claude Code')).toHaveAttribute('aria-selected', 'true'))
      expect(await screen.findByRole('tree')).toBeInTheDocument()
    })
  })
})
