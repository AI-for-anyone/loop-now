import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody } from '@/test/api'
import { chooseModel, runnerTab } from '@/test/model-picker'
import { renderRoutes } from '@/test/render'
import { runtimeList } from '@/test/runtimes'
import { server } from '@/test/server'
import { automaticSettings, haikuWriter, textGenerationBackend } from '@/test/text-generation'
import { chooseThinking } from '@/test/thinking'
import { WritingSettings } from './WritingSettings'

const claudeModels: Schemas['Model'][] = [
  { id: 'opus', name: 'Claude Opus (latest)', reasoning: true, thinkingLevels: ['low', 'medium', 'high'], defaultThinkingLevel: 'medium', contextWindow: 200000, maxTokens: 64000, input: ['text'] },
  { id: 'haiku', name: 'Claude Haiku (latest)', reasoning: true, thinkingLevels: ['low', 'medium', 'high'], defaultThinkingLevel: 'medium', contextWindow: 200000, maxTokens: 64000, input: ['text'] },
]

function setup(init: Parameters<typeof textGenerationBackend>[0] = {}) {
  const backend = textGenerationBackend(init)
  server.use(
    ...backend.handlers,
    http.get(apiUrl('/runtimes'), () => HttpResponse.json(runtimeList())),
    http.get(apiUrl('/model-providers'), () =>
      HttpResponse.json({
        piVersion: '1.4.0',
        catalogGeneratedAt: null,
        providers: [
          { id: 'anthropic', name: null, source: 'builtin', runtime: 'pi', pluginId: null, pluginDisplayName: null, unavailableReason: null, usable: null, apiKeyEnvVars: [], modelCount: 1, authMethods: ['api_key'] },
          { id: 'claude-code', name: null, source: 'local_cli', runtime: 'claude-code', pluginId: null, pluginDisplayName: null, unavailableReason: null, usable: null, apiKeyEnvVars: [], modelCount: 2, authMethods: [] },
        ],
      }),
    ),
    http.get(apiUrl('/model-providers/claude-code/models'), () => HttpResponse.json({ providerId: 'claude-code', models: claudeModels })),
  )
  renderRoutes([{ path: '/', element: <WritingSettings /> }], '/')
  return { ...backend, user: userEvent.setup() }
}
const panel = () => screen.getByRole('tabpanel')

describe('Who writes with AI, in global settings', () => {
  it('Automatic comes first and is in use, naming who writes now; only CLIs with their own login are offered, not the bundled Pi', async () => {
    setup()
    expect(await screen.findByText('When you click "Write with AI" beside a prompt, the CLI chosen here writes it with its own login on this computer. No Agent is created.')).toBeInTheDocument()
    const automatic = await runnerTab(document.body, 'Automatic')
    expect(automatic).toHaveAccessibleName('Automatic, Now Claude Code, Current')
    await vi.waitFor(() => expect(within(screen.getByRole('tablist')).getAllByRole('tab').map((tab) => tab.getAttribute('aria-label'))).toEqual(['Automatic, Now Claude Code, Current', 'Claude Code, Ready']))
    expect(within(panel()).getByText('Claude Code')).toBeInTheDocument()
    expect(within(panel()).getByText('Claude Haiku (latest)')).toBeInTheDocument()
    expect(within(panel()).getByText('Using Automatic')).toBeInTheDocument()
    expect(within(panel()).queryByRole('button', { name: 'Use Automatic' })).toBeNull()
    expect(screen.getByText('Nothing to choose with Automatic')).toBeInTheDocument()
  })

  it('a model is saved as soon as it is chosen, then its level; Use Automatic goes back to Automatic', async () => {
    const { user, state } = setup()
    await vi.waitFor(async () => expect(await runnerTab(document.body, 'Claude Code')).toBeInTheDocument())
    await chooseModel(user, document.body, 'opus', { runtime: 'Claude Code' })
    await vi.waitFor(() => expect(state.puts).toEqual([{ selection: { provider: 'claude-code', modelId: 'opus' } }]))
    expect(await runnerTab(document.body, 'Claude Code')).toHaveAccessibleName('Claude Code, Ready, Current')
    expect(await runnerTab(document.body, 'Automatic')).toHaveAccessibleName('Automatic, The first one ready')
    await chooseThinking(user, document.body, 'High')
    await vi.waitFor(() => expect(state.puts.at(-1)).toEqual({ selection: { provider: 'claude-code', modelId: 'opus', thinkingLevel: 'high' } }))
    await user.click(await runnerTab(document.body, 'Automatic'))
    await user.click(within(panel()).getByRole('button', { name: 'Use Automatic' }))
    await vi.waitFor(() => expect(state.puts.at(-1)).toEqual({ selection: null }))
    expect(await within(panel()).findByText('Using Automatic')).toBeInTheDocument()
  })

  it('when no CLI is ready, says so and links to the runtimes section', async () => {
    setup({ settings: automaticSettings(null) })
    expect(await screen.findByText('No CLI on this computer can write yet. Install one and log in under Agent runtimes first.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Go to Agent runtimes' })).toHaveAttribute('href', '/settings#settings-runtimes')
    expect(await runnerTab(document.body, 'Automatic')).toHaveAccessibleName('Automatic, None ready yet, Current')
  })

  it('when the chosen one is not ready, says who writes instead', async () => {
    setup({ settings: { selection: { provider: 'claude-code', modelId: 'opus' }, writer: haikuWriter({ runtime: 'codex', provider: 'codex-cli', modelId: 'gpt-5-mini', modelName: 'GPT-5 mini', fellBack: true }) } })
    expect(await screen.findByText("Claude Code · opus can't write right now, so codex · GPT-5 mini writes for now.")).toBeInTheDocument()
  })

  it('a failed save says why and shows what is saved again', async () => {
    const { user } = setup()
    server.use(http.put(apiUrl('/text-generation-settings'), () => HttpResponse.json(errorBody('validation_failed', 'provider is not a local CLI'), { status: 422 })))
    await vi.waitFor(async () => expect(await runnerTab(document.body, 'Claude Code')).toBeInTheDocument())
    await chooseModel(user, document.body, 'opus', { runtime: 'Claude Code' })
    expect(await screen.findByText(/provider is not a local CLI/)).toBeInTheDocument()
    expect(await runnerTab(document.body, 'Automatic')).toHaveAccessibleName('Automatic, Now Claude Code, Current')
  })

  it('says why when the setting cannot be read', async () => {
    server.use(http.get(apiUrl('/text-generation-settings'), () => HttpResponse.json(errorBody('internal_error', 'database is locked'), { status: 500 })))
    renderRoutes([{ path: '/', element: <WritingSettings /> }], '/')
    expect(await screen.findByText(/database is locked/)).toBeInTheDocument()
    expect(screen.queryByRole('tablist')).toBeNull()
  })
})
