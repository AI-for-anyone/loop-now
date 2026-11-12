import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultAgentsPath, makeAgent, platformAgentsUrl, toListItem } from '@/test/api'
import { agentParty, consoleParty, emitInbox, externalParty, makeIdentity, makeInboxMessage, openInboxStream, summaryOf } from '@/test/inbox'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const agent = makeAgent({ name: '代码审查助手', address: '测试组织/测试项目/代码审查助手' })
const tester = makeAgent({ name: '测试助手', address: '测试组织/测试项目/测试助手' })

// 打开 Agent 的收件箱页签，记录对收件箱接口的全部请求（方法、路径与查询参数）。
async function renderAgentInbox(messages: Schemas['InboxMessage'][]) {
  const requests: string[] = []
  server.use(
    http.all(apiUrl('/inbox/*'), ({ request }) => {
      requests.push(`${request.method} ${new URL(request.url).pathname}`)
      return HttpResponse.json({}, { status: 500 })
    }),
    http.get(apiUrl(`/agents/${agent.id}/inbox`), ({ request }) => {
      const url = new URL(request.url)
      requests.push(`GET ${url.pathname}${url.search}`)
      const unread = url.searchParams.get('unread') === 'true'
      const list = messages.filter((m) => !unread || !m.readAt).map(summaryOf)
      return HttpResponse.json({ messages: list, nextCursor: null })
    }),
    http.get(apiUrl(`/agents/${agent.id}/inbox/:messageId`), ({ request, params }) => {
      requests.push(`GET ${new URL(request.url).pathname}`)
      return HttpResponse.json(messages.find((m) => m.id === params.messageId))
    }),
    ...baseHandlers([agent, tester]),
  )
  const user = userEvent.setup()
  const result = renderRoutes(routes, `${defaultAgentsPath}/${agent.id}/inbox`)
  const list = within(await screen.findByRole('list', { name: '代码审查助手收到的消息' }))
  return { ...result, user, list, requests }
}

const received = () => [
  makeInboxMessage({ from: agentParty(tester), to: agentParty(agent), text: 'naming 包的测试我补了两条，跑一下看看。' }),
  makeInboxMessage({ from: consoleParty, to: agentParty(agent), text: '帮我审一下 naming 包', whenBusy: 'interrupt', readAt: new Date().toISOString() }),
]

describe('每个 Agent 的收件箱只读', () => {
  it('被人取消的排队消息标明已被人取消，不算未读', async () => {
    const cancelled = makeInboxMessage({ from: agentParty(tester), to: agentParty(agent), text: '这条不用处理了', cancelledAt: '2026-10-25T08:00:00Z' })
    const { list } = await renderAgentInbox([cancelled])
    const row = list.getByRole('button', { name: /测试助手/ })
    expect(within(row).getByText('已被人取消')).toBeInTheDocument()
    expect(within(row).queryByText('未读')).toBeNull()
  })

  it('查看 Agent 收到的消息：列出发件人、已读或未读，打断的消息标明', async () => {
    const [first, second] = received()
    const { list } = await renderAgentInbox([second, first])
    const items = list.getAllByRole('listitem')
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent(/控制台@console打断.*已读 \d{2}:\d{2}/)
    expect(items[1]).toHaveTextContent(/测试助手测试组织\/测试项目\/测试助手.*naming 包的测试我补了两条.*未读$/)
    expect(screen.getByText('只读查看：打开全文不会改变已读状态')).toBeInTheDocument()
  })

  it('只看未读：列表中只有 Agent 尚未读取的消息', async () => {
    const [first, second] = received()
    const { user, requests } = await renderAgentInbox([second, first])
    await user.click(screen.getByRole('radio', { name: '只看未读' }))
    await waitFor(() => expect(requests).toContain(`GET /api/v1/agents/${agent.id}/inbox?limit=20&unread=true`))
    const list = within(await screen.findByRole('list', { name: '代码审查助手收到的消息' }))
    await waitFor(() => expect(list.getAllByRole('listitem')).toHaveLength(1))
    expect(list.getByRole('listitem')).toHaveTextContent('测试助手')
  })

  it('只看未读且没有未读消息时，说明没有未读消息', async () => {
    const [, second] = received()
    const { user } = await renderAgentInbox([second])
    await user.click(screen.getByRole('radio', { name: '只看未读' }))
    expect(await screen.findByText('没有未读消息。')).toBeInTheDocument()
  })

  it('查看全文不改变已读状态：只请求只读的全文接口，没有标记已读的操作', async () => {
    const [first] = received()
    const { user, list, requests } = await renderAgentInbox([first])
    await user.click(list.getByRole('button', { name: /测试助手/ }))
    expect(await list.findByText('naming 包的测试我补了两条，跑一下看看。', { selector: 'p' })).toBeInTheDocument()
    expect(requests.filter((r) => !r.startsWith('GET'))).toEqual([])
    expect(requests).toContain(`GET /api/v1/agents/${agent.id}/inbox/${first.id}`)
    expect(within(list.getByRole('button', { name: /测试助手/ })).getByText('未读')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /标为已读|标为未读/ })).toBeNull()
  })

  it('Agent 读取消息后，已读状态随收件箱事件流实时变化', async () => {
    const [first] = received()
    const { list } = await renderAgentInbox([first])
    await openInboxStream(0)
    const readAt = new Date().toISOString()
    first.readAt = readAt
    await emitInbox('inbox.message.read', { id: first.id, threadId: first.threadId, to: agentParty(agent), readAt, unreadTotal: 0 })
    await waitFor(() => expect(list.getByRole('listitem')).toHaveTextContent(/已读 \d{2}:\d{2}$/))
  })

  it('新消息到达时列表随之更新；与其他 Agent、操作者无关的消息不影响', async () => {
    const [first] = received()
    const messages = [first]
    const { list } = await renderAgentInbox(messages)
    await openInboxStream(0)
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const fresh = makeInboxMessage({ from: externalParty(ciBot), to: agentParty(agent), text: '夜间构建失败了' })
    messages.unshift(fresh)
    await emitInbox('inbox.message.created', { message: summaryOf(fresh), unreadTotal: 0 })
    await waitFor(() => expect(list.getAllByRole('listitem')).toHaveLength(2))
    expect(list.getAllByRole('listitem')[0]).toHaveTextContent('ci-bot@ci-bot')
  })
})

