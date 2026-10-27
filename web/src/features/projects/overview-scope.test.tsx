import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject, makeProject, projectsUrl } from '@/test/api'
import { FakeEventSource } from '@/test/fake-event-source'
import { anomalyPage, makeAgentOverview, makeAnomaly, makeUsageRow, nextId, renderOverview, usagePage } from '@/test/overview'
import { scopeSwitcher, sidebar } from '@/test/shell'

const other = makeProject({ name: '模型接入' })
const empty = makeProject({ name: '临时实验' })
const base = (name: string) => `/p/${encodeURIComponent(name)}`
const globalNavigation = () => within(sidebar()).queryAllByRole('link').filter(link => ['/plugins', '/skills', '/identities', '/settings'].includes(link.getAttribute('href') ?? ''))
const navLink = (name: string) => within(sidebar()).getByRole('link', { name })
const query = (name: string) => `/?${new URLSearchParams({ project: name })}`

// 概览事件流：按范围筛选时地址带查询参数，所以按包含而不是结尾查找。
const stream = () => FakeEventSource.instances.filter((s) => s.url.includes('/overview/events')).at(-1)!
async function snapshot(agents: Schemas['AgentOverview'][]) {
  await act(async () => stream().open())
  await act(async () => {
    stream().emit('snapshot', { agents }, nextId())
    await new Promise((resolve) => setTimeout(resolve, 10))
  })
}

// 记录用量与异常请求的查询参数。
function recordingHandlers(usage: Schemas['UsagePage'] = usagePage([]), anomalies: Schemas['AnomalyPage'] = anomalyPage([])) {
  const queries = { usage: [] as URLSearchParams[], anomalies: [] as URLSearchParams[] }
  return {
    queries,
    handlers: [
      http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject, other, empty] })),
      http.get(apiUrl('/usage'), ({ request }) => {
        queries.usage.push(new URL(request.url).searchParams)
        return HttpResponse.json(usage)
      }),
      http.get(apiUrl('/anomalies'), ({ request }) => {
        queries.anomalies.push(new URL(request.url).searchParams)
        return HttpResponse.json(anomalies)
      }),
    ],
  }
}

