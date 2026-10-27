import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { agentsUrl, apiUrl, defaultAgentPath, errorBody, makeAgent, makeRun, makeStatus, platformAgentsUrl, toListItem } from '@/test/api'
import { renderChat } from '@/test/chat'
import { makeSleep } from '@/test/sleep'
import { providers } from '@/test/config'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const heading = (name: string) => screen.findByRole('heading', { level: 1, name })

describe('切换 Agent', () => {
  // 项目 Agent 的模型账号在项目里维护；直接录入 API Key 的对话框只在平台 Agent 上。
  it('后退到已缓存的 Agent 时，不保留上一个 Agent 的凭证对话框，也不会提交到这个 Agent', async () => {
    const a = makeAgent({ name: 'A 助手', scope: 'platform', projectId: null })
    const b = makeAgent({ name: 'B 助手', scope: 'platform', projectId: null })
    const configPath = (agent: Schemas['Agent']) => `/platform/agents/${agent.id}/config`
    const puts: string[] = []
    server.use(
      http.put(apiUrl('/agents/:agentId/model-credential'), ({ params }) => {
        puts.push(String(params.agentId))
        return HttpResponse.json(errorBody('internal_error'), { status: 500 })
      }),
      http.get(apiUrl('/model-providers'), () => HttpResponse.json(providers)),
      http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [a, b].map((agent) => toListItem(agent)) })),
      http.get(agentsUrl, () => HttpResponse.json({ agents: [] })),
      ...baseHandlers([a, b]),
    )
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, configPath(b))
    await heading('B 助手')
    await act(() => router.navigate(configPath(a)))
    await heading('A 助手')
    const section = await screen.findByRole('region', { name: '模型账号凭证' })
    await user.click(await within(section).findByRole('button', { name: '录入 API Key' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('API Key'), 'sk-for-a')

    // 浏览器后退（POP）到之前看过、已在缓存中的 B。
    await act(() => router.navigate(-1))
    await heading('B 助手')
    expect(router.state.location.pathname).toBe(configPath(b))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.body.innerHTML).not.toContain('sk-for-a')
    expect(puts).toEqual([])
  })

  it('切换到已缓存的 Agent 时，不带上上一个 Agent 的对话草稿', async () => {
    const a = makeAgent({ name: 'A 助手' })
    const b = makeAgent({ name: 'B 助手' })
    const posts: string[] = []
    server.use(
      http.post(apiUrl('/agents/:agentId/messages'), ({ params }) => {
        posts.push(String(params.agentId))
        return HttpResponse.json(errorBody('internal_error'), { status: 500 })
      }),
      ...baseHandlers([a, b]),
    )
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentPath(b.id, 'chat'))
    await heading('B 助手')
    await act(() => router.navigate(defaultAgentPath(a.id, 'chat')))
    await heading('A 助手')
    await user.type(await screen.findByRole('textbox', { name: '消息' }), '给 A 的草稿')

    await act(() => router.navigate(-1))
    await heading('B 助手')
    const input = await screen.findByRole('textbox', { name: '消息' })
    expect(input).toHaveValue('')
    await user.type(input, '{Enter}')
    expect(posts).toEqual([])
  })
})

describe('后台刷新失败', () => {
  it('已有数据时保留页签内容与未保存的编辑，在上方提示原因', async () => {
    const agent = makeAgent({ name: 'A 助手' })
    let fail = false
    server.use(
      http.get(apiUrl(`/agents/${agent.id}`), () => (fail ? HttpResponse.json(errorBody('internal_error'), { status: 500 }) : HttpResponse.json(agent))),
      ...baseHandlers([agent]),
    )
    const user = userEvent.setup()
    const { client } = renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const prompt = await screen.findByLabelText('Agent Prompt')
    await user.type(prompt, '还没保存的提示')

    fail = true
    await act(() => client.refetchQueries({ queryKey: keys.agent(agent.id) }))
    expect(await screen.findByText('服务端内部错误')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: 'A 助手' })).toBeInTheDocument()
    expect(screen.getByLabelText('Agent Prompt')).toHaveValue('还没保存的提示')
  })

  it('没有数据时显示整页错误', async () => {
    const agent = makeAgent()
    server.use(http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(errorBody('internal_error'), { status: 500 })), ...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    expect(await screen.findByText('服务端内部错误')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('heading', { level: 1 })).toBeNull())
  })
})

