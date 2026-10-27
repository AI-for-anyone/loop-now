import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultAgentsPath, defaultProject, makeAgent, makeProject } from '@/test/api'
import { projectBackend } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { scopeSwitcher, sidebar } from '@/test/shell'

const base = (name: string) => `/p/${encodeURIComponent(name)}`
const lastScope = () => window.localStorage.getItem('loop-now.lastScope')

function setup() {
  const other = makeProject({ name: '模型接入' })
  const a = makeAgent({ name: '开发助手', projectId: other.id })
  const b = makeAgent({ name: '巡检助手', scope: 'platform', projectId: null })
  const c = makeAgent({ name: '默认助手' })
  const backend = projectBackend({ projects: [defaultProject, other], agents: [a, b, c] })
  server.use(...backend.handlers)
  return { ...backend, other, a, b, c }
}

const agentLinks = async () => within(await screen.findByRole('list', { name: 'Agent' })).findAllByRole('link')

describe('带项目的地址', () => {
  it('直接打开项目中 Agent 的对话页：左侧栏是该项目的列表并选中它，范围切换显示该项目', async () => {
    const { other, a } = setup()
    renderRoutes(routes, `${base('模型接入')}/agents/${a.id}/chat`)
    const list = await screen.findByRole('list', { name: 'Agent' })
    await waitFor(() => expect(within(list).getByRole('link', { name: /开发助手/ })).toHaveAttribute('aria-current', 'page'))
    expect(within(list).queryByRole('link', { name: /默认助手/ })).toBeNull()
    expect(screen.getByRole('link', { name: '对话' })).toHaveAttribute('aria-current', 'page')
    expect(scopeSwitcher('模型接入')).toBeInTheDocument()
    expect(lastScope()).toBe(other.id)
  })

  it('项目的 Agent 页不停在空白提示：进入 Manager 的对话，没有 Manager 时进入第一个 Agent；平台仍提示从左侧栏选择', async () => {
    const { state, other, a } = setup()
    const second = makeAgent({ name: '规划助手', projectId: other.id })
    state.agents.push(second)
    state.projects.find((p) => p.id === other.id)!.managerAgentId = second.id
    const first = renderRoutes(routes, `${base('模型接入')}/agents`)
    await waitFor(() => expect(first.router.state.location.pathname).toBe(`${base('模型接入')}/agents/${second.id}/chat`))
    expect(first.router.state.historyAction).toBe('REPLACE')
    first.unmount()

    state.projects.find((p) => p.id === other.id)!.managerAgentId = null
    const next = renderRoutes(routes, `${base('模型接入')}/agents`)
    await waitFor(() => expect(next.router.state.location.pathname).toBe(`${base('模型接入')}/agents/${a.id}/chat`))
    next.unmount()

    const platform = renderRoutes(routes, '/platform/agents')
    expect(await screen.findByText('从左侧栏选择一个 Agent。')).toBeInTheDocument()
    expect(platform.router.state.location.pathname).toBe('/platform/agents')
  })

  it('项目名不存在：说明可能已改名或删除，并提供进入默认项目的操作', async () => {
    setup()
    renderRoutes(routes, `${base('旧名称')}/agents`)
    expect(await screen.findByText('找不到项目「旧名称」')).toBeInTheDocument()
    expect(screen.getByText('它可能已改名或删除。可以在左上角的范围切换里找其他项目。')).toBeInTheDocument()
    expect(scopeSwitcher('旧名称（找不到）')).toBeInTheDocument()
    expect(within(sidebar()).getByRole('link', { name: '概览' })).toHaveAttribute('href', `/?${new URLSearchParams({ project: '旧名称' })}`)
    expect(within(sidebar()).queryByRole('list', { name: 'Agent' })).toBeNull()
    expect(screen.getByRole('link', { name: '进入默认项目' })).toHaveAttribute('href', defaultAgentsPath)
  })

  it('名称区分大小写，含空格与特殊字符的项目名可以往返', async () => {
    const weird = makeProject({ name: 'Data Lab ?%' })
    const agent = makeAgent({ name: '分析助手', projectId: weird.id })
    server.use(...projectBackend({ projects: [defaultProject, weird], agents: [agent] }).handlers)
    const { router } = renderRoutes(routes, `${base('Data Lab ?%')}/agents/${agent.id}`)
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('Data Lab ?%')}/agents/${agent.id}/chat`))
    await waitFor(() => expect(screen.getByRole('link', { name: '对话' })).toHaveAttribute('aria-current', 'page'))
  })

  it('停留期间项目被改名：按 ID 换到新名称的地址并提示，不新增历史记录', async () => {
    const { state, other, a } = setup()
    const { router, client } = renderRoutes(routes, `${base('模型接入')}/agents/${a.id}/chat`)
    await agentLinks()
    state.projects.find((p) => p.id === other.id)!.name = '模型适配'
    await act(() => client.invalidateQueries({ queryKey: ['projects'] }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('模型适配')}/agents/${a.id}/chat`))
    expect(router.state.historyAction).toBe('REPLACE')
    const note = await screen.findByText('项目「模型接入」已在别处改名为「模型适配」，地址已随之更新。')
    await userEvent.setup().click(within(note.parentElement!).getByRole('button', { name: '知道了' }))
    expect(screen.queryByText(/已在别处改名/)).toBeNull()
    expect(scopeSwitcher('模型适配')).toBeInTheDocument()
  })

  it('停留期间项目被删除：说明项目已删除，并提供进入默认项目的操作', async () => {
    const { state, other } = setup()
    const { client } = renderRoutes(routes, `${base('模型接入')}/agents`)
    await agentLinks()
    state.projects = state.projects.filter((p) => p.id !== other.id)
    await act(() => client.invalidateQueries({ queryKey: ['projects'] }))
    expect(await screen.findByText('项目「模型接入」已删除')).toBeInTheDocument()
    expect(screen.getByText('它在别处被删除，其中的 Agent 已从列表中隐藏。')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '进入默认项目' })).toBeInTheDocument()
  })

  it('打开旧地址：改用 Agent 实际所在的地址，保留锚点', async () => {
    const { a } = setup()
    const { router } = renderRoutes(routes, `/agents/${a.id}/chat#run-r1`)
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('模型接入')}/agents/${a.id}/chat`))
    expect(router.state.location.hash).toBe('#run-r1')
  })

  it('地址中的项目与 Agent 不一致：改用平台 Agent 实际所在的地址，左侧栏换成全局', async () => {
    const { b } = setup()
    const { router } = renderRoutes(routes, `${defaultAgentsPath}/${b.id}/config`)
    await waitFor(() => expect(router.state.location.pathname).toBe(`/platform/agents/${b.id}/config`))
    // 改写前默认项目的列表也在页面上出现过，每次重新查找列表。
    await waitFor(() =>
      expect(within(screen.getByRole('list', { name: '平台 Agent' })).getByRole('link', { name: /巡检助手/ })).toHaveAttribute('aria-current', 'page'),
    )
    expect(scopeSwitcher('全局')).toBeInTheDocument()
  })

  it('旧地址 /agents：进入最近使用的平台；记录的项目已删除时进入默认项目', async () => {
    setup()
    window.localStorage.setItem('loop-now.lastScope', 'platform')
    const first = renderRoutes(routes, '/agents')
    await waitFor(() => expect(first.router.state.location.pathname).toBe('/platform/agents'))
    first.unmount()

    window.localStorage.setItem('loop-now.lastScope', '00000000-0000-7000-8000-ffffffffffff')
    const second = renderRoutes(routes, '/agents')
    await waitFor(() => expect(second.router.state.location.pathname).toBe(defaultAgentsPath))
  })
})

// 范围切换进入所选范围的概览，概览要读用量与异常。
const overviewHandlers = [
  http.get(apiUrl('/usage'), () => HttpResponse.json({ from: null, to: null, rows: [] })),
  http.get(apiUrl('/anomalies'), () => HttpResponse.json({ items: [], total: 0, nextCursor: null })),
]

describe('范围切换', () => {
  it('依次列出全局与各个项目（默认在前、带 Agent 数）；选择项目后进入它的概览并记住，左侧栏换成它的 Agent', async () => {
    const { other } = setup()
    server.use(...overviewHandlers)
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentsPath)
    await agentLinks()
    await user.click(scopeSwitcher('测试项目'))
    const options = within(await screen.findByRole('listbox', { name: '范围' })).getAllByRole('option')
    expect(options.map((o) => o.textContent)).toEqual(['全局', '测试项目默认1 个 Agent', '模型接入1 个 Agent'])
    expect(options[1]).toHaveAttribute('aria-selected', 'true')
    await user.click(options[2])
    await waitFor(() => expect(router.state.location.search).toBe(`?${new URLSearchParams({ project: '模型接入' })}`))
    expect(router.state.location.pathname).toBe('/')
    expect(scopeSwitcher('模型接入')).toBeInTheDocument()
    await waitFor(async () => expect((await agentLinks()).map((l) => l.textContent)).toEqual(['开发助手']))
    expect(lastScope()).toBe(other.id)
  })

  it('键盘切换到全局：上下键移动，回车选择；全局里列出项目与平台 Agent', async () => {
    setup()
    server.use(...overviewHandlers)
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentsPath)
    await agentLinks()
    await user.click(scopeSwitcher('测试项目'))
    await screen.findByRole('listbox', { name: '范围' })
    await user.keyboard('{ArrowUp}{Enter}')
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(router.state.location.search).toBe('')
    expect(scopeSwitcher('全局')).toBeInTheDocument()
    expect(within(sidebar()).getByRole('list', { name: '项目' })).toHaveTextContent('测试项目模型接入')
    expect(await within(within(sidebar()).getByRole('list', { name: '平台 Agent' })).findByRole('link', { name: '巡检助手' })).toBeInTheDocument()
    expect(lastScope()).toBe('platform')
  })

  it('按名称过滤项目，“全局”与“新建项目”仍然可用，不再有项目设置的捷径', async () => {
    const extra = makeProject({ name: '写作组' })
    server.use(...projectBackend({ projects: [defaultProject, makeProject({ name: '模型接入' }), extra] }).handlers)
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentsPath)
    await user.click(await within(sidebar()).findByRole('button', { name: '切换范围，当前：测试项目' }))
    await user.type(await screen.findByRole('searchbox', { name: '按名称过滤项目' }), '接入')
    const options = within(screen.getByRole('listbox', { name: '范围' })).getAllByRole('option')
    expect(options.map((o) => o.textContent?.replace(/\d+ 个 Agent$/, ''))).toEqual(['全局', '模型接入'])
    const popover = screen.getByRole('dialog', { name: '范围' })
    expect(within(popover).getByRole('button', { name: '新建项目' })).toBeInTheDocument()
    expect(within(popover).queryByRole('link')).toBeNull()
  })

  it('全局范围的“项目”分组：点一个项目即切换到它的概览', async () => {
    const { other } = setup()
    server.use(...overviewHandlers)
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, '/')
    await user.click(await within(await within(sidebar()).findByRole('list', { name: '项目' })).findByRole('link', { name: '模型接入' }))
    expect(router.state.location.search).toBe(`?${new URLSearchParams({ project: '模型接入' })}`)
    expect(scopeSwitcher('模型接入')).toBeInTheDocument()
    expect(lastScope()).toBe(other.id)
  })

  it('直接打开全局页面时沿用最近使用的项目；没有记录时是全局', async () => {
    const { other } = setup()
    window.localStorage.setItem('loop-now.lastScope', other.id)
    const first = renderRoutes(routes, '/skills')
    expect(await within(sidebar()).findByRole('button', { name: '切换范围，当前：模型接入' })).toBeInTheDocument()
    expect(await within(await screen.findByRole('list', { name: 'Agent' })).findByRole('link', { name: '开发助手' })).toBeInTheDocument()
    first.unmount()

    window.localStorage.removeItem('loop-now.lastScope')
    renderRoutes(routes, '/skills')
    expect(await within(sidebar()).findByRole('button', { name: '切换范围，当前：全局' })).toBeInTheDocument()
  })
})

describe('新建项目', () => {
  it('预检命名规则；重名时在名称旁说明并保留内容；成功后进入新项目的空 Agent 页', async () => {
    const { state } = setup()
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentsPath)
    await user.click(await within(sidebar()).findByRole('button', { name: '切换范围，当前：测试项目' }))
    await user.click(await screen.findByRole('button', { name: '新建项目' }))
    const dialog = await screen.findByRole('dialog', { name: '新建项目' })
    const input = within(dialog).getByLabelText('项目名称')

    await user.type(input, 'a/b')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    expect(within(dialog).getByText('名称不能含“/”或“#”')).toBeInTheDocument()
    expect(state.requests.filter((r) => r.method === 'POST')).toHaveLength(0)

    await user.clear(input)
    await user.type(input, '模型接入')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    expect(await within(dialog).findByText('已有同名的项目，请换一个名称')).toBeInTheDocument()
    expect(input).toHaveValue('模型接入')

    await user.clear(input)
    await user.type(input, '新项目')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('新项目')}/agents`))
    expect(await screen.findByRole('region', { name: '还没有 Agent。' })).toBeInTheDocument()
    expect(scopeSwitcher('新项目')).toBeInTheDocument()
  })
})

