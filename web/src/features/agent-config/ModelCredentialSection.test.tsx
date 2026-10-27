import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { modelAuthKey } from '@/api/model-auth'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { providers, renderConfig } from '@/test/config'
import { codexProvider, makeModelAccount, makeModelLogin } from '@/test/model-auth'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { ModelCredentialSection } from './ModelCredentialSection'

const area = () => screen.getByRole('region', { name: '模型账号凭证' })
const codex = () => makeAgent({ scope: 'platform', projectId: null, model: { provider: 'openai-codex', modelId: 'gpt-5-codex' }, modelCredential: makeModelAccount() })
const directory = http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, codexProvider] }))
afterEach(() => vi.restoreAllMocks())

describe('按正式提供方接口认证', () => {
  it('发起绑定配置版本、窗口拦截后只重开授权、成功以服务端保存元数据为准', async () => {
    const agent = codex(), starts: unknown[] = []
    let state: Schemas['ModelAuthState'] = { credential: agent.modelCredential, operation: null }
    const popup = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const { user, client, router } = await renderConfig({ agent, handlers: [directory,
      http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json(state)),
      http.post(apiUrl(`/agents/${agent.id}/model-auth/operations`), async ({ request }) => {
        starts.push(await request.json()); state = { ...state, operation: makeModelLogin(agent.id) }; return HttpResponse.json(state.operation)
      }),
    ] })
    const relogin = await within(area()).findByRole('button', { name: '重新登录' })
    await waitFor(() => expect(relogin).toBeEnabled()); await user.click(relogin)
    await within(area()).findByText('等待浏览器授权')
    expect(starts).toEqual([{ provider: 'openai-codex', expectedConfigVersion: 1 }])
    expect(within(area()).getByText(/fixture-old@example.test/)).toBeInTheDocument()
    expect(relogin).toBeDisabled()
    expect(within(area()).getByText(/授权页未打开/)).toBeInTheDocument()
    open.mockReturnValue(popup as unknown as Window)
    await user.click(within(area()).getByRole('button', { name: '打开授权页' }))
    expect(popup.location.replace).toHaveBeenCalledOnce(); expect(starts).toHaveLength(1)
    expect(within(area()).queryByText('本次登录成功')).toBeNull()
    state = { credential: makeModelAccount({ accountLabel: 'fixture-new@example.test' }), operation: makeModelLogin(agent.id, { status: 'succeeded', authorizationUrl: null }) }
    await act(() => client.invalidateQueries({ queryKey: modelAuthKey(agent.id, agent.model.provider, 1) }))
    expect(await within(area()).findByText('本次登录成功')).toBeInTheDocument()
    expect(within(area()).getByText(/fixture-new@example.test/)).toBeInTheDocument()
    expect(within(area()).queryByText(/fixture-old@example.test/)).toBeNull()
    expect(router.state.location.pathname + router.state.location.search).not.toContain('authorize')
    expect(document.body.innerHTML).not.toContain('client_id=fixture-only')
    expect(JSON.stringify({ ...localStorage, ...sessionStorage })).not.toContain('client_id=fixture-only')
    expect(screen.queryByLabelText('环境变量名')).toBeNull()
  })

  it('重新登录请求失败时不把上一次成功当成本次成功', async () => {
    const agent = codex()
    const { user } = await renderConfig({ agent, handlers: [directory,
      http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: agent.modelCredential, operation: makeModelLogin(agent.id, { status: 'succeeded', authorizationUrl: null }) })),
      http.post(apiUrl(`/agents/${agent.id}/model-auth/operations`), () => HttpResponse.json(errorBody('internal_error', '认证组件暂不可用'), { status: 503 })),
    ] })
    vi.spyOn(window, 'open').mockReturnValue(null)
    await user.click(await within(area()).findByRole('button', { name: '重新登录' }))
    expect(await within(area()).findByText('本次登录失败')).toBeInTheDocument()
    expect(within(area()).queryByText('本次登录成功')).toBeNull()
    expect(within(area()).getByText(/fixture-old@example.test/)).toBeInTheDocument()
  })

  it.each(['failed', 'expired', 'cancelled'] as const)('恢复%s操作，旧账号保持并可重新发起', async status => {
    const agent = codex()
    await renderConfig({ agent, handlers: [directory, http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: agent.modelCredential, operation: makeModelLogin(agent.id, { status, authorizationUrl: null, errorCode: status === 'failed' ? 'auth_port_busy' : null }) }))] })
    expect(await within(area()).findByRole('button', { name: '重新发起登录' })).toBeEnabled()
    expect(within(area()).getByText(/fixture-old@example.test/)).toBeInTheDocument()
    expect(within(area()).getByText('已登录', { exact: true })).toBeInTheDocument()
    if (status === 'failed') expect(within(area()).getByText(/端口已被占用/)).toBeInTheDocument()
  })

  it('取消必须等服务端确认，失败保留等待与旧账号，重复取消不提交', async () => {
    const agent = codex(); let cancelCalls = 0
    let finish: ((response: Response) => void) | undefined
    const { user } = await renderConfig({ agent, handlers: [directory,
      http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: agent.modelCredential, operation: makeModelLogin(agent.id) })),
      http.post(apiUrl(`/agents/${agent.id}/model-auth/operations/:operationId/cancel`), async () => { cancelCalls++; return await new Promise<Response>(resolve => { finish = resolve }) }),
    ] })
    await user.click(await within(area()).findByRole('button', { name: '取消登录' }))
    expect(await within(area()).findByRole('button', { name: '正在取消…' })).toBeDisabled()
    expect(within(area()).queryByText('本次登录已取消')).toBeNull()
    await user.click(within(area()).getByRole('button', { name: '正在取消…' })); expect(cancelCalls).toBe(1)
    await act(async () => finish?.(HttpResponse.json(errorBody('internal_error', '取消未完成'), { status: 500 })))
    await within(area()).findByRole('alert')
    expect(within(area()).getByText('等待浏览器授权')).toBeInTheDocument()
    expect(within(area()).getByText(/fixture-old@example.test/)).toBeInTheDocument()
  })

  it('切Agent时迟到发起响应不导航、不污染新Agent，切provider不显示旧操作', async () => {
    const a = codex(), b = codex(); b.modelCredential = null
    let finish: ((response: Response) => void) | undefined
    server.use(directory,
      http.get(apiUrl('/agents/:agentId/model-auth'), ({ params }) => HttpResponse.json({ credential: params.agentId === a.id ? a.modelCredential : null, operation: null })),
      http.post(apiUrl(`/agents/${a.id}/model-auth/operations`), () => new Promise<Response>(resolve => { finish = resolve })),
    )
    const popup = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    const { rerender, client } = renderWithQuery(<ModelCredentialSection key={a.id} agent={a} />)
    const user = userEvent.setup(); await user.click(await within(area()).findByRole('button', { name: '重新登录' }))
    await waitFor(() => expect(finish).toBeDefined())
    rerender(<ModelCredentialSection key={b.id} agent={b} />)
    await within(area()).findByRole('button', { name: '登录 Codex' })
    await act(async () => finish?.(HttpResponse.json(makeModelLogin(a.id))))
    expect(popup.location.replace).not.toHaveBeenCalled(); expect(popup.close).toHaveBeenCalled()
    expect(within(area()).queryByText('等待浏览器授权')).toBeNull()
    const other = { ...b, model: { provider: 'anthropic', modelId: 'claude' } }
    await act(async () => client.setQueryData(modelAuthKey(b.id, 'anthropic', 1), { credential: null, operation: makeModelLogin(b.id) }))
    rerender(<ModelCredentialSection key={`${b.id}:anthropic`} agent={other} />)
    expect(within(area()).queryByText('等待浏览器授权')).toBeNull()
  })

  it('API Key使用provider/key写入，不再有env字段或秘密缓存；取消清空', async () => {
    const agent = makeAgent({ scope: 'platform', projectId: null }); let body: unknown = null, credential: Schemas['CredentialMeta'] | null = null
    const { user, client } = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential, operation: null })),
      http.put(apiUrl(`/agents/${agent.id}/model-credential`), async ({ request }) => { body = await request.json(); credential = makeModelAccount({ provider: 'anthropic', authMethod: 'api_key', accountLabel: null }); return HttpResponse.json(credential) }),
    ] })
    await user.click(await within(area()).findByRole('button', { name: '录入 API Key' }))
    await user.type(screen.getByLabelText('API Key'), 'fixture-secret-key'); await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(body).toEqual({ provider: 'anthropic', key: 'fixture-secret-key', expectedConfigVersion: 1 })
    expect(JSON.stringify(client.getMutationCache().getAll().map(m => m.state))).not.toContain('fixture-secret-key')
    expect(JSON.stringify(client.getQueryCache().getAll().map(q => q.state))).not.toContain('fixture-secret-key')
    expect(document.body.innerHTML).not.toContain('fixture-secret-key')
    await user.click(within(area()).getByRole('button', { name: '替换 API Key' })); await user.type(screen.getByLabelText('API Key'), 'cancelled-key'); await user.click(screen.getByRole('button', { name: '取消' }))
    await user.click(within(area()).getByRole('button', { name: '替换 API Key' })); expect(screen.getByLabelText('API Key')).toHaveValue('')
  })
})
