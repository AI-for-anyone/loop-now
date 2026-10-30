import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { agentsUrl, apiUrl, context, defaultAgentsPath, errorBody, makeAgent, makeStatus, toListItem } from '@/test/api'
import { providers, catalogHandlers } from '@/test/config'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { sidebar } from '@/test/shell'
import { chooseModel } from '@/test/model-picker'

function setup(onCreate: (body: Schemas['AgentCreate']) => Response) {
  const agents: Schemas['Agent'][] = []
  server.use(
    http.get(apiUrl('/context'), () => HttpResponse.json(context)),
    http.get(agentsUrl, () => HttpResponse.json({ agents: agents.map((a) => toListItem(a)) })),
    http.post(agentsUrl, async ({ request }) => {
      const body = (await request.json()) as Schemas['AgentCreate']
      const response = onCreate(body)
      if (response.status === 201) agents.push((await response.clone().json()) as Schemas['Agent'])
      return response
    }),
    http.get(apiUrl('/agents/:agentId'), ({ params }) => HttpResponse.json(agents.find((a) => a.id === params.agentId))),
    http.get(apiUrl('/agents/:agentId/status'), ({ params }) => HttpResponse.json(makeStatus(params.agentId as string))),
    http.get(apiUrl('/agents/:agentId/skills'), () => HttpResponse.json({ skills: [] })),
    http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: [] })),
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
    ...catalogHandlers,
  )
  const user = userEvent.setup()
  return { user, ...renderRoutes(routes, defaultAgentsPath) }
}

async function fill(user: ReturnType<typeof userEvent.setup>, name: string, pickModel = true) {
  // 没有 Agent 时主区的空状态也有一个“新建 Agent”，这里用左侧栏的“+”。
  await user.click(await within(sidebar()).findByRole('button', { name: '新建 Agent' }))
  const dialog = await screen.findByRole('dialog')
  await user.type(within(dialog).getByLabelText('显示名称'), name)
  if (pickModel) {
    await user.selectOptions(await within(dialog).findByLabelText('模型提供方'), 'anthropic')
    await chooseModel(user, dialog, 'claude-sonnet-5-5')
  }
  return dialog
}

