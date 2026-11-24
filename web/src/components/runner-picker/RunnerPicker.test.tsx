import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl } from '@/test/api'
import { chooseModel, chooseRunner, modelOptions, modelSearch, openProvider, runnerTab } from '@/test/model-picker'
import { renderWithQuery } from '@/test/render'
import { runtimeList } from '@/test/runtimes'
import { server } from '@/test/server'
import { chooseThinking, thinkingChoice, thinkingOptions } from '@/test/thinking'
import { RunnerPicker, type RunnerChoice } from './RunnerPicker'

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
function useCatalog(statuses: Schemas['RuntimeList'] = runtimeList()) {
  const asked: URLSearchParams[] = []
  server.use(
    http.get(apiUrl('/runtimes'), () => HttpResponse.json(statuses)),
    http.get(apiUrl('/model-providers'), ({ request }) => {
      const query = new URL(request.url).searchParams
      asked.push(query)
      const forSomeone = query.has('agentId') || query.has('projectId') || query.has('scope')
      const list = [provider('anthropic'), provider('grok-cli', { source: 'plugin', name: 'Grok CLI', pluginId: 'grok', pluginDisplayName: { 'en-US': 'Grok', 'zh-CN': 'Grok' }, authMethods: ['oauth'], modelCount: 1 }), provider('openrouter'), provider('claude-code', { source: 'local_cli', runtime: 'claude-code', authMethods: [] })].map((p) => ({
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

function Picker({ initial = null, level = null, onChoose = () => {}, agentId, newIn }: { initial?: Schemas['ModelRef'] | null; level?: Schemas['ThinkingLevel'] | null; onChoose?: (choice: RunnerChoice) => void; agentId?: string; newIn?: string }) {
  const [value, setValue] = useState(initial)
  const [thinking, setThinking] = useState(level)
  return (
    <div>
      <RunnerPicker
        value={value}
        thinkingLevel={thinking}
        agentId={agentId}
        newIn={newIn}
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

  it('the thinking level is a ruler of the chosen model’s levels', async () => {
    useCatalog()
    const user = userEvent.setup()
    renderWithQuery(<Picker initial={{ provider: 'claude-code', modelId: 'haiku' }} />)
    await screen.findByText('Claude Haiku (latest)')
    await vi.waitFor(() => expect(thinkingOptions(document.body)).toEqual(['Default (Medium)', 'Max', 'Extra high', 'High', 'Medium', 'Low']))
    await chooseThinking(user, document.body, 'Extra high')
    expect(shown()).toBe('claude-code/haiku xhigh')
  })
})
