import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { focusManager } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import type { Schemas } from '@/api/client'
import { acceptCLIAuthState, cliAuthKeys } from '@/api/cli-auth'
import { apiUrl, defaultProject } from '@/test/api'
import { githubAccount, githubOperation, githubOperationId as id } from '@/test/github-login'
import { projectAccount } from '@/test/project-credentials'
import { renderRoutes, createTestQueryClient } from '@/test/render'
import { server } from '@/test/server'
import { GitHubLogin } from './GitHubLogin'
import { githubLocatorKey } from './github-authorization'
import { t } from '@/i18n'

const copy = t.credentials.github

function renderLogin(account?: Schemas['ProjectCredentialAccount'], operationId?: string) {
  const key = githubLocatorKey(defaultProject.id, account?.id)
  return renderRoutes([{ path: '/', element: <GitHubLogin projectId={defaultProject.id} account={account} /> }], operationId ? `/?${key}=${operationId}` : '/')
}
function listEmpty() { server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), ({ request }) => { expect(new URL(request.url).searchParams.get('cli')).toBe('gh'); return HttpResponse.json({ operations: [] }) })) }
afterEach(() => { focusManager.setFocused(undefined); vi.restoreAllMocks() })

describe('GitHub project login, formal HTTP fixture', () => {
  it('the route commits the original ID before a single POST, without name and without preopening a blank window; link and copy happen only on user action', async () => {
    const user = userEvent.setup(), open = vi.spyOn(window, 'open'), clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    listEmpty(); const writes: Schemas['CLIAuthStart'][] = []
    const env = renderLogin()
    server.use(http.post<{ projectId: string }, Schemas['CLIAuthStart']>(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => {
      const body: Schemas['CLIAuthStart'] = await request.json(); writes.push(body)
      expect(new URLSearchParams(env.router.state.location.search).get(githubLocatorKey(defaultProject.id))).toBe(body.operationId)
      return HttpResponse.json({ operation: githubOperation('awaiting_authorization', body.operationId), account: null }, { status: 202 })
    }), http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => HttpResponse.json({ operation: githubOperation('awaiting_authorization', String(params.operationId)), account: null })))
    await waitFor(() => expect(screen.getByRole('button', { name: copy.login })).toBeEnabled())
    await user.dblClick(screen.getByRole('button', { name: copy.login }))
    const link = await screen.findByRole('link', { name: copy.open })
    expect(writes).toHaveLength(1); expect(Object.keys(writes[0]).sort()).toEqual(['authType', 'cli', 'operationId'])
    expect(link).toHaveAttribute('href', 'https://github.com/login/device'); expect(link).toHaveAttribute('rel', 'noopener noreferrer'); expect(link).toHaveAttribute('target', '_blank')
    expect(open).not.toHaveBeenCalled(); expect(clipboard).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: copy.copy })); expect(clipboard).toHaveBeenCalledExactlyOnceWith('ABCD-1234')
    expect(env.router.state.location.search).not.toContain('ABCD'); expect(localStorage.length).toBe(0); expect(sessionStorage.length).toBe(0)
  })

  it('after an unknown POST it only GETs the original ID; a 404 after refresh does not POST again or change the ID, and the original terminal state is read later', async () => {
    const user = userEvent.setup(); listEmpty(); let writes = 0, found = false
    server.use(http.post(apiUrl('/projects/:projectId/cli-auth/operations'), () => { writes++; return HttpResponse.json({ error: { code: 'internal_error', message: 'fixture unknown' } }, { status: 503 }) }),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => found ? HttpResponse.json({ operation: githubOperation('failed', String(params.operationId)), account: null }) : HttpResponse.json({ error: { code: 'not_found', message: 'operation not found' } }, { status: 404 })))
    const first = renderLogin(); await waitFor(() => expect(screen.getByRole('button', { name: copy.login })).toBeEnabled()); await user.click(screen.getByRole('button', { name: copy.login }))
    await screen.findByText(copy.notFound); const saved = new URLSearchParams(first.router.state.location.search).get(githubLocatorKey(defaultProject.id))!
    first.unmount(); const second = renderLogin(undefined, saved)
    await screen.findByText(copy.notFound); expect(writes).toBe(1); expect(screen.getByRole('button', { name: copy.login })).toBeDisabled()
    found = true; await user.click(screen.getByRole('button', { name: copy.recover })); await screen.findByText(copy.failed)
    expect(new URLSearchParams(second.router.state.location.search).get(githubLocatorKey(defaultProject.id))).toBe(saved); expect(writes).toBe(1)
  })

  it('the cancelled terminal state absorbs late detail/list replies; no URL or user code is left in the three places, and a new ID is independent', async () => {
    const user = userEvent.setup(); let current = githubOperation(), stale = current; const cancelBodies: string[] = []
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [stale] })),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation: current, account: null })),
      http.post(apiUrl('/projects/:projectId/cli-auth/operations/:operationId/cancel'), async ({ request }) => { cancelBodies.push(await request.text()); current = githubOperation('cancelled'); return HttpResponse.json({ operation: current, account: null }) }))
    const env = renderLogin(undefined, id); await screen.findByRole('link', { name: copy.open }); await user.click(screen.getByRole('button', { name: copy.cancel })); await screen.findByText(copy.cancelled)
    current = stale
    await act(() => env.client.invalidateQueries({ queryKey: cliAuthKeys.operation(defaultProject.id, id, 'gh') }))
    await act(() => env.client.invalidateQueries({ queryKey: cliAuthKeys.project(defaultProject.id, 'gh'), exact: true }))
    expect(screen.queryByRole('link', { name: copy.open })).toBeNull(); expect(screen.queryByText('ABCD-1234')).toBeNull(); expect(cancelBodies).toEqual([''])
    const cached = JSON.stringify(env.client.getQueryCache().getAll().map(q => q.state.data)); expect(cached).not.toContain('ABCD-1234'); expect(cached).not.toContain('https://github.com/login/device')
    expect(env.client.getQueryData(cliAuthKeys.project(defaultProject.id, 'gh'))).toEqual({ operations: [] })
    stale = githubOperation('pending', '31000000-0000-7000-8000-000000000002')
    expect(acceptCLIAuthState(env.client, defaultProject.id, 'gh', stale.id, { operation: stale, account: null }).operation.status).toBe('pending')
  })

  it.each(['failed', 'cancelled', 'expired'] as const)('PAT account relogin %s keeps the old note and revision, does not guess the old identity, and submits no name', async terminal => {
    const user = userEvent.setup(), account = projectAccount({ purpose: 'cli', provider: null, cli: 'gh', authMethod: null, githubIdentity: null }), writes: Schemas['CLIAuthStart'][] = []; listEmpty()
    server.use(http.post<{ projectId: string }, Schemas['CLIAuthStart']>(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => { const body: Schemas['CLIAuthStart'] = await request.json(); writes.push(body); return HttpResponse.json({ operation: githubOperation(terminal, body.operationId, { targetAccountId: account.id, targetRevision: account.revision }), account }, { status: 202 }) }),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => HttpResponse.json({ operation: githubOperation(terminal, String(params.operationId), { targetAccountId: account.id, targetRevision: account.revision }), account })))
    renderLogin(account); await waitFor(() => expect(screen.getByRole('button', { name: copy.relogin })).toBeEnabled()); await user.click(screen.getByRole('button', { name: copy.relogin }))
    await screen.findByText(copy.retained); expect(writes).toEqual([{ operationId: expect.any(String), cli: 'gh', authType: 'github_user', accountId: account.id, expectedRevision: account.revision }]); expect(account.revision).toBe(1); expect(account.githubIdentity).toBeNull()
  })

  it('a backend pending state with a delayed user code is still read; at the deadline only the entry is hidden and nothing is written', async () => {
    const user = userEvent.setup(); let op = githubOperation('pending'), posts = 0, reads = 0
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [op] })), http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => { reads++; return HttpResponse.json({ operation: op, account: null }) }), http.post(apiUrl('/projects/:projectId/cli-auth/operations'), () => { posts++; return HttpResponse.error() }))
    const env = renderLogin(undefined, id); await screen.findByText(copy.opening); act(() => focusManager.setFocused(false)); op = githubOperation(); await screen.findByRole('link', { name: copy.open }, { timeout: 3000 }); expect(reads).toBeGreaterThan(1)
    op = { ...githubOperation(), expiresAt: new Date(Date.now() - 1000).toISOString() }; await act(() => env.client.invalidateQueries({ queryKey: cliAuthKeys.operation(defaultProject.id, id, 'gh') })); await screen.findByText(copy.deadlineReached)
    expect(screen.queryByRole('link', { name: copy.open })).toBeNull(); await user.click(screen.getByRole('button', { name: copy.recover })); expect(posts).toBe(0)
  })

  it('project and CLI caches are independent; a reply with the wrong scope or target shows no user code', async () => {
    listEmpty(); const account = githubAccount()
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation: githubOperation(), account: null })))
    renderLogin(account, id); await screen.findByText(copy.mismatch); expect(screen.queryByText('ABCD-1234')).toBeNull()
    const client = createTestQueryClient(), op = githubOperation('cancelled')
    acceptCLIAuthState(client, defaultProject.id, 'gh', id, { operation: op, account: null })
    expect(client.getQueryData(cliAuthKeys.operation(defaultProject.id, id, 'gcloud'))).toBeUndefined(); expect(client.getQueryData(cliAuthKeys.operation('other-project', id, 'gh'))).toBeUndefined()
    expect(() => acceptCLIAuthState(client, 'other-project', 'gh', id, { operation: op, account: null })).toThrow(copy.mismatch)
  })
})

