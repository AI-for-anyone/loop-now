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
import { ProjectAccounts } from './ProjectAccounts'
import { ProjectLogin } from './ProjectLogin'

afterEach(() => vi.restoreAllMocks())
const directory = http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, codexProvider] }))

describe('项目账号正式HTTP fixture', () => {
  it('新建API Key只发送正式credential对象，不进query/mutation缓存；成功清输入', async () => {
    const user = userEvent.setup(), requests: unknown[] = []
    let accounts: Schemas['ProjectCredentialAccount'][] = []
    server.use(directory, http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts })),
      http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accountId: null, agents: [] })),
      http.post(apiUrl('/projects/:projectId/credential-accounts'), async ({ request }) => { requests.push(await request.json()); accounts = [projectAccount()]; return HttpResponse.json(accounts[0], { status: 201 }) }),
    )
    const { client } = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="model" /></DraftNavigationGuard> }], '/')
    await screen.findByRole('option', { name: 'anthropic' })
    await user.selectOptions(screen.getByLabelText('添加模型账号 · 提供方'), 'anthropic')
    await user.click(screen.getByRole('button', { name: '录入 API Key' }))
    await user.type(screen.getByLabelText('账号名称'), '项目账号A'); await user.type(screen.getByLabelText('API Key'), 'fixture-secret')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByLabelText('API Key')).toBeNull())
    expect(requests).toEqual([{ purpose: 'model', provider: 'anthropic', name: '项目账号A', credential: { type: 'api_key', key: 'fixture-secret' } }])
    expect(screen.getByText('已保存', { exact: true })).toBeInTheDocument()
    expect(JSON.stringify(client.getQueryCache().getAll().map(item => item.state))).not.toContain('fixture-secret')
    expect(JSON.stringify(client.getMutationCache().getAll().map(item => item.state))).not.toContain('fixture-secret')
  })

  it('迁移屏障以服务端mode显示，null只写当前provider，切B不误报automatic', async () => {
    const agent = makeAgent(), writes: unknown[] = []
    let latestAgent = agent
    let data = emptySelections(agent)
    data.selections[0] = { ...data.selections[0], mode: 'legacy_cleared', selectedAccountId: null, resolvedAccountId: null, resolvedVersionId: null, status: 'not_configured', candidates: [projectAccount()] }
    const env = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(latestAgent)),
      http.get(apiUrl(`/agents/${agent.id}/credential-selections`), () => HttpResponse.json(data)),
      http.put(apiUrl(`/agents/${agent.id}/credential-selections`), async ({ request }) => { writes.push(await request.json()); latestAgent = { ...agent, configVersion: 2 }; data = { ...data, configVersion: 2, selections: [{ ...data.selections[0], mode: 'automatic', selectedAccountId: null, status: 'resolved', resolvedAccountId: projectAccount().id }] }; return HttpResponse.json(data) }),
    ] })
    const area = screen.getByRole('region', { name: '模型账号凭证' })
    await within(area).findByText(/原模型设置已清除/)
    await env.user.click(within(area).getByRole('button', { name: '恢复当前提供方自动取用' }))
    await within(area).findByText('自动使用唯一对应的项目账号')
    expect(writes).toEqual([{ purpose: 'model', provider: 'anthropic', accountId: null, expectedConfigVersion: 1 }])
    // 模拟正式GET切provider后的权威返回，不从null猜mode。
    const b = { ...agent, configVersion: 3, model: { provider: 'openai', modelId: 'gpt-x' } }
    latestAgent = b
    data = { ...data, configVersion: 3, selections: [...data.selections, { ...emptySelections(b).selections[0], mode: 'legacy_cleared', selectedAccountId: null, resolvedAccountId: null, resolvedVersionId: null, status: 'not_configured' }] }
    await act(async () => { env.client.setQueryData(['agent', agent.id], b); await env.client.invalidateQueries({ queryKey: credentialKeys.selections(agent.id) }) })
    expect(await within(area).findByText(/原模型设置已清除/)).toBeInTheDocument()
    expect(writes).toHaveLength(1)
  })

  it('远端删除正在编辑账号时不卸载秘密草稿，412后读tombstone不能采用或复活', async () => {
    const user = userEvent.setup(), account = projectAccount()
    let accounts = [account], puts = 0
    server.use(directory, http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts })),
      http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accountId: account.id, agents: [] })),
      http.get(apiUrl('/projects/:projectId/credential-accounts/:accountId'), () => HttpResponse.json({ ...account, status: 'deleted', revision: 2, deletedAt: account.updatedAt })),
      http.put(apiUrl('/projects/:projectId/credential-accounts/:accountId/credential'), () => { puts++; return HttpResponse.json(errorBody('precondition_failed'), { status: 412 }) }),
    )
    const { client } = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="model" /></DraftNavigationGuard> }], '/')
    await user.click(await screen.findByRole('button', { name: '整体替换凭证' }))
    await user.type(screen.getByLabelText('API Key'), 'keep-local-secret')
    accounts = []
    await act(() => client.invalidateQueries({ queryKey: credentialKeys.project(defaultProject.id) }))
    expect(screen.getByLabelText('API Key')).toHaveValue('keep-local-secret')
    expect(await screen.findByText(/账号已不在当前列表中，保留本次编辑/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '保存' }))
    await screen.findByText(/账号已删除，不能继续替换/)
    expect(screen.getByRole('button', { name: '已核对，采用此版本' })).toBeDisabled()
    expect(puts).toBe(1)
  })

  it('项目Agent没有私有秘密录入；平台Agent仍走原私有接口', async () => {
    const agent = makeAgent({ clis: [{ name: 'gh', hasTemplate: true, credential: null }] })
    await renderConfig({ agent })
    await screen.findAllByText('未配置对应账号，请去项目设置维护')
    expect(screen.queryByRole('button', { name: '录入 API Key' })).toBeNull()
    expect(screen.queryByRole('button', { name: '录入凭证' })).toBeNull()
    expect(screen.queryByLabelText('API Key')).toBeNull()
  })

  it('选择412只提交一次，保留选择草稿，核对后按最新configVersion单项保存', async () => {
    const agent = makeAgent(), a = projectAccount(), b = projectAccount({ id: '10000000-0000-7000-8000-000000000002', name: '账号B', status: 'unavailable' }), bodies: unknown[] = []
    let data = emptySelections(agent)
    data.selections[0] = { ...data.selections[0], mode: 'automatic', selectedAccountId: null, status: 'selection_required', candidates: [a, b] }
    const env = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json({ ...agent, configVersion: data.configVersion })),
      http.get(apiUrl(`/agents/${agent.id}/credential-selections`), () => HttpResponse.json(data)),
      http.put(apiUrl(`/agents/${agent.id}/credential-selections`), async ({ request }) => {
        bodies.push(await request.json())
        if (bodies.length === 1) {
          data = { ...data, configVersion: 2 }
          return HttpResponse.json(errorBody('precondition_failed', '配置已修改'), { status: 412 })
        }
        data = { ...data, configVersion: 3, selections: [{ ...data.selections[0], mode: 'explicit', selectedAccountId: a.id, resolvedAccountId: a.id, status: 'resolved' }] }
        return HttpResponse.json(data)
      }),
    ] })
    const area = screen.getByRole('region', { name: '模型账号凭证' })
    await within(area).findByText(/多个对应账号/)
    await env.user.selectOptions(within(area).getByRole('combobox'), a.id)
    await env.user.click(within(area).getByRole('button', { name: '保存选择' }))
    await within(area).findByText('配置已在别处更新，尚未保存本次修改')
    expect(bodies).toHaveLength(1); expect(within(area).getByRole('combobox')).toHaveValue(a.id)
    await env.user.click(within(area).getByRole('button', { name: '已核对，采用此版本' }))
    await env.user.click(within(area).getByRole('button', { name: '保存选择' }))
    await within(area).findByText('明确使用所选项目账号')
    expect(bodies).toEqual([1, 2].map(expectedConfigVersion => ({ purpose: 'model', provider: 'anthropic', accountId: a.id, expectedConfigVersion })))
  })

  it('Agent基本配置与selection版本不一致时禁止写，刷新同步后才允许保存', async () => {
    const agent = makeAgent(), data = emptySelections(agent), writes: unknown[] = []
    data.configVersion = 2
    data.selections[0] = { ...data.selections[0], mode: 'automatic', selectedAccountId: null, status: 'selection_required', candidates: [projectAccount(), projectAccount({ id: '10000000-0000-7000-8000-000000000002' })] }
    let current = agent
    const env = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
      http.get(apiUrl(`/agents/${agent.id}/credential-selections`), () => HttpResponse.json(data)),
      http.put(apiUrl(`/agents/${agent.id}/credential-selections`), async ({ request }) => { writes.push(await request.json()); return HttpResponse.json(data) }),
    ] })
    const area = screen.getByRole('region', { name: '模型账号凭证' })
    await within(area).findByText(/基本配置与账号来源版本尚未同步/)
    expect(within(area).getByRole('combobox')).toBeDisabled()
    expect(within(area).getByRole('button', { name: '保存选择' })).toBeDisabled()
    expect(writes).toHaveLength(0)
    current = { ...agent, configVersion: 2 }
    await act(() => env.client.invalidateQueries({ queryKey: ['agent', agent.id] }))
    await waitFor(() => expect(within(area).getByRole('combobox')).toBeEnabled())
    await env.user.selectOptions(within(area).getByRole('combobox'), projectAccount().id)
    await env.user.click(within(area).getByRole('button', { name: '保存选择' }))
    await waitFor(() => expect(writes).toEqual([{ purpose: 'model', provider: 'anthropic', accountId: projectAccount().id, expectedConfigVersion: 2 }]))
  })

  it('项目模型/CLI独立草稿共用一个离开守卫；保存模型不清CLI风险表单', async () => {
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
      { path: '/other', element: <p>已离开设置</p> },
    ], '/')
    const model = screen.getByRole('region', { name: '模型账号' }), cli = screen.getByRole('region', { name: 'CLI 账号' })
    await within(model).findByRole('option', { name: 'anthropic' })
    await user.selectOptions(within(model).getByRole('combobox'), 'anthropic')
    await user.click(within(model).getByRole('button', { name: '录入 API Key' }))
    await user.type(within(model).getByLabelText('账号名称'), '项目账号A'); await user.type(within(model).getByLabelText('API Key'), 'model-local-secret')
    await within(cli).findByRole('option', { name: 'lark-cli' })
    expect(within(cli).queryByRole('option', { name: 'jq' })).toBeNull()
    expect(within(cli).getByText(/没有模板的工具不可录入/)).toBeInTheDocument()
    await user.selectOptions(within(cli).getByRole('combobox'), 'lark-cli')
    await user.click(within(cli).getByRole('button', { name: '录入应用身份账号' }))
    expect(within(cli).getByRole('note')).toHaveTextContent('宿主用户本人的飞书身份')
    expect(within(cli).getByLabelText('App Secret *')).toHaveAttribute('type', 'password')
    expect(within(cli).getByLabelText('品牌 *')).toHaveValue('feishu')
    await user.type(within(cli).getByLabelText('App Secret *'), 'cli-local-secret')
    await act(async () => { void env.router.navigate('/other') })
    expect(await screen.findAllByRole('alertdialog')).toHaveLength(1)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '取消' }))
    expect(within(model).getByLabelText('API Key')).toHaveValue('model-local-secret')
    expect(within(cli).getByLabelText('App Secret *')).toHaveValue('cli-local-secret')
    await user.click(within(model).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(within(model).queryByLabelText('API Key')).toBeNull())
    expect(within(cli).getByLabelText('App Secret *')).toHaveValue('cli-local-secret')
    expect(bodies).toHaveLength(1)
    await act(async () => { void env.router.navigate('/other') })
    expect(await screen.findAllByRole('alertdialog')).toHaveLength(1)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '离开' }))
    expect(await screen.findByText('已离开设置')).toBeInTheDocument()
    expect(screen.queryByLabelText('App Secret *')).toBeNull()
  })
})