describe('概览按项目或平台筛选', () => {
  it('只看一个项目：三块都只请求这个项目，刷新后仍是它，各行不标所属', async () => {
    const a = makeAgentOverview({ name: '规划助手', projectId: other.id })
    const { queries, handlers } = recordingHandlers(usagePage([makeUsageRow({ agentName: '规划助手', projectId: other.id })]))
    await renderOverview({ path: `/?project=${encodeURIComponent('模型接入')}`, handlers })
    await waitFor(() => expect(stream().url).toContain(`projectId=${other.id}`))
    await snapshot([a])
    expect(screen.getByRole('combobox', { name: '范围' })).toHaveDisplayValue('模型接入')
    expect(scopeSwitcher('模型接入')).toBeInTheDocument()
    expect(navLink('概览')).toHaveAttribute('href', query(other.name))
    expect(navLink('概览')).toHaveAttribute('aria-current', 'page')
    expect(navLink('任务')).toHaveAttribute('href', `${base(other.name)}/tasks`)
    expect(navLink('项目设置')).toHaveAttribute('href', `${base(other.name)}/settings`)
    // 全局入口在项目里照样可达。
    expect(globalNavigation()).toHaveLength(4)
    const table = await screen.findByRole('table', { name: /Agent/ })
    expect(within(table).getByText('规划助手')).toBeInTheDocument()
    expect(within(table).queryByText('模型接入')).toBeNull()
    await waitFor(() => expect(queries.usage.at(-1)?.get('projectId')).toBe(other.id))
    await waitFor(() => expect(queries.anomalies.at(-1)?.get('projectId')).toBe(other.id))
    expect(queries.usage.at(-1)?.get('platform')).toBeNull()
  })

  it('选平台：请求带 platform=true，地址为 ?platform=1，左侧栏在全局范围', async () => {
    const { queries, handlers } = recordingHandlers()
    const { user, router } = await renderOverview({ handlers })
    await snapshot([makeAgentOverview()])
    await user.selectOptions(screen.getByRole('combobox', { name: '范围' }), '平台')
    await waitFor(() => expect(router.state.location.search).toBe('?platform=1'))
    expect(scopeSwitcher('全局')).toBeInTheDocument()
    expect(navLink('概览')).toHaveAttribute('href', '/')
    expect(within(sidebar()).queryByRole('link', { name: '任务' })).toBeNull()
    expect(globalNavigation()).toHaveLength(4)
    await waitFor(() => expect(stream().url).toContain('platform=true'))
    await waitFor(() => expect(queries.usage.at(-1)?.get('platform')).toBe('true'))
  })

  it('选“全部”时，运行状态、用量与异常的各行标出所属项目、平台或已删除的项目', async () => {
    const a = makeAgentOverview({ name: '规划助手', projectId: other.id })
    const b = makeAgentOverview({ name: '联调巡检', scope: 'platform', projectId: null })
    const { handlers } = recordingHandlers(
      usagePage([
        makeUsageRow({ agentId: a.agentId, agentName: '规划助手', projectId: other.id }),
        makeUsageRow({ agentName: '旧版接入助手', agentDeleted: true, projectId: '00000000-0000-7000-8000-dddddddddddd' }),
      ]),
      anomalyPage([makeAnomaly({ agentName: '联调巡检', scope: 'platform', projectId: null })]),
    )
    await renderOverview({ handlers })
    await snapshot([a, b])
    const table = await screen.findByRole('table', { name: /Agent/ })
    const rowOf = (name: string) => within(table).getByText(name).closest('tr')!
    await waitFor(() => expect(rowOf('规划助手')).toHaveTextContent('模型接入'))
    expect(rowOf('联调巡检')).toHaveTextContent('平台')
    expect(await screen.findByText('已删除的项目')).toBeInTheDocument()
    const anomalies = await screen.findByRole('region', { name: '异常与失败' })
    expect(within(anomalies).getByText('联调巡检').parentElement).toHaveTextContent('平台')
  })

  it('选定的项目还没有 Agent：说明这里只显示这个项目，并可以进入它的 Agent 页', async () => {
    const { handlers } = recordingHandlers()
    await renderOverview({ path: `/?project=${encodeURIComponent('临时实验')}`, handlers })
    await waitFor(() => expect(stream().url).toContain(`projectId=${empty.id}`))
    await snapshot([])
    expect(await screen.findByRole('heading', { name: '「临时实验」中还没有 Agent。' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '进入「临时实验」的 Agent 页' })).toHaveAttribute('href', `${base('临时实验')}/agents`)
  })

  it('地址中的项目找不到：说明并可以回到全部', async () => {
    const { handlers } = recordingHandlers()
    const { user, router } = await renderOverview({ path: `/?project=${encodeURIComponent('旧名称')}`, handlers })
    expect(await screen.findByRole('heading', { name: '找不到项目「旧名称」' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '范围' })).toHaveDisplayValue('旧名称（找不到）')
    expect(scopeSwitcher('旧名称（找不到）')).toBeInTheDocument()
    expect(navLink('任务')).toHaveAttribute('href', `${base('旧名称')}/tasks`)
    expect(within(sidebar()).queryByRole('list', { name: 'Agent' })).toBeNull()
    await user.click(screen.getByRole('button', { name: '回到全部' }))
    await waitFor(() => expect(router.state.location.search).toBe(''))
    expect(screen.getByRole('combobox', { name: '范围' })).toHaveDisplayValue('全部')
  })

  it('范围切换选了项目后，左侧栏的概览保留这个项目', async () => {
    const { handlers } = recordingHandlers()
    const { user, router } = await renderOverview({ handlers })
    await snapshot([makeAgentOverview()])
    await user.click(within(sidebar()).getByRole('button', { name: /^切换范围，当前：/ }))
    await user.click(within(await screen.findByRole('listbox', { name: '范围' })).getByRole('option', { name: /模型接入/ }))
    await waitFor(() => expect(router.state.location.search).toBe(query(other.name).slice(1)))
    expect(await screen.findByRole('combobox', { name: '范围' })).toHaveDisplayValue(other.name)
    await user.click(navLink('概览'))
    expect(router.state.location.pathname).toBe('/')
    expect(router.state.location.search).toBe(query(other.name).slice(1))
  })

  it('标志回到全局概览，保留快捷入口记忆，根页不回填最近项目', async () => {
    window.localStorage.setItem('loop-now.lastScope', other.id)
    const { queries, handlers } = recordingHandlers()
    const { user, router } = await renderOverview({ path: query(other.name), handlers })
    await waitFor(() => expect(queries.usage.at(-1)?.get('projectId')).toBe(other.id))
    await user.click(screen.getByRole('link', { name: 'LOOP NOW' }))
    await waitFor(() => expect(router.state.location.search).toBe(''))
    expect(router.state.location.pathname).toBe('/')
    expect(screen.getByRole('combobox', { name: '范围' })).toHaveDisplayValue('全部')
    expect(scopeSwitcher('全局')).toBeInTheDocument()
    await waitFor(() => expect(queries.usage.at(-1)?.get('projectId')).toBeNull())
    expect(window.localStorage.getItem('loop-now.lastScope')).toBe(other.id)
    expect(navLink('概览')).toHaveAttribute('href', '/')
    expect(within(sidebar()).queryByRole('link', { name: '任务' })).toBeNull()
    expect(globalNavigation().map(link => link.getAttribute('href'))).toEqual(['/plugins', '/skills', '/identities', '/settings'])
    await user.click(scopeSwitcher('全局'))
    const options = within(screen.getByRole('listbox', { name: '范围' })).getAllByRole('option')
    expect(options.filter(option => option.getAttribute('aria-selected') === 'true').map(option => option.textContent)).toEqual(['全局'])
  })

  it('概览范围下拉改变后，左侧栏同步项目与全局', async () => {
    const { handlers } = recordingHandlers()
    const { user } = await renderOverview({ handlers })
    const select = () => screen.getByRole('combobox', { name: '范围' })
    await screen.findByRole('option', { name: other.name })
    await user.selectOptions(select(), other.name)
    expect(scopeSwitcher('模型接入')).toBeInTheDocument()
    expect(navLink('任务')).toHaveAttribute('href', `${base(other.name)}/tasks`)
    await user.selectOptions(select(), '平台')
    expect(scopeSwitcher('全局')).toBeInTheDocument()
    await user.selectOptions(select(), '全部')
    expect(scopeSwitcher('全局')).toBeInTheDocument()
  })

  it('平台概览在全局范围：左侧栏列出项目，没有任务入口；不跳到记忆中的项目', async () => {
    window.localStorage.setItem('loop-now.lastScope', other.id)
    const { handlers } = recordingHandlers()
    await renderOverview({ path: '/?platform=1', handlers })
    expect(scopeSwitcher('全局')).toBeInTheDocument()
    expect(await within(within(sidebar()).getByRole('list', { name: '项目' })).findByRole('link', { name: '模型接入' })).toHaveAttribute('href', query(other.name))
    expect(within(sidebar()).queryByRole('link', { name: '任务' })).toBeNull()
    expect(window.localStorage.getItem('loop-now.lastScope')).toBe(other.id)
  })
})
