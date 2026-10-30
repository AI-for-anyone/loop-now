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

describe('GitHub项目登录正式HTTP fixture', () => {
  it('路由先提交原ID再单POST，不带name、不预开空白窗，链接/复制仅由用户触发', async () => {
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

  it('unknown POST后仅GET原ID，刷新404不重POST/换ID，随后读取原终态', async () => {
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

  it('取消终态吸收迟到detail/list，三处不留URL/用户码，新ID独立', async () => {
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

  it.each(['failed', 'cancelled', 'expired'] as const)('PAT重登%s保旧备注/版本，不猜旧identity，不提交name', async terminal => {
    const user = userEvent.setup(), account = projectAccount({ purpose: 'cli', provider: null, cli: 'gh', authMethod: null, githubIdentity: null }), writes: Schemas['CLIAuthStart'][] = []; listEmpty()
    server.use(http.post<{ projectId: string }, Schemas['CLIAuthStart']>(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => { const body: Schemas['CLIAuthStart'] = await request.json(); writes.push(body); return HttpResponse.json({ operation: githubOperation(terminal, body.operationId, { targetAccountId: account.id, targetRevision: account.revision }), account }, { status: 202 }) }),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => HttpResponse.json({ operation: githubOperation(terminal, String(params.operationId), { targetAccountId: account.id, targetRevision: account.revision }), account })))
    renderLogin(account); await waitFor(() => expect(screen.getByRole('button', { name: copy.relogin })).toBeEnabled()); await user.click(screen.getByRole('button', { name: copy.relogin }))
    await screen.findByText(copy.retained); expect(writes).toEqual([{ operationId: expect.any(String), cli: 'gh', authType: 'github_user', accountId: account.id, expectedRevision: account.revision }]); expect(account.revision).toBe(1); expect(account.githubIdentity).toBeNull()
  })

  it('后台pending延迟用户码仍读取，期限到只隐藏入口不写请求', async () => {
    const user = userEvent.setup(); let op = githubOperation('pending'), posts = 0, reads = 0
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [op] })), http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => { reads++; return HttpResponse.json({ operation: op, account: null }) }), http.post(apiUrl('/projects/:projectId/cli-auth/operations'), () => { posts++; return HttpResponse.error() }))
    const env = renderLogin(undefined, id); await screen.findByText(copy.opening); act(() => focusManager.setFocused(false)); op = githubOperation(); await screen.findByRole('link', { name: copy.open }, { timeout: 3000 }); expect(reads).toBeGreaterThan(1)
    op = { ...githubOperation(), expiresAt: new Date(Date.now() - 1000).toISOString() }; await act(() => env.client.invalidateQueries({ queryKey: cliAuthKeys.operation(defaultProject.id, id, 'gh') })); await screen.findByText(copy.deadlineReached)
    expect(screen.queryByRole('link', { name: copy.open })).toBeNull(); await user.click(screen.getByRole('button', { name: copy.recover })); expect(posts).toBe(0)
  })

  it('项目/CLI缓存独立，响应错scope或错目标不显示用户码', async () => {
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
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [{ name: 'gh', version: '2.100.0', injection: 'env', fields: [{ name: 'token', label: '个人访问令牌', type: 'secret', required: true }], warnings: [], install: { status: 'installed' } }] })),
    http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 2, accounts: accounts() })),
    http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 2, accountId: null, agents: [] })),
    http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [] })))
}
function renderAccounts() { return renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="cli" /></DraftNavigationGuard> }], '/') }

