import { focusManager } from '@tanstack/react-query'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { keys } from '@/api/queries'
import { config } from '@/config'
import { localeStorageKey, page as browserPage } from '@/i18n/locale'
import { agentsUrl, apiUrl, defaultAgentPath, defaultAgentsPath, defaultProject, errorBody, makeAgent, platformAgentsUrl, toListItem } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, currentSidebarLinks, scopeSwitcher, sidebar, sidebarLinks } from '@/test/shell'
import { routes } from './routes'
import { chooseModel } from '@/test/model-picker'
import { openInboxStream } from '@/test/inbox'
import { AppLayout } from './AppLayout'
import { FakeEventSource } from '@/test/fake-event-source'

// 全局连接提示：以说明中的服务端地址区分于各页面内的错误提示。
const banner = () => screen.queryByText(/控制台访问的服务端地址是/)?.closest<HTMLElement>('[role="alert"]') ?? null
const findBanner = async () => (await screen.findByText(/控制台访问的服务端地址是/)).closest<HTMLElement>('[role="alert"]')!

// 概览页的接口：用量与异常为空；概览事件流不推送时运行状态停在加载中。
const overviewHandlers = () => [
  http.get(apiUrl('/usage'), () => HttpResponse.json({ from: null, to: null, rows: [] })),
  http.get(apiUrl('/anomalies'), () => HttpResponse.json({ items: [], total: 0, nextCursor: null })),
]

const project = `/p/${encodeURIComponent('测试项目')}`
const globalEntries = ['/plugins', '/skills', '/stats', '/identities', '/settings']
const hrefs = () => sidebarLinks().map((link) => link.getAttribute('href'))
const names = () => sidebarLinks().map((link) => link.textContent)