describe('Skills 库是全局的', () => {
  const templates: Schemas['SkillTemplateSummary'][] = ['pdf-tools', 'git-review'].map((name) => ({
    id: `sk-${name}`,
    name,
    description: `${name} 的说明`,
    fileCount: 1,
    createdAt: '2026-10-12T08:00:00Z',
    updatedAt: '2026-10-12T08:00:00Z',
  }))

  it('从项目打开的 Skills 库页，以及不同作用域中 Agent 的“从库中添加”，列出同一组模板', async () => {
    const { other, a, b } = setup()
    server.use(http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: templates })))
    const user = userEvent.setup()
    window.localStorage.setItem('loop-now.lastScope', other.id)
    const { unmount } = renderRoutes(routes, '/skills')
    const names = async () => (await screen.findAllByRole('link', { name: /pdf-tools|git-review/ })).map((l) => l.textContent)
    const first = await names()
    expect(first).toHaveLength(2)
    unmount()

    for (const path of [`${base('模型接入')}/agents/${a.id}/config`, `/platform/agents/${b.id}/config`]) {
      const view = renderRoutes(routes, path)
      await user.click(within(await screen.findByRole('region', { name: 'Skills' })).getByRole('button', { name: '从库中添加' }))
      const dialog = await screen.findByRole('dialog')
      expect(await within(dialog).findByText('pdf-tools')).toBeInTheDocument()
      expect(within(dialog).getByText('git-review')).toBeInTheDocument()
      view.unmount()
    }
  })
})