describe('项目OAuth正式HTTP fixture，供应商不导航', () => {
  it('切项目后迟到start不导航、不关闭新项目表单；不自动重发', async () => {
    const user = userEvent.setup(), starts: unknown[] = []
    let finish: ((response: Response) => void) | undefined
    const popup = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    server.use(http.post(apiUrl('/projects/:projectId/model-auth/operations'), async ({ request }) => { starts.push(await request.json()); return new Promise<Response>(resolve => { finish = resolve }) }))
    const { router } = renderRoutes([
      { path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} /></DraftNavigationGuard> },
      { path: '/other', element: <p>另一个项目</p> },
    ], '/')
    await user.type(screen.getByLabelText('新 Codex 账号名称'), '新账号')
    await waitFor(() => expect(screen.getByRole('button', { name: '登录 Codex' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '登录 Codex' }))
    await waitFor(() => expect(finish).toBeDefined())
    await act(async () => { void router.navigate('/other') })
    // 名称草稿只有一个离开守卫；确认后卸载旧拥有者。
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: '离开' }))
    await screen.findByText('另一个项目')
    await act(async () => finish?.(HttpResponse.json({ operation: projectOperation(), account: null }, { status: 202 })))
    expect(popup.location.replace).not.toHaveBeenCalled(); expect(popup.close).toHaveBeenCalled()
    expect(starts).toHaveLength(1)
  })
  it('刷新恢复新建活动并取消，仅一个POST；终态URL不入DOM', async () => {
    const user = userEvent.setup(), op = projectOperation(), calls: unknown[] = []
    let state: Schemas['ProjectModelAuthState'] = { operation: op, account: null }
    server.use(
      http.get(apiUrl('/projects/:projectId/model-auth/operations'), () => HttpResponse.json({ operations: state.operation.status === 'cancelled' ? [] : [state.operation] })),
      http.get(apiUrl('/projects/:projectId/model-auth/operations/:operationId'), () => HttpResponse.json(state)),
      http.post(apiUrl('/projects/:projectId/model-auth/operations/:operationId/cancel'), async ({ request }) => { calls.push(request.url); state = { operation: { ...op, status: 'cancelled', authorizationUrl: null }, account: null }; return HttpResponse.json(state) }),
    )
    renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} /></DraftNavigationGuard> }], '/')
    await screen.findByText('等待浏览器授权')
    expect(screen.getByLabelText('新 Codex 账号名称')).toHaveValue(op.name)
    expect(screen.getByLabelText('新 Codex 账号名称')).toBeDisabled()
    expect(screen.getByLabelText('新 Codex 账号名称')).toHaveAccessibleDescription(/已有一项新建登录正在进行.*打开授权页.*取消登录.*结束或过期后/ )
    expect(screen.getByRole('button', { name: '登录 Codex' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '取消登录' }))
    expect(await screen.findByText('本次登录已取消')).toBeInTheDocument()
    expect(screen.getByLabelText('新 Codex 账号名称')).toBeEnabled()
    await user.type(screen.getByLabelText('新 Codex 账号名称'), '取消后另起名称')
    expect(screen.getByRole('button', { name: '登录 Codex' })).toBeEnabled()
    expect(calls).toHaveLength(1)
    expect(document.body.innerHTML).not.toContain('client_id=fixture-only')
    expect(JSON.stringify({ ...localStorage, ...sessionStorage })).not.toContain('client_id=fixture-only')
  })

  it.each(['cancelled', 'expired', 'failed'] as const)('他处活动恢复名称并保留本地草稿，%s后才恢复编辑且不自动start', async terminal => {
    const user = userEvent.setup(), posts = vi.fn()
    const op = projectOperation({ name: '另一窗口发起的账号' })
    let state: Schemas['ProjectModelAuthState'] | null = null
    server.use(
      http.get(apiUrl('/projects/:projectId/model-auth/operations'), () => HttpResponse.json({ operations: state && (state.operation.status === 'pending' || state.operation.status === 'awaiting_authorization') ? [state.operation] : [] })),
      http.get(apiUrl('/projects/:projectId/model-auth/operations/:operationId'), () => HttpResponse.json(state)),
      http.post(apiUrl('/projects/:projectId/model-auth/operations'), () => { posts(); return HttpResponse.json({}, { status: 500 }) }),
    )
    const { client } = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} /></DraftNavigationGuard> }], '/')
    await user.type(screen.getByLabelText('新 Codex 账号名称'), '用户未提交草稿')
    state = { operation: op, account: null }
    await act(() => client.invalidateQueries({ queryKey: credentialKeys.operations(defaultProject.id) }))
    await screen.findByText('等待浏览器授权')
    expect(screen.getByLabelText('新 Codex 账号名称')).toHaveValue(op.name)
    expect(screen.getByLabelText('新 Codex 账号名称')).toBeDisabled()
    expect(screen.getByLabelText('新 Codex 账号名称')).toHaveAccessibleDescription(/名称已锁定/)
    state = { operation: { ...op, status: terminal, authorizationUrl: null }, account: null }
    await act(() => client.invalidateQueries({ queryKey: credentialKeys.operations(defaultProject.id) }))
    await waitFor(() => expect(screen.getByLabelText('新 Codex 账号名称')).toBeEnabled())
    expect(screen.getByLabelText('新 Codex 账号名称')).toHaveValue('用户未提交草稿')
    expect(screen.getByLabelText('新 Codex 账号名称')).not.toHaveAttribute('aria-describedby')
    await user.clear(screen.getByLabelText('新 Codex 账号名称'))
    await user.type(screen.getByLabelText('新 Codex 账号名称'), '终态后新名称')
    expect(screen.getByRole('button', { name: '登录 Codex' })).toBeEnabled()
    expect(posts).not.toHaveBeenCalled()
  })

  it.each(['url-rejected', 'navigation-rejected'] as const)('%s收掉未导航空窗，显示继续引导且只start一次', async failure => {
    const user = userEvent.setup(), starts = vi.fn()
    const child = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    if (failure === 'navigation-rejected') child.location.replace.mockImplementation(() => { throw new Error('navigation rejected') })
    const op = projectOperation(failure === 'url-rejected' ? { authorizationUrl: 'https://other.test/oauth/authorize' } : {})
    server.use(
      http.post(apiUrl('/projects/:projectId/model-auth/operations'), () => { starts(); return HttpResponse.json({ operation: op, account: null }, { status: 202 }) }),
      http.get(apiUrl('/projects/:projectId/model-auth/operations/:operationId'), () => HttpResponse.json({ operation: op, account: null })),
    )
    const { client } = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} /></DraftNavigationGuard> }], '/')
    await user.type(screen.getByLabelText('新 Codex 账号名称'), '离线窗口测试')
    await waitFor(() => expect(screen.getByRole('button', { name: '登录 Codex' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '登录 Codex' }))
    if (failure === 'url-rejected') await screen.findByText(/未打开未知地址，也不会自动重新发起登录/ )
    else await screen.findByText(/授权页未打开，可能被浏览器拦截/)
    await waitFor(() => expect(child.close).toHaveBeenCalledOnce())
    if (failure === 'navigation-rejected') expect(screen.getByRole('link', { name: '打开授权页' })).toHaveAttribute('rel', 'noopener noreferrer')
    else expect(screen.queryByRole('link', { name: '打开授权页' })).toBeNull()
    expect(starts).toHaveBeenCalledOnce()
    if (failure === 'url-rejected') expect(child.location.replace).not.toHaveBeenCalled()
    else {
      await user.click(screen.getByRole('link', { name: '打开授权页' }))
      expect(window.open).toHaveBeenCalledOnce() // 只有初次start预留；手动继续走原生链接。
      expect(screen.getByRole('link', { name: '打开授权页' })).toHaveAttribute('href', op.authorizationUrl)
      op.authorizationUrl += '&originator=pi'
      await act(() => client.invalidateQueries({ queryKey: credentialKeys.operations(defaultProject.id) }))
      expect(screen.queryByText(/授权页未打开，可能被浏览器拦截/)).toBeNull()
      expect(child.close).toHaveBeenCalledOnce()
      expect(starts).toHaveBeenCalledOnce()
    }
  })

  it('重登失败旧账号仍单独存在，外层只用当前目标revision发起', async () => {
    const user = userEvent.setup(), account = projectAccount({ provider: 'openai-codex', authMethod: 'oauth', status: 'logged_in', revision: 7 }), starts: unknown[] = []
    vi.spyOn(window, 'open').mockReturnValue(null)
    const failed = projectOperation({ targetAccountId: account.id, targetRevision: 7, status: 'failed', authorizationUrl: null })
    server.use(http.post(apiUrl('/projects/:projectId/model-auth/operations'), async ({ request }) => { starts.push(await request.json()); return HttpResponse.json({ operation: failed, account }, { status: 202 }) }))
    renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} account={account} /></DraftNavigationGuard> }], '/')
    await waitFor(() => expect(screen.getByRole('button', { name: '重新登录' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '重新登录' }))
    expect(await screen.findByText('本次登录失败')).toBeInTheDocument()
    expect(screen.getByText('原有账号保持不变。')).toBeInTheDocument()
    expect(starts).toEqual([{ provider: 'openai-codex', accountId: account.id, expectedRevision: 7 }])
  })
})
