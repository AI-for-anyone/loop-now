import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { createBrowserRouter, RouterProvider } from 'react-router'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { agentsUrl, apiUrl, defaultAgentPath, defaultAgentsPath, errorBody, makeAgent, toListItem } from '@/test/api'
import { pluginProvider, providers, renderConfig } from '@/test/config'
import { createTestQueryClient } from '@/test/render'
import { chooseModel } from '@/test/model-picker'

const region = (name: string) => screen.getByRole('region', { name })
const saveButton = () => within(region('基本信息')).getByRole('button', { name: '保存' })

describe('未保存修改的离开确认', () => {
  // On browser back/forward (POP navigation), popstate fires asynchronously; the memory router
  // fires it synchronously and cannot reproduce this, so a browser router is used here.
  it('浏览器后退时点“离开”能真正离开，不再弹出确认', async () => {
    const agent = makeAgent()
    // Borrow renderConfig to register the API handlers, then render again with a browser router.
    const { unmount, user } = await renderConfig({ agent })
    unmount()
    window.history.replaceState(null, '', defaultAgentPath(agent.id, 'chat'))
    const router = createBrowserRouter(routes)
    render(
      <QueryClientProvider client={createTestQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )
    try {
      await user.click(await screen.findByRole('link', { name: '配置' }))
      await user.type(await screen.findByLabelText('Agent Prompt'), '新的提示')
      window.history.back()
      const dialog = await screen.findByRole('alertdialog')
      expect(dialog).toHaveTextContent('有未保存的修改')
      await waitFor(() => expect(window.location.pathname).toMatch(/\/config$/))
      await user.click(within(dialog).getByRole('button', { name: '离开' }))
      await waitFor(() => expect(window.location.pathname).toMatch(/\/chat$/))
      await waitFor(() => expect(router.state.location.pathname).toMatch(/\/chat$/))
      // Wait until every async popstate has been handled, and confirm that navigation was not blocked again.
      await new Promise((resolve) => setTimeout(resolve, 50))
      expect(screen.queryByRole('alertdialog')).toBeNull()
      expect(window.location.pathname).toMatch(/\/chat$/)
    } finally {
      router.dispose()
    }
  })

  it('有未保存的修改时删除 Agent，删除后直接回到列表', async () => {
    let deleted = false
    const agent = makeAgent()
    const { user, router } = await renderConfig({
      agent,
      handlers: [
        // After the deletion the list no longer contains it: the project has no Agents left, so
        // the Agent page stays on the first-use empty state instead of jumping to another Agent.
        http.get(agentsUrl, () => HttpResponse.json({ agents: deleted ? [] : [toListItem(agent)] })),
        http.delete(apiUrl(`/agents/${agent.id}`), () => {
          deleted = true
          return new HttpResponse(null, { status: 204 })
        }),
      ],
    })
    await user.type(screen.getByLabelText('Agent Prompt'), '没保存的提示')
    const danger = region('删除 Agent')
    await waitFor(() => expect(within(danger).getByRole('button', { name: '删除这个 Agent' })).toBeEnabled())
    await user.click(within(danger).getByRole('button', { name: '删除这个 Agent' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '删除' }))
    await waitFor(() => expect(deleted).toBe(true))
    await waitFor(() => expect(router.state.location.pathname).toBe(defaultAgentsPath))
    expect(screen.queryByText('有未保存的修改，确定离开吗？离开后修改将丢失。')).toBeNull()
  })
})

describe('跟随服务端刷新', () => {
  it('没有本地改动时，表单换成服务端的最新值，不显示未保存', async () => {
    const agent = makeAgent()
    let current = agent
    const { client, user, router } = await renderConfig({ agent, handlers: [http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current))] })
    current = { ...agent, name: '改名后的助手', prompt: '别处改的提示', promptVersion: 2, model: { provider: 'openai', modelId: 'gpt-x' } }
    await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
    await waitFor(() => expect(screen.getByLabelText('显示名称')).toHaveValue('改名后的助手'))
    expect(screen.getByLabelText('Agent Prompt')).toHaveValue('别处改的提示')
    await waitFor(() => expect(screen.getByLabelText('模型提供方')).toHaveValue('openai'))
    expect(saveButton()).toBeDisabled()
    await user.click(screen.getByRole('link', { name: '对话' }))
    await waitFor(() => expect(router.state.location.pathname).toMatch(/\/chat$/))
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('有本地改动时保留人的编辑并提示，保存只提交改过的项', async () => {
    let saved: unknown = null
    const agent = makeAgent()
    let current = agent
    const { client, user } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          saved = await request.json()
          current = { ...current, ...(saved as Schemas['AgentUpdate']) }
          return HttpResponse.json(current)
        }),
      ],
    })
    await user.clear(screen.getByLabelText('显示名称'))
    await user.type(screen.getByLabelText('显示名称'), '我的名字')
    current = { ...agent, name: '改名后的助手', prompt: '别处改的提示' }
    await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
    // The server's new prompt reaches the input only after the prompt editor syncs it, so wait
    // for it to arrive; the name the person edited never changes, so it can't be used to wait on.
    await waitFor(() => expect(screen.getByLabelText('Agent Prompt')).toHaveValue(current.prompt))
    expect(screen.getByLabelText('显示名称')).toHaveValue('我的名字')
    expect(within(region('基本信息')).getByText(/服务端的配置已更新/)).toBeInTheDocument()
    await user.click(saveButton())
    await waitFor(() => expect(saved).toEqual({ name: '我的名字' }))
    await waitFor(() => expect(within(region('基本信息')).queryByText(/服务端的配置已更新/)).toBeNull())
    await waitFor(() => expect(saveButton()).toBeDisabled())
  })

  it('有本地改动时，别处只改了人没改过的项也提示；之后保存自己的修改不再提示', async () => {
    const agent = makeAgent()
    let current = agent
    const { client, user } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          current = { ...current, ...((await request.json()) as Schemas['AgentUpdate']) }
          return HttpResponse.json(current)
        }),
      ],
    })
    await user.clear(screen.getByLabelText('显示名称'))
    await user.type(screen.getByLabelText('显示名称'), '我的名字')
    current = { ...agent, model: { provider: 'openai', modelId: 'gpt-x' } }
    await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
    // Same as above: wait for the notice that the server config was updated, not for the name, which never changes.
    await waitFor(() => expect(within(region('基本信息')).getByText(/服务端的配置已更新/)).toBeInTheDocument())
    expect(screen.getByLabelText('显示名称')).toHaveValue('我的名字')
    expect(screen.getByLabelText('Agent Prompt')).toHaveValue(current.prompt)
    await user.click(saveButton())
    await waitFor(() => expect(saveButton()).toBeDisabled())
    await user.type(screen.getByLabelText('显示名称'), '再改')
    expect(within(region('基本信息')).queryByText(/服务端的配置已更新/)).toBeNull()
  })
})

