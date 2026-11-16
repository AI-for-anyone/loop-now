import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { agentsUrl, apiUrl, context, defaultAgentsPath, errorBody, makeAgent, makeStatus, toListItem } from '@/test/api'
import { pluginProvider, providers, catalogHandlers, withClaudeCode } from '@/test/config'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { sidebar } from '@/test/shell'
import { chooseModel } from '@/test/model-picker'
import { chooseThinking, thinkingChoice, thinkingOptions } from '@/test/thinking'

function setup(onCreate: (body: Schemas['AgentCreate']) => Response) {
  const agents: Schemas['Agent'][] = []
  server.use(
    http.get(apiUrl('/context'), () => HttpResponse.json(context)),
    http.get(agentsUrl, () => HttpResponse.json({ agents: agents.map((a) => toListItem(a)) })),
    http.post(agentsUrl, async ({ request }) => {
      const body = (await request.json()) as Schemas['AgentCreate']
      const response = onCreate(body)
      if (response.status === 201) agents.push((await response.clone().json()) as Schemas['Agent'])
      return response
    }),
    http.get(apiUrl('/agents/:agentId'), ({ params }) => HttpResponse.json(agents.find((a) => a.id === params.agentId))),
    http.get(apiUrl('/agents/:agentId/status'), ({ params }) => HttpResponse.json(makeStatus(params.agentId as string))),
    http.get(apiUrl('/agents/:agentId/skills'), () => HttpResponse.json({ skills: [] })),
    http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: [] })),
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
    ...catalogHandlers,
  )
  const user = userEvent.setup()
  return { user, ...renderRoutes(routes, defaultAgentsPath) }
}

async function fill(user: ReturnType<typeof userEvent.setup>, name: string, pickModel = true) {
  // When there is no Agent, the empty state in the main area also has a “New Agent” button; here we use the “+” in the sidebar.
  await user.click(await within(sidebar()).findByRole('button', { name: 'New Agent' }))
  const dialog = await screen.findByRole('dialog')
  await user.type(within(dialog).getByLabelText('Display name'), name)
  if (pickModel) {
    await user.selectOptions(await within(dialog).findByLabelText('Model provider'), 'anthropic')
    await chooseModel(user, dialog, 'claude-sonnet-5-5')
  }
  return dialog
}

