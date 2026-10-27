import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, errorBody, makeAgent, makeRun, makeStatus } from '@/test/api'
import { FakeEventSource } from '@/test/fake-event-source'
import { anomalyPage, makeAgentOverview, makeAnomaly, makeUsageRow, renderOverview, usagePage } from '@/test/overview'
import { baseHandlers } from '@/test/shell'

const section = (name: string) => screen.getByRole('region', { name })

describe('概览页的入口', () => {
  it('打开根地址显示概览：三块内容，左侧栏在全局范围，“概览”是第一项且为当前项', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    expect(section('运行状态')).toBeInTheDocument()
    expect(section('用量')).toBeInTheDocument()
    expect(section('异常与失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '切换范围，当前：全局' })).toBeInTheDocument()
    const links = within(screen.getByRole('navigation', { name: '主导航' })).getAllByRole('link')
    expect(links[1]).toHaveTextContent('概览')
    expect(links[1]).toHaveAttribute('aria-current', 'page')
    expect(links.filter((link) => link.getAttribute('aria-current') === 'page')).toHaveLength(1)
  })

  it('概览页的内容区不显示服务健康，左侧栏的连接状态照常显示', async () => {
    const { snapshot } = await renderOverview({
      handlers: [
        http.get(apiUrl('/health'), () =>
          HttpResponse.json({ ok: false, postgres: { ok: true, error: null }, redis: { ok: false, error: 'connection refused' } }, { status: 503 }),
        ),
      ],
    })
    await snapshot([makeAgentOverview()])
    const status = screen.getByRole('status', { name: '连接状态' })
    await waitFor(() => expect(status).toHaveTextContent('部分依赖不可用'))
    const main = screen.getByRole('main')
    expect(main).not.toHaveTextContent('Redis')
    expect(main).not.toHaveTextContent('PostgreSQL')
    expect(main).not.toHaveTextContent('部分依赖不可用')
  })

  it('没有 Agent 时显示空状态与新建 Agent 的入口，不显示三块内容', async () => {
    const { snapshot, user } = await renderOverview()
    await snapshot([])
    const empty = screen.getByRole('region', { name: '还没有 Agent。' })
    expect(empty).toHaveTextContent('新建一个 Agent 后，这里会显示它的运行状态、用量和异常。')
    expect(screen.queryByRole('region', { name: '用量' })).toBeNull()
    await user.click(within(empty).getByRole('button', { name: '新建 Agent' }))
    expect(await screen.findByRole('dialog', { name: '新建 Agent' })).toBeInTheDocument()
  })

  it('只有用量加载失败时，只在用量一块提示错误、请求编号与重试，其余照常显示', async () => {
    let fail = true
    const { snapshot, user } = await renderOverview({
      anomalies: anomalyPage([makeAnomaly({ agentName: '测试助手' })]),
      handlers: [
        http.get(apiUrl('/usage'), () =>
          fail
            ? HttpResponse.json(errorBody('internal_error', '服务端内部错误'), {
                status: 500,
                headers: { 'Loop-Now-Request-Id': '01890a5d-ac96-774b-bcce-b302099a8057' },
              })
            : HttpResponse.json(usagePage([makeUsageRow({ input: 1200, output: 34 })])),
        ),
      ],
    })
    await snapshot([makeAgentOverview({ name: '开发助手' })])
    const usage = section('用量')
    const alert = await within(usage).findByRole('alert')
    expect(alert).toHaveTextContent('用量加载失败')
    expect(alert).toHaveTextContent('01890a5d-ac96-774b-bcce-b302099a8057')
    expect(within(section('运行状态')).getByRole('table')).toHaveTextContent('开发助手')
    expect(await within(section('异常与失败')).findByText('测试助手')).toBeInTheDocument()
    expect(within(section('异常与失败')).queryByRole('alert')).toBeNull()
    fail = false
    await user.click(within(alert).getByRole('button', { name: '重试' }))
    expect(await within(usage).findByText('1,234', { selector: 'span.riso-misreg' })).toBeInTheDocument()
    expect(within(usage).queryByRole('alert')).toBeNull()
  })
})

describe('运行状态', () => {
  it('Agent 开始回复：不刷新页面就显示回复中与当前阶段，“回复中”的汇总数加一', async () => {
    const agent = makeAgentOverview({ name: '开发助手' })
    const { snapshot, emit } = await renderOverview()
    await snapshot([agent, makeAgentOverview({ name: '测试助手' })])
    const tiles = screen.getByRole('list', { name: '运行状态汇总' })
    const busyTile = within(tiles).getByText('回复中').closest('li')!
    expect(busyTile).toHaveTextContent('0')
    expect(within(tiles).getByText('空闲').closest('li')).toHaveTextContent('2')

    const run = makeRun({ status: 'starting', phase: 'preparing', endedAt: null, runningAt: null, configVersion: 3 })
    await emit('agent.status', { agent: { ...agent, status: makeStatus(agent.agentId, { busy: true, currentRun: run, processState: 'starting' }) } })
    expect(busyTile).toHaveTextContent('1')
    expect(busyTile).toHaveTextContent('开发助手')
    const row = within(screen.getByRole('table')).getByText('开发助手').closest('tr')!
    expect(row).toHaveTextContent('回复中')
    expect(row).toHaveTextContent('准备运行环境')
    expect(row).toHaveTextContent('配置 v3')
  })

  it('排队数与会话丢失计入汇总', async () => {
    const { snapshot } = await renderOverview()
    const busy = makeAgentOverview({ name: 'A' })
    busy.status = makeStatus(busy.agentId, { busy: true, currentRun: makeRun({ status: 'running', endedAt: null }), queued: { interrupt: 1, normal: 2 } })
    const missing = makeAgentOverview({ name: 'B' })
    missing.status = makeStatus(missing.agentId, { session: { state: 'missing', epoch: 1 } })
    await snapshot([busy, missing])
    const tiles = screen.getByRole('list', { name: '运行状态汇总' })
    expect(within(tiles).getByText('会话丢失').closest('li')).toHaveTextContent(/会话丢失\s*1\s*B/)
    expect(within(tiles).getByText('排队中').closest('li')).toHaveTextContent('3打断 1 条 · 普通 2 条')
    const rowA = within(screen.getByRole('table')).getByText('A').closest('tr')!
    expect(rowA).toHaveTextContent('排队 3 条：打断 1 条，普通 2 条')
    expect(within(screen.getByRole('table')).getByText('B').closest('tr')).toHaveTextContent('会话丢失')
  })

  it('Pi 进程空闲退出后显示未运行，并说明退出的时间与原因', async () => {
    const agent = makeAgentOverview({ name: '开发助手' })
    agent.status = makeStatus(agent.agentId, { processState: 'idle', processPid: 4321, processStartedAt: new Date(Date.now() - 60_000).toISOString() })
    const { snapshot, emit } = await renderOverview()
    await snapshot([agent])
    const row = () => within(screen.getByRole('table')).getByText('开发助手').closest('tr')!
    expect(row()).toHaveTextContent('待命 · PID 4321')
    const exitAt = new Date()
    exitAt.setHours(10, 20, 0, 0)
    await emit('agent.status', {
      agent: { ...agent, status: makeStatus(agent.agentId, { processState: 'stopped' }), lastProcessExit: { at: exitAt.toISOString(), reason: 'idle_timeout' } },
    })
    expect(row()).toHaveTextContent('已停止')
    expect(row()).not.toHaveTextContent('PID')
    expect(row()).toHaveTextContent('10:20 · 空闲超时退出')
  })

  it('显示上一条回复的结果与结束时间', async () => {
    const failed = makeAgentOverview({ name: 'A', lastRun: makeRun({ status: 'failed', errorCode: 'session_missing' }) })
    const done = makeAgentOverview({ name: 'B', lastRun: makeRun({ status: 'completed' }) })
    const { snapshot } = await renderOverview()
    await snapshot([failed, done])
    const table = screen.getByRole('table')
    expect(within(table).getByText('A').closest('tr')).toHaveTextContent('上一条失败：会话文件丢失')
    expect(within(table).getByText('A').closest('tr')).toHaveTextContent('errorCode=session_missing')
    expect(within(table).getByText('B').closest('tr')).toHaveTextContent('上一条已完成')
  })

  it('点击 Agent 进入它的对话页', async () => {
    const agent = makeAgent({ name: '开发助手' })
    const { snapshot, user } = await renderOverview({ handlers: baseHandlers([agent]) })
    await snapshot([makeAgentOverview({ agentId: agent.id, name: agent.name })])
    await user.click(within(screen.getByRole('table')).getByRole('link', { name: /开发助手/ }))
    expect(await screen.findByRole('log', { name: '对话' })).toBeInTheDocument()
  })

  it('Agent 被删除后从表中移除；新建的 Agent 出现在表中', async () => {
    const a = makeAgentOverview({ name: 'A' })
    const { snapshot, emit } = await renderOverview()
    await snapshot([a])
    await emit('agent.status', { agent: makeAgentOverview({ name: 'C' }) })
    expect(within(screen.getByRole('table')).getByText('C')).toBeInTheDocument()
    await emit('agent.removed', { agentId: a.agentId, scope: 'project', projectId: null })
    expect(within(screen.getByRole('table')).queryByText('A')).toBeNull()
  })

  it('事件流断开时显示正在重连，恢复后显示最新状态', async () => {
    const agent = makeAgentOverview({ name: '开发助手' })
    const { snapshot, source, emit } = await renderOverview()
    await snapshot([agent])
    expect(screen.getByText('运行状态实时更新中')).toBeInTheDocument()
    await act(async () => source().fail())
    expect(await screen.findByText('连接中断，正在重连…')).toBeInTheDocument()
    await waitFor(() => expect(FakeEventSource.instances.filter((s) => s.url.endsWith('/overview/events'))).toHaveLength(2), { timeout: 3000 })
    await act(async () => source().open())
    await emit('snapshot', { agents: [{ ...agent, name: '改名后的助手' }] })
    expect(screen.getByText('运行状态实时更新中')).toBeInTheDocument()
    expect(within(screen.getByRole('table')).getByText('改名后的助手')).toBeInTheDocument()
  })
})

describe('用量', () => {
  it('默认显示今天、按 Agent；显示合计与四项明细，逐行显示总数并能查看明细，不显示费用', async () => {
    const { snapshot } = await renderOverview({
      usage: usagePage([
        makeUsageRow({ agentName: '开发助手', input: 92140, output: 8420, cacheRead: 61440, cacheWrite: 4096 }),
        makeUsageRow({ agentName: '旧助手', agentDeleted: true, input: 100, output: 10 }),
      ]),
    })
    await snapshot([makeAgentOverview()])
    const usage = section('用量')
    expect(within(usage).getByRole('radio', { name: '今天' })).toBeChecked()
    expect(within(usage).getByRole('radio', { name: '按 Agent' })).toBeChecked()
    expect(await within(usage).findByText('今天合计')).toBeInTheDocument()
    expect(within(usage).getByText('166,206', { selector: 'span.riso-misreg' })).toBeInTheDocument()
    const rows = within(usage).getAllByRole('listitem')
    expect(rows[0]).toHaveTextContent('开发助手')
    expect(rows[0]).toHaveAccessibleDescription('输入 92,140 · 输出 8,420 · 缓存读取 61,440 · 缓存写入 4,096 · 合计 166,096')
    expect(rows[1]).toHaveTextContent('旧助手已删除')
    expect(screen.getByRole('main')).not.toHaveTextContent(/费用|成本|美元|USD|\$/)
  })

  it('切换到本周时，从本周一零点开始统计', async () => {
    const froms: string[] = []
    const { snapshot, user } = await renderOverview({
      handlers: [
        http.get(apiUrl('/usage'), ({ request }) => {
          froms.push(new URL(request.url).searchParams.get('from')!)
          return HttpResponse.json(usagePage([makeUsageRow()]))
        }),
      ],
    })
    await snapshot([makeAgentOverview()])
    await waitFor(() => expect(froms).toHaveLength(1))
    await user.click(within(section('用量')).getByRole('radio', { name: '本周' }))
    await waitFor(() => expect(froms).toHaveLength(2))
    const monday = new Date(froms[1])
    expect(monday.getDay()).toBe(1)
    expect([monday.getHours(), monday.getMinutes(), monday.getSeconds()]).toEqual([0, 0, 0])
    expect(Date.now() - monday.getTime()).toBeLessThan(7 * 24 * 60 * 60 * 1000)
    expect(await within(section('用量')).findByText('本周合计')).toBeInTheDocument()
  })

  it('按模型查看时，同一模型的数值为各 Agent 之和；没有模型的显示为未知模型', async () => {
    const { snapshot, user } = await renderOverview({
      usage: usagePage([
        makeUsageRow({ agentName: 'A', model: 'x', input: 100, output: 10 }),
        makeUsageRow({ agentName: 'B', model: 'x', input: 200, output: 20 }),
        makeUsageRow({ agentName: 'B', provider: null, model: null, input: 5, output: 0 }),
      ]),
    })
    await snapshot([makeAgentOverview()])
    const usage = section('用量')
    await within(usage).findByText('今天合计')
    await user.click(within(usage).getByRole('radio', { name: '按模型' }))
    const rows = within(usage).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('anthropic / x')
    expect(rows[0]).toHaveTextContent('330')
    expect(rows[1]).toHaveTextContent('未知模型')
  })

  it('所选时间段没有用量时显示空状态', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    expect(await within(section('用量')).findByText('今天还没有用量。')).toBeInTheDocument()
  })
})

