import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultAgentsPath, defaultProject, makeAgent, makeMessage, makeRun } from '@/test/api'
import { renderChat } from '@/test/chat'
import { agentParty, consoleParty, externalParty, fakeInbox, makeIdentity, makeInboxMessage, pendingOf, summaryOf } from '@/test/inbox'
import { FakeIntersectionObserver } from '@/test/intersection-observer'
import { makeAgentOverview, renderOverview } from '@/test/overview'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { makeTaskDetail, taskId, nodeId } from '@/test/tasks'
import { InboxRedirect } from './InboxRedirect'

type Message = Schemas['InboxMessage']
const open = (options: string[]): Message['question'] => ({ options: options.map((label, i) => ({ id: String(i + 1), label })), expiresAt: '2099-01-01T18:00:00Z', state: 'open', answer: null })

function chatSetup() {
  const agent = makeAgent({ name: '开发助手' })
  const run = makeRun({ queuedAt: '2026-10-12T08:00:00Z', startedAt: '2026-10-12T08:00:00Z', endedAt: '2026-10-12T08:00:30Z' })
  const turn = [makeMessage({ text: '先改写入顺序', run, createdAt: '2026-10-12T08:00:00Z' }), makeMessage({ role: 'assistant', text: '改完了', run, createdAt: '2026-10-12T08:00:01Z' })]
  const note = makeInboxMessage({ from: agentParty(agent), to: consoleParty, text: 'server-main 已合并，请确认预期值', whenBusy: null })
  const question = makeInboxMessage({ from: agentParty(agent), to: consoleParty, text: '预期值按 v3 新表还是旧表？', whenBusy: null, question: open(['按 v3 新表', '保留旧表']) })
  const mine = makeInboxMessage({ from: consoleParty, to: agentParty(agent), text: '你发给它的消息不在这里重复' })
  const inbox = fakeInbox({ messages: [note, question, mine] })
  return { agent, turn, note, question, inbox }
}

const consoleItem = (message: Message) => {
  const el = document.querySelector<HTMLElement>(`[data-console-message-id="${message.id}"]`)
  if (!el) throw new Error('message not rendered yet')
  return el
}