describe('左侧栏与页面结构', () => {
  it('打开根地址时是全局范围的概览：概览为当前项，下面是项目、平台 Agent 与底部的全局入口', async () => {
    const platform = makeAgent({ name: '巡检助手', scope: 'platform', projectId: null })
    server.use(...overviewHandlers(), http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(platform)] })), ...baseHandlers([makeAgent()]))
    renderRoutes(routes, '/')
    expect(await screen.findByRole('heading', { name: '概览', level: 1 })).toBeInTheDocument()
    await within(sidebar()).findByRole('link', { name: '巡检助手' })
    expect(scopeSwitcher('全局')).toBeInTheDocument()
    expect(names()).toEqual(['LOOP NOW', '概览', '测试项目', '巡检助手', '插件市场', 'Skills 库', '统计', '外部身份', '全局设置'])
    expect(hrefs()).toEqual(['/', '/', '/?project=%E6%B5%8B%E8%AF%95%E9%A1%B9%E7%9B%AE', `/platform/agents/${platform.id}`, ...globalEntries])
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['概览'])
    expect(within(sidebar()).getByRole('list', { name: '平台 Agent' })).toBeInTheDocument()
  })

  it('打开不存在的地址时回到概览页', async () => {
    server.use(...overviewHandlers(), ...baseHandlers([makeAgent()]))
    const { router } = renderRoutes(routes, '/no/such/page')
    expect(await screen.findByRole('heading', { name: '概览', level: 1 })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/')
  })

  it('在 Skills 库时只有“Skills 库”是当前项，“概览”不是', async () => {
    server.use(...baseHandlers([]))
    renderRoutes(routes, '/skills')
    await screen.findByRole('heading', { name: 'Skills 库' })
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Skills 库'])
  })

  it('直接打开某个 Agent 的配置页：左侧栏是它的项目，选中该 Agent；全局入口在项目里照样可达', async () => {
    const agent = makeAgent({ name: '开发助手' })
    server.use(...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const list = await within(sidebar()).findByRole('list', { name: 'Agent' })
    await waitFor(() => expect(within(list).getByRole('link', { name: '开发助手' })).toHaveAttribute('aria-current', 'page'))
    expect(scopeSwitcher('测试项目')).toBeInTheDocument()
    expect(names()).toEqual(['LOOP NOW', '概览', '任务', '项目文件夹', '项目设置', '开发助手', '插件市场', 'Skills 库', '统计', '外部身份', '全局设置'])
    expect(hrefs()).toEqual(['/', '/?project=%E6%B5%8B%E8%AF%95%E9%A1%B9%E7%9B%AE', `${project}/tasks`, `${project}/folder`, `${project}/settings`, `${project}/agents/${agent.id}`, ...globalEntries])
    expect(within(sidebar()).queryByRole('link', { name: /收件箱/ })).toBeNull()
    expect(await screen.findByRole('link', { name: '配置' })).toHaveAttribute('aria-current', 'page')
    // 主区页头：位置链、展示字标题与模型。
    expect(screen.getByRole('navigation', { name: '页面位置' })).toHaveTextContent('测试项目›Agent›开发助手')
    expect(screen.getByRole('heading', { name: '开发助手', level: 1 })).toBeInTheDocument()
    expect(screen.getByText('anthropic · claude-sonnet-5-5')).toBeInTheDocument()
  })

  it('从项目里打开全局页面（插件市场、全局设置）时，左侧栏保留当前项目', async () => {
    const agent = makeAgent({ name: '开发助手' })
    server.use(...baseHandlers([agent]))
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentPath(agent.id, 'chat'))
    await within(sidebar()).findByRole('link', { name: '开发助手' })
    await user.click(within(sidebar()).getByRole('link', { name: '插件市场' }))
    expect(router.state.location.pathname).toBe('/plugins')
    expect(scopeSwitcher('测试项目')).toBeInTheDocument()
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['插件市场'])
    await user.click(within(sidebar()).getByRole('link', { name: '全局设置' }))
    expect(router.state.location.pathname).toBe('/settings')
    expect(scopeSwitcher('测试项目')).toBeInTheDocument()
    expect(within(sidebar()).getByRole('link', { name: '开发助手' })).not.toHaveAttribute('aria-current')
  })

  it('平台 Agent 的地址在全局范围里：左侧栏列出项目与平台 Agent，并选中它', async () => {
    const platform = makeAgent({ name: '巡检助手', scope: 'platform', projectId: null })
    server.use(http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(platform)] })), ...baseHandlers([platform]))
    renderRoutes(routes, `/platform/agents/${platform.id}/chat`)
    const list = await within(sidebar()).findByRole('list', { name: '平台 Agent' })
    await waitFor(() => expect(within(list).getByRole('link', { name: '巡检助手' })).toHaveAttribute('aria-current', 'page'))
    expect(scopeSwitcher('全局')).toBeInTheDocument()
    expect(within(sidebar()).getByRole('list', { name: '项目' })).toHaveTextContent('测试项目')
    expect(screen.getByRole('navigation', { name: '页面位置' })).toHaveTextContent('全局›平台 Agent›巡检助手')
  })

  it('完整的导航没有收件箱入口，收件箱事件流照常建立（发给你的消息与待你处理靠它更新）', async () => {
    server.use(...overviewHandlers(), ...baseHandlers([]))
    renderRoutes(routes, '/')
    await screen.findByRole('heading', { name: '概览', level: 1 })
    await openInboxStream(3)
    expect(within(sidebar()).queryByRole('link', { name: /收件箱/ })).toBeNull()
  })

  it('直接打开某个 Agent 的收件箱页签地址时，选中该 Agent 并显示收件箱页签', async () => {
    const agent = makeAgent({ name: '开发助手' })
    server.use(http.get(apiUrl(`/agents/${agent.id}/inbox`), () => HttpResponse.json({ messages: [], nextCursor: null })), ...baseHandlers([agent]))
    renderRoutes(routes, `${defaultAgentsPath}/${agent.id}/inbox`)
    const list = await screen.findByRole('list', { name: 'Agent' })
    await waitFor(() => expect(within(list).getByRole('link', { name: /开发助手/ })).toHaveAttribute('aria-current', 'page'))
    const tabs = await screen.findByRole('navigation', { name: '开发助手' })
    expect(within(tabs).getByRole('link', { name: '收件箱' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByText('还没有收到 Inbox 消息。')).toBeInTheDocument()
  })

  it('Agent 的“文件夹”页签：排在配置与收件箱之间，打开这个 Agent 唯一的文件夹', async () => {
    const agent = makeAgent({ name: '开发助手' })
    const reads: string[] = []
    server.use(
      http.get(apiUrl(`/agents/${agent.id}/folder/entries`), ({ request }) => {
        reads.push(new URL(request.url).searchParams.get('path') ?? '')
        return HttpResponse.json({ path: '', entries: [{ name: 'AGENTS.md', path: 'AGENTS.md', type: 'file', size: 12, modifiedAt: '2026-10-25T08:00:00Z', symlinkTarget: null }], nextCursor: null })
      }),
      http.get(apiUrl(`/agents/${agent.id}/folder/content`), () => new HttpResponse('# 工作说明\n', { headers: { 'Content-Type': 'application/octet-stream' } })),
      ...baseHandlers([agent]),
    )
    renderRoutes(routes, `${defaultAgentsPath}/${agent.id}/folder`)
    const tabs = await screen.findByRole('navigation', { name: '开发助手' })
    expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['对话', '配置', '文件夹', '收件箱'])
    expect(within(tabs).getByRole('link', { name: '文件夹' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByRole('tree', { name: '文件树' })).toHaveTextContent('开发助手 的文件夹')
    expect(await within(screen.getByRole('region', { name: 'AGENTS.md' })).findByText('工作说明')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '从 Git 拉取' })).toBeInTheDocument()
    expect(reads).toContain('')
  })

  it('左侧栏的“项目文件夹”打开项目共用的文件夹，并成为当前项', async () => {
    const reads: string[] = []
    server.use(
      http.get(apiUrl(`/projects/${defaultProject.id}/context-folder/entries`), ({ request }) => {
        reads.push(new URL(request.url).searchParams.get('path') ?? '')
        return HttpResponse.json({ path: '', entries: [], nextCursor: null })
      }),
      ...baseHandlers([]),
    )
    renderRoutes(routes, `${project}/folder`)
    expect(await screen.findByRole('heading', { name: '项目文件夹', level: 1 })).toBeInTheDocument()
    expect(await screen.findByRole('tree', { name: '文件树' })).toHaveTextContent('项目文件夹')
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['项目文件夹'])
    expect(reads).toContain('')
  })

  it('没有任何 Agent 时显示空状态；左侧栏的“+”与空状态的按钮都打开新建对话框', async () => {
    server.use(...baseHandlers([]))
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentsPath)
    const empty = await screen.findByRole('region', { name: '还没有 Agent。' })
    expect(await within(sidebar()).findByText('还没有 Agent。')).toBeInTheDocument()
    await user.click(within(empty).getByRole('button', { name: '新建 Agent' }))
    expect(within(await screen.findByRole('dialog')).getByRole('heading', { name: '新建 Agent' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await user.click(within(sidebar()).getByRole('button', { name: '新建 Agent' }))
    expect(within(await screen.findByRole('dialog')).getByRole('heading', { name: '新建 Agent' })).toBeInTheDocument()
  })

  it.each(['/skills', '/identities', '/plugins', '/settings'])('全局子页面 %s 保留五个全局入口和收件箱事件流', async path => {
    renderRoutes([{ path: '/', element: <AppLayout />, children: [{ path: path.slice(1), element: <p>全局页面内容</p> }] }], path)
    await screen.findByText('全局页面内容')
    for (const href of globalEntries) expect(hrefs()).toContain(href)
    expect(FakeEventSource.instances.some(source => source.url.endsWith('/inbox/events') && source.readyState !== FakeEventSource.CLOSED)).toBe(true)
  })

  it('窄屏的菜单按钮打开抽屉式左侧栏，点入口换了地址后自动关上', async () => {
    const agent = makeAgent({ name: '开发助手' })
    server.use(...baseHandlers([agent]))
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentPath(agent.id, 'chat'))
    await within(sidebar()).findByRole('link', { name: '开发助手' })
    const menu = screen.getByRole('button', { name: '打开导航' })
    expect(menu).toHaveAttribute('aria-expanded', 'false')
    await user.click(menu)
    const drawer = await screen.findByRole('dialog', { name: '主导航' })
    expect(menu).toHaveAttribute('aria-expanded', 'true')
    expect(await within(drawer).findByRole('link', { name: '开发助手' })).toHaveAttribute('aria-current', 'page')
    await user.click(within(drawer).getByRole('link', { name: '项目设置' }))
    expect(router.state.location.pathname).toBe(`${project}/settings`)
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '主导航' })).toBeNull())
    await user.click(screen.getByRole('button', { name: '打开导航' }))
    await user.click(within(await screen.findByRole('dialog', { name: '主导航' })).getByRole('button', { name: '关闭导航' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '主导航' })).toBeNull())
  })

  it('⌘K 聚焦左侧栏的搜索', async () => {
    server.use(...baseHandlers([makeAgent()]))
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentsPath)
    const search = await within(sidebar()).findByRole('searchbox', { name: '搜索 Agent' })
    await user.keyboard('{Meta>}k{/Meta}')
    expect(search).toHaveFocus()
  })
})

