import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject, errorBody } from '@/test/api'
import { pluginProvider } from '@/test/config'
import { projectAccount, projectOperation } from '@/test/project-credentials'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { ProjectAccounts } from './ProjectAccounts'
import { ProjectLogin } from './ProjectLogin'

// 插件注册的提供方的项目登录：浏览器授权、设备码、登录中途的问题。
const grok = pluginProvider('grok-cli')
const grokOp = (overrides: Partial<Schemas['ProjectModelAuthOperation']> = {}) =>
  projectOperation({ id: '30000000-0000-7000-8000-0000000000a1', provider: 'grok-cli', name: 'Grok 账号', status: 'pending', authorizationUrl: null, ...overrides })

afterEach(() => vi.restoreAllMocks())

// 登录状态由测试控制：发起返回 start，之后每次读取返回 state（回答、取消改写它）。
function setup(start: Schemas['ProjectModelAuthOperation'], options: { account?: Schemas['ProjectCredentialAccount']; list?: Schemas['ProjectModelAuthOperation'][]; answer?: (body: Schemas['ProjectModelLoginInput']) => Response | undefined } = {}) {
  const starts: unknown[] = [], answers: Schemas['ProjectModelLoginInput'][] = []
  const box: { state: Schemas['ProjectModelAuthState'] | null } = { state: null }
  const open = vi.spyOn(window, 'open').mockReturnValue(null)
  server.use(
    http.get(apiUrl('/projects/:projectId/model-auth/operations'), ({ request }) => {
      expect(new URL(request.url).searchParams.has('provider')).toBe(false)
      return HttpResponse.json({ operations: options.list ?? [] })
    }),
    http.post(apiUrl('/projects/:projectId/model-auth/operations'), async ({ request }) => {
      starts.push(await request.json())
      box.state = { operation: start, account: null }
      return HttpResponse.json(box.state, { status: 202 })
    }),
    http.get(apiUrl('/projects/:projectId/model-auth/operations/:operationId'), () => HttpResponse.json(box.state)),
    http.post(apiUrl('/projects/:projectId/model-auth/operations/:operationId/input'), async ({ request }) => {
      const body = (await request.json()) as Schemas['ProjectModelLoginInput']
      answers.push(body)
      const custom = options.answer?.(body)
      if (custom) return custom
      box.state = { operation: { ...box.state!.operation, prompt: null }, account: null }
      return HttpResponse.json(box.state)
    }),
  )
  const user = userEvent.setup()
  const result = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} provider={grok} account={options.account} /></DraftNavigationGuard> }], '/')
  const begin = async () => {
    await user.type(screen.getByLabelText('新 Grok CLI 账号名称'), 'Grok 账号')
    await waitFor(() => expect(screen.getByRole('button', { name: '登录 Grok CLI' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '登录 Grok CLI' }))
  }
  return { ...result, user, starts, answers, open, box, begin }
}

