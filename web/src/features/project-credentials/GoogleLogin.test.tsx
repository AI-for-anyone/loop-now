import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { focusManager } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import type { Schemas } from '@/api/client'
import { cliAuthKeys, readCLIAuth } from '@/api/cli-auth'
import { apiUrl, defaultProject } from '@/test/api'
import { projectAccount } from '@/test/project-credentials'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { GoogleLogin } from './GoogleLogin'
import { ProjectAccounts } from './ProjectAccounts'

const googleURL = 'https://accounts.google.com/o/oauth2/auth?client_id=fixture-client&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A8085%2F&scope=fixture-scope&state=fixture-state&code_challenge=fixture-challenge&code_challenge_method=S256&access_type=offline'
const id = '30000000-0000-7000-8000-000000000001'
type GoogleOperation = Extract<Schemas['CLIAuthOperation'], { cli: 'gcloud' }>
function operation(status: GoogleOperation['status'] = 'pending', extra: Partial<Omit<GoogleOperation, 'status' | 'authorizationUrl'>> = {}): GoogleOperation {
  const base = { id, projectId: defaultProject.id, cli: 'gcloud' as const, authType: 'google_user' as const, targetAccountId: null, targetRevision: null, name: '已恢复目标', accountId: null, startedAt: '2026-10-15T00:00:00Z', expiresAt: '2026-10-15T01:00:00Z', completedAt: null, errorCode: null, errorMessage: null, ...extra }
  return status === 'pending' || status === 'awaiting_authorization' ? { ...base, status, authorizationUrl: status === 'pending' ? null : googleURL } : { ...base, status, authorizationUrl: null }
}
function popup() {
  const child = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
  vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
  return child
}
function renderLogin(account?: Schemas['ProjectCredentialAccount']) {
  return renderRoutes([{ path: '/', element: <DraftNavigationGuard><GoogleLogin projectId={defaultProject.id} account={account} /></DraftNavigationGuard> }], '/')
}
afterEach(() => { focusManager.setFocused(undefined); vi.restoreAllMocks() })

describe('正式CLIAuth响应的纯null类型兼容', () => {
  const targets = [{ targetAccountId: null, targetRevision: null }, { targetAccountId: '10000000-0000-7000-8000-000000000001', targetRevision: 2 }]
  for (const target of targets) {
    const base = { id, projectId: defaultProject.id, cli: 'gh' as const, authType: 'github_user' as const, ...target, name: null, accountId: null, startedAt: '2026-10-18T00:00:00Z', expiresAt: '2026-10-18T00:05:00Z', completedAt: null, authorizationUrl: null, errorCode: null, errorMessage: null, userCode: null }
    const replies = [
      { ...base, status: 'pending' },
      { ...base, status: 'awaiting_authorization', authorizationUrl: 'https://github.com/login/device', userCode: 'ABCD-1234' },
      { ...base, status: 'succeeded', accountId: '10000000-0000-7000-8000-000000000001', completedAt: '2026-10-18T00:01:00Z' },
      ...(['failed', 'cancelled', 'expired'] as const).map(status => ({ ...base, status, completedAt: '2026-10-18T00:01:00Z', errorCode: 'oauth_failed' as const, errorMessage: '固定fixture错误' })),
    ] satisfies Schemas['CLIAuthOperation'][]
    it.each(replies)(`${target.targetAccountId ? '原账号' : '新建目标'} $status保留正式响应全部null和目标字段`, async reply => {
      server.use(http.get(apiUrl(`/projects/${defaultProject.id}/cli-auth/operations/${id}`), () => HttpResponse.json({ operation: reply, account: null })))
      const result = await readCLIAuth(defaultProject.id, id)
      expect(result).toEqual({ operation: reply, account: null })
      expect(Object.keys(result.operation).sort()).toEqual(Object.keys(reply).sort())
    })
  }
  it('旧Google终态不增加GitHub用户码键', async () => {
    const reply = operation('cancelled')
    server.use(http.get(apiUrl(`/projects/${defaultProject.id}/cli-auth/operations/${id}`), () => HttpResponse.json({ operation: reply, account: null })))
    const result = await readCLIAuth(defaultProject.id, id)
    expect(result.operation).toEqual(reply)
    expect(result.operation).not.toHaveProperty('userCode')
  })
  it('不把不成对的目标默默投影为新建目标', async () => {
    server.use(http.get(apiUrl(`/projects/${defaultProject.id}/cli-auth/operations/${id}`), () => HttpResponse.json({ operation: { id, projectId: defaultProject.id, cli: 'gh', authType: 'github_user', status: 'pending', targetAccountId: '10000000-0000-7000-8000-000000000001', targetRevision: null }, account: null })))
    await expect(readCLIAuth(defaultProject.id, id)).rejects.toThrow('Invalid CLI auth target pair')
  })
})