describe('GitHub账号页面与既有PAT表单', () => {
  it('默认登录无名称，PAT是备选且秘密遮蔽/成功清空，不进缓存', async () => {
    const user = userEvent.setup(), writes: unknown[] = []; let accounts: Schemas['ProjectCredentialAccount'][] = []
    accountsHandlers(() => accounts)
    server.use(http.post(apiUrl('/projects/:projectId/credential-accounts'), async ({ request }) => { writes.push(await request.json()); accounts = [projectAccount({ purpose: 'cli', provider: null, cli: 'gh', authMethod: null })]; return HttpResponse.json(accounts[0], { status: 201 }) }))
    const env = renderAccounts(); await screen.findByRole('option', { name: 'gh' }); await user.selectOptions(screen.getByLabelText('添加 CLI 账号 · 工具'), 'gh')
    expect(await screen.findByRole('button', { name: copy.login })).toBeEnabled(); expect(screen.queryByLabelText('账号名称')).toBeNull()
    expect(screen.getByRole('link', { name: copy.patHelp })).toHaveAttribute('rel', 'noopener noreferrer')
    await user.click(screen.getByRole('button', { name: copy.pat })); await user.type(screen.getByLabelText('账号名称'), 'PAT备注'); const secret = screen.getByLabelText('个人访问令牌 *'); expect(secret).toHaveAttribute('type', 'password'); await user.type(secret, 'synthetic-pat-only'); await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByLabelText('个人访问令牌 *')).toBeNull()); expect(writes).toEqual([{ purpose: 'cli', cli: 'gh', name: 'PAT备注', credential: { type: 'cli', fields: { token: 'synthetic-pat-only' } } }])
    expect(JSON.stringify(env.client.getQueryCache().getAll().map(q => q.state.data))).not.toContain('synthetic-pat-only')
    expect(await screen.findByText(copy.identityMissing)).toBeInTheDocument()
  })

  it.each([null, []] as const)('新版本可换GitHub身份且保留备注，实际scope=%j不混淆未知/空', async scopes => {
    const user = userEvent.setup(), old = githubAccount(['repo']); old.githubIdentity = { ...old.githubIdentity!, userId: '11', login: 'old-user' }
    let accounts: Schemas['ProjectCredentialAccount'][] = [old]; const next = githubAccount(scopes === null ? null : [])
    accountsHandlers(() => accounts)
    server.use(http.post<{ projectId: string }, Schemas['CLIAuthStart']>(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => { const body = await request.json(); accounts = [next]; return HttpResponse.json({ operation: githubOperation('succeeded', body.operationId, { targetAccountId: old.id, targetRevision: old.revision }), account: next }, { status: 202 }) }),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => HttpResponse.json({ operation: githubOperation('succeeded', String(params.operationId), { targetAccountId: old.id, targetRevision: old.revision }), account: next })))
    renderAccounts(); await user.click(await screen.findByRole('button', { name: copy.relogin })); await screen.findByText('@verified-new-user')
    expect(screen.getByRole('heading', { name: '保留的备注' })).toBeInTheDocument(); expect(screen.queryByText('@old-user')).toBeNull(); expect(screen.getByText('9007199254740993')).toBeInTheDocument()
    expect(screen.getByText(scopes === null ? copy.unknownScopes : copy.emptyScopes)).toBeInTheDocument(); expect(screen.queryByRole('link', { name: copy.open })).toBeNull()
  })

  it('412后保留备注草稿；PAT替换后不借历史OAuth身份；多账号仍各有原目标', async () => {
    const user = userEvent.setup(), account = githubAccount(); let accounts: Schemas['ProjectCredentialAccount'][] = [account, { ...githubAccount(), id: '10000000-0000-7000-8000-000000000002', name: '另一个备注' }], puts = 0
    accountsHandlers(() => accounts)
    server.use(http.patch(apiUrl('/projects/:projectId/credential-accounts/:accountId'), () => { puts++; return HttpResponse.json({ error: { code: 'precondition_failed', message: 'fixture conflict' } }, { status: 412 }) }))
    const env = renderAccounts(); const row = (await screen.findByRole('heading', { name: '保留的备注' })).closest('li')!
    expect(await screen.findAllByRole('button', { name: copy.relogin })).toHaveLength(2)
    await user.click(within(row).getByRole('button', { name: '改名' })); await user.clear(screen.getByLabelText('账号名称')); await user.type(screen.getByLabelText('账号名称'), '本地备注草稿'); await user.click(screen.getByRole('button', { name: '保存' })); await waitFor(() => expect(puts).toBe(1)); expect(screen.getByLabelText('账号名称')).toHaveValue('本地备注草稿')
    accounts = [projectAccount({ purpose: 'cli', provider: null, cli: 'gh', authMethod: null, githubIdentity: null, revision: 3 })]; await act(() => env.client.invalidateQueries({ queryKey: credentialKeys.project(defaultProject.id) }))
    expect(await screen.findByText(copy.identityMissing)).toBeInTheDocument(); expect(screen.queryAllByText('@verified-new-user')).toHaveLength(0); expect(screen.getByLabelText('账号名称')).toHaveValue('本地备注草稿')
  })
})