describe('Agent 列表', () => {
  it('列出未删除的 Agent', async () => {
    const a = makeAgent({ name: '开发助手' })
    const b = makeAgent({ name: '测试助手', model: { provider: 'openai', modelId: 'gpt-x' } })
    server.use(...baseHandlers([a, b]))
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agent' })
    await waitFor(() => expect(within(list).getAllByRole('link').map((link) => link.textContent)).toEqual(['开发助手', '测试助手']))
  })

  // jsdom 不排版，只能确认滚动区是定位容器；整页不被撑高由浏览器截图检查（.local-build/chat-scroll-fix）。
  it('列表的滚动区是定位容器，待你处理角标里的说明文字不会撑高整页', async () => {
    server.use(...baseHandlers([makeAgent({ name: '开发助手' })]))
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agent' })
    expect(list.closest('.overflow-y-auto')).toHaveClass('relative')
  })
  it('打开已删除 Agent 的地址时提示不存在，并显示列表', async () => {
    const agent = makeAgent()
    server.use(http.get(apiUrl('/agents/gone'), () => HttpResponse.json(errorBody('not_found'), { status: 404 })), ...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath('gone', 'chat'))
    expect(await screen.findByText('该 Agent 不存在或已删除。')).toBeInTheDocument()
    expect(await screen.findByRole('list', { name: 'Agent' })).toBeInTheDocument()
  })

  it('显示回复中与会话丢失，刷新后回复中的标签去掉（空闲不显示标签）', async () => {
    const a = makeAgent({ name: 'A', busy: true })
    const b = makeAgent({ name: 'B' })
    let busy = true
    server.use(
      http.get(agentsUrl, () =>
        HttpResponse.json({ agents: [{ ...toListItem(a), busy }, toListItem(b, 'missing')] }),
      ),
      ...baseHandlers([a, b]),
    )
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agent' })
    const itemA = await within(list).findByRole('link', { name: /^A/ })
    const itemB = within(list).getByRole('link', { name: /^B/ })
    expect(within(itemA).getByText('回复中')).toBeInTheDocument()
    expect(within(itemB).getByText('会话丢失')).toBeInTheDocument()
    busy = false
    focusManager.setFocused(false)
    focusManager.setFocused(true)
    await waitFor(() => expect(itemA).toHaveTextContent(/^A$/))
    focusManager.setFocused(undefined)
  })

  it('默认每 5 秒刷新一次列表', () => {
    expect(config.agentListPollMs).toBe(5000)
  })
})