describe('项目Google CLI登录正式HTTP fixture', () => {
  it('同项目gcloud同时提供个人与SA，SA JSON只按正式字符串字段提交/成功清秘密', async () => {
    const user = userEvent.setup(), writes: unknown[] = []; let accounts: Schemas['ProjectCredentialAccount'][] = []
    const synthetic = '{"type":"service_account","private_key":"fixture-only"}'
    server.use(http.get(apiUrl('/model-providers'), () => HttpResponse.json({ piVersion: '1.1.0', catalogGeneratedAt: null, providers: [] })),
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [{ name: 'gcloud', version: '568.0.0', injection: 'files', fields: [{ name: 'serviceAccountJson', label: '服务账号 JSON', type: 'secret', required: true, multiline: true }, { name: 'defaultProject', label: '默认 Google 项目', type: 'string', required: false }], warnings: [], install: { status: 'not_installed' } }] })),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [] })),
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts })),
      http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accountId: null, agents: [] })),
      http.post(apiUrl('/projects/:projectId/credential-accounts'), async ({ request }) => { writes.push(await request.json()); accounts = [projectAccount({ name: 'Google SA', purpose: 'cli', provider: null, cli: 'gcloud', authMethod: 'service_account' })]; return HttpResponse.json(accounts[0], { status: 201 }) }))
    const env = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="cli" /></DraftNavigationGuard> }], '/')
    await screen.findByRole('option', { name: 'gcloud' })
    await user.selectOptions(await screen.findByLabelText('添加 CLI 账号 · 工具'), 'gcloud')
    expect(await screen.findByRole('button', { name: '登录 Google' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '录入服务账号 JSON' }))
    await user.type(screen.getByLabelText('账号名称'), 'Google SA')
    await user.click(screen.getByLabelText('服务账号 JSON *')); await user.paste(synthetic)
    await user.type(screen.getByLabelText('默认 Google 项目'), 'synthetic-project')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByLabelText('服务账号 JSON *')).toBeNull())
    expect(writes).toEqual([{ purpose: 'cli', cli: 'gcloud', name: 'Google SA', credential: { type: 'cli', fields: { serviceAccountJson: synthetic, defaultProject: 'synthetic-project' } } }])
    expect(JSON.stringify(env.client.getQueryCache().getAll().map(query => query.state.data))).not.toContain('fixture-only')
    expect(JSON.stringify(env.client.getMutationCache().getAll())).not.toContain('fixture-only')
  })

  it('首次pending且后台延迟URL仍继续读取并导航，终态清URL/停detail轮询', async () => {
    const user = userEvent.setup(), child = popup(), writes: Schemas['CLIAuthStart'][] = []
    let op: Schemas['CLIAuthOperation'] | null = null, reads = 0
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: op && (op.status === 'pending' || op.status === 'awaiting_authorization') ? [op] : [] })),
      http.post(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => { const body = await request.json() as Schemas['CLIAuthStart']; writes.push(body); op = operation('pending', { id: body.operationId }); return HttpResponse.json({ operation: op, account: null }, { status: 202 }) }),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => { reads++; return HttpResponse.json({ operation: op, account: null }) }))
    const env = renderLogin()
    await user.type(screen.getByLabelText('新 Google 账号名称'), '草稿新账号')
    await waitFor(() => expect(screen.getByRole('button', { name: '登录 Google' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '登录 Google' }))
    await waitFor(() => expect(reads).toBeGreaterThan(0))
    expect(writes).toHaveLength(1); expect(writes[0]).toMatchObject({ cli: 'gcloud', authType: 'google_user', name: '草稿新账号' })
    expect(writes[0].operationId).toMatch(/^[0-9a-f-]{36}$/)
    expect(child.location.replace).not.toHaveBeenCalled()
    act(() => focusManager.setFocused(false)); op = operation('awaiting_authorization', { id: writes[0].operationId })
    await waitFor(() => expect(child.location.replace).toHaveBeenCalledWith(googleURL), { timeout: 3000 })
    expect(child.opener).toBeNull()
    const link = screen.getByRole('link', { name: '打开授权页' }); expect(link).toHaveAttribute('rel', 'noopener noreferrer'); expect(link).toHaveAttribute('target', '_blank')
    op = operation('succeeded', { id: writes[0].operationId, accountId: '10000000-0000-7000-8000-000000000001' })
    await waitFor(() => expect(screen.getByText('本次登录成功')).toBeInTheDocument(), { timeout: 3000 })
    expect(screen.queryByRole('link', { name: '打开授权页' })).toBeNull()
    expect(screen.getByLabelText('新 Google 账号名称')).toHaveValue('')
    const count = reads; await act(() => new Promise(resolve => setTimeout(resolve, 1650))); expect(reads).toBe(count)
    expect(child.close).not.toHaveBeenCalled() // Already navigated issuer window is user-owned.
    expect(JSON.stringify(env.client.getQueryCache().getAll().map(query => query.state.data))).not.toContain('fixture-challenge')
  }, 10000)

  it('恢复已有操作不新POST，取消原目标后终态移除入口', async () => {
    const user = userEvent.setup(); let op = operation('awaiting_authorization'), starts = 0, cancels = 0
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: op.status === 'awaiting_authorization' ? [op] : [] })),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation: op, account: null })),
      http.post(apiUrl('/projects/:projectId/cli-auth/operations'), () => { starts++; return HttpResponse.error() }),
      http.post(apiUrl('/projects/:projectId/cli-auth/operations/:operationId/cancel'), ({ params }) => { cancels++; expect(params.operationId).toBe(id); op = operation('cancelled'); return HttpResponse.json({ operation: op, account: null }) }))
    renderLogin()
    expect(await screen.findByRole('link', { name: '打开授权页' })).toHaveAttribute('href', googleURL)
    expect(screen.getByLabelText('新 Google 账号名称')).toHaveValue('已恢复目标')
    await user.click(screen.getByRole('button', { name: '取消登录' }))
    expect(await screen.findByText('本次登录已取消')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '打开授权页' })).toBeNull(); expect(cancels).toBe(1); expect(starts).toBe(0)
    expect(screen.getByLabelText('新 Google 账号名称')).toBeEnabled()
  })

  it('未知start不自动POST，按已知ID读取终态再用新ID人工重试', async () => {
    const user = userEvent.setup(); popup(); const writes: Schemas['CLIAuthStart'][] = []
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [] })),
      http.post(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => { writes.push(await request.json() as Schemas['CLIAuthStart']); return HttpResponse.error() }),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => HttpResponse.json({ operation: operation('failed', { id: String(params.operationId), errorCode: 'oauth_failed' }), account: null })))
    renderLogin(); await user.type(screen.getByLabelText('新 Google 账号名称'), '原草稿')
    await waitFor(() => expect(screen.getByRole('button', { name: '登录 Google' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '登录 Google' }))
    await screen.findByText(/本次发起结果未知/); expect(writes).toHaveLength(1)
    await user.click(screen.getByRole('button', { name: '读取 Google 活动登录' }))
    await screen.findByText('Google 授权未完成。')
    expect(screen.getByLabelText('新 Google 账号名称')).toHaveValue('原草稿')
    await user.click(screen.getByRole('button', { name: '重新发起登录' }))
    await waitFor(() => expect(writes).toHaveLength(2)); expect(writes[1].operationId).not.toBe(writes[0].operationId)
  })

  it('结果仍未知时人工重试保同operationId与固定body，不另造目标', async () => {
    const user = userEvent.setup(); popup(); const writes: Schemas['CLIAuthStart'][] = []
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [] })),
      http.post(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => { const body = await request.json() as Schemas['CLIAuthStart']; writes.push(body); return writes.length === 1 ? HttpResponse.error() : HttpResponse.json({ operation: operation('pending', { id: body.operationId }), account: null }, { status: 202 }) }),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), ({ params }) => HttpResponse.json({ operation: operation('pending', { id: String(params.operationId) }), account: null })))
    renderLogin(); await user.type(screen.getByLabelText('新 Google 账号名称'), '固定目标')
    await waitFor(() => expect(screen.getByRole('button', { name: '登录 Google' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '登录 Google' }))
    await user.click(await screen.findByRole('button', { name: '按原操作 ID 重试' }))
    await waitFor(() => expect(writes).toHaveLength(2)); expect(writes[1]).toEqual(writes[0])
  })

  it('另一个账号的活动操作不占当前目标或触发读取', async () => {
    const account = projectAccount({ purpose: 'cli', provider: null, cli: 'gcloud', authMethod: 'oauth' }); let detailReads = 0
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [operation('pending', { targetAccountId: '10000000-0000-7000-8000-000000000099' })] })),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => { detailReads++; return HttpResponse.error() }))
    renderLogin(account)
    await waitFor(() => expect(screen.getByRole('button', { name: '重新登录 Google' })).toBeEnabled())
    expect(screen.queryByText(/本次登录目标已固定/)).toBeNull(); expect(detailReads).toBe(0)
  })

  it('URL校验失败清未导航预留窗，不开放入口/不重POST', async () => {
    const user = userEvent.setup(), child = popup(); let writes = 0
    const bad = { ...operation('awaiting_authorization'), authorizationUrl: googleURL + '&access_token=not-allowed' }
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [] })),
      http.post(apiUrl('/projects/:projectId/cli-auth/operations'), () => { writes++; return HttpResponse.json({ operation: bad, account: null }, { status: 202 }) }),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation: bad, account: null })))
    renderLogin(); await user.type(screen.getByLabelText('新 Google 账号名称'), '安全校验')
    await waitFor(() => expect(screen.getByRole('button', { name: '登录 Google' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '登录 Google' }))
    await screen.findByText('授权地址未通过安全校验，不会打开未知地址或自动新建操作。')
    expect(child.location.replace).not.toHaveBeenCalled(); expect(child.close).toHaveBeenCalledOnce(); expect(writes).toBe(1)
    expect(screen.queryByRole('link', { name: '打开授权页' })).toBeNull()
  })

  it('重登失败保原账号，目标仅原account/revision；不显示原provider错误', async () => {
    const user = userEvent.setup(); popup(); const account = projectAccount({ purpose: 'cli', provider: null, cli: 'gcloud', authMethod: 'oauth', status: 'logged_in' }), writes: Schemas['CLIAuthStart'][] = []
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: [] })),
      http.post(apiUrl('/projects/:projectId/cli-auth/operations'), async ({ request }) => { const body = await request.json() as Schemas['CLIAuthStart']; writes.push(body); return HttpResponse.json({ operation: operation('failed', { id: body.operationId, targetAccountId: account.id, targetRevision: account.revision, errorCode: 'account_mismatch', errorMessage: 'raw-provider-private-error' }), account }, { status: 202 }) }))
    renderLogin(account)
    await waitFor(() => expect(screen.getByRole('button', { name: '重新登录 Google' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '重新登录 Google' }))
    await screen.findByText('原有账号保持不变。')
    expect(writes[0]).toMatchObject({ accountId: account.id, expectedRevision: account.revision, cli: 'gcloud', authType: 'google_user' })
    expect(writes[0]).not.toHaveProperty('name'); expect(screen.queryByText('raw-provider-private-error')).toBeNull()
  })

  it.each(['failed', 'cancelled', 'expired'] as const)('恢复活动不覆盖草稿，%s后回到原草稿', async terminal => {
    const user = userEvent.setup(); let op: Schemas['CLIAuthOperation'] | null = null
    server.use(http.get(apiUrl('/projects/:projectId/cli-auth/operations'), () => HttpResponse.json({ operations: op && (op.status === 'pending' || op.status === 'awaiting_authorization') ? [op] : [] })),
      http.get(apiUrl('/projects/:projectId/cli-auth/operations/:operationId'), () => HttpResponse.json({ operation: op, account: null })))
    const env = renderLogin(); await user.type(screen.getByLabelText('新 Google 账号名称'), '本地未提交草稿')
    op = operation('pending')
    await act(() => env.client.invalidateQueries({ queryKey: cliAuthKeys.project(defaultProject.id) }))
    await waitFor(() => expect(screen.getByLabelText('新 Google 账号名称')).toHaveValue('已恢复目标'))
    op = operation(terminal)
    await act(() => env.client.invalidateQueries({ queryKey: cliAuthKeys.operation(defaultProject.id, id) }))
    await waitFor(() => expect(screen.getByLabelText('新 Google 账号名称')).toBeEnabled())
    expect(screen.getByLabelText('新 Google 账号名称')).toHaveValue('本地未提交草稿')
  })
})
