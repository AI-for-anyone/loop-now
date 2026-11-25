import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { projectPromptKey } from '@/api/prompts'
import { routes } from '@/app/routes'
import { apiUrl, defaultProject, errorBody, makeAgent } from '@/test/api'
import { sidebar } from '@/test/shell'
import { renderConfig } from '@/test/config'
import { projectBackend } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { textGenerationBackend } from '@/test/text-generation'

const promptInput = () => screen.getByLabelText('Agent prompt')
const saveAgent = () => screen.getByRole('button', { name: 'Save Agent prompt' })
const basic = () => screen.getByRole('region', { name: 'Basics' })
function setupProject(handlers: HttpHandler[] = []) {
  const backend = projectBackend()
  server.use(...handlers, ...backend.handlers)
  return { ...backend, ...renderRoutes(routes, `/p/${encodeURIComponent(defaultProject.name)}/settings`), user: userEvent.setup() }
}

describe('Agent prompt: formal CAS and a separate draft', () => {
  it('submits the old base even for the same text and is protected by 412; after reviewing V2, a background V3 does not silently change the adopted version', async () => {
    const agent = makeAgent({ prompt: 'Original' }), submissions: Schemas['AgentUpdate'][] = []
    let current = agent
    const { user, client } = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
      http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => { submissions.push(await request.json() as Schemas['AgentUpdate']); return HttpResponse.json(errorBody('precondition_failed'), { status: 412 }) }),
    ] })
    await user.clear(promptInput()); await user.type(promptInput(), 'Draft')
    current = { ...agent, prompt: 'Draft', promptVersion: 2 }
    await act(() => client.invalidateQueries({ queryKey: keys.agent(agent.id) }))
    await user.click(saveAgent()); await screen.findByText('Server text · version 2')
    expect(submissions[0]).toEqual({ prompt: 'Draft', expectedPromptVersion: 1 })
    current = { ...current, prompt: 'Server third version', promptVersion: 3 }
    await act(() => client.invalidateQueries({ queryKey: keys.agent(agent.id) }))
    expect(screen.getByText('Server text · version 2')).toBeInTheDocument()
    expect(screen.queryByText('Server text · version 3')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Reviewed version 2; keep editing my draft' }))
    expect(promptInput()).toHaveValue('Draft'); expect(screen.getByText('Editing from version 2')).toBeInTheDocument()
    await user.click(saveAgent()); await screen.findByText('Server text · version 3')
    expect(submissions[1]).toEqual({ prompt: 'Draft', expectedPromptVersion: 2 })
  })
  it('when the GET after a 412 fails, keeps the draft and the original base and allows no adopting; after an explicit re-read it can be reviewed', async () => {
    const agent = makeAgent({ prompt: 'Original' }); let failRead = false
    const { user } = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => failRead ? HttpResponse.json(errorBody('internal_error'), { status: 500 }) : HttpResponse.json(agent)),
      http.patch(apiUrl(`/agents/${agent.id}`), () => { failRead = true; return HttpResponse.json(errorBody('precondition_failed'), { status: 412 }) }),
    ] })
    await user.type(promptInput(), 'Draft'); await user.click(saveAgent())
    await screen.findByText(/Couldn't read the latest content\. Your draft and editing baseline are kept/)
    expect(promptInput()).toHaveValue('OriginalDraft'); expect(screen.getByText('Editing from version 1')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Reviewed version/ })).toBeNull()
    failRead = false; await user.click(screen.getByRole('button', { name: 'Read the latest content again' })); await screen.findByText('Server text · version 1')
  })
  it.each(['basic', 'prompt'] as const)('after saving %s, the other block is still dirty and there is only one leave confirmation', async savedBlock => {
    const agent = makeAgent(); let current = agent; const submissions: unknown[] = []
    const { user, router } = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
      http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => { const body = await request.json() as Schemas['AgentUpdate']; submissions.push(body); current = { ...current, ...body, promptVersion: current.promptVersion + ('prompt' in body ? 1 : 0) }; return HttpResponse.json(current) }),
    ] })
    await user.type(screen.getByLabelText('Display name'), 'Rename'); await user.type(promptInput(), 'Draft')
    await user.click(savedBlock === 'basic' ? within(basic()).getByRole('button', { name: 'Save' }) : saveAgent())
    await waitFor(() => expect(savedBlock === 'basic' ? within(basic()).getByRole('button', { name: 'Save' }) : saveAgent()).toBeDisabled())
    expect(savedBlock === 'basic' ? saveAgent() : within(basic()).getByRole('button', { name: 'Save' })).toBeEnabled()
    expect(submissions[0]).toEqual(savedBlock === 'basic' ? { name: 'dev-helperRename' } : { prompt: 'Draft', expectedPromptVersion: 1 })
    await user.click(screen.getByRole('link', { name: 'Edit in project settings' }))
    expect(await screen.findAllByRole('alertdialog')).toHaveLength(1)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))
    expect(router.state.location.pathname).toMatch(/\/config$/); expect(promptInput()).toHaveValue('Draft')
  })
  it('refreshing project rules updates only the inherited section and leaves the Agent draft alone', async () => {
    let project: Schemas['ProjectPrompt'] = { prompt: 'Shared old version', promptVersion: 1 }
    const { user, client } = await renderConfig({ handlers: [http.get(apiUrl('/projects/:projectId/prompt'), () => HttpResponse.json(project))] })
    await user.type(promptInput(), 'My duties'); project = { prompt: 'Shared new version', promptVersion: 2 }
    await act(() => client.invalidateQueries({ queryKey: projectPromptKey(defaultProject.id) }))
    expect(await screen.findByText('Shared new version')).toBeInTheDocument(); expect(promptInput()).toHaveValue('My duties')
    expect(screen.getByText('Editing from version 1')).toBeInTheDocument()
  })
  it('a platform Agent edits only its own rules and does not read the project prompt', async () => {
    const agent = makeAgent({ scope: 'platform', projectId: null }); let inheritedReads = 0
    const backend = projectBackend({ agents: [agent] })
    server.use(http.get(apiUrl('/projects/:projectId/prompt'), () => { inheritedReads++; return HttpResponse.json({ prompt: 'Should not be read', promptVersion: 1 }) }),
      http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: null, operation: null })), ...backend.handlers)
    renderRoutes(routes, `/platform/agents/${agent.id}/config`)
    await screen.findByLabelText('Agent prompt')
    expect(screen.queryByRole('region', { name: 'Project prompt (read-only)' })).toBeNull()
    expect(screen.getByText('Platform Agents use only their own prompt; they don\'t inherit project rules.')).toBeInTheDocument()
    expect(inheritedReads).toBe(0)
    expect(screen.getByText('The current reply keeps the old config; the next dispatched message uses the new Agent prompt.')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Prompt' })).queryByText(/two-level snapshot/)).toBeNull()
  })
})

