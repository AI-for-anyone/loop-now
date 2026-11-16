import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { createBrowserRouter, RouterProvider } from 'react-router'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { agentsUrl, apiUrl, defaultAgentPath, defaultAgentsPath, errorBody, makeAgent, toListItem } from '@/test/api'
import { pluginProvider, providers, renderConfig } from '@/test/config'
import { createTestQueryClient } from '@/test/render'
import { chooseModel } from '@/test/model-picker'

const region = (name: string) => screen.getByRole('region', { name })
const saveButton = () => within(region('Basics')).getByRole('button', { name: 'Save' })

describe('Leave confirmation for unsaved changes', () => {
  // On browser back/forward (POP navigation), popstate fires asynchronously; the memory router
  // fires it synchronously and cannot reproduce this, so a browser router is used here.
  it('on browser back, clicking "Leave" really leaves and the confirmation does not appear again', async () => {
    const agent = makeAgent()
    // Borrow renderConfig to register the API handlers, then render again with a browser router.
    const { unmount, user } = await renderConfig({ agent })
    unmount()
    window.history.replaceState(null, '', defaultAgentPath(agent.id, 'chat'))
    const router = createBrowserRouter(routes)
    render(
      <QueryClientProvider client={createTestQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )
    try {
      await user.click(await screen.findByRole('link', { name: 'Config' }))
      await user.type(await screen.findByLabelText('Agent prompt'), 'New prompt')
      window.history.back()
      const dialog = await screen.findByRole('alertdialog')
      expect(dialog).toHaveTextContent('You have unsaved changes')
      await waitFor(() => expect(window.location.pathname).toMatch(/\/config$/))
      await user.click(within(dialog).getByRole('button', { name: 'Leave' }))
      await waitFor(() => expect(window.location.pathname).toMatch(/\/chat$/))
      await waitFor(() => expect(router.state.location.pathname).toMatch(/\/chat$/))
      // Wait until every async popstate has been handled, and confirm that navigation was not blocked again.
      await new Promise((resolve) => setTimeout(resolve, 50))
      expect(screen.queryByRole('alertdialog')).toBeNull()
      expect(window.location.pathname).toMatch(/\/chat$/)
    } finally {
      router.dispose()
    }
  })

  it('deleting the Agent with unsaved changes goes straight back to the list', async () => {
    let deleted = false
    const agent = makeAgent()
    const { user, router } = await renderConfig({
      agent,
      handlers: [
        // After the deletion the list no longer contains it: the project has no Agents left, so
        // the Agent page stays on the first-use empty state instead of jumping to another Agent.
        http.get(agentsUrl, () => HttpResponse.json({ agents: deleted ? [] : [toListItem(agent)] })),
        http.delete(apiUrl(`/agents/${agent.id}`), () => {
          deleted = true
          return new HttpResponse(null, { status: 204 })
        }),
      ],
    })
    await user.type(screen.getByLabelText('Agent prompt'), 'Unsaved prompt')
    const danger = region('Delete Agent')
    await waitFor(() => expect(within(danger).getByRole('button', { name: 'Delete this Agent' })).toBeEnabled())
    await user.click(within(danger).getByRole('button', { name: 'Delete this Agent' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(deleted).toBe(true))
    await waitFor(() => expect(router.state.location.pathname).toBe(defaultAgentsPath))
    expect(screen.queryByText('You have unsaved changes. Leave anyway? They will be lost.')).toBeNull()
  })
})

describe('Following server refreshes', () => {
  it('without local changes, the form takes the latest server values and does not show unsaved', async () => {
    const agent = makeAgent()
    let current = agent
    const { client, user, router } = await renderConfig({ agent, handlers: [http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current))] })
    current = { ...agent, name: 'Renamed assistant', prompt: 'Prompt changed elsewhere', promptVersion: 2, model: { provider: 'openai', modelId: 'gpt-x' } }
    await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
    await waitFor(() => expect(screen.getByLabelText('Display name')).toHaveValue('Renamed assistant'))
    expect(screen.getByLabelText('Agent prompt')).toHaveValue('Prompt changed elsewhere')
    await waitFor(() => expect(screen.getByLabelText('Model provider')).toHaveValue('openai'))
    expect(saveButton()).toBeDisabled()
    await user.click(screen.getByRole('link', { name: 'Chat' }))
    await waitFor(() => expect(router.state.location.pathname).toMatch(/\/chat$/))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it("with local changes, keeps the person's edits and shows a notice; saving submits only the changed fields", async () => {
    let saved: unknown = null
    const agent = makeAgent()
    let current = agent
    const { client, user } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          saved = await request.json()
          current = { ...current, ...(saved as Schemas['AgentUpdate']) }
          return HttpResponse.json(current)
        }),
      ],
    })
    await user.clear(screen.getByLabelText('Display name'))
    await user.type(screen.getByLabelText('Display name'), 'My name')
    current = { ...agent, name: 'Renamed assistant', prompt: 'Prompt changed elsewhere' }
    await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
    // The server's new prompt reaches the input only after the prompt editor syncs it, so wait
    // for it to arrive; the name the person edited never changes, so it can't be used to wait on.
    await waitFor(() => expect(screen.getByLabelText('Agent prompt')).toHaveValue(current.prompt))
    expect(screen.getByLabelText('Display name')).toHaveValue('My name')
    expect(within(region('Basics')).getByText(/The server's config was updated/)).toBeInTheDocument()
    await user.click(saveButton())
    await waitFor(() => expect(saved).toEqual({ name: 'My name' }))
    await waitFor(() => expect(within(region('Basics')).queryByText(/The server's config was updated/)).toBeNull())
    await waitFor(() => expect(saveButton()).toBeDisabled())
  })

  it("with local changes, the notice also shows when only untouched fields changed elsewhere; after saving one's own changes it no longer shows", async () => {
    const agent = makeAgent()
    let current = agent
    const { client, user } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          current = { ...current, ...((await request.json()) as Schemas['AgentUpdate']) }
          return HttpResponse.json(current)
        }),
      ],
    })
    await user.clear(screen.getByLabelText('Display name'))
    await user.type(screen.getByLabelText('Display name'), 'My name')
    current = { ...agent, model: { provider: 'openai', modelId: 'gpt-x' } }
    await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
    // Same as above: wait for the notice that the server config was updated, not for the name, which never changes.
    await waitFor(() => expect(within(region('Basics')).getByText(/The server's config was updated/)).toBeInTheDocument())
    expect(screen.getByLabelText('Display name')).toHaveValue('My name')
    expect(screen.getByLabelText('Agent prompt')).toHaveValue(current.prompt)
    await user.click(saveButton())
    await waitFor(() => expect(saveButton()).toBeDisabled())
    await user.type(screen.getByLabelText('Display name'), ' again')
    expect(within(region('Basics')).queryByText(/The server's config was updated/)).toBeNull()
  })
})

describe('Model choice', () => {
  it('switching to another provider and back restores the original model; a model missing from the catalog is restored too and other changes can be saved', async () => {
    let saved: unknown = null
    const agent = makeAgent({ model: { provider: 'anthropic', modelId: 'retired-model' } })
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          saved = await request.json()
          return HttpResponse.json(agent)
        }),
      ],
    })
    const basic = region('Basics')
    expect(await within(basic).findByText('The current model isn\'t in the server\'s model catalog')).toBeInTheDocument()
    await user.type(screen.getByLabelText('Display name'), '2')
    const provider = within(basic).getByLabelText('Model provider')
    await user.selectOptions(provider, 'openai')
    expect(within(basic).queryByText('The current model isn\'t in the server\'s model catalog')).toBeNull()
    await user.selectOptions(provider, 'anthropic')
    expect(await within(basic).findByText('The current model isn\'t in the server\'s model catalog')).toBeInTheDocument()
    expect(within(basic).getByText('anthropic / retired-model')).toBeInTheDocument()
    expect(saveButton()).toBeEnabled()
    await user.click(saveButton())
    await waitFor(() => expect(saved).toEqual({ name: 'dev-helper2' }))
  })

  it("when a provider's model list fails to load, shows the reason and keeps showing the current model", async () => {
    await renderConfig({
      handlers: [http.get(apiUrl('/model-providers/anthropic/models'), () => HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 }))],
    })
    const basic = region('Basics')
    expect(await within(basic).findByText('Couldn\'t load the model catalog, so the model can\'t be changed right now.')).toBeInTheDocument()
    expect(within(basic).getByText(/^Reason: Internal server error/)).toBeInTheDocument()
    expect(within(basic).getByText('anthropic / claude-sonnet-5-5')).toBeInTheDocument()
  })

  it('when the provider list fails to load, the reason is shown as well', async () => {
    await renderConfig({
      handlers: [http.get(apiUrl('/model-providers'), () => HttpResponse.error())],
    })
    const basic = region('Basics')
    expect(await within(basic).findByText("Reason: Can't reach the server")).toBeInTheDocument()
    expect(within(basic).getByText('anthropic / claude-sonnet-5-5')).toBeInTheDocument()
  })

  it('server field validation errors for the model show next to the model picker; all other field errors are listed', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.patch(apiUrl(`/agents/${agent.id}`), () =>
          HttpResponse.json(
            errorBody('validation_failed', 'model.modelId: Provider anthropic has no model claude-haiku-5-5', {
              fields: [
                { field: 'model.modelId', message: 'Provider anthropic has no model claude-haiku-5-5' },
                { field: 'prompt', message: 'Prompt is too long' },
                { field: 'other', message: 'Unknown field' },
              ],
            }),
            { status: 422 },
          ),
        ),
      ],
    })
    const basic = region('Basics')
    await chooseModel(user, basic, 'claude-haiku-5-5')
    await user.click(saveButton())
    expect(await within(basic).findByText('Provider anthropic has no model claude-haiku-5-5')).toBeInTheDocument()
    const alert = within(basic).getByRole('alert')
    expect(alert).toHaveTextContent('prompt: Prompt is too long')
    expect(alert).toHaveTextContent('other: Unknown field')
    expect(alert).not.toHaveTextContent('model.modelId')
  })
})