describe('Agent 对话里发给你的消息', () => {
  it('按时间排进对话，名称一行带“发给你”与未读圆点；你发给它的不重复显示；顶栏没有收件箱', async () => {
    const { agent, turn, note, question, inbox } = chatSetup()
    await renderChat({ agent, messages: turn, handlers: inbox.handlers })
    const item = await waitFor(() => consoleItem(note))
    expect(within(item).getByText('发给你')).toBeVisible()
    expect(within(item).getByText('未读')).toBeInTheDocument()
    expect(within(item).getByText('server-main 已合并，请确认预期值')).toBeVisible()
    expect(consoleItem(question)).toBeTruthy()
    expect(screen.queryByText('你发给它的消息不在这里重复')).toBeNull()
    // 排在开始时间更早的那一轮之后。
    const log = screen.getByRole('log')
    const order = [...log.querySelectorAll('[data-run-id], [data-console-message-id]')].map((el) => el.getAttribute('data-console-message-id') ?? 'turn')
    expect(order).toEqual(['turn', note.id, question.id])
    expect(within(screen.getByRole('navigation', { name: '主导航' })).queryByRole('link', { name: /收件箱/ })).toBeNull()
  })

  it('进入视野时标为已读，只标一次，圆点随之去掉', async () => {
    const { agent, turn, note, inbox } = chatSetup()
    await renderChat({ agent, messages: turn, handlers: inbox.handlers })
    const item = await waitFor(() => consoleItem(note))
    act(() => FakeIntersectionObserver.trigger(item, true))
    act(() => FakeIntersectionObserver.trigger(item, true))
    await waitFor(() => expect(within(consoleItem(note)).queryByText('未读')).toBeNull())
    expect(inbox.readMessages).toEqual([note.id])
  })

  it('点选项就提交到这条原题（排队，不带正文）；答过后所选的一项高亮、其余变淡', async () => {
    const { agent, turn, question, inbox } = chatSetup()
    const { user } = await renderChat({ agent, messages: turn, handlers: inbox.handlers })
    const item = await waitFor(() => consoleItem(question))
    expect(within(item).getByText(/问题 · .+ 前有效/)).toBeVisible()
    await user.click(within(item).getByRole('button', { name: '保留旧表' }))
    await waitFor(() => expect(inbox.sent).toEqual([{ optionId: '2', whenBusy: 'queue', replyTo: question.id }]))
    await waitFor(() => expect(within(consoleItem(question)).getByRole('button', { name: '保留旧表' })).toHaveAttribute('aria-pressed', 'true'))
    expect(within(consoleItem(question)).getByRole('button', { name: '按 v3 新表' })).toBeDisabled()
    expect(within(consoleItem(question)).getByText(/你在 .+ 选了「保留旧表」/)).toBeVisible()
  })

  it('迟到的选项（原题已关闭）只提交一次，按钮随之变淡，不改成普通消息', async () => {
    const { agent, turn, question, inbox } = chatSetup()
    let calls = 0
    const closed = http.post(apiUrl(`/inbox/messages/${question.id}/reply`), () => {
      calls++
      return HttpResponse.json({ error: { code: 'question_closed', message: '原题已关闭', details: { question: { ...question.question!, state: 'closed' } } } }, { status: 409 })
    })
    const { user } = await renderChat({ agent, messages: turn, handlers: [closed, ...inbox.handlers] })
    const item = await waitFor(() => consoleItem(question))
    await user.click(within(item).getByRole('button', { name: '按 v3 新表' }))
    expect(await within(consoleItem(question)).findByText('已关闭')).toBeVisible()
    expect(within(consoleItem(question)).getByRole('button', { name: '保留旧表' })).toBeDisabled()
    expect(within(consoleItem(question)).queryByRole('button', { name: '回复这个问题' })).toBeNull()
    expect(calls).toBe(1); expect(inbox.sent).toEqual([])
  })

  it('“回复这个问题”让输入框进入回复模式：文字作为这条原题的回答发出，不发普通对话消息；取消后回到普通发送', async () => {
    const { agent, turn, question, inbox } = chatSetup()
    let chatPosts = 0
    const { user } = await renderChat({ agent, messages: turn, handlers: [...inbox.handlers, http.post(apiUrl(`/agents/${agent.id}/messages`), () => { chatPosts++; return HttpResponse.json({}, { status: 500 }) })] })
    const item = await waitFor(() => consoleItem(question))
    await user.click(within(item).getByRole('button', { name: '回复这个问题' }))
    expect(screen.getByText('回复 开发助手 的问题')).toBeVisible()
    const editor = screen.getByRole('textbox', { name: '消息' })
    expect(editor).toHaveFocus()
    expect(editor).toHaveAttribute('placeholder', '输入对这个问题的回答…')
    await user.click(screen.getByRole('button', { name: '取消回复这个问题' }))
    expect(screen.queryByText('回复 开发助手 的问题')).toBeNull()
    await user.click(within(consoleItem(question)).getByRole('button', { name: '回复这个问题' }))
    await user.type(screen.getByRole('textbox', { name: '消息' }), '按旧表，先别动定价{Enter}')
    await waitFor(() => expect(inbox.sent).toEqual([{ text: '按旧表，先别动定价', whenBusy: 'queue', replyTo: question.id }]))
    await waitFor(() => expect(screen.queryByText('回复 开发助手 的问题')).toBeNull())
    expect(screen.getByRole('textbox', { name: '消息' })).toHaveValue('')
    expect(chatPosts).toBe(0)
  })

  it('文字没有记为答案（响应的 answerTo 不是原题）时说明已存为普通留言', async () => {
    const { agent, turn, question, inbox } = chatSetup()
    const ordinary = http.post(apiUrl(`/inbox/messages/${question.id}/reply`), async ({ request }) => {
      const body = (await request.json()) as Schemas['InboxReplyInput']
      return HttpResponse.json(makeInboxMessage({ from: consoleParty, to: question.from, threadId: question.threadId, inReplyTo: question.id, text: body.text!, answerTo: null }), { status: 201 })
    })
    const { user } = await renderChat({ agent, messages: turn, handlers: [ordinary, ...inbox.handlers] })
    await user.click(within(await waitFor(() => consoleItem(question))).getByRole('button', { name: '回复这个问题' }))
    await user.type(screen.getByRole('textbox', { name: '消息' }), '迟到的文字{Enter}')
    expect(await screen.findByText('已保存为普通留言，未记为问题答案。')).toBeVisible()
  })
})

describe('旧的收件箱地址', () => {
  const renderRedirect = (path: string) => renderRoutes([{ path: '/inbox/:threadId?', element: <InboxRedirect /> }, { path: '*', element: <p>到达</p> }], path)

  it('Agent 的线索转到它的对话，外部身份的到它的页面，读不到或没有线索时回到概览', async () => {
    const agent = makeAgent(), identity = makeIdentity({ name: 'ci-bot' })
    const withAgent = makeInboxMessage({ from: agentParty(agent), to: consoleParty })
    const withIdentity = makeInboxMessage({ from: externalParty(identity), to: consoleParty })
    server.use(...fakeInbox({ messages: [withAgent, withIdentity] }).handlers)
    let view = renderRedirect(`/inbox/${withAgent.threadId}`)
    await waitFor(() => expect(view.router.state.location.pathname).toBe(`/agents/${agent.id}/chat`))
    view.unmount()
    view = renderRedirect(`/inbox/${withIdentity.threadId}`)
    await waitFor(() => expect(view.router.state.location.pathname).toBe(`/identities/${identity.id}`))
    view.unmount()
    view = renderRedirect('/inbox/00000000-0000-7000-a000-999999999999')
    await waitFor(() => expect(view.router.state.location.pathname).toBe('/'))
    view.unmount()
    view = renderRedirect('/inbox')
    await waitFor(() => expect(view.router.state.location.pathname).toBe('/'))
  })

  it('任务实例的线索按消息的任务归属转到那个任务节点', async () => {
    const detail = makeTaskDetail(defaultProject.id)
    const agent = makeAgent()
    const message = makeInboxMessage({ from: agentParty(agent), to: consoleParty, task: { taskId, nodeRunId: nodeId, runtimeId: detail.nodes[1].runtimeId } })
    server.use(...fakeInbox({ messages: [message] }).handlers, http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)))
    const view = renderRedirect(`/inbox/${message.threadId}`)
    await waitFor(() => expect(view.router.state.location.pathname).toBe(`/p/${encodeURIComponent(defaultProject.name)}/tasks/${taskId}`))
    expect(new URLSearchParams(view.router.state.location.search).get('record')).toBe(nodeId)
  })
})