describe('连接与错误展示', () => {
  it('服务端未启动时显示地址与重试，左侧栏的连接状态为无法连接', async () => {
    // 所有接口都连不上：默认处理器（如待处理的消息）有响应时，提示会随之消失又出现。
    server.use(http.all(apiUrl('/*'), () => HttpResponse.error()))
    renderRoutes(routes, defaultAgentsPath)
    // 上一个用例遗留的请求可能稍后才收到响应，提示随之重新挂载，所以每次重新取提示。
    await waitFor(() => {
      const alert = banner()
      expect(alert).toHaveTextContent('无法连接到服务端')
      expect(alert).toHaveTextContent(config.apiBase)
      expect(within(alert!).getByRole('button', { name: '重试' })).toBeInTheDocument()
      expect(within(screen.getByRole('status', { name: '连接状态' })).getByText('无法连接')).toBeInTheDocument()
    })
  })

  it('加载之后断连时同样显示全局提示；重试重新获取失败的查询，连上后提示消失', async () => {
    const agent = makeAgent({ name: '开发助手' })
    let up = true
    let listCalls = 0
    // 断连时所有接口都连不上（连上时交给后面的处理器）：只让列表失败时，页面上其他请求的成功会让提示
    // 消失又出现，测试拿到的提示可能已被替换，点它的重试不会发出请求。
    server.use(
      http.all(apiUrl('/*'), () => (up ? undefined : HttpResponse.error())),
      http.get(agentsUrl, () => {
        listCalls += 1
        return HttpResponse.json({ agents: [toListItem(agent)] })
      }),
      ...baseHandlers([agent]),
    )
    const user = userEvent.setup()
    const { client } = renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agent' })
    await within(list).findByRole('link', { name: /开发助手/ })
    expect(banner()).toBeNull()

    up = false
    await act(() => client.refetchQueries({ queryKey: keys.agents }))
    const alert = await findBanner()
    expect(alert).toHaveTextContent(config.apiBase)

    up = true
    const before = listCalls
    await user.click(within(alert).getByRole('button', { name: '重试' }))
    await waitFor(() => expect(banner()).toBeNull())
    expect(listCalls).toBeGreaterThan(before)
    await waitFor(() => expect(screen.queryByText('无法连接服务端')).toBeNull())
  })

  it('修改因无法连接而失败时也显示全局提示，之后任一请求成功即消失', async () => {
    const agent = makeAgent()
    server.use(http.delete(apiUrl(`/agents/${agent.id}`), () => HttpResponse.error()), ...baseHandlers([agent]))
    const user = userEvent.setup()
    const { client } = renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const remove = await screen.findByRole('button', { name: '删除这个 Agent' })
    await waitFor(() => expect(remove).toBeEnabled())
    await user.click(remove)
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '删除' }))
    expect(await findBanner()).toHaveTextContent(config.apiBase)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '取消' }))
    await act(() => client.refetchQueries({ queryKey: keys.agents }))
    await waitFor(() => expect(banner()).toBeNull())
  })

  it('默认访问本机回环地址上的服务端', () => {
    expect(config.apiBase).toBe('http://127.0.0.1:8780/api/v1')
  })

  it('校验失败时在输入项旁显示原因，并保留已填写的内容', async () => {
    server.use(
      http.get(apiUrl('/model-providers'), () =>
        HttpResponse.json({ piVersion: '1.1.0', catalogGeneratedAt: null, providers: [{ id: 'anthropic', apiKeyEnvVars: ['ANTHROPIC_API_KEY'], modelCount: 1 }] }),
      ),
      http.get(apiUrl('/model-providers/anthropic/models'), () =>
        HttpResponse.json({ providerId: 'anthropic', models: [{ id: 'm1', name: 'M1', reasoning: false, contextWindow: 1, maxTokens: 1, input: [] }] }),
      ),
      http.post(agentsUrl, () =>
        HttpResponse.json(errorBody('validation_failed', 'bad', { fields: [{ field: 'name', message: '名称过长' }] }), { status: 422 }),
      ),
      ...baseHandlers([]),
    )
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentsPath)
    // 没有 Agent 时主区的空状态也有一个“新建 Agent”，这里用左侧栏的“+”。
    await user.click(await within(sidebar()).findByRole('button', { name: '新建 Agent' }))
    await user.type(screen.getByLabelText('显示名称'), '很长的名字')
    await user.selectOptions(await screen.findByLabelText('模型提供方'), 'anthropic')
    await chooseModel(user, document.body, 'm1')
    await user.click(screen.getByRole('button', { name: '创建' }))
    expect(await screen.findByText('名称过长')).toBeInTheDocument()
    expect(screen.getByLabelText('显示名称')).toHaveValue('很长的名字')
  })
})

