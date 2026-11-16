import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { credentialKeys } from '@/api/project-credentials'
import { apiUrl, defaultProject, errorBody, makeAgent } from '@/test/api'
import { codexProvider } from '@/test/model-auth'
import { emptySelections, projectAccount, projectOperation } from '@/test/project-credentials'
import { renderConfig, providers, larkTemplate } from '@/test/config'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { ProjectAccounts, ProjectCredentialSettings } from './ProjectAccounts'
import { ProjectLogin } from './ProjectLogin'

afterEach(() => vi.restoreAllMocks())
const directory = http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, codexProvider] }))

describe('Project accounts, formal HTTP fixture', () => {
  it('a new API key sends only the formal credential object and stays out of query and mutation caches; success clears the input', async () => {
    const user = userEvent.setup(), requests: unknown[] = []
    let accounts: Schemas['ProjectCredentialAccount'][] = []
    server.use(directory, http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts })),
      http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accountId: null, agents: [] })),
      http.post(apiUrl('/projects/:projectId/credential-accounts'), async ({ request }) => { requests.push(await request.json()); accounts = [projectAccount()]; return HttpResponse.json(accounts[0], { status: 201 }) }),
    )
    const { client } = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="model" /></DraftNavigationGuard> }], '/')
    await user.click(screen.getByLabelText('Add model account · provider'))
    await user.click(await screen.findByRole('option', { name: /^Anthropic/ }))
    await user.type(screen.getByLabelText('Account name'), '项目账号A'); await user.type(screen.getByLabelText('API Key'), 'fixture-secret')
    await user.click(screen.getByRole('button', { name: 'Save key' }))
    await waitFor(() => expect(screen.queryByLabelText('API Key')).toBeNull())
    expect(requests).toEqual([{ purpose: 'model', provider: 'anthropic', name: '项目账号A', credential: { type: 'api_key', key: 'fixture-secret' } }])
    expect(screen.getByText('Saved', { exact: true })).toBeInTheDocument()
    expect(JSON.stringify(client.getQueryCache().getAll().map(item => item.state))).not.toContain('fixture-secret')
    expect(JSON.stringify(client.getMutationCache().getAll().map(item => item.state))).not.toContain('fixture-secret')
  })

  it.each([
    ['active', 'note', '"Use local logins" is on: this uses the extension\'s own login on this computer, not the project account chosen here.'],
    ['missing', 'alert', '"Use local logins" is on, but this computer has no login for the extension, so the Agent will fail to start. Turn the switch off, or log in on this computer, and try again.'],
  ] as const)('a plugin provider with "Use local logins" on (localLogin=%s): the account choice explains it', async (localLogin, role, message) => {
    const agent = makeAgent({ model: { provider: 'grok-cli', modelId: 'grok-4' } })
    const data = emptySelections(agent)
    const account = projectAccount({ provider: 'grok-cli', authMethod: 'oauth', status: 'logged_in' })
    data.selections[0] = { purpose: 'model', provider: 'grok-cli', cli: null, mode: 'automatic', selectedAccountId: null, resolvedAccountId: account.id, resolvedVersionId: account.currentVersionId, status: 'resolved', candidates: [account], localLogin }
    await renderConfig({ agent, handlers: [http.get(apiUrl(`/agents/${agent.id}/credential-selections`), () => HttpResponse.json(data))] })
    const area = screen.getByRole('region', { name: 'Model account credential' })
    const hint = await within(area).findByText(message)
    expect(hint.closest(`[role="${role}"]`)).not.toBeNull()
    const link = within(area).queryByRole('link', { name: 'Open local mode settings' })
    if (localLogin === 'missing') expect(link).toHaveAttribute('href', '/settings#local-mode')
    else expect(link).toBeNull()
  })

  it('without "Use local logins" (no localLogin) there is no explanation', async () => {
    const agent = makeAgent()
    await renderConfig({ agent })
    const area = screen.getByRole('region', { name: 'Model account credential' })
    await within(area).findByText(/project account|[Aa]utomatic/)
    expect(within(area).queryByText(/Use local logins/)).toBeNull()
  })

  it('the migration barrier is shown from the server mode; null writes only the current provider, and switching to B does not falsely report automatic', async () => {
    const agent = makeAgent(), writes: unknown[] = []
    let latestAgent = agent
    let data = emptySelections(agent)
    data.selections[0] = { ...data.selections[0], mode: 'legacy_cleared', selectedAccountId: null, resolvedAccountId: null, resolvedVersionId: null, status: 'not_configured', candidates: [projectAccount()] }
    const env = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(latestAgent)),
      http.get(apiUrl(`/agents/${agent.id}/credential-selections`), () => HttpResponse.json(data)),
      http.put(apiUrl(`/agents/${agent.id}/credential-selections`), async ({ request }) => { writes.push(await request.json()); latestAgent = { ...agent, configVersion: 2 }; data = { ...data, configVersion: 2, selections: [{ ...data.selections[0], mode: 'automatic', selectedAccountId: null, status: 'resolved', resolvedAccountId: projectAccount().id }] }; return HttpResponse.json(data) }),
    ] })
    const area = screen.getByRole('region', { name: 'Model account credential' })
    await within(area).findByText(/the old model setting was cleared/)
    await env.user.click(within(area).getByRole('button', { name: 'Restore automatic selection for this provider' }))
    await within(area).findByText('Automatically uses the only matching project account')
    expect(writes).toEqual([{ purpose: 'model', provider: 'anthropic', accountId: null, expectedConfigVersion: 1 }])
    // Simulate the authoritative response of the real GET after the provider switch, instead of guessing mode from null.
    const b = { ...agent, configVersion: 3, model: { provider: 'openai', modelId: 'gpt-x' } }
    latestAgent = b
    data = { ...data, configVersion: 3, selections: [...data.selections, { ...emptySelections(b).selections[0], mode: 'legacy_cleared', selectedAccountId: null, resolvedAccountId: null, resolvedVersionId: null, status: 'not_configured' }] }
    await act(async () => { env.client.setQueryData(['agent', agent.id], b); await env.client.invalidateQueries({ queryKey: credentialKeys.selections(agent.id) }) })
    expect(await within(area).findByText(/the old model setting was cleared/)).toBeInTheDocument()
    expect(writes).toHaveLength(1)
  })

  it('a remote delete of the account being edited does not unmount the secret draft; after 412 the tombstone read cannot be adopted or revived', async () => {
    const user = userEvent.setup(), account = projectAccount()
    let accounts = [account], puts = 0
    server.use(directory, http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts })),
      http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accountId: account.id, agents: [] })),
      http.get(apiUrl('/projects/:projectId/credential-accounts/:accountId'), () => HttpResponse.json({ ...account, status: 'deleted', revision: 2, deletedAt: account.updatedAt })),
      http.put(apiUrl('/projects/:projectId/credential-accounts/:accountId/credential'), () => { puts++; return HttpResponse.json(errorBody('precondition_failed'), { status: 412 }) }),
    )
    const { client } = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="model" /></DraftNavigationGuard> }], '/')
    await user.click(await screen.findByRole('button', { name: 'Replace credential' }))
    await user.type(screen.getByLabelText('API Key'), 'keep-local-secret')
    accounts = []
    await act(() => client.invalidateQueries({ queryKey: credentialKeys.project(defaultProject.id) }))
    expect(screen.getByLabelText('API Key')).toHaveValue('keep-local-secret')
    expect(await screen.findByText(/The account is no longer in the list; your edit is kept/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await screen.findByText(/The account was deleted, so it can't be replaced/)
    expect(screen.getByRole('button', { name: 'Reviewed, adopt this version' })).toBeDisabled()
    expect(puts).toBe(1)
  })

  it('a project Agent has no private secret entry; a platform Agent still uses the original private endpoint', async () => {
    const agent = makeAgent({ clis: [{ name: 'gh', hasTemplate: true, credential: null }] })
    await renderConfig({ agent })
    await screen.findAllByText('No matching account; set one up in project settings')
    expect(screen.queryByRole('button', { name: 'Enter API key' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Enter credential' })).toBeNull()
    expect(screen.queryByLabelText('API Key')).toBeNull()
  })

  it('a selection 412 submits only once and keeps the selection draft; after review it saves the single item with the latest configVersion', async () => {
    const agent = makeAgent(), a = projectAccount(), b = projectAccount({ id: '10000000-0000-7000-8000-000000000002', name: 'Account B', status: 'unavailable' }), bodies: unknown[] = []
    let data = emptySelections(agent)
    data.selections[0] = { ...data.selections[0], mode: 'automatic', selectedAccountId: null, status: 'selection_required', candidates: [a, b] }
    const env = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json({ ...agent, configVersion: data.configVersion })),
      http.get(apiUrl(`/agents/${agent.id}/credential-selections`), () => HttpResponse.json(data)),
      http.put(apiUrl(`/agents/${agent.id}/credential-selections`), async ({ request }) => {
        bodies.push(await request.json())
        if (bodies.length === 1) {
          data = { ...data, configVersion: 2 }
          return HttpResponse.json(errorBody('precondition_failed', 'The configuration has changed'), { status: 412 })
        }
        data = { ...data, configVersion: 3, selections: [{ ...data.selections[0], mode: 'explicit', selectedAccountId: a.id, resolvedAccountId: a.id, status: 'resolved' }] }
        return HttpResponse.json(data)
      }),
    ] })
    const area = screen.getByRole('region', { name: 'Model account credential' })
    await within(area).findByText(/Several matching accounts/)
    await env.user.selectOptions(within(area).getByRole('combobox'), a.id)
    await env.user.click(within(area).getByRole('button', { name: 'Save selection' }))
    await within(area).findByText('The config was updated elsewhere; your changes weren\'t saved')
    expect(bodies).toHaveLength(1); expect(within(area).getByRole('combobox')).toHaveValue(a.id)
    await env.user.click(within(area).getByRole('button', { name: 'Reviewed, adopt this version' }))
    await env.user.click(within(area).getByRole('button', { name: 'Save selection' }))
    await within(area).findByText('Uses the chosen project account')
    expect(bodies).toEqual([1, 2].map(expectedConfigVersion => ({ purpose: 'model', provider: 'anthropic', accountId: a.id, expectedConfigVersion })))
  })

  it('writes are blocked while the Agent basic config and the selection version differ; saving is allowed only after a refresh syncs them', async () => {
    const agent = makeAgent(), data = emptySelections(agent), writes: unknown[] = []
    data.configVersion = 2
    data.selections[0] = { ...data.selections[0], mode: 'automatic', selectedAccountId: null, status: 'selection_required', candidates: [projectAccount(), projectAccount({ id: '10000000-0000-7000-8000-000000000002' })] }
    let current = agent
    const env = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
      http.get(apiUrl(`/agents/${agent.id}/credential-selections`), () => HttpResponse.json(data)),
      http.put(apiUrl(`/agents/${agent.id}/credential-selections`), async ({ request }) => { writes.push(await request.json()); return HttpResponse.json(data) }),
    ] })
    const area = screen.getByRole('region', { name: 'Model account credential' })
    await within(area).findByText(/The basic config and the account source are out of sync/)
    expect(within(area).getByRole('combobox')).toBeDisabled()
    expect(within(area).getByRole('button', { name: 'Save selection' })).toBeDisabled()
    expect(writes).toHaveLength(0)
    current = { ...agent, configVersion: 2 }
    await act(() => env.client.invalidateQueries({ queryKey: ['agent', agent.id] }))
    await waitFor(() => expect(within(area).getByRole('combobox')).toBeEnabled())
    await env.user.selectOptions(within(area).getByRole('combobox'), projectAccount().id)
    await env.user.click(within(area).getByRole('button', { name: 'Save selection' }))
    await waitFor(() => expect(writes).toEqual([{ purpose: 'model', provider: 'anthropic', accountId: projectAccount().id, expectedConfigVersion: 2 }]))
  })

  it('separate project model and CLI drafts share one leave guard; saving the model does not clear the risky CLI form', async () => {
    const user = userEvent.setup(), bodies: unknown[] = []
    let accounts: Schemas['ProjectCredentialAccount'][] = []
    server.use(directory,
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [larkTemplate] })),
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts })),
      http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accountId: null, agents: [] })),
      http.post(apiUrl('/projects/:projectId/credential-accounts'), async ({ request }) => { bodies.push(await request.json()); accounts = [projectAccount()]; return HttpResponse.json(accounts[0], { status: 201 }) }),
    )
    const env = renderRoutes([
      { path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="model" /><ProjectAccounts projectId={defaultProject.id} purpose="cli" /></DraftNavigationGuard> },
      { path: '/other', element: <p>Left settings</p> },
    ], '/')
    const model = screen.getByRole('region', { name: 'Model accounts' }), cli = screen.getByRole('region', { name: 'CLI accounts' })
    await user.click(within(model).getByLabelText('Add model account · provider'))
    await user.click(await within(model).findByRole('option', { name: /^Anthropic/ }))
    await user.type(within(model).getByLabelText('Account name'), '项目账号A'); await user.type(within(model).getByLabelText('API Key'), 'model-local-secret')
    await within(cli).findByRole('option', { name: 'lark-cli' })
    expect(within(cli).queryByRole('option', { name: 'jq' })).toBeNull()
    expect(within(cli).getByText(/Tools without a template can't be entered/)).toBeInTheDocument()
    await user.selectOptions(within(cli).getByRole('combobox'), 'lark-cli')
    await user.click(within(cli).getByRole('button', { name: 'Enter an app identity account' }))
    expect(within(cli).getByRole('note')).toHaveTextContent('the host user\'s own Lark identity')
    expect(within(cli).getByLabelText('App Secret *')).toHaveAttribute('type', 'password')
    expect(within(cli).getByLabelText('品牌 *')).toHaveValue('feishu')
    await user.type(within(cli).getByLabelText('App Secret *'), 'cli-local-secret')
    await act(async () => { void env.router.navigate('/other') })
    expect(await screen.findAllByRole('alertdialog')).toHaveLength(1)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))
    expect(within(model).getByLabelText('API Key')).toHaveValue('model-local-secret')
    expect(within(cli).getByLabelText('App Secret *')).toHaveValue('cli-local-secret')
    await user.click(within(model).getByRole('button', { name: 'Save key' }))
    await waitFor(() => expect(within(model).queryByLabelText('API Key')).toBeNull())
    expect(within(cli).getByLabelText('App Secret *')).toHaveValue('cli-local-secret')
    expect(bodies).toHaveLength(1)
    await act(async () => { void env.router.navigate('/other') })
    expect(await screen.findAllByRole('alertdialog')).toHaveLength(1)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Leave' }))
    expect(await screen.findByText('Left settings')).toBeInTheDocument()
    expect(screen.queryByLabelText('App Secret *')).toBeNull()
  })
})