describe('头部的状态标签', () => {
  const sticker = () => document.querySelector<HTMLElement>('[data-agent-status]')

  // 在配置页签打开 Agent，状态标签取定期查询的状态接口。
  async function renderConfigWith(status: Partial<Schemas['AgentStatus']>) {
    const agent = makeAgent({ name: 'A 助手' })
    if (status.sleep) status = { ...status, sleep: { ...status.sleep, agentId: agent.id } }
    server.use(http.get(apiUrl(`/agents/${agent.id}/status`), () => HttpResponse.json(makeStatus(agent.id, status))), ...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    await heading('A 助手')
  }

  it('会话丢失时显示会话丢失，即使 Agent 正忙', async () => {
    await renderConfigWith({ busy: true, currentRun: makeRun({ status: 'running' }), session: { state: 'missing', epoch: 1 } })
    await waitFor(() => expect(sticker()?.textContent).toBe('会话丢失'))
    expect(sticker()).toHaveAttribute('data-tone', 'danger')
    expect(sticker()).toHaveAttribute('role', 'status')
  })

  it('正忙时加上当前执行的阶段', async () => {
    await renderConfigWith({ busy: true, currentRun: makeRun({ status: 'starting', phase: 'preparing' }) })
    await waitFor(() => expect(sticker()?.textContent).toBe('正忙准备运行环境'))
    expect(sticker()).toHaveAttribute('data-tone', 'busy')
  })

  it('空闲时显示空闲', async () => {
    await renderConfigWith({})
    await waitFor(() => expect(sticker()?.textContent).toBe('空闲'))
    expect(sticker()).toHaveAttribute('data-tone', 'success')
  })

  it('空闲睡眠时保留唤醒时间、倒计时和原因', async () => {
    const wakeAt = new Date(Date.now() + 120_000).toISOString()
    await renderConfigWith({ sleepRevision: 2, sleep: makeSleep({ wakeAt, reason: '等待外部确认后继续' }) })
    await screen.findByText('等待外部确认后继续')
    expect(document.querySelector('time[datetime="' + wakeAt + '"]')).toBeInTheDocument()
    expect(sticker()?.textContent).toBe('空闲')
    expect(document.querySelector('time[datetime="' + wakeAt + '"]')?.parentElement?.textContent).toMatch(/剩余.*秒/)
  })

  it('对话页收到快照后以实时状态为准，离开对话页后回到定期查询的状态', async () => {
    const agent = makeAgent({ name: 'A 助手' })
    // 状态接口一直报告空闲（例如还没有轮到下一次查询）。
    const { router, emit } = await renderChat({ agent, handlers: baseHandlers([agent]) })
    await waitFor(() => expect(sticker()?.textContent).toBe('空闲'))

    const run = makeRun({ status: 'running', endedAt: null })
    await emit('snapshot', { status: makeStatus(agent.id, { busy: true, currentRun: run }), currentText: '', queuedRuns: [], queueItems: [] })
    await waitFor(() => expect(sticker()?.textContent).toBe('正忙回复中'))
    expect(sticker()).toHaveAttribute('data-tone', 'busy')
    await emit('run.updated', { run: { ...run, status: 'aborting' } })
    await waitFor(() => expect(sticker()?.textContent).toBe('正忙正在打断'))

    await act(() => router.navigate(defaultAgentPath(agent.id, 'config')))
    await screen.findByLabelText('Agent Prompt')
    await waitFor(() => expect(sticker()?.textContent).toBe('空闲'))
  })

  it('实时状态为空闲时，不沿用状态接口中过时的正忙', async () => {
    const agent = makeAgent()
    const { emit } = await renderChat({ agent, status: { busy: true, currentRun: makeRun({ status: 'running' }) } })
    await waitFor(() => expect(sticker()?.textContent).toBe('正忙回复中'))
    await emit('snapshot', { status: makeStatus(agent.id), currentText: null, queuedRuns: [], queueItems: [] })
    await waitFor(() => expect(sticker()?.textContent).toBe('空闲'))
  })
})
