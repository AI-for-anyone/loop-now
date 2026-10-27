import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { localTime, statsWindow } from '@/api/stats'
import { routes } from '@/app/routes'
import { apiUrl, defaultProject, makeAgent } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { currentSidebarLinks, baseHandlers } from '@/test/shell'
import { emptyStatsFixture, statsFixture, type StatsAgentName, type StatsAgentRef } from '@/test/stats-fixture'

const NAMES: StatsAgentName[] = ['web', 'server', 'manager', 'test', 'worker', 'cli']
const region = (name: string) => screen.getByRole('region', { name })
const project = `/p/${encodeURIComponent(defaultProject.name)}`

function renderStats({ path = '/stats', report }: { path?: string; report?: (refs: Record<StatsAgentName, StatsAgentRef>) => Schemas['StatsReport'] } = {}) {
  const agents = NAMES.map((name) => makeAgent({ name }))
  const refs = Object.fromEntries(agents.map((a) => [a.name, { agentId: a.id, projectId: a.projectId }])) as Record<StatsAgentName, StatsAgentRef>
  const requests: URLSearchParams[] = []
  server.use(
    http.get(apiUrl('/stats'), ({ request }) => {
      requests.push(new URL(request.url).searchParams)
      return HttpResponse.json(report ? report(refs) : statsFixture(refs))
    }),
    ...baseHandlers(agents),
  )
  return { ...renderRoutes(routes, path), agents, refs, requests }
}

