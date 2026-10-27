import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { tasksPath } from '@/api/tasks'
import { apiUrl, defaultProject, projectsUrl } from '@/test/api'
import { FakeEventSource } from '@/test/fake-event-source'
import { consoleParty, fakeInbox, makeInboxMessage } from '@/test/inbox'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { makeTaskDetail, nodeId, taskId } from '@/test/tasks'

// 任务实例发给你的问题排在节点对话里，在那里点选项或用节点输入框回复；能否回答以任务给出的问题状态为准。
function setup(canAnswer = true) {
  const detail = makeTaskDetail(defaultProject.id), node = detail.nodes[1]
  const from: Schemas['InboxParticipant'] = { kind: 'agent', id: node.agentId, name: node.agentName, address: `测试项目/${node.agentName}#${taskId}`, deleted: false, pluginId: null, channelId: null }
  const question = makeInboxMessage({ from, to: consoleParty, text: '验收要 12 个用例全过吗？', whenBusy: null, task: { taskId, nodeRunId: nodeId, runtimeId: node.runtimeId } })
  question.question = { options: [{ id: '1', label: '全过' }, { id: '2', label: '允许 1 个已知失败' }], expiresAt: '2099-01-01T00:00:00Z', state: 'open', answer: null }
  node.question = { id: question.id, threadId: question.threadId, messageId: question.id, text: question.text, status: 'open', canAnswer }
  const inbox = fakeInbox({ messages: [question] })
  let nodePosts = 0
  server.use(
    ...inbox.handlers, ...baseHandlers([]),
    http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: params.node, agentId: node.agentId, runtimeId: node.runtimeId, canSend: true, reason: null, handlingHint: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), () => HttpResponse.json({ messages: [], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/inbox-messages`), ({ params }) => HttpResponse.json({ items: params.node === nodeId ? [{ taskId, nodeRunId: nodeId, agentId: node.agentId, runtimeId: node.runtimeId, message: inbox.messages.find((m) => m.id === question.id)!, runLinks: [], notExecuted: null }] : [], nextCursor: null })),
    http.post(apiUrl(`/tasks/${taskId}/nodes/:node/inbox-messages`), () => { nodePosts++; return HttpResponse.json({}, { status: 500 }) }),
  )
  const rendered = renderRoutes(routes, `${tasksPath(defaultProject.name)}/${taskId}?step=check&record=${nodeId}`)
  return { detail, node, question, inbox, nodePosts: () => nodePosts, ...rendered }
}
const item = (question: Schemas['InboxMessage']) => {
  const el = document.querySelector<HTMLElement>(`[data-console-message-id="${question.id}"]`)
  if (!el) throw new Error('message not rendered yet')
  return el
}

it('节点的问题显示为“发给你”，点选项提交到这条原题；状态栏的问题块只提供跳到对话，不再链接收件箱', async () => {
  const { question, inbox } = setup()
  const user = userEvent.setup()
  const message = await waitFor(() => item(question))
  expect(within(message).getByText('发给你')).toBeVisible()
  expect(within(message).getByText('验收要 12 个用例全过吗？')).toBeVisible()
  expect(document.querySelector('a[href^="/inbox"]')).toBeNull()
  expect(screen.getByRole('button', { name: '在对话中回答' })).toBeInTheDocument()
  await user.click(within(message).getByRole('button', { name: '允许 1 个已知失败' }))
  await waitFor(() => expect(inbox.sent).toEqual([{ optionId: '2', whenBusy: 'queue', replyTo: question.id }]))
})

it('任务说当前身份不能回答时只显示状态，没有可点的选项与回复入口', async () => {
  const { question, inbox } = setup(false)
  const message = await waitFor(() => item(question))
  expect(within(message).getByText('这个问题现在不能回答，请检查任务的最新状态。')).toBeVisible()
  expect(within(message).getByRole('button', { name: '全过' })).toBeDisabled()
  expect(within(message).queryByRole('button', { name: '回复这个问题' })).toBeNull()
  expect(inbox.sent).toEqual([])
})

it('查看期间任务把问题关闭，选项随之不可点', async () => {
  const { question, detail } = setup()
  const message = await waitFor(() => item(question))
  expect(within(message).getByRole('button', { name: '全过' })).toBeEnabled()
  detail.nodes[1].question = { ...detail.nodes[1].question!, status: 'closed', canAnswer: false }
  const es = FakeEventSource.instances.find((s) => s.url.endsWith(`/tasks/${taskId}/events`))!
  act(() => es.emit('node.updated', { node: detail.nodes[1] }, '5'))
  await waitFor(() => expect(within(item(question)).getByRole('button', { name: '全过' })).toBeDisabled())
})

it('“回复这个问题”让节点输入框进入回复模式：文字作为原题的回答发出，不发节点消息', async () => {
  const { question, inbox, nodePosts } = setup()
  const user = userEvent.setup()
  await user.click(within(await waitFor(() => item(question))).getByRole('button', { name: '回复这个问题' }))
  expect(screen.getByText('回复 任务检查员 的问题')).toBeVisible()
  await user.type(screen.getByRole('textbox', { name: '消息草稿' }), '允许一个，写进结论{Enter}')
  await waitFor(() => expect(inbox.sent).toEqual([{ text: '允许一个，写进结论', whenBusy: 'queue', replyTo: question.id }]))
  await waitFor(() => expect(screen.queryByText('回复 任务检查员 的问题')).toBeNull())
  expect(nodePosts()).toBe(0)
})