describe('待你处理', () => {
  it('Agent 列表上显示待你回复的数字，读屏说明其中几个问题、几条新消息', async () => {
    const { agent, note, question } = chatSetup()
    const other = makeAgent({ name: '测试助手' })
    server.use(http.get(apiUrl('/inbox/pending'), () => HttpResponse.json(pendingOf([note, question]))), ...baseHandlers([agent, other]))
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agent' })
    const row = await within(list).findByRole('link', { name: /开发助手/ })
    expect(await within(row).findByText('待你回复：1 个问题、1 条新消息')).toBeInTheDocument()
    expect(within(row).getByText('2')).toBeVisible()
    expect(within(within(list).getByRole('link', { name: /测试助手/ })).queryByText(/待你回复/)).toBeNull()
  })

  it('概览顶部列出发给你的问题与新消息，问题在前，各自链接到对应的对话；没有时整块不显示', async () => {
    const { agent, note, question } = chatSetup()
    const { snapshot } = await renderOverview({ handlers: [http.get(apiUrl('/inbox/pending'), () => HttpResponse.json(pendingOf([note, question])))] })
    await snapshot([makeAgentOverview({ agentId: agent.id, name: agent.name })])
    const section = await screen.findByRole('region', { name: '待你处理' })
    const rows = within(section).getAllByRole('link')
    expect(rows.map((row) => row.textContent)).toEqual([expect.stringMatching(/问题.*开发助手.*预期值按 v3 新表还是旧表？.*去回答/), expect.stringMatching(/新消息.*开发助手.*server-main 已合并.*查看/)])
    expect(rows[0]).toHaveAttribute('href', `/agents/${agent.id}/chat`)
  })

  it('没有待你处理的内容时概览上没有这一块', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    await screen.findByRole('heading', { name: '运行状态' })
    expect(screen.queryByRole('region', { name: '待你处理' })).toBeNull()
  })
})

describe('外部身份与你的往来', () => {
  function identitySetup() {
    const identity = makeIdentity({ name: 'ci-bot' }), agent = makeAgent()
    const fromIt = makeInboxMessage({ from: externalParty(identity), to: consoleParty, text: '构建失败了，要重跑吗？', whenBusy: null })
    const toAgent = makeInboxMessage({ from: externalParty(identity), to: agentParty(agent), text: '发给 Agent 的不在这里' })
    const inbox = fakeInbox({ messages: [fromIt, toAgent], recipients: { '@ci-bot': externalParty(identity) } })
    server.use(
      ...inbox.handlers, ...baseHandlers([]),
      http.get(apiUrl('/external-identities'), () => HttpResponse.json({ identities: [identity] })),
      http.get(apiUrl('/external-identities/:id'), () => HttpResponse.json(identity)),
      http.get(apiUrl('/external-identities/:id/tokens'), () => HttpResponse.json({ tokens: [] })),
      http.get(apiUrl('/external-identities/:id/inbox'), () => HttpResponse.json({ messages: [fromIt, toAgent].map(summaryOf), nextCursor: null })),
    )
    return { identity, fromIt, inbox }
  }

  it('显示它与你之间的消息并可回复最近一条；新消息另起线索发给 @名称', async () => {
    const { identity, fromIt, inbox } = identitySetup()
    const user = userEvent.setup()
    renderRoutes(routes, `/identities/${identity.id}`)
    const section = await screen.findByRole('region', { name: '与你的往来' })
    expect(await within(section).findByText('构建失败了，要重跑吗？')).toBeVisible()
    expect(within(section).queryByText('发给 Agent 的不在这里')).toBeNull()
    await user.type(within(section).getByRole('textbox', { name: '回复 ci-bot…' }), '重跑一次')
    await user.click(within(section).getByRole('button', { name: '回复' }))
    await waitFor(() => expect(inbox.sent).toEqual([{ text: '重跑一次', replyTo: fromIt.id }]))
    await user.click(within(section).getByRole('button', { name: '新消息' }))
    await user.type(within(section).getByRole('textbox', { name: '给 ci-bot 发新消息' }), '另一件事')
    await user.click(within(section).getByRole('button', { name: '发送' }))
    await waitFor(() => expect(inbox.sent.at(-1)).toEqual({ to: '@ci-bot', text: '另一件事' }))
  })
})