describe('全局设置页', () => {
  it('显示默认组织与项目、连接状态与 CLI 模板，服务端信息没有修改操作；全局文件夹可以编辑', async () => {
    server.use(
      http.get(apiUrl('/health'), () => HttpResponse.json({ ok: false, postgres: { ok: true }, redis: { ok: false, error: '连接被拒绝' } }, { status: 503 })),
      http.get(apiUrl('/cli-templates'), () =>
        HttpResponse.json({
          templates: [
            { name: 'gh', version: '2.100.0', fields: [], injection: 'env', install: { status: 'failed', error: '摘要不符' }, warnings: [] },
            {
              name: 'lark-cli',
              version: '1.0.94',
              fields: [],
              injection: 'files',
              install: { status: 'installed' },
              warnings: [{ code: 'host_keychain_risk', message: 'keychain' }],
            },
          ],
        }),
      ),
      ...baseHandlers([]),
    )
    renderRoutes(routes, '/settings')
    // 测试数据的名称与行标签不同，断言限定在标签对应的值上。
    await waitFor(() => expect(screen.getByText('默认组织').nextElementSibling).toHaveTextContent('测试组织'))
    expect(screen.getByText('默认项目').nextElementSibling).toHaveTextContent('测试项目')
    // 左侧栏的连接状态也显示“部分依赖不可用”，这里限定在页面主体内。
    const page = screen.getByRole('main')
    expect(await within(page).findByText('部分依赖不可用')).toBeInTheDocument()
    expect(within(page).getByText('Redis：连接被拒绝')).toBeInTheDocument()
    const gh = await screen.findByRole('region', { name: 'gh' })
    expect(within(gh).getByText('安装失败')).toBeInTheDocument()
    expect(within(gh).getByText('摘要不符')).toBeInTheDocument()
    const lark = screen.getByRole('region', { name: 'lark-cli' })
    expect(within(lark).getByRole('note')).toHaveTextContent('钥匙串')
    // 左侧栏有范围切换与搜索，“没有修改操作”指页面主体里文件夹以外的部分。
    expect(within(page).getByRole('region', { name: '全局文件夹' })).toBeInTheDocument()
    const outsideFolder = (el: HTMLElement) => !el.closest('[data-file-manager]')
    expect(within(page).queryAllByRole('button').filter(outsideFolder)).toEqual([])
    expect(within(page).queryAllByRole('textbox').filter(outsideFolder)).toEqual([])
  })
  it('服务端一栏显示版本：取自服务端响应的 Loop-Now-Version 头', async () => {
    server.use(
      http.get(apiUrl('/health'), () =>
        HttpResponse.json({ ok: true, postgres: { ok: true, error: null }, redis: { ok: true, error: null } }, { headers: { 'Loop-Now-Version': 'dev-d6eed7b50c04' } }),
      ),
      ...baseHandlers([]),
    )
    renderRoutes(routes, '/settings')
    await waitFor(() => expect(screen.getByText('版本', { selector: 'dt' }).nextElementSibling).toHaveTextContent('dev-d6eed7b50c04'))
  })

  it('界面语言保存在这个浏览器里，换语言后重新加载页面', async () => {
    server.use(...baseHandlers([]))
    const reload = vi.spyOn(browserPage, 'reload').mockImplementation(() => {})
    const user = userEvent.setup()
    renderRoutes(routes, '/settings')
    const select = await screen.findByRole('combobox', { name: '界面语言' })
    expect(select).toHaveDisplayValue('中文')
    expect(select).toHaveAccessibleDescription('只影响这个浏览器；切换后页面会重新加载。')
    await user.selectOptions(select, 'English')
    expect(window.localStorage.getItem(localeStorageKey)).toBe('en-US')
    expect(reload).toHaveBeenCalledOnce()
  })
})