import { within } from '@testing-library/react'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { credentialKeys } from '@/api/project-credentials'
import { ProjectAccounts } from './ProjectAccounts'

function accountsHandlers(accounts: () => Schemas['ProjectCredentialAccount'][]) {
  server.use(http.get(apiUrl('/model-providers'), () => HttpResponse.json({ providers: [] })),
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [{ name: 'gh', version: '2.100.0', injection: 'env', fields: [{ name: 'token', label: 'Personal access token', type: 'secret', required: true }], warnings: [], install: { status: 'installed' } }] })),
    http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 2, accounts: accounts() })),
    http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 2, accountId: null, agents: [] })),
    http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [] })))
}
function renderAccounts() { return renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="cli" /></DraftNavigationGuard> }], '/') }

describe('GitHub accounts page and the existing PAT form', () => {
  it('the default login has no name; PAT is the alternative, its secret is masked, cleared on success and kept out of caches', async () => {
    const user = userEvent.setup(), writes: unknown[] = []; let accounts: Schemas['ProjectCredentialAccount'][] = []
    accountsHandlers(() => accounts)
    server.use(http.post(apiUrl('/projects/:projectId/credential-accounts'), async ({ request }) => { writes.push(await request.json()); accounts = [projectAccount({ purpose: 'cli', provider: null, cli: 'gh', authMethod: null })]; return HttpResponse.json(accounts[0], { status: 201 }) }))
    const env = renderAccounts(); await screen.findByRole('option', { name: 'gh' }); await user.selectOptions(screen.getByLabelText('Add CLI account · tool'), 'gh')
    expect(await screen.findByRole('button', { name: copy.login })).toBeEnabled(); expect(screen.queryByLabelText('Account name')).toBeNull()
    expect(screen.getByRole('link', { name: copy.patHelp })).toHaveAttribute('rel', 'noopener noreferrer')
    await user.click(screen.getByRole('button', { name: copy.pat })); await user.type(screen.getByLabelText('Account name'), 'PAT note'); const secret = screen.getByLabelText('Personal access token *'); expect(secret).toHaveAttribute('type', 'password'); await user.type(secret, 'synthetic-pat-only'); await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByLabelText('Personal access token *')).toBeNull()); expect(writes).toEqual([{ purpose: 'cli', cli: 'gh', name: 'PAT note', credential: { type: 'cli', fields: { token: 'synthetic-pat-only' } } }])
    expect(JSON.stringify(env.client.getQueryCache().getAll().map(q => q.state.data))).not.toContain('synthetic-pat-only')
    expect(await screen.findByText(copy.identityMissing)).toBeInTheDocument()
  })

  it.each([null, []] as const)('a new version may switch the GitHub identity and keeps the note; actual scopes=%j keep unknown and empty apart', async scopes => {
    const user = userEvent.setup(), old = githubAccount(['repo']); old.githubIdentity = { ...old.githubIdentity!, userId: '11', login: 'old-user' }
    let accounts: Schemas['ProjectCredentialAccount'][] = [old]; const next = githubAccount(scopes === null ? null : [])
    accountsHandlers(() => accounts)
    server.use(http.post<{ projectId: string }, Schemas['CLIAuthStart']>(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => { const body = await request.json(); accounts = [next]; return HttpResponse.json({ operation: githubOperation('succeeded', body.operationId, { targetAccountId: old.id, targetRevision: old.revision }), account: next }, { status: 202 }) }),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => HttpResponse.json({ operation: githubOperation('succeeded', String(params.operationId), { targetAccountId: old.id, targetRevision: old.revision }), account: next })))
    renderAccounts(); await user.click(await screen.findByRole('button', { name: copy.relogin })); await screen.findByText('@verified-new-user')
    expect(screen.getByRole('heading', { name: '保留的备注' })).toBeInTheDocument(); expect(screen.queryByText('@old-user')).toBeNull(); expect(screen.getByText('9007199254740993')).toBeInTheDocument()
    expect(screen.getByText(scopes === null ? copy.unknownScopes : copy.emptyScopes)).toBeInTheDocument(); expect(screen.queryByRole('link', { name: copy.open })).toBeNull()
  })

  it('after 412 the note draft is kept; after PAT replacement no historical OAuth identity is borrowed; several accounts keep their own targets', async () => {
    const user = userEvent.setup(), account = githubAccount(); let accounts: Schemas['ProjectCredentialAccount'][] = [account, { ...githubAccount(), id: '10000000-0000-7000-8000-000000000002', name: 'Another note' }], puts = 0
    accountsHandlers(() => accounts)
    server.use(http.patch(apiUrl('/projects/:projectId/credential-accounts/:accountId'), () => { puts++; return HttpResponse.json({ error: { code: 'precondition_failed', message: 'fixture conflict' } }, { status: 412 }) }))
    const env = renderAccounts(); const row = (await screen.findByRole('heading', { name: '保留的备注' })).closest('li')!
    expect(await screen.findAllByRole('button', { name: copy.relogin })).toHaveLength(2)
    await user.click(within(row).getByRole('button', { name: 'Rename' })); await user.clear(screen.getByLabelText('Account name')); await user.type(screen.getByLabelText('Account name'), 'Local note draft'); await user.click(screen.getByRole('button', { name: 'Save' })); await waitFor(() => expect(puts).toBe(1)); expect(screen.getByLabelText('Account name')).toHaveValue('Local note draft')
    accounts = [projectAccount({ purpose: 'cli', provider: null, cli: 'gh', authMethod: null, githubIdentity: null, revision: 3 })]; await act(() => env.client.invalidateQueries({ queryKey: credentialKeys.project(defaultProject.id) }))
    expect(await screen.findByText(copy.identityMissing)).toBeInTheDocument(); expect(screen.queryAllByText('@verified-new-user')).toHaveLength(0); expect(screen.getByLabelText('Account name')).toHaveValue('Local note draft')
  })
})