describe('插件提供方的项目登录', () => {
  it('浏览器授权：按提供方 id 发起，不预开空白窗口；地址通过通用检查后给原生链接，写明目标主机', async () => {
    const op = grokOp({ status: 'awaiting_authorization', authorizationUrl: 'https://accounts.x.ai/oauth2/auth?client_id=grok&state=s' })
    const { starts, open, begin } = setup(op)
    await begin()
    expect(await screen.findByText('等待浏览器授权')).toBeInTheDocument()
    expect(starts).toEqual([{ provider: 'grok-cli', name: 'Grok 账号' }])
    expect(open).not.toHaveBeenCalled()
    const link = screen.getByRole('link', { name: '打开授权页（accounts.x.ai）' })
    expect(link).toHaveAttribute('href', op.authorizationUrl)
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(screen.getByLabelText('新 Grok CLI 账号名称')).toHaveAccessibleDescription(/打开授权页/)
  })

  it('地址不合通用检查（http、带用户名、带 fragment）时不给链接，说明未打开未知地址', async () => {
    for (const authorizationUrl of ['http://accounts.x.ai/auth', 'https://user:pw@accounts.x.ai/auth', 'https://accounts.x.ai/auth#token']) {
      const { begin, unmount } = setup(grokOp({ status: 'awaiting_authorization', authorizationUrl }))
      await begin()
      expect(await screen.findByText(/未打开未知地址，也不会自动重新发起登录/)).toBeInTheDocument()
      expect(screen.queryByRole('link', { name: /打开授权页/ })).toBeNull()
      unmount()
    }
  })

  it('设备码：大号显示代码，可复制；链接到输入代码的地址并写明主机；写明代码有效期', async () => {
    const { begin, user } = setup(grokOp({ status: 'awaiting_authorization', deviceCode: { userCode: 'ABCD-1234', verificationUri: 'https://x.ai/device', expiresAt: '2026-10-15T01:10:00Z' } }))
    await begin()
    expect(await screen.findByText('等待浏览器授权')).toBeInTheDocument()
    expect(screen.getByText('打开 x.ai，输入下面的代码完成登录。')).toBeInTheDocument()
    expect(screen.getByText('ABCD-1234')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '打开 x.ai' })).toHaveAttribute('href', 'https://x.ai/device')
    expect(screen.getByText(/^代码有效期至 /)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '复制' }))
    expect(await navigator.clipboard.readText()).toBe('ABCD-1234')
    expect(screen.getByRole('button', { name: '已复制' })).toBeInTheDocument()
    expect(screen.getByLabelText('新 Grok CLI 账号名称')).toHaveAccessibleDescription(/请按下方的提示继续本次登录/)
  })

  it('中途让人选一项：不替人预选，没选时提示；提交选项 id 后清空，回答不进任何缓存', async () => {
    const prompt: Schemas['ProjectModelLoginPrompt'] = { id: 'p1', kind: 'select', message: '用哪种方式登录？', options: [{ id: 'browser', label: '浏览器（本机）' }, { id: 'device', label: '<b>设备码</b>' }] }
    const { begin, user, answers, client } = setup(grokOp({ prompt }))
    await begin()
    expect(await screen.findByText('登录需要你回答')).toBeInTheDocument()
    const group = screen.getByRole('group', { name: '用哪种方式登录？' })
    const radios = within(group).getAllByRole('radio')
    expect(radios.map((radio) => (radio as HTMLInputElement).checked)).toEqual([false, false])
    expect(within(group).getByText('<b>设备码</b>')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '提交' }))
    expect(screen.getByText('请选择一项')).toBeInTheDocument()
    expect(answers).toEqual([])
    await user.click(within(group).getByRole('radio', { name: '<b>设备码</b>' }))
    await user.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(answers).toEqual([{ promptId: 'p1', value: 'device' }]))
    await waitFor(() => expect(screen.queryByText('登录需要你回答')).toBeNull())
    expect(screen.queryByRole('group', { name: '用哪种方式登录？' })).toBeNull()
    expect(client.getMutationCache().getAll()).toEqual([])
  })

  it('粘贴回调地址（manual_code，扩展没给说明）：用默认说明；提交后清空；回答不对时显示原因，回答不进查询缓存', async () => {
    const prompt: Schemas['ProjectModelLoginPrompt'] = { id: 'p2', kind: 'manual_code', message: '', options: [] }
    let reject = true
    const { begin, user, answers, client } = setup(grokOp({ prompt }), {
      answer: () => (reject ? HttpResponse.json(errorBody('validation_failed', 'value：回调地址不对', { fields: [{ field: 'value', message: '回调地址不对' }] }), { status: 422 }) : undefined),
    })
    await begin()
    const input = await screen.findByLabelText('浏览器回调到不了时，把浏览器地址栏里的回调地址，或页面上显示的授权码粘贴到这里。')
    await user.click(screen.getByRole('button', { name: '提交' }))
    expect(screen.getByText('请填写回答')).toBeInTheDocument()
    await user.type(input, 'http://localhost:56122/callback?code=secret-code-1')
    await user.click(screen.getByRole('button', { name: '提交' }))
    expect(await screen.findByText(/回调地址不对/)).toBeInTheDocument()
    expect(input).toHaveValue('')
    reject = false
    await user.type(input, 'http://localhost:56122/callback?code=secret-code-2')
    await user.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(answers.map((answer) => answer.value)).toEqual(['http://localhost:56122/callback?code=secret-code-1', 'http://localhost:56122/callback?code=secret-code-2']))
    await waitFor(() => expect(screen.queryByText('登录需要你回答')).toBeNull())
    const cached = JSON.stringify(client.getQueryCache().getAll().map((query) => query.state.data))
    expect(cached).not.toContain('secret-code')
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.variables))).not.toContain('secret-code')
    expect(document.body.innerHTML).not.toContain('secret-code')
  })

  it('填文字的问题显示扩展给的说明（纯文本），回答原样提交', async () => {
    const prompt: Schemas['ProjectModelLoginPrompt'] = { id: 'p3', kind: 'text', message: '输入组织名 <org>', options: [] }
    const { begin, user, answers } = setup(grokOp({ prompt }))
    await begin()
    await user.type(await screen.findByLabelText('输入组织名 <org>'), 'acme')
    await user.click(screen.getByRole('button', { name: '提交' }))
    await waitFor(() => expect(answers).toEqual([{ promptId: 'p3', value: 'acme' }]))
  })

  it('进行中的登录按 provider 与目标账号一起匹配：别的提供方的新建登录不锁住这里', async () => {
    const codexActive = projectOperation({ name: 'Codex 进行中' })
    const grokActive = grokOp({ id: '30000000-0000-7000-8000-0000000000a2', name: 'Grok 进行中', status: 'awaiting_authorization', authorizationUrl: 'https://accounts.x.ai/auth' })
    const { unmount } = setup(grokActive, { list: [codexActive] })
    await waitFor(() => expect(screen.getByRole('button', { name: '登录 Grok CLI' })).toBeDisabled())
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(screen.getByLabelText('新 Grok CLI 账号名称')).toBeEnabled()
    expect(screen.queryByText('等待浏览器授权')).toBeNull()
    unmount()
    setup(grokActive, { list: [codexActive, grokActive] })
    await waitFor(() => expect(screen.getByLabelText('新 Grok CLI 账号名称')).toHaveValue('Grok 进行中'))
    expect(screen.getByLabelText('新 Grok CLI 账号名称')).toBeDisabled()
  })

  it('失败说明新的错误码；插件没在项目启用时发起被拒并说明', async () => {
    const { begin, unmount } = setup(grokOp({ status: 'failed', errorCode: 'extension_load_failed' }))
    await begin()
    expect(await screen.findByText('插件的扩展加载失败，或者没有注册这个提供方的登录，本次登录未完成。')).toBeInTheDocument()
    unmount()
    server.use(http.post(apiUrl('/projects/:projectId/model-auth/operations'), () => HttpResponse.json(errorBody('plugin_not_enabled', '插件 grok 没有在这个项目启用'), { status: 409 })))
    renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} provider={grok} /></DraftNavigationGuard> }], '/')
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('新 Grok CLI 账号名称'), 'Grok 账号')
    await waitFor(() => expect(screen.getByRole('button', { name: '登录 Grok CLI' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '登录 Grok CLI' }))
    expect(await screen.findByText(/^插件还没有启用/)).toBeInTheDocument()
  })
})