describe('模型选择', () => {
  it('切到别的提供方再切回时恢复原模型，目录中没有的模型也能恢复并保存其他修改', async () => {
    let saved: unknown = null
    const agent = makeAgent({ model: { provider: 'anthropic', modelId: 'retired-model' } })
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          saved = await request.json()
          return HttpResponse.json(agent)
        }),
      ],
    })
    const basic = region('基本信息')
    expect(await within(basic).findByText('当前模型不在服务端的模型目录中')).toBeInTheDocument()
    await user.type(screen.getByLabelText('显示名称'), '2')
    const provider = within(basic).getByLabelText('模型提供方')
    await user.selectOptions(provider, 'openai')
    expect(within(basic).queryByText('当前模型不在服务端的模型目录中')).toBeNull()
    await user.selectOptions(provider, 'anthropic')
    expect(await within(basic).findByText('当前模型不在服务端的模型目录中')).toBeInTheDocument()
    expect(within(basic).getByText('anthropic / retired-model')).toBeInTheDocument()
    expect(saveButton()).toBeEnabled()
    await user.click(saveButton())
    await waitFor(() => expect(saved).toEqual({ name: '开发助手2' }))
  })

  it('某个提供方的模型列表加载失败时显示原因，并保留显示当前模型', async () => {
    await renderConfig({
      handlers: [http.get(apiUrl('/model-providers/anthropic/models'), () => HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 }))],
    })
    const basic = region('基本信息')
    expect(await within(basic).findByText('无法获取模型目录，暂时不能修改模型。')).toBeInTheDocument()
    expect(within(basic).getByText(/^原因：服务端内部错误/)).toBeInTheDocument()
    expect(within(basic).getByText('anthropic / claude-sonnet-5-5')).toBeInTheDocument()
  })

  it('提供方列表加载失败时同样显示原因', async () => {
    await renderConfig({
      handlers: [http.get(apiUrl('/model-providers'), () => HttpResponse.error())],
    })
    const basic = region('基本信息')
    expect(await within(basic).findByText('原因：无法连接服务端')).toBeInTheDocument()
    expect(within(basic).getByText('anthropic / claude-sonnet-5-5')).toBeInTheDocument()
  })

  it('服务端对模型的字段级校验错误显示在模型选择旁，其余字段错误全部列出', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.patch(apiUrl(`/agents/${agent.id}`), () =>
          HttpResponse.json(
            errorBody('validation_failed', 'model.modelId: Provider anthropic has no model claude-haiku-5-5', {
              fields: [
                { field: 'model.modelId', message: 'Provider anthropic has no model claude-haiku-5-5' },
                { field: 'prompt', message: 'Prompt is too long' },
                { field: 'other', message: 'Unknown field' },
              ],
            }),
            { status: 422 },
          ),
        ),
      ],
    })
    const basic = region('基本信息')
    await chooseModel(user, basic, 'claude-haiku-5-5')
    await user.click(saveButton())
    expect(await within(basic).findByText('Provider anthropic has no model claude-haiku-5-5')).toBeInTheDocument()
    const alert = within(basic).getByRole('alert')
    expect(alert).toHaveTextContent('prompt：Prompt is too long')
    expect(alert).toHaveTextContent('other：Unknown field')
    expect(alert).not.toHaveTextContent('model.modelId')
  })
})