describe('Project OAuth, formal HTTP fixture, no navigation to the provider', () => {
  it('after switching projects, a late start does not navigate or close the new project form, and nothing is resent automatically', async () => {
    const user = userEvent.setup(), starts: unknown[] = []
    let finish: ((response: Response) => void) | undefined
    const popup = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    server.use(http.post(apiUrl('/projects/:projectId/model-auth/operations'), async ({ request }) => { starts.push(await request.json()); return new Promise<Response>(resolve => { finish = resolve }) }))
    const { router } = renderRoutes([
      { path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} provider={codexProvider} /></DraftNavigationGuard> },
      { path: '/other', element: <p>Another project</p> },
    ], '/')
    await user.type(screen.getByLabelText('New OpenAI Codex account name'), 'New account')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Log in to OpenAI Codex' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Log in to OpenAI Codex' }))
    await waitFor(() => expect(finish).toBeDefined())
    await act(async () => { void router.navigate('/other') })
    // The name draft has only one leave guard; after confirming, the old owner is unmounted.
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Leave' }))
    await screen.findByText('Another project')
    await act(async () => finish?.(HttpResponse.json({ operation: projectOperation(), account: null }, { status: 202 })))
    expect(popup.location.replace).not.toHaveBeenCalled(); expect(popup.close).toHaveBeenCalled()
    expect(starts).toHaveLength(1)
  })
  it('a refresh restores the active new login and cancels it with a single POST; the terminal URL stays out of the DOM', async () => {
    const user = userEvent.setup(), op = projectOperation(), calls: unknown[] = []
    let state: Schemas['ProjectModelAuthState'] = { operation: op, account: null }
    server.use(
      http.get(apiUrl('/projects/:projectId/model-auth/operations'), () => HttpResponse.json({ operations: state.operation.status === 'cancelled' ? [] : [state.operation] })),
      http.get(apiUrl('/projects/:projectId/model-auth/operations/:operationId'), () => HttpResponse.json(state)),
      http.post(apiUrl('/projects/:projectId/model-auth/operations/:operationId/cancel'), async ({ request }) => { calls.push(request.url); state = { operation: { ...op, status: 'cancelled', authorizationUrl: null }, account: null }; return HttpResponse.json(state) }),
    )
    renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} provider={codexProvider} /></DraftNavigationGuard> }], '/')
    await screen.findByText('Waiting for browser authorization')
    expect(screen.getByLabelText('New OpenAI Codex account name')).toHaveValue(op.name)
    expect(screen.getByLabelText('New OpenAI Codex account name')).toBeDisabled()
    expect(screen.getByLabelText('New OpenAI Codex account name')).toHaveAccessibleDescription(/A new login is already in progress.*Open authorization page.*Cancel login.*ends or expires/ )
    expect(screen.getByRole('button', { name: 'Log in to OpenAI Codex' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Cancel login' }))
    expect(await screen.findByText('Login cancelled')).toBeInTheDocument()
    expect(screen.getByLabelText('New OpenAI Codex account name')).toBeEnabled()
    await user.type(screen.getByLabelText('New OpenAI Codex account name'), 'New name after cancelling')
    expect(screen.getByRole('button', { name: 'Log in to OpenAI Codex' })).toBeEnabled()
    expect(calls).toHaveLength(1)
    expect(document.body.innerHTML).not.toContain('client_id=fixture-only')
    expect(JSON.stringify({ ...localStorage, ...sessionStorage })).not.toContain('client_id=fixture-only')
  })

  it.each(['cancelled', 'expired', 'failed'] as const)('an active login from elsewhere restores its name and keeps the local draft; editing returns only after %s, without an automatic start', async terminal => {
    const user = userEvent.setup(), posts = vi.fn()
    const op = projectOperation({ name: 'Account started in another window' })
    let state: Schemas['ProjectModelAuthState'] | null = null
    server.use(
      http.get(apiUrl('/projects/:projectId/model-auth/operations'), () => HttpResponse.json({ operations: state && (state.operation.status === 'pending' || state.operation.status === 'awaiting_authorization') ? [state.operation] : [] })),
      http.get(apiUrl('/projects/:projectId/model-auth/operations/:operationId'), () => HttpResponse.json(state)),
      http.post(apiUrl('/projects/:projectId/model-auth/operations'), () => { posts(); return HttpResponse.json({}, { status: 500 }) }),
    )
    const { client } = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} provider={codexProvider} /></DraftNavigationGuard> }], '/')
    await user.type(screen.getByLabelText('New OpenAI Codex account name'), 'Unsubmitted user draft')
    state = { operation: op, account: null }
    await act(() => client.invalidateQueries({ queryKey: credentialKeys.operations(defaultProject.id) }))
    await screen.findByText('Waiting for browser authorization')
    expect(screen.getByLabelText('New OpenAI Codex account name')).toHaveValue(op.name)
    expect(screen.getByLabelText('New OpenAI Codex account name')).toBeDisabled()
    expect(screen.getByLabelText('New OpenAI Codex account name')).toHaveAccessibleDescription(/the account name is locked/)
    state = { operation: { ...op, status: terminal, authorizationUrl: null }, account: null }
    await act(() => client.invalidateQueries({ queryKey: credentialKeys.operations(defaultProject.id) }))
    await waitFor(() => expect(screen.getByLabelText('New OpenAI Codex account name')).toBeEnabled())
    expect(screen.getByLabelText('New OpenAI Codex account name')).toHaveValue('Unsubmitted user draft')
    expect(screen.getByLabelText('New OpenAI Codex account name')).not.toHaveAttribute('aria-describedby')
    await user.clear(screen.getByLabelText('New OpenAI Codex account name'))
    await user.type(screen.getByLabelText('New OpenAI Codex account name'), 'New name after the terminal state')
    expect(screen.getByRole('button', { name: 'Log in to OpenAI Codex' })).toBeEnabled()
    expect(posts).not.toHaveBeenCalled()
  })

  it.each(['url-rejected', 'navigation-rejected'] as const)('%s closes the blank window that has not navigated, shows how to continue, and starts only once', async failure => {
    const user = userEvent.setup(), starts = vi.fn()
    const child = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    if (failure === 'navigation-rejected') child.location.replace.mockImplementation(() => { throw new Error('navigation rejected') })
    const op = projectOperation(failure === 'url-rejected' ? { authorizationUrl: 'https://other.test/oauth/authorize' } : {})
    server.use(
      http.post(apiUrl('/projects/:projectId/model-auth/operations'), () => { starts(); return HttpResponse.json({ operation: op, account: null }, { status: 202 }) }),
      http.get(apiUrl('/projects/:projectId/model-auth/operations/:operationId'), () => HttpResponse.json({ operation: op, account: null })),
    )
    const { client } = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} provider={codexProvider} /></DraftNavigationGuard> }], '/')
    await user.type(screen.getByLabelText('New OpenAI Codex account name'), 'Offline window test')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Log in to OpenAI Codex' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Log in to OpenAI Codex' }))
    if (failure === 'url-rejected') await screen.findByText(/the unknown URL wasn't opened and the login won't restart automatically/ )
    else await screen.findByText(/The authorization page didn't open; the browser may have blocked it/)
    await waitFor(() => expect(child.close).toHaveBeenCalledOnce())
    if (failure === 'navigation-rejected') expect(screen.getByRole('link', { name: 'Open authorization page' })).toHaveAttribute('rel', 'noopener noreferrer')
    else expect(screen.queryByRole('link', { name: 'Open authorization page' })).toBeNull()
    expect(starts).toHaveBeenCalledOnce()
    if (failure === 'url-rejected') expect(child.location.replace).not.toHaveBeenCalled()
    else {
      await user.click(screen.getByRole('link', { name: 'Open authorization page' }))
      expect(window.open).toHaveBeenCalledOnce() // Only the initial start reserves a window; continuing by hand goes through the native link.
      expect(screen.getByRole('link', { name: 'Open authorization page' })).toHaveAttribute('href', op.authorizationUrl)
      op.authorizationUrl += '&originator=pi'
      await act(() => client.invalidateQueries({ queryKey: credentialKeys.operations(defaultProject.id) }))
      expect(screen.queryByText(/The authorization page didn't open; the browser may have blocked it/)).toBeNull()
      expect(child.close).toHaveBeenCalledOnce()
      expect(starts).toHaveBeenCalledOnce()
    }
  })

  it('after a failed relogin the old account still exists on its own, and the outer form starts only with the current target revision', async () => {
    const user = userEvent.setup(), account = projectAccount({ provider: 'openai-codex', authMethod: 'oauth', status: 'logged_in', revision: 7 }), starts: unknown[] = []
    vi.spyOn(window, 'open').mockReturnValue(null)
    const failed = projectOperation({ targetAccountId: account.id, targetRevision: 7, status: 'failed', authorizationUrl: null })
    server.use(http.post(apiUrl('/projects/:projectId/model-auth/operations'), async ({ request }) => { starts.push(await request.json()); return HttpResponse.json({ operation: failed, account }, { status: 202 }) }))
    renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} provider={codexProvider} account={account} /></DraftNavigationGuard> }], '/')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Log in again' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Log in again' }))
    expect(await screen.findByText('Login failed')).toBeInTheDocument()
    expect(screen.getByText('The existing account is unchanged.')).toBeInTheDocument()
    expect(starts).toEqual([{ provider: 'openai-codex', accountId: account.id, expectedRevision: 7 }])
  })
})