describe('创建 Agent', () => {
  it('从同一模型目录选择支持的思考等级，创建时保存显式选择', async () => {
    let created: Schemas['AgentCreate'] | null = null
    const { user } = setup(body => { created = body; return HttpResponse.json(makeAgent({ name: body.name, model: body.model, thinkingLevel: body.thinkingLevel }), { status: 201 }) })
    const model: Schemas['Model'] = { id: 'claude-sonnet-5-5', name: 'Sonnet', reasoning: true, contextWindow: 100, maxTokens: 100, input: ['text'], thinkingLevels: ['low', 'high'], defaultThinkingLevel: 'low' }
    server.use(http.get(apiUrl('/model-providers/anthropic/models'), () => HttpResponse.json({ providerId: 'anthropic', models: [model] })))
    const dialog = await fill(user, '思考助手')
    const picker = within(dialog).getByLabelText('思考等级')
    expect(picker).toHaveDisplayValue('默认（低）')
    expect(within(picker).getAllByRole('option').map(option => option.getAttribute('value'))).toEqual(['', 'low', 'high'])
    await user.selectOptions(picker, 'high')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    await waitFor(() => expect(created).toEqual({ name: '思考助手', model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' }, thinkingLevel: 'high' }))
  })

  it('新建Codex独立选择速度与思考等级，切其他提供方再返回恢复标准并保留Prompt', async () => {
    let created: Schemas['AgentCreate'] | null = null
    const { user } = setup(body => { created = body; return HttpResponse.json(makeAgent({ name: body.name, model: body.model, serviceTier: body.serviceTier }), { status: 201 }) })
    server.use(
      http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, { id: 'openai-codex', source: 'builtin', modelCount: 1, apiKeyEnvVars: [], authMethods: ['oauth'] }] })),
      http.get(apiUrl('/model-providers/openai-codex/models'), () => HttpResponse.json({ providerId: 'openai-codex', models: [{ id: 'gpt-a', name: 'GPT A', reasoning: true, contextWindow: 100, maxTokens: 100, input: ['text'], thinkingLevels: ['medium', 'high'], defaultThinkingLevel: 'medium' }] })),
    )
    const dialog = await fill(user, '速度助手', false)
    const provider = within(dialog).getByLabelText('模型提供方')
    await user.selectOptions(provider, 'openai-codex'); await chooseModel(user, dialog, 'gpt-a')
    expect(within(dialog).getByLabelText('Codex 速度')).toHaveDisplayValue('标准')
    await user.selectOptions(within(dialog).getByLabelText('Codex 速度'), 'priority')
    await user.type(within(dialog).getByLabelText('Prompt'), '保留这份草稿')
    await user.selectOptions(provider, 'anthropic'); await chooseModel(user, dialog, 'claude-sonnet-5-5')
    expect(within(dialog).queryByLabelText('Codex 速度')).toBeNull()
    await user.selectOptions(provider, 'openai-codex')
    expect(within(dialog).getByLabelText('Codex 速度')).toHaveDisplayValue('标准')
    expect(within(dialog).getByLabelText('Prompt')).toHaveValue('保留这份草稿')
    await user.selectOptions(within(dialog).getByLabelText('Codex 速度'), 'ultrafast')
    await user.selectOptions(within(dialog).getByLabelText('思考等级'), 'high')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    await waitFor(() => expect(created).toEqual({ name: '速度助手', model: { provider: 'openai-codex', modelId: 'gpt-a' }, thinkingLevel: 'high', serviceTier: 'ultrafast', prompt: '保留这份草稿' }))
  })

  it('创建成功后选中新 Agent，打开配置页', async () => {
    let created: Schemas['AgentCreate'] | null = null
    const { user, router } = setup((body) => {
      created = body
      return HttpResponse.json(makeAgent({ name: body.name, model: body.model }), { status: 201 })
    })
    const dialog = await fill(user, '开发助手')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    await waitFor(() => expect(created).toEqual({ name: '开发助手', model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' } }))
    await waitFor(() => expect(router.state.location.pathname).toMatch(new RegExp(`^${defaultAgentsPath}/.+/config$`)))
    const list = await screen.findByRole('list', { name: 'Agent' })
    await waitFor(() => expect(within(list).getByRole('link', { name: /开发助手/ })).toHaveAttribute('aria-current', 'page'))
  })

  it('未选择模型时不提交', async () => {
    let calls = 0
    const { user } = setup(() => {
      calls += 1
      return HttpResponse.json({}, { status: 201 })
    })
    const dialog = await fill(user, '开发助手', false)
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    expect(await within(dialog).findByText('请选择模型')).toBeInTheDocument()
    expect(calls).toBe(0)
  })

  it('名称重复时在名称旁显示原因，保留内容', async () => {
    const { user } = setup(() => HttpResponse.json(errorBody('name_conflict', 'dup'), { status: 409 }))
    const dialog = await fill(user, '开发助手')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    expect(await within(dialog).findByText('项目内已有同名的 Agent，请换一个名称')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('显示名称')).toHaveValue('开发助手')
    expect(within(dialog).getByRole('combobox', { name: '模型' })).toHaveTextContent('claude-sonnet-5-5')
  })

  it('网络错误时显示无法连接，保留已填内容', async () => {
    const { user } = setup(() => HttpResponse.error())
    const dialog = await fill(user, '开发助手')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    expect(await within(dialog).findByText('无法连接服务端')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('显示名称')).toHaveValue('开发助手')
  })

  it('名称不符合命名规则时先在本地提示，不提交', async () => {
    let calls = 0
    const { user } = setup(() => {
      calls += 1
      return HttpResponse.json({}, { status: 201 })
    })
    const dialog = await fill(user, '前端/开发')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    expect(await within(dialog).findByText('名称不能含“/”或“#”')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('显示名称')).toHaveValue('前端/开发')
    expect(calls).toBe(0)
  })

  it('服务端按命名规则拒绝时，在名称旁显示原因', async () => {
    const { user } = setup(() =>
      HttpResponse.json(errorBody('validation_failed', 'bad', { fields: [{ field: 'name', message: '名称不能是 UUID 格式' }] }), { status: 422 }),
    )
    const dialog = await fill(user, '看起来正常')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    expect(await within(dialog).findByText('名称不能是 UUID 格式')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('显示名称')).toHaveValue('看起来正常')
  })

  it('服务端同时拒绝名称与模型时，两处原因都显示在对应输入旁', async () => {
    const { user } = setup(() =>
      HttpResponse.json(
        errorBody('validation_failed', 'bad', {
          fields: [
            { field: 'name', message: '名称不能是 UUID 格式' },
            { field: 'model.modelId', message: '提供方 anthropic 下没有模型 claude-sonnet-5-5' },
          ],
        }),
        { status: 422 },
      ),
    )
    const dialog = await fill(user, '看起来正常')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    expect(await within(dialog).findByText('名称不能是 UUID 格式')).toBeInTheDocument()
    expect(within(dialog).getByText('提供方 anthropic 下没有模型 claude-sonnet-5-5')).toBeInTheDocument()
    expect(within(dialog).queryByRole('alert')).toBeNull()
  })

  it('提供方的模型列表加载失败时显示原因', async () => {
    const { user } = setup(() => HttpResponse.json({}, { status: 201 }))
    server.use(http.get(apiUrl('/model-providers/openai/models'), () => HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 })))
    const dialog = await fill(user, '开发助手', false)
    await user.selectOptions(await within(dialog).findByLabelText('模型提供方'), 'openai')
    expect(await within(dialog).findByText('无法获取模型目录，暂时不能修改模型。')).toBeInTheDocument()
    expect(within(dialog).getByText(/^原因：服务端内部错误/)).toBeInTheDocument()
  })
})
