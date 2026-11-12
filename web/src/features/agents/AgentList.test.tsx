import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { config } from '@/config'
import { agentsUrl, apiUrl, defaultAgentPath, defaultAgentsPath, makeAgent, toListItem } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, sidebar } from '@/test/shell'
import { makeSleep } from '@/test/sleep'

const names = () =>
  within(screen.getByRole('list', { name: 'Agent' }))
    .queryAllByRole('link')
    .map((link) => link.textContent ?? '')

function setup(agents: Schemas['Agent'][], path = defaultAgentsPath) {
  let listCalls = 0
  const listed = [...agents]
  server.use(
    http.get(agentsUrl, () => {
      listCalls += 1
      return HttpResponse.json({ agents: listed.map((a) => toListItem(a)) })
    }),
    ...baseHandlers(agents),
  )
  const user = userEvent.setup()
  return { user, listed, listCalls: () => listCalls, ...renderRoutes(routes, path) }
}

const review = makeAgent({ name: '代码审查助手' })
const writer = makeAgent({ name: '写作助手' })
const deploy = makeAgent({ name: 'Deploy Bot' })

describe('左侧栏的搜索', () => {
  it('按名称做不区分大小写的包含匹配，去掉首尾空白；清空后恢复全部', async () => {
    const { user } = setup([review, writer, deploy])
    await waitFor(() => expect(names()).toHaveLength(3))
    const search = within(sidebar()).getByRole('searchbox', { name: '搜索 Agent' })
    expect(search).toHaveAttribute('placeholder', '搜索')

    await user.type(search, '  deploy ')
    expect(names()).toEqual([expect.stringContaining('Deploy Bot')])

    await user.clear(search)
    await user.type(search, '助手')
    expect(names()).toHaveLength(2)

    await user.clear(search)
    expect(names()).toHaveLength(3)
  })

  it('按地址匹配：输入地址的一部分时筛出对应的 Agent；没有地址的 Agent 只按名称匹配', async () => {
    const reviewer = makeAgent({ name: '审查', address: '测试组织/测试项目/审查' })
    const legacy = makeAgent({ name: 'old/bot', address: null })
    const { user } = setup([reviewer, legacy, makeAgent({ name: '写作助手', address: '测试组织/测试项目/写作助手' })])
    await waitFor(() => expect(names()).toHaveLength(3))
    const search = within(sidebar()).getByRole('searchbox', { name: '搜索 Agent' })
    await user.type(search, '测试项目/审')
    expect(names()).toEqual([expect.stringContaining('审查')])
    await user.clear(search)
    await user.type(search, '测试组织')
    expect(names()).toHaveLength(2)
    expect(names().some((name) => name.includes('old/bot'))).toBe(false)
  })

  it('没有匹配的 Agent 时说明没有匹配项，右侧已打开的 Agent 保持不变', async () => {
    const { user, router } = setup([review, writer, deploy], defaultAgentPath(review.id, 'config'))
    await waitFor(() => expect(names()).toHaveLength(3))
    expect(await screen.findByRole('link', { name: '配置' })).toHaveAttribute('aria-current', 'page')

    await user.type(within(sidebar()).getByRole('searchbox', { name: '搜索 Agent' }), '不存在的名字')
    expect(within(sidebar()).getByText('没有匹配的 Agent')).toBeInTheDocument()
    expect(names()).toHaveLength(0)
    expect(router.state.location.pathname).toBe(defaultAgentPath(review.id, 'config'))
    expect(screen.getByRole('link', { name: '配置' })).toHaveAttribute('aria-current', 'page')
  })

  it('搜索期间定期刷新仍在进行，新出现的匹配项会显示', async () => {
    // 在列表开始轮询前接管计时器，列表的定时刷新才能被推进。
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const { user, listed, listCalls } = setup([review, deploy])
      await waitFor(() => expect(names()).toHaveLength(2))
      await user.type(within(sidebar()).getByRole('searchbox', { name: '搜索 Agent' }), 'DEPLOY')
      expect(names()).toHaveLength(1)

      listed.push(makeAgent({ name: 'deploy 预发' }))
      const before = listCalls()
      await act(async () => {
        await vi.advanceTimersByTimeAsync(config.agentListPollMs + 100)
      })
      await waitFor(() => expect(names()).toHaveLength(2))
      expect(listCalls()).toBeGreaterThan(before)
      expect(within(sidebar()).getByRole('searchbox', { name: '搜索 Agent' })).toHaveValue('DEPLOY')
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('左侧栏 Agent 行的外观与状态', () => {
  it('每行是名称与一个状态标签：回复中、Manager；空闲不显示标签；新建按钮是“+”', async () => {
    const busy = makeAgent({ name: 'A', busy: true })
    const idle = makeAgent({ name: 'B' })
    const manager = makeAgent({ name: 'C', isManager: true })
    setup([busy, idle, manager])
    const list = await screen.findByRole('list', { name: 'Agent' })
    const itemA = await within(list).findByRole('link', { name: /^A/ })
    expect(itemA).toHaveTextContent(/^A回复中$/)
    expect(within(list).getByRole('link', { name: /^B/ })).toHaveTextContent(/^B$/)
    expect(within(within(list).getByRole('link', { name: /^C/ })).getByText('Manager')).toHaveAttribute('title', '项目 Manager')
    const create = within(sidebar()).getByRole('button', { name: '新建 Agent' })
    expect(create).toHaveTextContent('')
    expect(create).toBeEnabled()
  })

  it('Claude Code 的 Agent 在名称后有一个小图标标出运行时；Pi 的不标', async () => {
    setup([makeAgent({ name: 'Pi 助手' }), makeAgent({ name: '本机助手', model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code', busy: true })])
    const list = await screen.findByRole('list', { name: 'Agent' })
    const claude = await within(list).findByRole('link', { name: /^本机助手/ })
    expect(claude).toHaveTextContent(/^本机助手运行时：本机的 Claude Code回复中$/)
    expect(within(claude).getByTitle('运行时：本机的 Claude Code')).toBeInTheDocument()
    expect(within(list).getByRole('link', { name: /^Pi 助手/ })).toHaveTextContent(/^Pi 助手$/)
  })

  it('sleep 中显示“zZ sleep”，唤醒时间与原因在悬停提示里', async () => {
    const agent = makeAgent({ name: '等待中的助手' })
    const reason = '等待外部确认并保留当前成果。'
    const sleep = makeSleep({ agentId: agent.id, reason, wakeAt: new Date(Date.now() + 120_000).toISOString() })
    server.use(http.get(agentsUrl, () => HttpResponse.json({ agents: [{ ...toListItem(agent), sleep, sleepRevision: sleep.revision }] })), ...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agent' })
    const chip = await within(list).findByText('zZ sleep')
    expect(chip.getAttribute('title')).toMatch(/^sleep 至 .+ · 等待外部确认并保留当前成果。$/)
    expect(chip.closest('a')).toHaveTextContent(/^等待中的助手zZ sleep$/)
  })

  it('发给你、还没处理的问题与消息在这一行显示“待你回复”的贴纸', async () => {
    const agent = makeAgent({ name: '开发助手' })
    server.use(
      http.get(apiUrl('/inbox/pending'), () => HttpResponse.json({ questions: [], unread: [], byAgent: [{ agentId: agent.id, projectId: null, questions: 1, unread: 2 }], byTask: [] })),
      ...baseHandlers([agent]),
    )
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agent' })
    expect(await within(list).findByRole('link', { name: /^开发助手\s*待你回复：1 个问题、2 条新消息/ })).toHaveTextContent('3')
  })

  it('无法连接时显示网点骨架和“无法连接服务端”，新建按钮禁用', async () => {
    server.use(http.all(`${config.apiBase}/*`, () => HttpResponse.error()))
    renderRoutes(routes, defaultAgentsPath)
    // 上一个用例还在途中的请求可能在这里返回、短暂改变连接状态；等左侧栏稳定在骨架上再看。
    await waitFor(() => {
      expect(within(sidebar()).getByText('无法连接服务端')).toBeInTheDocument()
      expect(sidebar().querySelectorAll('.riso-skeleton')).toHaveLength(3)
    })
    expect(within(sidebar()).getByRole('button', { name: '新建 Agent' })).toBeDisabled()
    expect(within(sidebar()).queryByRole('list', { name: 'Agent' })).toBeNull()
  })
})

// 改版“一张印出来的纸”第 10 条：项目卡片与选中行统一描边和投影，选中行里的 Manager 标记，搜索框与装饰。
describe('左侧栏的样式', () => {
  const manager = makeAgent({ name: 'manager', isManager: true })
  const other = makeAgent({ name: '研究员', isManager: true })

  it('项目卡片与选中的 Agent 行都是 2px 藏青描边加 4px 藏青实色投影', async () => {
    setup([manager, other], defaultAgentPath(manager.id, 'config'))
    const row = await within(sidebar()).findByRole('link', { name: /manager/ })
    await waitFor(() => expect(row).toHaveAttribute('aria-current', 'page'))
    expect(row).toHaveClass('border-2', 'border-ink', 'bg-riso-yellow', 'shadow-[4px_4px_0_var(--ink)]')
    const card = within(sidebar()).getByRole('button', { name: /^切换范围/ })
    expect(card).toHaveClass('border-2', 'border-ink', 'bg-paper-raised', 'shadow-[4px_4px_0_var(--ink)]')
    const unselected = within(sidebar()).getByRole('link', { name: /研究员/ })
    expect(unselected).not.toHaveClass('bg-riso-yellow')
    expect(unselected).not.toHaveClass('shadow-[4px_4px_0_var(--ink)]')
  })

  it('选中行里的 Manager 标记是藏青底白字；没选中的行仍是半透明藏青底', async () => {
    setup([manager, other], defaultAgentPath(manager.id, 'config'))
    const row = await within(sidebar()).findByRole('link', { name: /manager/ })
    await waitFor(() => expect(row).toHaveAttribute('aria-current', 'page'))
    expect(within(row).getByText('Manager')).toHaveClass('bg-ink', 'text-white', 'rounded-full')
    const tag = within(within(sidebar()).getByRole('link', { name: /研究员/ })).getByText('Manager')
    expect(tag).toHaveClass('bg-ink/45', 'text-white')
    expect(tag).not.toHaveClass('bg-ink')
  })

  // 改版第 3 步：选中的 Manager 行原来只给名字留 66px（侧栏出现滚动条时 55px），“manager”被截成“man…”。
  it('名字放得下时完整显示：名字先占满一行，右边的运行时、状态与 Manager 标记放不下时换到下一行；过长的才截断，悬停看到全名', async () => {
    setup([manager, other], defaultAgentPath(manager.id, 'config'))
    const row = await within(sidebar()).findByRole('link', { name: /manager/ })
    await waitFor(() => expect(row).toHaveAttribute('aria-current', 'page'))
    // 选中行不再固定高度，换行时随内容长高。
    expect(row).toHaveClass('min-h-10')
    expect(row).not.toHaveClass('h-10')
    const name = within(row).getByText('manager')
    expect(name).toHaveAttribute('title', 'manager')
    expect(name).toHaveClass('max-w-full', 'truncate')
    expect(name.parentElement).toHaveClass('flex-wrap', 'min-w-0', 'flex-1')
    const tags = within(row).getByText('Manager').parentElement!
    expect(tags).toHaveClass('ml-auto', 'shrink-0')
    expect(tags).not.toContainElement(name)
    // 没选中的行同样排法。
    const unselected = within(sidebar()).getByRole('link', { name: /研究员/ })
    expect(unselected).toHaveClass('min-h-9')
    expect(within(unselected).getByText('研究员')).toHaveAttribute('title', '研究员')
  })

  it('侧栏的滚动区在蓝底上用一条细的半透明白色滚动条', async () => {
    setup([manager])
    const list = await within(sidebar()).findByRole('list', { name: 'Agent' })
    const scroller = list.closest('.overflow-y-auto')!
    expect(scroller).toHaveClass('[scrollbar-width:thin]', '[scrollbar-color:rgb(255_255_255/.45)_transparent]')
  })

  it('搜索框高 34px；快捷键提示有细描边', async () => {
    setup([manager])
    const search = await within(sidebar()).findByRole('searchbox', { name: '搜索 Agent' })
    expect(search.closest('label')).toHaveClass('h-[34px]', 'bg-ink/[0.42]')
    expect(search.closest('label')!.querySelector('kbd')).toHaveClass('border', 'border-white/45')
  })

  it('中下部的网点圆和两个色点是装饰：读屏不读、不挡点击，放在按高度决定显示的空位里', async () => {
    setup([manager])
    await within(sidebar()).findByRole('link', { name: /manager/ })
    const deco = sidebar().querySelector<HTMLElement>('[data-sidebar-deco]')!
    expect(deco).toHaveAttribute('aria-hidden', 'true')
    // 窄屏的抽屉里不显示。
    expect(deco).toHaveClass('pointer-events-none', 'sidebar-deco', 'max-nav:hidden')
    expect(deco.parentElement).toHaveClass('sidebar-deco-slot')
    expect(deco.querySelectorAll('span')).toHaveLength(3)
  })
})