describe('账号页里的插件提供方', () => {
  it('选插件提供方直接进入登录；已登录的插件账号行显示扩展给的名称并可重新登录（带账号与版本）', async () => {
    const account = projectAccount({ id: '10000000-0000-7000-8000-0000000000b1', provider: 'grok-cli', authMethod: 'oauth', status: 'logged_in', name: '我的 Grok', revision: 4 })
    const starts: unknown[] = []
    vi.spyOn(window, 'open').mockReturnValue(null)
    server.use(
      http.get(apiUrl('/model-providers'), () => HttpResponse.json({ piVersion: '1.4.0', catalogGeneratedAt: null, providers: [grok] })),
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts: [account] })),
      http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accountId: null, agents: [] })),
      http.get(apiUrl('/projects/:projectId/model-auth/operations'), () => HttpResponse.json({ operations: [] })),
      http.post(apiUrl('/projects/:projectId/model-auth/operations'), async ({ request }) => {
        starts.push(await request.json())
        return HttpResponse.json({ operation: grokOp({ targetAccountId: account.id, targetRevision: 4, status: 'failed', errorCode: 'auth_failed' }), account }, { status: 202 })
      }),
    )
    const user = userEvent.setup()
    renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="model" /></DraftNavigationGuard> }], '/')
    const row = (await screen.findByRole('heading', { name: '我的 Grok' })).closest('li')!
    await waitFor(() => expect(row).toHaveTextContent('Grok CLIgrok-cli'))
    await user.click(within(row).getByRole('button', { name: '重新登录' }))
    await waitFor(() => expect(starts).toEqual([{ provider: 'grok-cli', accountId: account.id, expectedRevision: 4 }]))
    await user.click(screen.getByRole('button', { name: '添加模型账号 · 提供方' }))
    await user.click(await screen.findByRole('option', { name: /Grok CLI/ }))
    expect(await screen.findByLabelText('新 Grok CLI 账号名称')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '登录 Grok CLI' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '录入 API Key' })).toBeNull()
  })
})