describe('异常与失败', () => {
  it('以最近 24 小时为开始时间，显示总条数，从新到旧列出，可以加载更多', async () => {
    const requests: URLSearchParams[] = []
    const first = [makeAnomaly({ agentName: 'B', kind: 'run_interrupted', errorCode: null, errorMessage: null, endReason: 'user_interrupt' })]
    const second = [makeAnomaly({ agentName: 'A', errorCode: 'workspace_sync_failed', errorMessage: '克隆失败：Authentication failed' })]
    const { snapshot, user } = await renderOverview({
      handlers: [
        http.get(apiUrl('/anomalies'), ({ request }) => {
          const params = new URL(request.url).searchParams
          requests.push(params)
          return HttpResponse.json(params.get('cursor') ? anomalyPage(second, 2) : anomalyPage(first, 2, 'c1'))
        }),
      ],
    })
    await snapshot([makeAgentOverview()])
    const list = section('异常与失败')
    expect(await within(list).findByText('最近 24 小时 · 2 条')).toBeInTheDocument()
    const from = Date.parse(requests[0].get('from')!)
    expect(Math.abs(Date.now() - 24 * 60 * 60 * 1000 - from)).toBeLessThan(60_000)
    await user.click(within(list).getByRole('button', { name: '加载更多' }))
    await waitFor(() => expect(within(list).getAllByRole('link')).toHaveLength(2))
    expect(requests[1].get('cursor')).toBe('c1')
    expect(requests[1].get('from')).toBe(requests[0].get('from'))
    const [b, a] = within(list).getAllByRole('link')
    expect(b).toHaveTextContent('已打断')
    expect(b).toHaveTextContent('endReason=user_interrupt')
    expect(a).toHaveTextContent('errorCode=workspace_sync_failed')
    expect(a).toHaveTextContent('工作区同步失败：克隆失败：Authentication failed')
    expect(within(list).queryByRole('button', { name: '加载更多' })).toBeNull()
  })

  it('CLI 安装失败显示为失败，原因中包含 CLI 名称', async () => {
    const { snapshot } = await renderOverview({
      anomalies: anomalyPage([makeAnomaly({ errorCode: 'cli_install_failed', errorMessage: 'gh 2.100.0：下载的文件 SHA-256 与模板不一致' })]),
    })
    await snapshot([makeAgentOverview()])
    const item = await within(section('异常与失败')).findByRole('link')
    expect(item).toHaveTextContent('失败')
    expect(item).toHaveTextContent('CLI 安装失败：gh 2.100.0')
  })

  it('会话文件丢失进入对话页、不带锚点；其他条目定位到这条回复', async () => {
    const missing = makeAnomaly({ errorCode: 'session_missing', errorMessage: null })
    const other = makeAnomaly()
    const { snapshot } = await renderOverview({ anomalies: anomalyPage([missing, other]) })
    await snapshot([makeAgentOverview()])
    const [a, b] = await within(section('异常与失败')).findAllByRole('link')
    expect(a).toHaveAttribute('href', `/agents/${missing.agentId}/chat`)
    expect(a).toHaveTextContent('去处理')
    expect(b).toHaveAttribute('href', `/agents/${other.agentId}/chat#run-${other.runId}`)
    expect(b).toHaveTextContent('查看回复')
  })

  it('最近 24 小时没有异常时显示空状态', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    expect(await within(section('异常与失败')).findByText('最近 24 小时没有异常。')).toBeInTheDocument()
  })
})