describe('显示 Agent 地址', () => {
  it('复制地址：剪贴板中是完整地址', async () => {
    const { user } = await renderAgentInbox(received().slice(0, 1))
    expect(screen.getByText('测试组织/测试项目/代码审查助手')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '复制地址' }))
    await expect(navigator.clipboard.readText()).resolves.toBe('测试组织/测试项目/代码审查助手')
    expect(await screen.findByRole('button', { name: '复制地址' })).toHaveTextContent('已复制')
  })

  it('平台 Agent 的地址是名称本身', async () => {
    const watcher = makeAgent({ name: '联调巡检', scope: 'platform', projectId: null, address: '联调巡检' })
    server.use(http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(watcher)] })), ...baseHandlers([watcher]))
    renderRoutes(routes, `/platform/agents/${watcher.id}/config`)
    expect(await screen.findByText('联调巡检', { selector: 'span' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '复制地址' })).toBeInTheDocument()
  })

  it('名称不满足规则时没有地址：说明需要先改名，发消息时改用内部 ID', async () => {
    const legacy = makeAgent({ name: 'old/bot', address: null })
    server.use(...baseHandlers([legacy]))
    renderRoutes(routes, `${defaultAgentsPath}/${legacy.id}/config`)
    expect(await screen.findByText(/这个 Agent 暂时没有地址.*改名后才有地址.*改用内部 ID/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '复制地址' })).toBeNull()
  })
})

// 改版“一张印出来的纸”第 2 步：收件箱页签直接排在纸上，右侧票根里是未读数与总数。
describe('纸张上的收件箱', () => {
  it('筛选是两颗胶囊：选中的一颗黄底、藏青描边加藏青投影；顶上一行与列表之间一道实线，只读说明是一行小字', async () => {
    await renderAgentInbox(received())
    const all = screen.getByRole('radio', { name: '全部' })
    const unread = screen.getByRole('radio', { name: '只看未读' })
    for (const pill of [all, unread]) expect(pill).toHaveClass('h-[34px]', 'rounded-full', 'border-2')
    expect(all).toHaveAttribute('aria-checked', 'true')
    expect(all).toHaveClass('data-[state=on]:bg-riso-yellow', 'data-[state=on]:border-ink', 'data-[state=on]:shadow-[3px_3px_0_var(--ink)]')
    const bar = all.closest('[data-inbox-bar]')!
    expect(bar).toHaveClass('border-b-2', 'border-ink')
    const note = within(bar as HTMLElement).getByText('只读查看：打开全文不会改变已读状态')
    expect(note).not.toHaveClass('bg-tint-yellow')
  })

  it('每封一行：发件方与等宽的地址、单行的正文、等宽的时间；未读是倾斜的粉色描边印章，已读是浅蓝胶囊；行与行之间虚线', async () => {
    const [first, second] = received()
    const { list } = await renderAgentInbox([second, first])
    const [read, fresh] = list.getAllByRole('listitem')
    for (const item of [read, fresh]) expect(item).toHaveClass('border-b-[1.5px]', 'border-dashed')
    expect(within(fresh).getByText('测试组织/测试项目/测试助手')).toHaveClass('font-mono', 'truncate')
    expect(within(fresh).getByText('naming 包的测试我补了两条，跑一下看看。')).toHaveClass('truncate', 'font-bold')
    expect(fresh.querySelector('time')).toHaveClass('font-mono')
    const stamp = within(fresh).getByText('未读').closest('[data-read-state]')!
    expect(stamp).toHaveAttribute('data-read-state', 'unread')
    expect(stamp).toHaveClass('riso-tilt', 'text-pink-text')
    const pill = within(read).getByText(/^已读 /).closest('[data-read-state]')!
    expect(pill).toHaveAttribute('data-read-state', 'read')
    expect(pill).toHaveClass('rounded-full', 'bg-tint-blue')
    // 已读的正文是次要文字色、不加粗。
    expect(within(read).getByText('帮我审一下 naming 包')).not.toHaveClass('font-bold')
  })

  it('票根“信件”写着未读数与总数；窄屏不显示', async () => {
    const [first, second] = received()
    await renderAgentInbox([second, first])
    const stub = await screen.findByRole('complementary', { name: '信件' })
    expect(stub).toHaveClass('max-nav:hidden')
    await waitFor(() => expect(within(stub).getByText('未读').previousElementSibling).toHaveTextContent(/^1$/))
    expect(within(stub).getByText('全部').previousElementSibling).toHaveTextContent(/^2$/)
    expect(within(stub).getByText('未读').previousElementSibling).toHaveClass('text-pink-text')
  })

  it('只看未读时，票根仍按全部信件计数', async () => {
    const [first, second] = received()
    const { user } = await renderAgentInbox([second, first])
    await user.click(screen.getByRole('radio', { name: '只看未读' }))
    const stub = screen.getByRole('complementary', { name: '信件' })
    await waitFor(() => expect(within(stub).getByText('全部').previousElementSibling).toHaveTextContent(/^2$/))
  })
})