describe('Project prompt: separate saving and leaving', () => {
  it('saving and clearing carry their own version; leaving with a draft asks for confirmation', async () => {
    let value: Schemas['ProjectPrompt'] = { prompt: 'Shared old version', promptVersion: 1 }; const requests: unknown[] = []
    const { user, router } = setupProject([
      http.get(apiUrl('/projects/:projectId/prompt'), () => HttpResponse.json(value)),
      http.put(apiUrl('/projects/:projectId/prompt'), async ({ request }) => { const body = await request.json() as Schemas['ProjectPromptUpdate']; requests.push(body); value = { prompt: body.prompt, promptVersion: value.promptVersion + 1 }; return HttpResponse.json(value) }),
    ])
    const input = await screen.findByRole('textbox', { name: 'Project prompt' }); await user.clear(input); await user.type(input, 'Shared new version')
    await user.click(screen.getByRole('button', { name: 'Save project prompt' })); await waitFor(() => expect(screen.getByRole('button', { name: 'Save project prompt' })).toBeDisabled())
    expect(requests[0]).toEqual({ prompt: 'Shared new version', expectedPromptVersion: 1 })
    await user.clear(input); await user.click(screen.getByRole('button', { name: 'Save project prompt' })); await waitFor(() => expect(requests).toHaveLength(2))
    expect(requests[1]).toEqual({ prompt: '', expectedPromptVersion: 2 })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save project prompt' })).toBeDisabled())
    await user.type(input, 'Unsaved'); await user.click(within(sidebar()).getByRole('link', { name: 'Tasks' }))
    await screen.findByRole('alertdialog'); expect(router.state.location.pathname).toMatch(/settings$/)
  })
  it('a rename with the same projectId normalizes the URL, keeps the prompt draft and does not wrongly show the leave confirmation', async () => {
    const { user, state, client, router } = setupProject()
    const input = await screen.findByRole('textbox', { name: 'Project prompt' }); await user.type(input, 'Draft during the rename')
    state.projects = state.projects.map(project => ({ ...project, name: 'Renamed project' }))
    await act(() => client.invalidateQueries({ queryKey: keys.projects }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/p/'+encodeURIComponent('Renamed project')+'/settings'))
    expect(input).toHaveValue('Draft during the rename'); expect(screen.queryByRole('alertdialog')).toBeNull()
  })
  it('a project PUT conflict keeps the draft; after the GET of the latest text, an explicit review saves on the new base', async () => {
    let value: Schemas['ProjectPrompt'] = { prompt: 'Original project', promptVersion: 1 }; const requests: Schemas['ProjectPromptUpdate'][] = []
    const { user } = setupProject([
      http.get(apiUrl('/projects/:projectId/prompt'), () => HttpResponse.json(value)),
      http.put(apiUrl('/projects/:projectId/prompt'), async ({ request }) => {
        const body=await request.json() as Schemas['ProjectPromptUpdate'];requests.push(body)
        if(body.expectedPromptVersion!==value.promptVersion)return HttpResponse.json(errorBody('precondition_failed'),{status:412})
        value={prompt:body.prompt,promptVersion:value.promptVersion+1};return HttpResponse.json(value)
      }),
    ])
    const input=await screen.findByRole('textbox',{name:'Project prompt'});await user.clear(input);await user.type(input,'Project draft')
    value={prompt:'Project rules from another page',promptVersion:2}
    await user.click(screen.getByRole('button',{name:'Save project prompt'}));await screen.findByText('Server text · version 2')
    expect(input).toHaveValue('Project draft');expect(requests[0]).toEqual({prompt:'Project draft',expectedPromptVersion:1})
    await user.click(screen.getByRole('button',{name:'Reviewed version 2; keep editing my draft'}));await user.click(screen.getByRole('button',{name:'Save project prompt'}))
    await waitFor(()=>expect(screen.getByRole('button',{name:'Save project prompt'})).toBeDisabled())
    expect(requests[1]).toEqual({prompt:'Project draft',expectedPromptVersion:2})
  })

})

describe('Write with AI in the Prompt editors', () => {
  it("writes the Agent prompt for this Agent; the draft is an unsaved edit that saves the usual way", async () => {
    const agent = makeAgent({ prompt: 'Original' }), submissions: Schemas['AgentUpdate'][] = []
    const backend = textGenerationBackend()
    const { user } = await renderConfig({ agent, handlers: [
      ...backend.handlers,
      http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => { const body = await request.json() as Schemas['AgentUpdate']; submissions.push(body); return HttpResponse.json({ ...agent, ...body, promptVersion: 2 }) }),
    ] })
    const section = screen.getByRole('region', { name: 'Prompt' })
    await user.click(within(section).getByRole('button', { name: 'Write with AI' }))
    await user.type(within(section).getByLabelText('What should it do?'), 'Sort issues')
    await user.click(within(section).getByRole('button', { name: 'Write' }))
    await waitFor(() => expect(promptInput()).toHaveValue('Written: Sort issues'))
    expect(backend.state.drafts).toEqual([{ target: 'agent', agentId: agent.id, request: 'Sort issues', currentText: 'Original' }])
    expect(within(section).getByText('Unsaved')).toBeInTheDocument()
    expect(submissions).toEqual([])
    await user.click(saveAgent())
    await waitFor(() => expect(submissions).toEqual([{ prompt: 'Written: Sort issues', expectedPromptVersion: 1 }]))
  })
  it('writes the project prompt for the project', async () => {
    const backend = textGenerationBackend()
    const { user } = setupProject(backend.handlers)
    const input = await screen.findByRole('textbox', { name: 'Project prompt' })
    await user.click(screen.getByRole('button', { name: 'Write with AI' }))
    await user.type(screen.getByLabelText('What should it do?'), 'Mobile app')
    await user.click(screen.getByRole('button', { name: 'Write' }))
    await waitFor(() => expect(input).toHaveValue('Written: Mobile app'))
    expect(backend.state.drafts).toEqual([{ target: 'project', projectId: defaultProject.id, request: 'Mobile app', currentText: '' }])
    expect(screen.getByRole('button', { name: 'Save project prompt' })).toBeEnabled()
  })
})
