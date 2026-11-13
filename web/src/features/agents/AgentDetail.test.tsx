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

describe('头部的运行时标记', () => {
  it('Claude Code 的 Agent 在名称旁标出运行时；Pi 的不标', async () => {
    const claude = makeAgent({ name: '本机助手', model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code' })
    const pi = makeAgent({ name: 'Pi 助手' })
    server.use(...baseHandlers([claude, pi]))
    const { router } = renderRoutes(routes, defaultAgentPath(claude.id, 'config'))
    const header = (await heading('本机助手')).closest('header')!
    expect(within(header).getByTitle('运行时：本机的 Claude Code')).toHaveTextContent(/^Claude Code$/)
    await act(() => router.navigate(defaultAgentPath(pi.id, 'config')))
    const piHeader = (await heading('Pi 助手')).closest('header')!
    expect(within(piHeader).queryByTitle('运行时：本机的 Claude Code')).toBeNull()
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

// 改版“一张印出来的纸”：页头、索引标签与纸张。
describe('头部与页签的版式', () => {
  async function open(agent: Schemas['Agent'], tab: 'chat' | 'config' = 'config') {
    server.use(...baseHandlers([agent]))
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentPath(agent.id, tab))
    const title = await heading(agent.name)
    return { user, title, header: title.closest('header')! }
  }

  it('标题是展示字（窄屏照旧最大 60px，宽屏收到 40px，错版投影保留），Manager 标记与状态标签在它旁边', async () => {
    const { title, header } = await open(makeAgent({ name: 'manager', isManager: true }))
    expect(title).toHaveClass('font-display-cn', 'text-[clamp(38px,6vw,60px)]', 'text-riso-blue', '[text-shadow:4px_4px_0_var(--riso-pink)]')
    expect(title).toHaveClass('nav:text-[40px]', 'nav:leading-none', 'nav:[text-shadow:3px_3px_0_var(--riso-pink)]')
    expect(within(header).getByTitle('项目 Manager')).toHaveTextContent('Manager')
    await waitFor(() => expect(header.querySelector('[data-agent-status]')).toHaveTextContent('空闲'))
  })

  it('模型写在等宽的浅蓝标签里；地址在虚线框里，旁边的按钮复制地址并提示已复制', async () => {
    const { user, header } = await open(makeAgent({ name: 'A 助手', address: '测试组织/测试项目/A 助手' }))
    expect(within(header).getByText('anthropic · claude-sonnet-5-5')).toHaveClass('font-mono', 'bg-tint-blue')
    const address = within(header).getByText('测试组织/测试项目/A 助手')
    expect(address).toHaveClass('font-mono')
    expect(address.parentElement).toHaveClass('border-dashed')
    const copy = within(header).getByRole('button', { name: '复制地址' })
    await user.click(copy)
    expect(await navigator.clipboard.readText()).toBe('测试组织/测试项目/A 助手')
    expect(await within(header).findByText('已复制')).toBeInTheDocument()
    expect(copy).toHaveAccessibleName('复制地址')
  })

  it('没有地址时说明原因，不显示复制按钮', async () => {
    const { header } = await open(makeAgent({ name: 'old/bot', address: null }))
    expect(within(header).getByText(/这个 Agent 暂时没有地址/)).toBeInTheDocument()
    expect(within(header).queryByRole('button', { name: '复制地址' })).toBeNull()
  })

  it('右上角的叠印装饰读屏不读；页头够宽才显示，显示时文字让出它的位置，不压在它上面', async () => {
    const { header, title } = await open(makeAgent({ name: 'A 助手' }))
    const deco = header.querySelector<HTMLElement>('[data-overprint-deco]')!
    expect(deco).toHaveAttribute('aria-hidden', 'true')
    expect(deco).toHaveClass('pointer-events-none', 'hidden', '@[820px]:block')
    // 宽屏的紧凑页头里装饰缩小到和页头同高（整体缩到 0.6 倍，贴着右上角）。
    expect(deco).toHaveClass('nav:top-2', 'nav:scale-[0.6]', 'origin-top-right')
    // 页头是容器：按它自己的宽度决定，而不是按窗口宽度。
    expect(header).toHaveClass('@container')
    // 标题、标记、模型与地址所在的那一块给装饰让出位置：窄屏照旧 316px，宽屏装饰缩小后让出 196px。
    const row = title.closest('[data-agent-title-row]')!
    expect(row).toHaveClass('@[820px]:pr-[316px]', 'nav:@[820px]:pr-[196px]')
    expect(row).toContainElement(within(header).getByText('anthropic · claude-sonnet-5-5'))
  })

  it('宽屏时页头紧凑：标题右边一行是身份、状态、运行时、模型和地址，放不下时换行；面包屑更紧；纸张上沿离窗口顶部更近', async () => {
    const { title, header } = await open(makeAgent({ name: 'manager', isManager: true, address: '测试组织/测试项目/manager' }))
    expect(header).toHaveClass('nav:gap-1.5', 'nav:pt-3', 'nav:pb-2.5')
    const row = title.closest('[data-agent-title-row]')!
    // 窄屏照旧两行（标题与标记一行，模型与地址一行）；宽屏两行的内容排进同一行，放不下时换行。
    expect(row).toHaveClass('flex-col', 'nav:flex-row', 'nav:flex-wrap', 'nav:items-center')
    const rows = [...row.children]
    expect(rows).toHaveLength(2)
    for (const inner of rows) expect(inner).toHaveClass('nav:contents')
    expect(rows[0]).toContainElement(title)
    expect(rows[1]).toContainElement(within(header).getByText('测试组织/测试项目/manager'))
    expect(within(header).getByRole('navigation', { name: '页面位置' }).querySelector('ol')).toHaveClass('nav:leading-4')
  })

  it('四个页签是贴在纸张上沿的索引标签：当前页黄色，其余米灰色', async () => {
    const agent = makeAgent({ name: 'A 助手' })
    await open(agent)
    const tabs = screen.getByRole('navigation', { name: 'A 助手' })
    expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['对话', '配置', '文件夹', '收件箱'])
    const current = within(tabs).getByRole('link', { name: '配置' })
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(current).toHaveClass('h-10', 'bg-riso-yellow', 'border-b-0')
    expect(within(tabs).getByRole('link', { name: '对话' })).toHaveClass('h-[34px]', 'bg-paper-tab')
    expect(within(tabs).getByRole('link', { name: '对话' })).toHaveAttribute('href', defaultAgentPath(agent.id, 'chat'))
  })

  it('页签内容在一张纸里，页头和页签在纸的外面', async () => {
    const { header } = await open(makeAgent({ name: 'A 助手' }))
    const tabs = screen.getByRole('navigation', { name: 'A 助手' })
    const sheet = tabs.nextElementSibling as HTMLElement
    expect(sheet).toHaveAttribute('data-sheet')
    expect(sheet).toContainElement(await screen.findByRole('heading', { name: '基本信息' }))
    expect(sheet).not.toContainElement(header)
    expect(header.closest('[data-sheet]')).toBeNull()
  })
})