describe('Plugin providers', () => {
  const grokModels = http.get(apiUrl('/model-providers/:providerId/models'), ({ params }) =>
    HttpResponse.json({ providerId: params.providerId, models: [{ id: 'grok-4', name: 'Grok 4', reasoning: false, contextWindow: 1, maxTokens: 1, input: ['text'] }] }),
  )
  const catalog = (plugins: Schemas['ModelProvider'][]) => ({ ...providers, providers: [...providers.providers, ...plugins] })
  const option = (select: HTMLElement, value: string) => within(select).getAllByRole('option').find((item) => (item as HTMLOptionElement).value === value) as HTMLOptionElement

  it('reads the catalog with agentId: unusable plugin providers show the reason and are disabled; with all plugins turned off globally they stay selectable but warn that starting will fail', async () => {
    const agent = makeAgent()
    let asked: string | null = null
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl('/model-providers'), ({ request }) => {
          asked = new URL(request.url).searchParams.get('agentId')
          return HttpResponse.json(
            catalog([
              pluginProvider('grok-cli', { unavailableReason: 'agent_not_granted' }),
              pluginProvider('grok-off', { name: null, unavailableReason: 'plugins_disabled' }),
              pluginProvider('grok-ok', { name: 'Grok OK' }),
            ]),
          )
        }),
        grokModels,
      ],
    })
    const basic = region('Basics')
    const select = within(basic).getByLabelText('Model provider')
    await waitFor(() => expect(option(select, 'grok-cli')).toBeDefined())
    expect(asked).toBe(agent.id)
    expect(option(select, 'anthropic')).toHaveTextContent(/^anthropic \(2 models\)$/)
    expect(option(select, 'grok-cli')).toHaveTextContent('Grok CLI · grok-cli (plugin "Grok subscription", 2 models) — plugin not granted to this Agent')
    expect(option(select, 'grok-cli')).toBeDisabled()
    expect(option(select, 'grok-ok')).toHaveTextContent(/^Grok OK · grok-ok \(plugin "Grok subscription", 2 models\)$/)
    expect(option(select, 'grok-ok')).toBeEnabled()
    expect(option(select, 'grok-off')).toHaveTextContent('grok-off (plugin "Grok subscription", 2 models) — all plugins are turned off')
    expect(option(select, 'grok-off')).toBeEnabled()
    const disabledNote = 'All plugins are turned off in global settings. You can save, but the Agent will fail to start.'
    expect(within(basic).queryByText(disabledNote)).toBeNull()
    await user.selectOptions(select, 'grok-off')
    expect(within(basic).getByText(disabledNote)).toHaveAttribute('role', 'note')
    await user.selectOptions(select, 'grok-ok')
    expect(within(basic).queryByText(disabledNote)).toBeNull()
  })

  it('the plugin provider the Agent uses is now unusable: a note below the picker gives the reason and where to fix it', async () => {
    const agent = makeAgent({ model: { provider: 'grok-cli', modelId: 'grok-4' } })
    await renderConfig({ agent, handlers: [http.get(apiUrl('/model-providers'), () => HttpResponse.json(catalog([pluginProvider('grok-cli', { unavailableReason: 'plugin_not_enabled' })]))), grokModels] })
    const basic = region('Basics')
    expect(await within(basic).findByText('This project hasn\'t enabled the plugin "Grok subscription". Enable it on the project\'s Plugins page, then grant it to this Agent under "Plugin grants" on this page.')).toHaveAttribute('role', 'note')
    expect(within(basic).getByLabelText('Model provider')).toHaveValue('grok-cli')
  })

  it("platform Agent: a granted plugin's providers can be chosen; without a grant the option is disabled, and for the one in use a note says to grant it on this page", async () => {
    const agent = makeAgent({ scope: 'platform', projectId: null, model: { provider: 'grok-cli', modelId: 'grok-4' } })
    // The model credential section also reads the catalog (without agentId), so record every read here.
    const asked: (string | null)[] = []
    await renderConfig({ agent, handlers: [http.get(apiUrl('/model-providers'), ({ request }) => {
      asked.push(new URL(request.url).searchParams.get('agentId'))
      return HttpResponse.json(catalog([pluginProvider('grok-cli', { unavailableReason: 'agent_not_granted' }), pluginProvider('grok-ok', { name: 'Grok OK' })]))
    }), grokModels] })
    const basic = region('Basics')
    const select = within(basic).getByLabelText('Model provider')
    await waitFor(() => expect(option(select, 'grok-cli')).toBeDisabled())
    expect(asked).toContain(agent.id)
    expect(option(select, 'grok-cli')).toHaveTextContent('Grok CLI · grok-cli (plugin "Grok subscription", 2 models) — plugin not granted to this Agent')
    expect(option(select, 'grok-ok')).toBeEnabled()
    expect(option(select, 'grok-ok')).toHaveTextContent(/^Grok OK · grok-ok \(plugin "Grok subscription", 2 models\)$/)
    expect(within(basic).getByText('The plugin "Grok subscription" isn\'t granted to this Agent yet. Grant it under "Plugin grants" on this page to choose this provider.')).toHaveAttribute('role', 'note')
    expect(within(basic).queryByText(/only for project Agents/)).toBeNull()
  })

  it('a 422 on save (model.provider): the message shows next to the model, and once the catalog is read again the reason is marked', async () => {
    const agent = makeAgent()
    let granted = true
    let reads = 0
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl('/model-providers'), () => {
          reads += 1
          return HttpResponse.json(catalog([pluginProvider('grok-cli', { unavailableReason: granted ? null : 'agent_not_granted' })]))
        }),
        grokModels,
        http.patch(apiUrl(`/agents/${agent.id}`), () => {
          granted = false
          return HttpResponse.json(errorBody('validation_failed', 'Plugin grok is not granted to this Agent', { fields: [{ field: 'model.provider', message: 'Plugin grok is not granted to this Agent' }] }), { status: 422 })
        }),
      ],
    })
    const basic = region('Basics')
    const select = within(basic).getByLabelText('Model provider')
    await waitFor(() => expect(option(select, 'grok-cli')).toBeEnabled())
    await user.selectOptions(select, 'grok-cli')
    await chooseModel(user, basic, 'grok-4')
    const before = reads
    await user.click(saveButton())
    expect(await within(basic).findByText('Plugin grok is not granted to this Agent')).toBeInTheDocument()
    await waitFor(() => expect(option(select, 'grok-cli')).toBeDisabled())
    expect(reads).toBeGreaterThan(before)
    expect(within(basic).getByText('The plugin "Grok subscription" isn\'t granted to this Agent yet. Grant it under "Plugin grants" on this page to choose this provider.')).toHaveAttribute('role', 'note')
  })
})