describe('插件提供方', () => {
  const grokModels = http.get(apiUrl('/model-providers/:providerId/models'), ({ params }) =>
    HttpResponse.json({ providerId: params.providerId, models: [{ id: 'grok-4', name: 'Grok 4', reasoning: false, contextWindow: 1, maxTokens: 1, input: ['text'] }] }),
  )
  const catalog = (plugins: Schemas['ModelProvider'][]) => ({ ...providers, providers: [...providers.providers, ...plugins] })
  const option = (select: HTMLElement, value: string) => within(select).getAllByRole('option').find((item) => (item as HTMLOptionElement).value === value) as HTMLOptionElement

  it('带 agentId 读目录：不能用的插件提供方带原因并禁用，全局停用全部插件的仍可选但提示启动会失败', async () => {
    const agent = makeAgent()
    let asked: string | null = null
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl('/model-providers'), ({ request }) => {
          asked = new URL(request.url).searchParams.get('agentId')
          return HttpResponse.json(
            catalog([
              pluginProvider('grok-cli', { unavailableReason: 'agent_not_granted' }),
              pluginProvider('grok-off', { name: null, unavailableReason: 'plugins_disabled' }),
              pluginProvider('grok-ok', { name: 'Grok OK' }),
            ]),
          )
        }),
        grokModels,
      ],
    })
    const basic = region('基本信息')
    const select = within(basic).getByLabelText('模型提供方')
    await waitFor(() => expect(option(select, 'grok-cli')).toBeDefined())
    expect(asked).toBe(agent.id)
    expect(option(select, 'anthropic')).toHaveTextContent(/^anthropic（2 个模型）$/)
    expect(option(select, 'grok-cli')).toHaveTextContent('Grok CLI · grok-cli（插件「Grok 订阅」，2 个模型） — 插件未授权给这个 Agent')
    expect(option(select, 'grok-cli')).toBeDisabled()
    expect(option(select, 'grok-ok')).toHaveTextContent(/^Grok OK · grok-ok（插件「Grok 订阅」，2 个模型）$/)
    expect(option(select, 'grok-ok')).toBeEnabled()
    expect(option(select, 'grok-off')).toHaveTextContent('grok-off（插件「Grok 订阅」，2 个模型） — 插件已全部停用')
    expect(option(select, 'grok-off')).toBeEnabled()
    const disabledNote = '全局设置里已停用全部插件：可以保存，但 Agent 启动时会失败。'
    expect(within(basic).queryByText(disabledNote)).toBeNull()
    await user.selectOptions(select, 'grok-off')
    expect(within(basic).getByText(disabledNote)).toHaveAttribute('role', 'note')
    await user.selectOptions(select, 'grok-ok')
    expect(within(basic).queryByText(disabledNote)).toBeNull()
  })

  it('Agent 正在用的插件提供方现在不能用：选择器下方说明原因和去哪里处理', async () => {
    const agent = makeAgent({ model: { provider: 'grok-cli', modelId: 'grok-4' } })
    await renderConfig({ agent, handlers: [http.get(apiUrl('/model-providers'), () => HttpResponse.json(catalog([pluginProvider('grok-cli', { unavailableReason: 'plugin_not_enabled' })]))), grokModels] })
    const basic = region('基本信息')
    expect(await within(basic).findByText('项目还没有启用插件「Grok 订阅」。在项目的插件页启用它、再在本页的“插件权限”一节授权给这个 Agent 后才能选。')).toHaveAttribute('role', 'note')
    expect(within(basic).getByLabelText('模型提供方')).toHaveValue('grok-cli')
  })

  it('平台 Agent：授权了插件就能选它的提供方；没授权时选项禁用，正在用的那个说明去本页授权', async () => {
    const agent = makeAgent({ scope: 'platform', projectId: null, model: { provider: 'grok-cli', modelId: 'grok-4' } })
    // The model credential section also reads the catalog (without agentId), so record every read here.
    const asked: (string | null)[] = []
    await renderConfig({ agent, handlers: [http.get(apiUrl('/model-providers'), ({ request }) => {
      asked.push(new URL(request.url).searchParams.get('agentId'))
      return HttpResponse.json(catalog([pluginProvider('grok-cli', { unavailableReason: 'agent_not_granted' }), pluginProvider('grok-ok', { name: 'Grok OK' })]))
    }), grokModels] })
    const basic = region('基本信息')
    const select = within(basic).getByLabelText('模型提供方')
    await waitFor(() => expect(option(select, 'grok-cli')).toBeDisabled())
    expect(asked).toContain(agent.id)
    expect(option(select, 'grok-cli')).toHaveTextContent('Grok CLI · grok-cli（插件「Grok 订阅」，2 个模型） — 插件未授权给这个 Agent')
    expect(option(select, 'grok-ok')).toBeEnabled()
    expect(option(select, 'grok-ok')).toHaveTextContent(/^Grok OK · grok-ok（插件「Grok 订阅」，2 个模型）$/)
    expect(within(basic).getByText('插件「Grok 订阅」还没有授权给这个 Agent。在本页的“插件权限”一节授权后才能选。')).toHaveAttribute('role', 'note')
    expect(within(basic).queryByText(/只给项目 Agent 用/)).toBeNull()
  })

  it('保存时 422（model.provider）：说明显示在模型旁，目录重新读取后标出原因', async () => {
    const agent = makeAgent()
    let granted = true
    let reads = 0
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl('/model-providers'), () => {
          reads += 1
          return HttpResponse.json(catalog([pluginProvider('grok-cli', { unavailableReason: granted ? null : 'agent_not_granted' })]))
        }),
        grokModels,
        http.patch(apiUrl(`/agents/${agent.id}`), () => {
          granted = false
          return HttpResponse.json(errorBody('validation_failed', 'Plugin grok is not granted to this Agent', { fields: [{ field: 'model.provider', message: 'Plugin grok is not granted to this Agent' }] }), { status: 422 })
        }),
      ],
    })
    const basic = region('基本信息')
    const select = within(basic).getByLabelText('模型提供方')
    await waitFor(() => expect(option(select, 'grok-cli')).toBeEnabled())
    await user.selectOptions(select, 'grok-cli')
    await chooseModel(user, basic, 'grok-4')
    const before = reads
    await user.click(saveButton())
    expect(await within(basic).findByText('Plugin grok is not granted to this Agent')).toBeInTheDocument()
    await waitFor(() => expect(option(select, 'grok-cli')).toBeDisabled())
    expect(reads).toBeGreaterThan(before)
    expect(within(basic).getByText('插件「Grok 订阅」还没有授权给这个 Agent。在本页的“插件权限”一节授权后才能选。')).toHaveAttribute('role', 'note')
  })
})
