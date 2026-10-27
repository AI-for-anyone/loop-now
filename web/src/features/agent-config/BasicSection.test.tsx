import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { createBrowserRouter, RouterProvider } from 'react-router'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { agentsUrl, apiUrl, defaultAgentPath, defaultAgentsPath, errorBody, makeAgent, toListItem } from '@/test/api'
import { renderConfig } from '@/test/config'
import { createTestQueryClient } from '@/test/render'
import { chooseModel } from '@/test/model-picker'

const region = (name: string) => screen.getByRole('region', { name })
const saveButton = () => within(region('基本信息')).getByRole('button', { name: '保存' })

describe('未保存修改的离开确认', () => {
  // 浏览器的后退/前进（POP 导航）中，popstate 是异步触发的；内存路由同步触发，复现不了，这里用浏览器路由。
  it('浏览器后退时点“离开”能真正离开，不再弹出确认', async () => {
    const agent = makeAgent()
    // 借用 renderConfig 注册接口处理器，再换成浏览器路由渲染。
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
      // 等异步的 popstate 都处理完，确认没有再次被拦下。
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
        // 删除后列表里不再有它：项目没有 Agent 了，Agent 页停在首次使用的空状态，不跳到别的 Agent。
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
    // 服务端的新提示词要经提示词编辑器的同步才进输入框，等它到达；人改过的名称一直不变，不能拿它来等。
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
    // 同上：等服务端更新的提示出现，不拿一直不变的名称来等。
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
            errorBody('validation_failed', 'model.modelId：提供方 anthropic 下没有模型 claude-haiku-5-5', {
              fields: [
                { field: 'model.modelId', message: '提供方 anthropic 下没有模型 claude-haiku-5-5' },
                { field: 'prompt', message: 'Prompt 太长' },
                { field: 'other', message: '其他问题' },
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
    expect(await within(basic).findByText('提供方 anthropic 下没有模型 claude-haiku-5-5')).toBeInTheDocument()
    const alert = within(basic).getByRole('alert')
    expect(alert).toHaveTextContent('prompt：Prompt 太长')
    expect(alert).toHaveTextContent('other：其他问题')
    expect(alert).not.toHaveTextContent('model.modelId')
  })
})