describe('GitHub entry safety and reply ownership', () => {
  it.each([
    { authorizationUrl: 'https://github.com/login/device?token=synthetic', userCode: 'ABCD-1234' },
    { authorizationUrl: 'https://github.example.invalid/login/device', userCode: 'ABCD-1234' },
    { authorizationUrl: 'https://github.com/login/device', userCode: 'malformed' },
  ])('does not offer a non-fixed URL or an invalid user code %#', async invalid => {
    listEmpty(); server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation: { ...githubOperation(), ...invalid }, account: null })))
    renderLogin(undefined, id); await screen.findByText(copy.rejected); expect(screen.queryByRole('link', { name: copy.open })).toBeNull(); expect(screen.queryByRole('button', { name: copy.copy })).toBeNull()
  })
  it('a late start success or active state cannot rewrite the terminal state already received for the same operation; other projects are independent', () => {
    const client = createTestQueryClient(), cancelled = githubOperation('cancelled')
    acceptCLIAuthState(client, defaultProject.id, 'gh', id, { operation: cancelled, account: null })
    const late = acceptCLIAuthState(client, defaultProject.id, 'gh', id, { operation: githubOperation(), account: null })
    expect(late.operation).toEqual(cancelled); expect(late.operation.authorizationUrl).toBeNull(); expect(acceptCLIAuthState(client, defaultProject.id, 'gh', id, { operation: githubOperation('succeeded'), account: githubAccount() })).toEqual({ operation: cancelled, account: null })
    const other = { ...githubOperation(), projectId: '00000000-0000-7000-8000-000000000099' }
    expect(acceptCLIAuthState(client, other.projectId, 'gh', id, { operation: other, account: null }).operation.status).toBe('awaiting_authorization')
  })
  it('a real account 404 differs from a missing-endpoint 404, neither POSTs automatically; 401 is not shown as unsupported', async () => {
    listEmpty(); server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ error: { code: 'unauthorized', message: 'fixture denied' } }, { status: 401 })))
    renderLogin(undefined, id); await screen.findByRole('alert'); expect(screen.queryByText(copy.unsupported)).toBeNull(); expect(screen.queryByText(copy.notFound)).toBeNull(); expect(screen.getByRole('button', { name: copy.login })).toBeDisabled()
  })
})

it('refreshing the accounts page with the original ID restores the gh block directly and only GETs that ID, including its terminal state', async () => {
  accountsHandlers(() => []); let posts = 0, reads = 0
  server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => { reads++; expect(params.operationId).toBe(id); return HttpResponse.json({ operation: githubOperation('expired'), account: null }) }), http.post(apiUrl('/projects/:projectId/cli-auth/operations'), () => { posts++; return HttpResponse.error() }))
  renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="cli" /></DraftNavigationGuard> }], `/?${githubLocatorKey(defaultProject.id)}=${id}`)
  await screen.findByText(copy.expired); expect(screen.getByLabelText('Add CLI account · tool')).toHaveValue('gh'); expect(reads).toBe(1); expect(posts).toBe(0)
})