// "Printed sheet" redesign, step 3: in project settings the accounts sit in the numbered section "Project accounts".
describe('Embedded in the numbered section of project settings', () => {
  it('model accounts and CLI accounts are two blocks in the section: one heading size smaller (h3), not cards, with a dashed line between them', async () => {
    server.use(directory, http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts: [] })))
    renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectCredentialSettings projectId={defaultProject.id} embedded /></DraftNavigationGuard> }], '/')
    const model = await screen.findByRole('region', { name: 'Model accounts' })
    const cli = screen.getByRole('region', { name: 'CLI accounts' })
    expect(within(model).getByRole('heading', { level: 3, name: 'Model accounts' })).toBeInTheDocument()
    for (const block of [model, cli]) expect(block).not.toHaveClass('shadow-print-rest', 'bg-paper-raised')
    expect(cli).toHaveClass('not-first:border-t-[1.5px]', 'not-first:border-dashed')
  })

  it('when unavailable, the notice goes straight into the section without its own heading', async () => {
    server.use(http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json(errorBody('not_found', 'not found'), { status: 404 })))
    renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectCredentialSettings projectId={defaultProject.id} embedded /></DraftNavigationGuard> }], '/')
    await screen.findByRole('button', { name: 'Add account' })
    expect(screen.queryByRole('heading', { name: 'Project accounts' })).toBeNull()
    expect(screen.queryByRole('region')).toBeNull()
  })
})
