import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { tasksPath } from '@/api/tasks'
import { routes } from '@/app/routes'
import { locale } from '@/i18n/locale'
import { agentsUrl, apiUrl, context, defaultAgentPath, defaultProject, makeAgent, makeProject, projectsUrl, toListItem } from '@/test/api'
import { makeSleep } from '@/test/sleep'
import { FakeEventSource } from '@/test/fake-event-source'
import { projectBackend } from '@/test/projects'
import { emptyStatsFixture } from '@/test/stats-fixture'
import { makeTaskDetail } from '@/test/tasks'
import { renderChat } from '@/test/chat'
import { renderConfig } from '@/test/config'
import { makeAgentOverview, renderOverview } from '@/test/overview'
import { fakePlugins } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, currentSidebarLinks, sidebar, sidebarLinks } from '@/test/shell'

// Chinese console: one smoke test per main page, confirming that each area renders the Chinese copy table. The behaviour of
// every page is covered by the English suite next to its code; this only checks that the Chinese UI is wired up.
// (These replace app/english.test.tsx, which did the same for English while the main suite ran in Chinese.)
describe('Chinese console', () => {
  it('picks zh-CN from the browser language', () => {
    expect(locale).toBe('zh-CN')
  })

  it('sidebar and scope switcher', async () => {
    const agent = makeAgent({ name: 'dev-helper' })
    server.use(...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const list = await within(sidebar()).findByRole('list', { name: 'Agent' })
    await within(list).findByRole('link', { name: 'dev-helper' })
    expect(sidebarLinks().map((link) => link.textContent)).toEqual([
      'LOOP NOW',
      '概览',
      '任务',
      '项目文件夹',
      '项目设置',
      'dev-helper',
      '插件市场',
      'Skills 库',
      '统计',
      '外部身份',
      '全局设置',
    ])
    expect(within(sidebar()).getByRole('button', { name: '切换范围，当前：test-project' })).toBeInTheDocument()
    expect(screen.getByRole('separator', { name: '调整侧边栏宽度' })).toBeInTheDocument()
  })

  it('sidebar Agent status symbols use Chinese tooltips and screen-reader text', async () => {
    const busy = makeAgent({ name: 'a-busy', busy: true })
    const bg = makeAgent({ name: 'b-bg', backgroundTaskCount: 2 })
    const sleeping = makeAgent({ name: 'c-sleep' })
    const manager = makeAgent({ name: 'd-mgr', isManager: true })
    const sleep = makeSleep({ agentId: sleeping.id, reason: '等待确认', wakeAt: new Date(Date.now() + 120_000).toISOString() })
    server.use(
      http.get(agentsUrl, () =>
        HttpResponse.json({
          agents: [toListItem(busy), toListItem(bg), { ...toListItem(sleeping), sleep, sleepRevision: sleep.revision }, toListItem(manager)],
        }),
      ),
      ...baseHandlers([busy, bg, sleeping, manager]),
    )
    renderRoutes(routes, defaultAgentPath(busy.id, 'config'))
    const list = await within(sidebar()).findByRole('list', { name: 'Agent' })
    expect(within(await within(list).findByRole('link', { name: /^a-busy/ })).getByTitle('回复中')).toHaveTextContent('回复中')
    expect(within(within(list).getByRole('link', { name: /^b-bg/ })).getByTitle('空闲，2 个后台任务还在运行')).toHaveTextContent('空闲，2 个后台任务还在运行')
    const sleepMark = within(list).getByRole('link', { name: /^c-sleep/ }).querySelector<HTMLElement>('[data-status="sleeping"]')!
    expect(sleepMark.getAttribute('title')).toMatch(/^sleep 至 .+ · 等待确认$/)
    expect(sleepMark.querySelector('.sr-only')?.textContent).toBe(sleepMark.getAttribute('title'))
    expect(within(within(list).getByRole('link', { name: /^d-mgr/ })).getByTitle('项目 Manager')).toHaveTextContent('项目 Manager')
  })

  it('overview sections and run status summary', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview({ name: 'dev-helper' })])
    for (const name of ['运行状态', '用量', '异常与失败']) expect(screen.getByRole('region', { name })).toBeInTheDocument()
    const tiles = screen.getByRole('list', { name: '运行状态汇总' })
    for (const label of ['回复中', '空闲', '排队中']) expect(within(tiles).getByText(label)).toBeInTheDocument()
  })

  it('chat composer', async () => {
    await renderChat()
    const box = screen.getByRole('textbox', { name: '消息' })
    expect(box).toHaveAttribute('placeholder', '给 dev-helper 发消息')
    // The Chinese UI writes Enter as “回车” in the key hints.
    expect(screen.getByText('Shift + 回车')).toBeInTheDocument()
    expect(screen.getByText('换行')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '发送' })).toBeInTheDocument()
  })

  it('Agent header bar and More menu', async () => {
    const agent = makeAgent({ name: 'dev-helper', address: 'test-org/test-project/dev-helper' })
    server.use(...baseHandlers([agent]))
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const tabs = await screen.findByRole('navigation', { name: 'dev-helper' })
    expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['对话', '配置', '文件夹', '收件箱'])
    await user.click(screen.getByRole('button', { name: '更多' }))
    const menu = await screen.findByRole('dialog', { name: 'Agent 信息' })
    expect(within(menu).getByText('Pi（内置）')).toBeInTheDocument()
    expect(within(menu).getByRole('button', { name: '复制地址' })).toBeInTheDocument()
    expect(within(menu).getByRole('link', { name: '返回项目 test-project' })).toBeInTheDocument()
  })

  it('Agent config sections', async () => {
    await renderConfig()
    for (const name of ['基本信息', '模型账号凭证', 'Skills', '可用 CLI', '删除 Agent']) {
      expect(screen.getByRole('region', { name })).toBeInTheDocument()
    }
  })

  it('plugin market', async () => {
    server.use(...fakePlugins({ details: [] }).handlers, ...baseHandlers([]))
    renderRoutes(routes, '/plugins')
    expect(await screen.findByRole('heading', { name: '插件市场', level: 1 })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '安装插件' })).toBeInTheDocument()
  })

  it('global settings shows 中文 as the chosen language', async () => {
    server.use(...baseHandlers([]))
    renderRoutes(routes, '/settings')
    const group = await screen.findByRole('radiogroup', { name: '界面语言' })
    expect(within(group).getByRole('radio', { name: '中文' })).toHaveAttribute('aria-checked', 'true')
    expect(group).toHaveAccessibleDescription('只影响这个浏览器；切换后页面会重新加载。')
  })
  it('tasks', async () => {
    const detail = makeTaskDetail(defaultProject.id, defaultProject.name)
    detail.task = { ...detail.task, currentStepId: 'verify', currentStepTitle: '验证' }
    server.use(
      ...baseHandlers([]),
      http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
      http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks: [detail.task], nextCursor: null })),
    )
    renderRoutes(routes, tasksPath(defaultProject.name))
    act(() => FakeEventSource.instances.forEach((es) => es.open()))
    expect(await screen.findByRole('heading', { name: '任务', level: 1 })).toBeInTheDocument()
    await screen.findByRole('link', { name: detail.task.title })
    const list = screen.getByRole('list', { name: '任务' })
    expect(within(list).getByText('阻塞')).toBeVisible()
    expect(within(list).getByText('验证')).toHaveAttribute('title', 'verify')
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['任务'])
  })

  it('project folder', async () => {
    server.use(
      http.get(apiUrl(`/projects/${defaultProject.id}/context-folder/entries`), () => HttpResponse.json({ path: '', entries: [], nextCursor: null })),
      ...baseHandlers([]),
    )
    renderRoutes(routes, `/p/${encodeURIComponent(defaultProject.name)}/folder`)
    expect(await screen.findByRole('heading', { name: '项目文件夹', level: 1 })).toBeInTheDocument()
    expect(await screen.findByRole('tree', { name: '文件树' })).toHaveTextContent('项目文件夹')
    expect(screen.getByRole('complementary', { name: '概况' })).toBeInTheDocument()
  })

  it('project settings', async () => {
    const other = makeProject({ name: 'model-access' })
    server.use(...projectBackend({ projects: [defaultProject, other], agents: [] }).handlers)
    renderRoutes(routes, `/p/${encodeURIComponent(other.name)}/settings`)
    expect(await screen.findByRole('heading', { name: '项目设置', level: 1 })).toBeInTheDocument()
    const nameCard = await screen.findByRole('region', { name: '项目名称' })
    expect(within(nameCard).getByRole('button', { name: '保存' })).toBeInTheDocument()
  })

  it('skills library', async () => {
    server.use(
      http.get(apiUrl('/context'), () => HttpResponse.json(context)),
      http.get(apiUrl('/skills'), () =>
        HttpResponse.json({ skills: [{ id: 'a', name: 'pdf-tools', description: 'Works with PDFs', fileCount: 2, createdAt: '2026-10-12T00:00:00Z', updatedAt: '2026-10-12T00:00:00Z' }] }),
      ),
    )
    renderRoutes(routes, '/skills')
    const list = await screen.findByRole('list', { name: 'Skills 库' })
    await within(list).findByRole('link', { name: /pdf-tools/ })
    expect(within(list).getByText(/更新于/)).toBeInTheDocument()
  })

  it('stats', async () => {
    server.use(http.get(apiUrl('/stats'), () => HttpResponse.json(emptyStatsFixture())), ...baseHandlers([]))
    renderRoutes(routes, '/stats')
    expect(await screen.findByRole('heading', { name: '统计', level: 1 })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: '近 7 天' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('combobox', { name: '范围' })).toHaveDisplayValue('全部项目')
  })

  it('external identities', async () => {
    server.use(http.get(apiUrl('/external-identities'), () => HttpResponse.json({ identities: [] })), ...baseHandlers([]))
    renderRoutes(routes, '/identities')
    expect(await screen.findByRole('heading', { name: /^外部身份/, level: 1 })).toBeInTheDocument()
    expect(screen.getByRole('complementary', { name: '外部身份列表' })).toBeInTheDocument()
  })
})
