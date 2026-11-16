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

// Questions a task instance sends you are listed in the node chat, where you click an option or reply in
// the node input; whether a question can be answered follows the question state that the task provides.
function setup(canAnswer = true, notExecuted: Schemas['TaskNodeInboxNotExecuted'] | null = null) {
  const detail = makeTaskDetail(defaultProject.id), node = detail.nodes[1]
  const from: Schemas['InboxParticipant'] = { kind: 'agent', id: node.agentId, name: node.agentName, address: `测试项目/${node.agentName}#${taskId}`, deleted: false, pluginId: null, channelId: null }
  const question = makeInboxMessage({ from, to: consoleParty, text: 'Do all 12 cases have to pass for acceptance?', whenBusy: null, task: { taskId, nodeRunId: nodeId, runtimeId: node.runtimeId } })
  question.question = { options: [{ id: '1', label: 'All pass' }, { id: '2', label: 'Allow 1 known failure' }], expiresAt: '2099-01-01T00:00:00Z', state: 'open', answer: null }
  node.question = { id: question.id, threadId: question.threadId, messageId: question.id, text: question.text, status: 'open', canAnswer }
  const inbox = fakeInbox({ messages: [question] })
  let nodePosts = 0
  server.use(
    ...inbox.handlers, ...baseHandlers([]),
    http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: params.node, agentId: node.agentId, runtimeId: node.runtimeId, canSend: true, reason: null, handlingHint: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), () => HttpResponse.json({ messages: [], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/inbox-messages`), ({ params }) => HttpResponse.json({ items: params.node === nodeId ? [{ taskId, nodeRunId: nodeId, agentId: node.agentId, runtimeId: node.runtimeId, message: inbox.messages.find((m) => m.id === question.id)!, runLinks: [], notExecuted }] : [], nextCursor: null })),
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

it('a node question shows as “For you” and clicking an option answers that original question; the question block in the status bar only jumps to the conversation and no longer links to the inbox', async () => {
  const { question, inbox } = setup()
  const user = userEvent.setup()
  const message = await waitFor(() => item(question))
  expect(within(message).getByText('For you')).toBeVisible()
  expect(within(message).getByText('Do all 12 cases have to pass for acceptance?')).toBeVisible()
  expect(document.querySelector('a[href^="/inbox"]')).toBeNull()
  expect(screen.getByRole('button', { name: 'Answer in the conversation' })).toBeInTheDocument()
  await user.click(within(message).getByRole('button', { name: 'Allow 1 known failure' }))
  await waitFor(() => expect(inbox.sent).toEqual([{ optionId: '2', whenBusy: 'queue', replyTo: question.id }]))
})

it('when the task says the current identity cannot answer, only the state shows, with no clickable options and no way to reply', async () => {
  const { question, inbox } = setup(false)
  const message = await waitFor(() => item(question))
  expect(within(message).getByText('This question can\'t be answered right now. Check the task\'s latest state.')).toBeVisible()
  expect(within(message).getByRole('button', { name: 'All pass' })).toBeDisabled()
  expect(within(message).queryByRole('button', { name: 'Reply to this question' })).toBeNull()
  expect(inbox.sent).toEqual([])
})

it('when the task closes the question while you are viewing it, the options become unclickable', async () => {
  const { question, detail } = setup()
  const message = await waitFor(() => item(question))
  expect(within(message).getByRole('button', { name: 'All pass' })).toBeEnabled()
  detail.nodes[1].question = { ...detail.nodes[1].question!, status: 'closed', canAnswer: false }
  const es = FakeEventSource.instances.find((s) => s.url.endsWith(`/tasks/${taskId}/events`))!
  act(() => es.emit('node.updated', { node: detail.nodes[1] }, '5'))
  await waitFor(() => expect(within(item(question)).getByRole('button', { name: 'All pass' })).toBeDisabled())
})

it('“Reply to this question” puts the node input in reply mode: the text goes out as the answer to the original question, not as a node message', async () => {
  const { question, inbox, nodePosts } = setup()
  const user = userEvent.setup()
  await user.click(within(await waitFor(() => item(question))).getByRole('button', { name: 'Reply to this question' }))
  expect(screen.getByText("Replying to 任务检查员's question")).toBeVisible()
  await user.type(screen.getByRole('textbox', { name: 'Message draft' }), 'Allow one, note it in the conclusion{Enter}')
  await waitFor(() => expect(inbox.sent).toEqual([{ text: 'Allow one, note it in the conclusion', whenBusy: 'queue', replyTo: question.id }]))
  await waitFor(() => expect(screen.queryByText("Replying to 任务检查员's question")).toBeNull())
  expect(nodePosts()).toBe(0)
})

it.each([
  ['task_ended', "The original node message didn't run · The task has ended"],
  ['cancelled', "The original node message didn't run · Cancelled"],
  ['some_new_reason', "The original node message didn't run · some_new_reason"],
])('a message that did not run says why in English (%s); an unknown reason code is shown as is', async (reason, text) => {
  const { question } = setup(true, { reason, at: '2026-10-18T00:02:00Z' })
  const message = await waitFor(() => item(question))
  expect(within(message).getByText(text)).toHaveAttribute('role', 'status')
})
