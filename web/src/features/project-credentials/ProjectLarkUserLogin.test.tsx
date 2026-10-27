import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { focusManager } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import type { Schemas } from '@/api/client'
import { acceptCLIAuthState, cliAuthKeys } from '@/api/cli-auth'
import { credentialKeys } from '@/api/project-credentials'
import { apiUrl, defaultProject } from '@/test/api'
import { larkApplicationAccount, larkOperation, larkOperationId, larkUserAccount } from '@/test/lark-login'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { larkText as copy } from '@/i18n/lark'
import { larkLoginLocatorKey } from '@/lib/useOriginalActionLocator'
import { ProjectLarkUserLogin } from './ProjectLarkUserLogin'

const source = larkApplicationAccount(), key = larkLoginLocatorKey(defaultProject.id)
function defaults() {
  server.use(http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts: [source] })), http.get(apiUrl('/projects/:projectId/cli-auth/operations'), ({ request }) => { expect(new URL(request.url).searchParams.get('cli')).toBe('lark-cli'); return HttpResponse.json({ operations: [] }) }))
}
function renderLogin(id?: string, account?: Schemas['ProjectCredentialAccount']) {
  return renderRoutes([{ path: '/', element: <ProjectLarkUserLogin projectId={defaultProject.id} account={account} /> }], id ? `/?${larkLoginLocatorKey(defaultProject.id, account?.id)}=${id}` : '/')
}
async function selectSource() {
  await waitFor(() => expect(screen.getByRole('combobox')).toBeEnabled())
  await userEvent.selectOptions(screen.getByRole('combobox'), source.id)
  await waitFor(() => expect(screen.getByRole('button', { name: copy.start })).toBeEnabled())
}
afterEach(() => { focusManager.setFocused(undefined); vi.restoreAllMocks() })
describe('Lark generated-contract HTTP fixtures, not App/provider', () => {
  it('commits scoped route before one strict source POST, uses native safe link with optional code absent', async () => {
    defaults(); const writes: Schemas['CLIAuthStart'][] = [], open = vi.spyOn(window, 'open')
    const env = renderLogin()
    server.use(http.post<{ projectId: string }, Schemas['CLIAuthStart']>(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => {
      const body = await request.json(); writes.push(body)
      expect(new URLSearchParams(env.router.state.location.search).get(key)).toBe(body.operationId)
      return HttpResponse.json({ operation: larkOperation('awaiting_authorization', body.operationId), account: null }, { status: 202 })
    }), http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => HttpResponse.json({ operation: larkOperation('awaiting_authorization', String(params.operationId)), account: null })))
    await selectSource(); await userEvent.dblClick(screen.getByRole('button', { name: copy.start }))
    const link = await screen.findByRole('link', { name: copy.open })
    expect(writes).toHaveLength(1)
    expect(Object.keys(writes[0]).sort()).toEqual(['operationId', 'cli', 'authType', 'sourceAccountId', 'expectedSourceRevision'].sort())
    expect(link).toHaveAttribute('href', larkOperation().authorizationUrl)
    expect(link).toHaveAttribute('target', '_blank'); expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(screen.queryByText(copy.code)).not.toBeInTheDocument(); expect(open).not.toHaveBeenCalled()
    expect(sessionStorage.length).toBe(0); expect(localStorage.length).toBe(0)
    expect(env.router.state.location.search).not.toMatch(/flow_id|user_code|secret/)
  })
  it('unknown POST/GET404 and reload retain original ID and never issue a second POST', async () => {
    defaults(); let writes = 0, found = false
    server.use(http.post(apiUrl('/projects/:projectId/cli-auth/operations'), () => { writes++; return HttpResponse.json({ error: { code: 'internal_error', message: 'synthetic unknown' } }, { status: 503 }) }), http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => found ? HttpResponse.json({ operation: larkOperation('failed', String(params.operationId)), account: null }) : HttpResponse.json({ error: { code: 'not_found', message: 'operation missing' } }, { status: 404 })))
    const first = renderLogin(); await selectSource(); await userEvent.click(screen.getByRole('button', { name: copy.start }))
    await screen.findByText(copy.notFound)
    const id = new URLSearchParams(first.router.state.location.search).get(key)!
    first.unmount(); const second = renderLogin(id)
    await screen.findByText(copy.notFound); expect(writes).toBe(1)
    found = true; await userEvent.click(screen.getByRole('button', { name: copy.recover }))
    await screen.findByText(copy.failed); expect(writes).toBe(1)
    expect(new URLSearchParams(second.router.state.location.search).get(key)).toBe(id)
  })
  it('active operation discovery only adopts original ID and reads, never starts an operation', async () => {
    defaults(); let writes = 0
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [larkOperation()] })), http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation: larkOperation(), account: null })), http.post(apiUrl('/projects/:projectId/cli-auth/operations'), () => { writes++; return HttpResponse.json({}) }))
    const env = renderLogin(); await screen.findByRole('link', { name: copy.open })
    expect(new URLSearchParams(env.router.state.location.search).get(key)).toBe(larkOperationId)
    expect(writes).toBe(0)
  })
  it('background polling obtains delayed onAuth URL while unfocused, no preopened blank', async () => {
    defaults(); let ready = false, reads = 0
    const open = vi.spyOn(window, 'open'); focusManager.setFocused(false)
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => { reads++; return HttpResponse.json({ operation: larkOperation(ready ? 'awaiting_authorization' : 'pending'), account: null }) }))
    renderLogin(larkOperationId); await screen.findByText(copy.pending)
    ready = true
    await waitFor(() => expect(screen.getByRole('link', { name: copy.open })).toBeInTheDocument(), { timeout: 3500 })
    expect(reads).toBeGreaterThanOrEqual(2); expect(open).not.toHaveBeenCalled()
  })
  it('terminal caches absorb late active URL/code for same ID, independently of a new operation', async () => {
    defaults(); server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation: larkOperation('cancelled'), account: null })))
    const env = renderLogin(larkOperationId); await screen.findByText(copy.cancelled)
    await act(async () => { acceptCLIAuthState(env.client, defaultProject.id, 'lark-cli', larkOperationId, { operation: larkOperation(), account: null }) })
    expect(screen.queryByRole('link', { name: copy.open })).not.toBeInTheDocument()
    expect(env.client.getQueryData<Schemas['CLIAuthState']>(cliAuthKeys.operation(defaultProject.id, larkOperationId, 'lark-cli'))?.operation.status).toBe('cancelled')
  })
  it('source revision update retains original selection and forbids implicit selection of its replacement', async () => {
    defaults(); const env = renderLogin(); await selectSource()
    await act(async () => { env.client.setQueryData(credentialKeys.project(defaultProject.id), { projectId: defaultProject.id, credentialsRevision: 2, accounts: [{ ...source, revision: 2 }] }) })
    await screen.findByText(copy.sourceChanged)
    expect(screen.getByRole('button', { name: copy.start })).toBeDisabled()
  })
  it.each([['unsafe URL', { ...larkOperation(), authorizationUrl: 'https://evil.test/login' }], ['wrong target', { ...larkOperation(), targetAccountId: '10000000-0000-7000-8000-000000000099', targetRevision: 1 }]] as const)('rejects %s instead of opening or seeding a different target', async (_label, operation) => {
    defaults(); server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation, account: null })))
    renderLogin(larkOperationId); await screen.findByText(copy.unsafeResponse)
    expect(screen.queryByRole('link', { name: copy.open })).not.toBeInTheDocument()
  })
  it('old backend rejects cli field: disabled unsupported, no bare repeated error or fallback', async () => {
    defaults(); server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ error: { code: 'validation_failed', message: 'unsupported cli', fields: [{ field: 'cli', message: 'unsupported' }] } }, { status: 422 })))
    renderLogin(); await screen.findByText(copy.unsupported)
    expect(screen.getByRole('button', { name: copy.start })).toBeDisabled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
  it('known user remains independent after source deletion; relogin policy unavailable does not create a POST', async () => {
    defaults(); server.use(http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 2, accounts: [larkUserAccount()] })))
    renderLogin(undefined, larkUserAccount()); await screen.findByText(copy.reloginUnavailable)
    expect(screen.getByRole('button', { name: copy.relogin })).toBeDisabled()
    expect(screen.getByText('核实的飞书用户')).toBeInTheDocument()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  })
  it('success reads real HTTP metadata, separates remark/identity and preserves source bot; no Agent selection PUT', async () => {
    defaults(); let accepted = false, writes = 0, selectionWrites = 0
    server.use(http.post<{ projectId: string }, Schemas['CLIAuthStart']>(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => {
      writes++; accepted = true
      const body = await request.json()
      return HttpResponse.json({ operation: larkOperation('succeeded', body.operationId), account: larkUserAccount() }, { status: 202 })
    }), http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: accepted ? 2 : 1, accounts: accepted ? [source, larkUserAccount()] : [source] })), http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => HttpResponse.json({ operation: larkOperation('succeeded', String(params.operationId)), account: larkUserAccount() })), http.put(apiUrl('/agents/:agentId/credential-selections'), () => { selectionWrites++; return HttpResponse.json({}) }))
    const env = renderLogin(); await selectSource(); await userEvent.click(screen.getByRole('button', { name: copy.start }))
    await screen.findByText(copy.succeeded)
    expect(screen.getByText('人的备注')).toBeInTheDocument()
    expect(screen.getByText('核实的飞书用户')).toBeInTheDocument()
    expect(screen.getByText(copy.unknownGrants)).toBeInTheDocument()
    expect(writes).toBe(1); expect(selectionWrites).toBe(0)
    const list = env.client.getQueryData<Schemas['ProjectCredentialAccountList']>(credentialKeys.project(defaultProject.id))
    await waitFor(() => expect(env.client.getQueryData<Schemas['ProjectCredentialAccountList']>(credentialKeys.project(defaultProject.id))?.accounts).toHaveLength(2))
    expect(list?.accounts.find(a => a.id === source.id)?.authMethod).toBeNull()
  })
  it('cancel only original operation, terminal removes URL and late awaiting cannot resurrect it', async () => {
    defaults(); let cancelled = false, cancels = 0
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation: larkOperation(cancelled ? 'cancelled' : 'awaiting_authorization'), account: null })), http.post(apiUrl('/projects/:projectId/cli-auth/operations/:operationId/cancel'), ({ params }) => { expect(params.operationId).toBe(larkOperationId); cancels++; cancelled = true; return HttpResponse.json({ operation: larkOperation('cancelled'), account: null }) }))
    const env = renderLogin(larkOperationId); await screen.findByRole('link', { name: copy.open })
    await userEvent.click(screen.getByRole('button', { name: copy.cancel })); await screen.findByText(copy.cancelled)
    expect(cancels).toBe(1)
    await act(async () => { acceptCLIAuthState(env.client, defaultProject.id, 'lark-cli', larkOperationId, { operation: larkOperation(), account: null }) })
    expect(screen.queryByRole('link', { name: copy.open })).not.toBeInTheDocument()
    expect(screen.queryByText(copy.code)).not.toBeInTheDocument()
  })
  it('forbidden operation is a real error, not unsupported or deleted account', async () => {
    defaults(); server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ error: { code: 'forbidden', message: 'synthetic forbidden' } }, { status: 403 })))
    renderLogin(larkOperationId); await screen.findByRole('alert')
    expect(screen.queryByText(copy.unsupported)).not.toBeInTheDocument()
    expect(screen.queryByText(copy.notFound)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.start })).toBeDisabled()
  })
})