describe('统计页', () => {
  it('左侧栏的“统计”是链接，打开后为当前项；默认近 7 天、全部项目，按天分桶', async () => {
    const { requests } = renderStats()
    expect(await screen.findByRole('heading', { name: '统计', level: 1 })).toBeInTheDocument()
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['统计'])
    await screen.findByRole('region', { name: '关键指标' })
    const { from } = statsWindow('week')
    expect(requests[0].get('bucket')).toBe('day')
    expect(requests[0].get('from')).toBe(localTime(from))
    expect(Date.parse(requests[0].get('to')!)).toBeGreaterThan(from.getTime())
    expect(requests[0].has('projectId')).toBe(false)
    expect(requests[0].has('platform')).toBe(false)
    expect(screen.getByRole('radio', { name: '近 7 天' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('combobox', { name: '范围' })).toHaveDisplayValue('全部项目')
  })

  it('六个关键指标：数值、说明，token 合计带按 API 单价的估算花费', async () => {
    renderStats()
    const kpis = await screen.findByRole('region', { name: '关键指标' })
    const tile = (label: string) => within(kpis).getByText(label).parentElement!
    expect(kpis.children).toHaveLength(6)
    expect(tile('无人要求的消耗')).toHaveTextContent('57%定时唤醒 84 次，3,720 万')
    expect(tile('每次回复平均上下文')).toHaveTextContent('9.1 万压缩 55 次，web 占 21')
    expect(tile('发出去多久开始回 p50')).toHaveTextContent('6 秒p90 2.4 分')
    expect(tile('失败 + 中断')).toHaveTextContent('76 次失败 25，中断 51')
    expect(tile('等你回答')).toHaveTextContent('14 个你平均 6 分钟回，现在 2 个在等')
    expect(tile('token 合计')).toHaveTextContent(/^token 合计6,510 万估算 \$[\d,]+，缓存命中 79%$/)
    expect(within(region('模型与进程')).getByText('按 API 单价折算，仅供参考')).toBeInTheDocument()
  })

  it('多系列的图都有图例；键盘移到某一列时提示框列出各系列与合计', async () => {
    renderStats()
    const origins = await screen.findByRole('region', { name: '每天用量，按谁触发' })
    expect(within(origins).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['你发的消息', '定时唤醒', 'Agent 互相提醒 / 任务', '其他'])
    expect(within(region('回复结果')).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['完成', '被打断 / 停止', '失败'])
    expect(within(region('发出去多久开始回')).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['一半在此之内（p50）', '九成（p90）'])

    const chart = within(origins).getByRole('group')
    fireEvent.keyDown(chart, { key: 'End' })
    const tip = within(origins).getByRole('tooltip')
    expect(chart).toHaveAttribute('aria-describedby', tip.id)
    expect(tip).toHaveTextContent('今天210 万你发的消息430 万定时唤醒80 万Agent 互相提醒 / 任务3 万其他723 万合计')
    fireEvent.keyDown(chart, { key: 'ArrowLeft' })
    expect(within(origins).getByRole('tooltip')).toHaveTextContent('380 万你发的消息')
    fireEvent.keyDown(chart, { key: 'Escape' })
    expect(within(origins).queryByRole('tooltip')).toBeNull()
  })

  it('平均上下文超过 12 万的 Agent 标“该重开”', async () => {
    renderStats()
    const card = await screen.findByRole('region', { name: '每次回复吃多少上下文' })
    const row = (name: string) => within(card).getByText(name).closest('li')!
    expect(row('web')).toHaveTextContent('14.2 万压缩 21 次该重开')
    expect(row('test')).toHaveTextContent('9.8 万压缩 9 次')
    expect(within(card).getAllByText('该重开')).toHaveLength(1)
  })

  it('等你回答的问题打开那个 Agent 的对话；“去看它的提示词”到唤醒消耗最多的 Agent 的提示词区块', async () => {
    const { refs } = renderStats()
    const questions = await screen.findByRole('region', { name: '等你回答' })
    const link = within(questions).getByRole('link', { name: /侧栏宽度 244 还是 260/ })
    expect(link).toHaveAttribute('href', `${project}/agents/${refs.web.agentId}/chat`)
    expect(link).toHaveTextContent('等了 5 分钟')
    const wakes = region('谁在睡醒后烧钱')
    expect(wakes).toHaveTextContent('定时唤醒 84 次，共 3,720 万')
    expect(wakes).toHaveTextContent('每 4 小时醒一次，每次 85 万')
    expect(wakes).toHaveTextContent('web 每次醒来平均 85 万，是其他 Agent 的 16 倍。')
    expect(within(wakes).getByRole('link', { name: '去看它的提示词' })).toHaveAttribute('href', `${project}/agents/${refs.web.agentId}/config#config-prompt`)
  })

  it('没完成的原因按结束原因与错误码写成中文，不认识的直接显示代码', async () => {
    renderStats()
    const card = await screen.findByRole('region', { name: '没完成的原因' })
    const rows = within(card).getAllByRole('listitem').map((item) => item.textContent)
    expect(rows).toEqual(['打断你发了打断消息30', '停止你按了停止16', '失败模型服务报错，重试后仍失败12', '失败Pi 进程异常退出7', '失败rate_limited3', '失败工作区同步失败3'])
    expect(card).toHaveTextContent('共 76 次')
    expect(card).toHaveTextContent('失败 25 次里有 12 次是模型服务报错，重试后仍失败。')
  })

  it('估算花费只算有单价的模型，没有单价的在说明里列出', async () => {
    renderStats({
      report: (refs) => {
        const report = statsFixture(refs)
        return { ...report, tokens: { ...report.tokens, byModel: [...report.tokens.byModel, { provider: 'p', model: 'house-model-1', total: 1000 }] } }
      },
    })
    const card = await screen.findByRole('region', { name: '模型与进程' })
    expect(card).toHaveTextContent('未计入没有单价的模型：house-model-1')
    expect(card).toHaveTextContent('Claude 71%')
  })

  it('换成“今天”按小时分桶，从本地零点起；范围选项目时带上项目 ID', async () => {
    const user = userEvent.setup()
    const { requests } = renderStats({ path: `/stats?project=${encodeURIComponent(defaultProject.name)}` })
    await screen.findByRole('region', { name: '关键指标' })
    expect(requests.at(-1)!.get('projectId')).toBe(defaultProject.id)
    await user.click(screen.getByRole('radio', { name: '今天' }))
    await waitFor(() => expect(requests.at(-1)!.get('bucket')).toBe('hour'))
    expect(requests.at(-1)!.get('from')).toBe(localTime(statsWindow('today').from))
    expect(requests.at(-1)!.get('projectId')).toBe(defaultProject.id)
    await user.selectOptions(screen.getByRole('combobox', { name: '范围' }), '平台')
    await waitFor(() => expect(requests.at(-1)!.get('platform')).toBe('true'))
  })

  it('这段时间没有记录时只显示一行说明，不画空图', async () => {
    renderStats({ report: () => emptyStatsFixture() })
    expect(await screen.findByText('这段时间没有记录')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: '关键指标' })).toBeNull()
    expect(screen.queryByRole('region', { name: '每天用量，按谁触发' })).toBeNull()
    expect(screen.getByRole('main').querySelector('svg[width]')).toBeNull()
  })
})
