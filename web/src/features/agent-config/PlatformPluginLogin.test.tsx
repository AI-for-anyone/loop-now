import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { modelAuthKey } from '@/api/model-auth'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { pluginProvider, providers, renderConfig } from '@/test/config'
import { makeModelAccount, makeModelLogin } from '@/test/model-auth'

// 平台 Agent（不属于项目）在自己的模型凭证里登录插件注册的提供方：浏览器授权、设备码、登录中途的问题、本机登录说明。
const area = () => screen.getByRole('region', { name: '模型账号凭证' })
const grokAgent = () => makeAgent({ scope: 'platform', projectId: null, model: { provider: 'grok-cli', modelId: 'grok-4' }, modelCredential: null })
const grokLogin = (agentId: string, overrides: Partial<Schemas['ModelAuthOperation']> = {}) =>
  makeModelLogin(agentId, { id: '00000000-0000-7000-8000-0000000003a1', provider: 'grok-cli', status: 'pending', authorizationUrl: null, ...overrides })
const catalog = http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, pluginProvider('grok-cli')] }))
const grokModels = http.get(apiUrl('/model-providers/:providerId/models'), ({ params }) =>
  HttpResponse.json({ providerId: params.providerId, models: [{ id: 'grok-4', name: 'Grok 4', reasoning: false, contextWindow: 1, maxTokens: 1, input: ['text'] }] }))

afterEach(() => vi.restoreAllMocks())

// 登录状态由测试控制：发起返回 start，之后每次读取返回 box.state（回答、取消改写它）。
async function setup(start: Schemas['ModelAuthOperation'] | ((agent: Schemas['Agent']) => Response), options: { state?: Partial<Schemas['ModelAuthState']>; answer?: (body: Schemas['ProjectModelLoginInput']) => Response | undefined } = {}) {
  const agent = grokAgent()
  const starts: unknown[] = [], answers: Schemas['ProjectModelLoginInput'][] = []
  const box: { state: Schemas['ModelAuthState'] } = { state: { credential: null, operation: null, ...options.state } }
  const open = vi.spyOn(window, 'open').mockReturnValue(null)
  const rendered = await renderConfig({ agent, handlers: [catalog, grokModels,
    http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json(box.state)),
    http.post(apiUrl(`/agents/${agent.id}/model-auth/operations`), async ({ request }) => {
      starts.push(await request.json())
      if (typeof start === 'function') return start(agent)
      const operation = { ...start, agentId: agent.id }
      box.state = { ...box.state, operation }
      return HttpResponse.json(operation, { status: 202 })
    }),
    http.post(apiUrl(`/agents/${agent.id}/model-auth/operations/:operationId/input`), async ({ request }) => {
      const body = (await request.json()) as Schemas['ProjectModelLoginInput']
      answers.push(body)
      const custom = options.answer?.(body)
      if (custom) return custom
      box.state = { ...box.state, operation: { ...box.state.operation!, prompt: null } }
      return HttpResponse.json(box.state)
    }),
    http.post(apiUrl(`/agents/${agent.id}/model-auth/operations/:operationId/cancel`), () => {
      box.state = { ...box.state, operation: { ...box.state.operation!, status: 'cancelled', authorizationUrl: null, deviceCode: null, prompt: null } }
      return HttpResponse.json(box.state.operation)
    }),
  ] })
  const login = async () => {
    const button = await within(area()).findByRole('button', { name: '登录 Grok CLI' })
    await waitFor(() => expect(button).toBeEnabled())
    await rendered.user.click(button)
  }
  return { ...rendered, agent, starts, answers, open, box, login }
}