describe('GitHub入口安全与响应归属', () => {
  it.each([
    { authorizationUrl: 'https://github.com/login/device?token=synthetic', userCode: 'ABCD-1234' },
    { authorizationUrl: 'https://github.example.invalid/login/device', userCode: 'ABCD-1234' },
    { authorizationUrl: 'https://github.com/login/device', userCode: 'malformed' },
  ])('不开放非固定URL或非法用户码 %#', async invalid => {
    listEmpty(); server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation: { ...githubOperation(), ...invalid }, account: null })))
    renderLogin(undefined, id); await screen.findByText(copy.rejected); expect(screen.queryByRole('link', { name: copy.open })).toBeNull(); expect(screen.queryByRole('button', { name: copy.copy })).toBeNull()
  })
  it('迟到start成功/活动都不能改写先收到的同op终态，其他project独立', () => {
    const client = createTestQueryClient(), cancelled = githubOperation('cancelled')
    acceptCLIAuthState(client, defaultProject.id, 'gh', id, { operation: cancelled, account: null })
    const late = acceptCLIAuthState(client, defaultProject.id, 'gh', id, { operation: githubOperation(), account: null })
    expect(late.operation).toEqual(cancelled); expect(late.operation.authorizationUrl).toBeNull(); expect(acceptCLIAuthState(client, defaultProject.id, 'gh', id, { operation: githubOperation('succeeded'), account: githubAccount() })).toEqual({ operation: cancelled, account: null })
    const other = { ...githubOperation(), projectId: '00000000-0000-7000-8000-000000000099' }
    expect(acceptCLIAuthState(client, other.projectId, 'gh', id, { operation: other, account: null }).operation.status).toBe('awaiting_authorization')
  })
  it('真实账号404和接口缺失404不同，均不自动POST；401不伪装未支持', async () => {
    listEmpty(); server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ error: { code: 'unauthorized', message: 'fixture denied' } }, { status: 401 })))
    renderLogin(undefined, id); await screen.findByRole('alert'); expect(screen.queryByText(copy.unsupported)).toBeNull(); expect(screen.queryByText(copy.notFound)).toBeNull(); expect(screen.getByRole('button', { name: copy.login })).toBeDisabled()
  })
})

it('账号页刷新带原ID时直接恢复gh区块，仅GET该ID含终态', async () => {
  accountsHandlers(() => []); let posts = 0, reads = 0
  server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => { reads++; expect(params.operationId).toBe(id); return HttpResponse.json({ operation: githubOperation('expired'), account: null }) }), http.post(apiUrl('/projects/:projectId/cli-auth/operations'), () => { posts++; return HttpResponse.error() }))
  renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="cli" /></DraftNavigationGuard> }], `/?${githubLocatorKey(defaultProject.id)}=${id}`)
  await screen.findByText(copy.expired); expect(screen.getByLabelText('添加 CLI 账号 · 工具')).toHaveValue('gh'); expect(reads).toBe(1); expect(posts).toBe(0)
})