describe('Creating an Agent', () => {
  it('picks a supported thinking level from the same model catalog and saves the explicit choice on create', async () => {
    let created: Schemas['AgentCreate'] | null = null
    const { user } = setup(body => { created = body; return HttpResponse.json(makeAgent({ name: body.name, model: body.model, thinkingLevel: body.thinkingLevel }), { status: 201 }) })
    const model: Schemas['Model'] = { id: 'claude-sonnet-5-5', name: 'Sonnet', reasoning: true, contextWindow: 100, maxTokens: 100, input: ['text'], thinkingLevels: ['low', 'high'], defaultThinkingLevel: 'low' }
    server.use(http.get(apiUrl('/model-providers/anthropic/models'), () => HttpResponse.json({ providerId: 'anthropic', models: [model] })))
    const dialog = await fill(user, 'Thinking assistant')
    expect(thinkingChoice(dialog)).toBe('Default (Low)')
    expect(thinkingOptions(dialog)).toEqual(['Default (Low)', 'Low', 'High'])
    await chooseThinking(user, dialog, 'High')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(created).toEqual({ name: 'Thinking assistant', model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' }, thinkingLevel: 'high' }))
  })

  it('a new Codex Agent picks speed and thinking level separately; switching to another provider and back resets speed to Standard and keeps the prompt', async () => {
    let created: Schemas['AgentCreate'] | null = null
    const { user } = setup(body => { created = body; return HttpResponse.json(makeAgent({ name: body.name, model: body.model, serviceTier: body.serviceTier }), { status: 201 }) })
    server.use(
      http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, { id: 'openai-codex', source: 'builtin', modelCount: 1, apiKeyEnvVars: [], authMethods: ['oauth'] }] })),
      http.get(apiUrl('/model-providers/openai-codex/models'), () => HttpResponse.json({ providerId: 'openai-codex', models: [{ id: 'gpt-a', name: 'GPT A', reasoning: true, contextWindow: 100, maxTokens: 100, input: ['text'], thinkingLevels: ['medium', 'high'], defaultThinkingLevel: 'medium' }] })),
    )
    const dialog = await fill(user, 'Speed assistant', false)
    const provider = within(dialog).getByLabelText('Model provider')
    await user.selectOptions(provider, 'openai-codex'); await chooseModel(user, dialog, 'gpt-a')
    expect(within(dialog).getByLabelText('Codex speed')).toHaveDisplayValue('Standard')
    await user.selectOptions(within(dialog).getByLabelText('Codex speed'), 'priority')
    await user.type(within(dialog).getByLabelText('Prompt'), 'Keep this draft')
    await user.selectOptions(provider, 'anthropic'); await chooseModel(user, dialog, 'claude-sonnet-5-5')
    expect(within(dialog).queryByLabelText('Codex speed')).toBeNull()
    await user.selectOptions(provider, 'openai-codex')
    expect(within(dialog).getByLabelText('Codex speed')).toHaveDisplayValue('Standard')
    expect(within(dialog).getByLabelText('Prompt')).toHaveValue('Keep this draft')
    await user.selectOptions(within(dialog).getByLabelText('Codex speed'), 'ultrafast')
    await chooseThinking(user, dialog, 'High')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(created).toEqual({ name: 'Speed assistant', model: { provider: 'openai-codex', modelId: 'gpt-a' }, thinkingLevel: 'high', serviceTier: 'ultrafast', prompt: 'Keep this draft' }))
  })

  it('after a successful create, selects the new Agent and opens its Config page', async () => {
    let created: Schemas['AgentCreate'] | null = null
    const { user, router } = setup((body) => {
      created = body
      return HttpResponse.json(makeAgent({ name: body.name, model: body.model }), { status: 201 })
    })
    const dialog = await fill(user, 'dev-helper')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(created).toEqual({ name: 'dev-helper', model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' } }))
    await waitFor(() => expect(router.state.location.pathname).toMatch(new RegExp(`^${defaultAgentsPath}/.+/config$`)))
    const list = await screen.findByRole('list', { name: 'Agents' })
    await waitFor(() => expect(within(list).getByRole('link', { name: /dev-helper/ })).toHaveAttribute('aria-current', 'page'))
  })

  it('a plugin provider cannot be picked at create time: the option is disabled and says to grant the plugin after creating, and the catalog request has no agentId', async () => {
    let query: string | null = null
    const { user } = setup((body) => HttpResponse.json(makeAgent({ name: body.name, model: body.model }), { status: 201 }))
    server.use(
      http.get(apiUrl('/model-providers'), ({ request }) => {
        query = new URL(request.url).search
        return HttpResponse.json({ ...providers, providers: [...providers.providers, pluginProvider('grok-cli')] })
      }),
    )
    const dialog = await fill(user, 'Grok assistant', false)
    const select = await within(dialog).findByLabelText('Model provider')
    const grok = await waitFor(() => {
      const found = within(select).getAllByRole('option').find((item) => (item as HTMLOptionElement).value === 'grok-cli')
      if (!found) throw new Error('grok-cli is not in the catalog yet')
      return found
    })
    expect(grok).toBeDisabled()
    expect(grok).toHaveTextContent('Grok CLI · grok-cli (plugin "Grok subscription", 2 models) — grant the plugin after creating, then choose')
    expect(query).toBe('')
  })

  it('Claude Code on this computer can be picked directly: notes that it uses the local login, shows no speed tier, and creates with the selection', async () => {
    let created: Schemas['AgentCreate'] | null = null
    const { user } = setup(body => { created = body; return HttpResponse.json(makeAgent({ name: body.name, model: body.model, runtime: 'claude-code' }), { status: 201 }) })
    server.use(http.get(apiUrl('/model-providers'), () => HttpResponse.json(withClaudeCode)))
    const dialog = await fill(user, 'Local assistant', false)
    const select = await within(dialog).findByLabelText('Model provider')
    const option = await within(select).findByRole('option', { name: 'Claude Code (this computer, 2 models)' })
    expect(option).toBeEnabled()
    await user.selectOptions(select, 'claude-code')
    expect(within(dialog).getByText('Uses the Claude Code installed on this computer and your login there. The platform doesn\'t store login details.')).toHaveAttribute('role', 'note')
    // The two points you need to know the moment you choose it; plugins and /pi: commands are explained only on the Config page.
    const differences = within(dialog).getByRole('region', { name: 'Running on Claude Code' })
    expect(within(differences).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      'It runs with your home directory and logins on this computer: gh, git and other commands it runs use your own logins.',
      'It runs commands without asking first, the same as Pi Agents.',
    ])
    await chooseModel(user, dialog, 'sonnet')
    expect(within(dialog).queryByLabelText('Codex speed')).toBeNull()
    await chooseThinking(user, dialog, 'Max')
    await user.selectOptions(select, 'anthropic')
    expect(within(dialog).queryByRole('region', { name: 'Running on Claude Code' })).toBeNull()
    await user.selectOptions(select, 'claude-code')
    await chooseModel(user, dialog, 'sonnet')
    await chooseThinking(user, dialog, 'Max')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(created).toEqual({ name: 'Local assistant', model: { provider: 'claude-code', modelId: 'sonnet' }, thinkingLevel: 'max' }))
  })

  it('does not submit without a model', async () => {
    let calls = 0
    const { user } = setup(() => {
      calls += 1
      return HttpResponse.json({}, { status: 201 })
    })
    const dialog = await fill(user, 'dev-helper', false)
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('Choose a model')).toBeInTheDocument()
    expect(calls).toBe(0)
  })

  it('on a duplicate name, shows the reason next to the name and keeps the input', async () => {
    const { user } = setup(() => HttpResponse.json(errorBody('name_conflict', 'dup'), { status: 409 }))
    const dialog = await fill(user, 'dev-helper')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('The project already has an Agent with this name. Pick another.')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Display name')).toHaveValue('dev-helper')
    expect(within(dialog).getByRole('combobox', { name: 'Model' })).toHaveTextContent('claude-sonnet-5-5')
  })

  it('on a network error, says the server is unreachable and keeps the input', async () => {
    const { user } = setup(() => HttpResponse.error())
    const dialog = await fill(user, 'dev-helper')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('Can\'t reach the server')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Display name')).toHaveValue('dev-helper')
  })

  it('a name that breaks the naming rules is flagged locally first and not submitted', async () => {
    let calls = 0
    const { user } = setup(() => {
      calls += 1
      return HttpResponse.json({}, { status: 201 })
    })
    const dialog = await fill(user, 'frontend/dev')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('Names can\'t contain "/" or "#"')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Display name')).toHaveValue('frontend/dev')
    expect(calls).toBe(0)
  })

  it('when the server rejects the name under the naming rules, shows the reason next to the name', async () => {
    const { user } = setup(() =>
      HttpResponse.json(errorBody('validation_failed', 'bad', { fields: [{ field: 'name', message: 'Name must not be a UUID' }] }), { status: 422 }),
    )
    const dialog = await fill(user, 'Looks fine')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('Name must not be a UUID')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Display name')).toHaveValue('Looks fine')
  })

  it('when the server rejects both the name and the model, shows each reason next to its input', async () => {
    const { user } = setup(() =>
      HttpResponse.json(
        errorBody('validation_failed', 'bad', {
          fields: [
            { field: 'name', message: 'Name must not be a UUID' },
            { field: 'model.modelId', message: 'Provider anthropic has no model claude-sonnet-5-5' },
          ],
        }),
        { status: 422 },
      ),
    )
    const dialog = await fill(user, 'Looks fine')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('Name must not be a UUID')).toBeInTheDocument()
    expect(within(dialog).getByText('Provider anthropic has no model claude-sonnet-5-5')).toBeInTheDocument()
    expect(within(dialog).queryByRole('alert')).toBeNull()
  })

  it('shows the reason when the provider\'s model list fails to load', async () => {
    const { user } = setup(() => HttpResponse.json({}, { status: 201 }))
    server.use(http.get(apiUrl('/model-providers/openai/models'), () => HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 })))
    const dialog = await fill(user, 'dev-helper', false)
    await user.selectOptions(await within(dialog).findByLabelText('Model provider'), 'openai')
    expect(await within(dialog).findByText('Couldn\'t load the model catalog, so the model can\'t be changed right now.')).toBeInTheDocument()
    expect(within(dialog).getByText(/^Reason: Internal server error/)).toBeInTheDocument()
  })
})