describe('平台 Agent 登录插件提供方', () => {
  it('浏览器授权：按当前提供方发起，不预开空白窗口；地址通过检查后给原生链接，写明目标主机；取消等服务端确认', async () => {
    const url = 'https://accounts.x.ai/oauth2/auth?client_id=grok&state=s'
    const { starts, open, login, user } = await setup(grokLogin('', { status: 'awaiting_authorization', authorizationUrl: url }))
    expect(within(area()).queryByText(/不能在这里登录/)).toBeNull()
    await login()
    expect(starts).toEqual([{ provider: 'grok-cli', expectedConfigVersion: 1 }])
    const link = await within(area()).findByRole('link', { name: '打开授权页（accounts.x.ai）' })
    expect(link).toHaveAttribute('href', url)
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(open).not.toHaveBeenCalled()
    expect(within(area()).getByRole('button', { name: '登录 Grok CLI' })).toBeDisabled()
    await user.click(within(area()).getByRole('button', { name: '取消登录' }))
    expect(await within(area()).findByText('本次登录已取消')).toBeInTheDocument()
    expect(within(area()).queryByRole('link', { name: /打开授权页/ })).toBeNull()
  })

  it('授权地址没通过通用检查：不给链接，说明原因并可重新读取', async () => {
    const { login } = await setup(grokLogin('', { status: 'awaiting_authorization', authorizationUrl: 'http://accounts.x.ai/oauth2/auth?state=s' }))
    await login()
    expect(await within(area()).findByText('授权地址未通过安全校验，未打开未知地址，也不会自动重新发起登录。')).toBeInTheDocument()
    expect(within(area()).queryByRole('link', { name: /打开授权页/ })).toBeNull()
    expect(within(area()).getByRole('button', { name: '重新读取登录状态' })).toBeEnabled()
  })

  it('设备码：大号显示代码，链接到输入代码的地址并写明有效期', async () => {
    const { login } = await setup(grokLogin('', { status: 'awaiting_authorization', deviceCode: { userCode: 'GROK-1234', verificationUri: 'https://accounts.x.ai/device', expiresAt: '2099-10-14T09:00:00Z' } }))
    await login()
    expect(await within(area()).findByText('GROK-1234')).toBeInTheDocument()
    expect(within(area()).getByText('打开 accounts.x.ai，输入下面的代码完成登录。')).toBeInTheDocument()
    expect(within(area()).getByRole('link', { name: '打开 accounts.x.ai' })).toHaveAttribute('href', 'https://accounts.x.ai/device')
    expect(within(area()).getByText(/代码有效期至/)).toBeInTheDocument()
    expect(within(area()).getByRole('button', { name: '取消登录' })).toBeEnabled()
  })

  it('选择题：扩展原文按纯文本显示，不替人预选；提交选中的 id 后问题消失', async () => {
    const prompt: Schemas['ProjectModelLoginPrompt'] = { id: 'p1', kind: 'select', message: '<b>选择账号类型</b>', options: [{ id: 'personal', label: '个人' }, { id: 'team', label: '团队 <i>' }] }
    const { login, user, answers } = await setup(grokLogin('', { prompt }))
    await login()
    expect(await within(area()).findByText('登录需要你回答')).toBeInTheDocument()
    expect(within(area()).getByText('<b>选择账号类型</b>')).toBeInTheDocument()
    expect(within(area()).getByRole('radio', { name: '个人' })).not.toBeChecked()
    expect(within(area()).getByRole('radio', { name: '团队 <i>' })).not.toBeChecked()
    await user.click(within(area()).getByRole('button', { name: '提交' }))
    expect(within(area()).getByText('请选择一项')).toBeInTheDocument()
    expect(answers).toEqual([])
    await user.click(within(area()).getByRole('radio', { name: '团队 <i>' }))
    await user.click(within(area()).getByRole('button', { name: '提交' }))
    await waitFor(() => expect(answers).toEqual([{ promptId: 'p1', value: 'team' }]))
    await waitFor(() => expect(within(area()).queryByRole('radio')).toBeNull())
  })

  it('手工输入：提交后输入框清空，回答不进缓存也不留在页面里；422 时说明并可再答', async () => {
    const prompt: Schemas['ProjectModelLoginPrompt'] = { id: 'p2', kind: 'manual_code', message: '', options: [] }
    let reject = true
    const { login, user, answers, client } = await setup(grokLogin('', { prompt }), {
      answer: () => {
        if (!reject) return undefined
        reject = false
        return HttpResponse.json(errorBody('validation_failed', '授权码不对', { fields: [{ field: 'value', message: '授权码不对' }] }), { status: 422 })
      },
    })
    await login()
    const input = await within(area()).findByLabelText('浏览器回调到不了时，把浏览器地址栏里的回调地址，或页面上显示的授权码粘贴到这里。')
    await user.type(input, 'fixture-secret-code')
    await user.click(within(area()).getByRole('button', { name: '提交' }))
    expect(await within(area()).findByText(/授权码不对/)).toBeInTheDocument()
    expect(input).toHaveValue('')
    await user.type(input, 'fixture-second-code')
    await user.click(within(area()).getByRole('button', { name: '提交' }))
    await waitFor(() => expect(within(area()).queryByLabelText(/浏览器回调到不了时/)).toBeNull())
    expect(answers.map(answer => answer.value)).toEqual(['fixture-secret-code', 'fixture-second-code'])
    const caches = JSON.stringify([client.getQueryCache().getAll().map(q => q.state), client.getMutationCache().getAll().map(m => m.state), { ...localStorage, ...sessionStorage }])
    expect(caches).not.toContain('fixture-secret-code')
    expect(caches).not.toContain('fixture-second-code')
    expect(document.body.innerHTML).not.toContain('fixture-second-code')
  })

  it('文字题：没有说明时用默认标签，空着提交只提示', async () => {
    const { login, user, answers } = await setup(grokLogin('', { prompt: { id: 'p3', kind: 'text', message: '  ', options: [] } }))
    await login()
    await within(area()).findByLabelText('回答')
    await user.click(within(area()).getByRole('button', { name: '提交' }))
    expect(within(area()).getByText('请填写回答')).toBeInTheDocument()
    expect(answers).toEqual([])
  })

  it('成功：以服务端保存的账号为准，登录按钮变成“重新登录”', async () => {
    const { login, box, client, agent } = await setup(grokLogin('', { status: 'awaiting_authorization', authorizationUrl: 'https://accounts.x.ai/oauth2/auth?state=s' }))
    await login()
    await within(area()).findByRole('link', { name: '打开授权页（accounts.x.ai）' })
    box.state = { credential: makeModelAccount({ provider: 'grok-cli', accountLabel: null }), operation: grokLogin(agent.id, { status: 'succeeded', completedAt: '2026-10-14T08:05:00Z' }) }
    await act(() => client.invalidateQueries({ queryKey: modelAuthKey(agent.id, 'grok-cli', 1) }))
    expect(await within(area()).findByText('本次登录成功')).toBeInTheDocument()
    expect(within(area()).getByText('已登录', { exact: true })).toBeInTheDocument()
    expect(within(area()).getByRole('button', { name: '重新登录' })).toBeEnabled()
  })

  it.each([
    ['plugin_unavailable', '插件已卸载或停用，本次登录未完成。'],
    ['extension_load_failed', '插件的扩展加载失败，或者没有注册这个提供方的登录，本次登录未完成。'],
  ] as const)('失败（%s）：写明原因，可重新发起', async (errorCode, text) => {
    const agent = grokAgent()
    await renderConfig({ agent, handlers: [catalog, grokModels, http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: null, operation: grokLogin(agent.id, { status: 'failed', errorCode, completedAt: '2026-10-14T08:05:00Z' }) }))] })
    expect(await within(area()).findByText(text)).toBeInTheDocument()
    expect(within(area()).getByText('本次登录失败')).toBeInTheDocument()
    expect(within(area()).getByRole('button', { name: '重新发起登录' })).toBeEnabled()
  })

  it('409 plugin_not_enabled：说明插件还没授权给这个 Agent，不说“没有启用”', async () => {
    const { login } = await setup(() => HttpResponse.json(errorBody('plugin_not_enabled', '插件 grok 没有授权给这个 Agent'), { status: 409 }))
    await login()
    expect(await within(area()).findByText('插件「Grok 订阅」还没有授权给这个 Agent，本次没有发起登录。在本页的“插件权限”一节授权后再登录。')).toBeInTheDocument()
    expect(within(area()).queryByText(/插件还没有启用/)).toBeNull()
    expect(within(area()).getByText('本次登录失败')).toBeInTheDocument()
  })

  it('开着“用本机的登录状态”：active 说明用的是本机的登录，missing 醒目提示启动会失败并给设置入口', async () => {
    const active = await setup(grokLogin(''), { state: { credential: makeModelAccount({ provider: 'grok-cli', accountLabel: null }), localLogin: 'active' } })
    expect(await within(area()).findByText('已开启“用本机的登录状态”：这里用的是本机该扩展自己的登录，不是这个 Agent 的登录。')).toHaveAttribute('role', 'note')
    expect(within(area()).queryByRole('alert')).toBeNull()
    active.unmount()
    await setup(grokLogin(''), { state: { localLogin: 'missing' } })
    const missing = await within(area()).findByRole('alert')
    expect(missing).toHaveTextContent('已开启“用本机的登录状态”，但本机没有该扩展的登录，Agent 启动会失败。关闭这个开关，或在本机登录后再试。')
    expect(within(missing).getByRole('link', { name: '去本机模式设置' })).toHaveAttribute('href', '/settings#local-mode')
  })
})
