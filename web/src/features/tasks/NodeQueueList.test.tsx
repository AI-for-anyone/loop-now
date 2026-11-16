import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { createTestQueryClient, renderRoutes } from '@/test/render'
import { taskNodeQueueProjection } from '@/api/task-node-queue'
import { makeTaskDetail, taskId, nodeId, taskTime } from '@/test/tasks'
import { agentParty, consoleParty } from '@/test/inbox'
import { taskInboxRecord } from '@/test/task-inbox'
import { defaultProject, projectsUrl, apiUrl, errorBody, makeQueueItem } from '@/test/api'
import { server } from '@/test/server'
import { FakeEventSource } from '@/test/fake-event-source'
import { baseHandlers } from '@/test/shell'
import { routes } from '@/app/routes'
import { tasksPath } from '@/api/tasks'

const detail = makeTaskDetail(defaultProject.id), node = detail.nodes[1]
const runI = '54000000-0000-7000-8000-000000000001', runQ = '54000000-0000-7000-8000-000000000002'
const manager = agentParty({ id: '56000000-0000-7000-8000-000000000001', name: 'Manager', address: '测试项目/Manager' })
const entry = (messageId: string, text: string, runId: string, whenBusy: Schemas['WhenBusy'] = 'queue', from: Schemas['InboxParticipant'] = consoleParty) => makeQueueItem({ kind: 'inbox', runId, messageId, from, text, whenBusy, queuedAt: taskTime })
const m3 = entry('53000000-0000-7000-8000-000000000003', 'Stop now and check', runI, 'interrupt', manager)
const m1 = entry('53000000-0000-7000-8000-000000000001', 'Note one: check the logs first', runQ)
const m2 = entry('53000000-0000-7000-8000-000000000002', 'Note two: then check the config', runQ)
const queueOf = (...items: Schemas['QueueItem'][]): Schemas['TaskNodeQueue'] => ({ taskId, nodeRunId: nodeId, runtimeId: node.runtimeId, items })
const initial = queueOf(m3, m1, m2)
const actionUrl = (operation: 'promote-to-interrupt' | 'cancel') => apiUrl(`/tasks/${taskId}/nodes/${nodeId}/inbox-messages/:messageId/${operation}`)

function setup({ queue = () => initial, messages = [] as Schemas['TaskMessage'][], inbox = [] as Schemas['TaskNodeInboxRecord'][] } = {}) {
  server.use(...baseHandlers([]), http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: params.node, agentId: node.agentId, runtimeId: node.runtimeId, canSend: true, reason: null, handlingHint: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), ({ params }) => HttpResponse.json({ messages: params.node === nodeId ? messages : [], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/inbox-messages`), ({ params }) => HttpResponse.json({ items: params.node === nodeId ? inbox : [], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/queue`), () => HttpResponse.json(queue())))
  renderRoutes(routes, `${tasksPath(defaultProject.name)}/${taskId}`)
}
const stream = () => FakeEventSource.instances.find(source => source.url.endsWith(`/tasks/${taskId}/events`))!
const queueList = () => screen.getByRole('region', { name: 'Queued messages' })
// One row per message; image thumbnails inside a row are list items too and are skipped.
const rows = () => within(queueList()).getAllByRole('listitem').filter(li => li.hasAttribute('data-kind'))
const rowTexts = () => rows().map(li => within(li).getByRole('button', { expanded: false }).textContent)
const toggle = (name: 'Expand' | 'Collapse') => within(queueList()).getByRole('button', { name, expanded: name === 'Collapse' })
// The node list starts collapsed to one line; open it to reach the rows.
async function openQueue(user: ReturnType<typeof userEvent.setup>) {
  await waitFor(() => expect(toggle('Expand')).toBeVisible())
  await user.click(toggle('Expand'))
  await waitFor(() => expect(rows()).toHaveLength(3))
}
type Body = Schemas['TaskNodeQueueActionInput']
const receipt = (operation: Schemas['TaskNodeQueueActionReceipt']['operation'], messageId: string | null, body: Body, item: Schemas['QueueItem'] | null = null): Schemas['TaskNodeQueueActionReceipt'] => ({ actionId: body.actionId, taskId, nodeRunId: nodeId, agentId: body.expectedAgentId, runtimeId: body.expectedRuntimeId, operation, status: 'accepted', messageId, runId: runI, item })

it('collapsed to one line by default: the queue count, the first message’s sender and summary (an interrupt marked as usual) and an expand control; expanded, one row per message, and it collapses again', async () => {
  setup()
  const user = userEvent.setup()
  await waitFor(() => expect(toggle('Expand')).toBeVisible())
  act(() => stream().open())
  expect(within(queueList()).getByRole('heading', { name: 'Queued · 3' })).toBeInTheDocument()
  const summary = queueList().querySelector<HTMLElement>('[data-queue-summary]')!
  expect(summary).toHaveTextContent('Up nextManagerStop now and check'); expect(within(summary).getByText('Manager')).toBeVisible()
  const list = document.getElementById(toggle('Expand').getAttribute('aria-controls')!)!
  expect(list.tagName).toBe('OL'); expect(list).not.toBeVisible()
  expect(within(queueList()).queryByRole('button', { name: 'Interrupt' })).toBeNull()

  await user.click(toggle('Expand'))
  expect(toggle('Collapse')).toHaveAttribute('aria-controls', list.id); expect(list).toBeVisible()
  expect(queueList().querySelector('[data-queue-summary]')).toBeNull()
  expect(rows().map(li => li.getAttribute('data-kind'))).toEqual(['interrupt', 'queue', 'queue'])
  expect(rowTexts()).toEqual(['Stop now and check', 'Note one: check the logs first', 'Note two: then check the config'])
  const [first, second, third] = rows()
  // Another Agent's message: sender chip, cancel, no edit; already an interrupt, so no “Interrupt”.
  expect(within(first).getByText('Manager')).toBeVisible(); expect(within(first).getByText('Up next')).toBeVisible()
  expect(within(first).queryByRole('button', { name: 'Interrupt' })).toBeNull()
  expect(within(first).queryByRole('button', { name: 'Edit this queued message' })).toBeNull()
  expect(within(first).getByRole('button', { name: 'Cancel this queued message' })).toBeEnabled()
  for (const row of [second, third]) { expect(within(row).getByText('You')).toBeVisible(); expect(within(row).getByRole('button', { name: 'Interrupt' })).toBeEnabled(); expect(within(row).getByRole('button', { name: 'Edit this queued message' })).toBeEnabled() }
  // The tray sits right above the node input.
  expect(queueList().compareDocumentPosition(screen.getByLabelText('Message draft')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

  // Queue changes do not change the expanded state; once collapsed, it goes back to one line.
  act(() => stream().emit('task.node.queue.updated', queueOf(m1, m2), '15'))
  await waitFor(() => expect(rows()).toHaveLength(2)); expect(toggle('Collapse')).toBeVisible()
  await user.click(toggle('Collapse'))
  expect(list).not.toBeVisible(); expect(queueList().querySelector('[data-queue-summary]')).toHaveTextContent(/^YouNote one: check the logs first$/)
  // When the list goes from non-empty to empty, the whole block disappears.
  act(() => stream().emit('task.node.queue.updated', queueOf(), '16'))
  await waitFor(() => expect(screen.queryByRole('region', { name: 'Queued messages' })).toBeNull())
  expect(document.querySelector('[data-node-queue]')).toBeNull()
})

it('interrupts a message from any sender with a new actionId and the original target, disabled while pending; the list follows task.node.queue.updated', async () => {
  const bodies: { messageId: string; body: Body }[] = []
  let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
  const other = entry('53000000-0000-7000-8000-000000000004', 'Note from another Agent', runQ, 'queue', manager)
  server.use(http.post(actionUrl('promote-to-interrupt'), async ({ params, request }) => { const body = await request.json() as Body; bodies.push({ messageId: String(params.messageId), body }); await gate; return HttpResponse.json(receipt('promote_inbox_message', String(params.messageId), body, other)) }))
  const user = userEvent.setup()
  setup({ queue: () => queueOf(m3, m1, other) }); await openQueue(user); act(() => stream().open())
  const target = rows()[2]
  await user.click(within(target).getByRole('button', { name: 'Interrupt' }))
  expect(within(target).getByRole('button', { name: 'Interrupting…' })).toBeDisabled()
  expect(bodies).toEqual([{ messageId: other.messageId, body: { actionId: expect.stringMatching(/^[\da-f-]{36}$/), expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId } }])
  await act(async () => release())
  act(() => stream().emit('task.node.queue.updated', queueOf(m3, { ...other, runId: runI, whenBusy: 'interrupt' }, m1), '20'))
  await waitFor(() => expect(rowTexts()).toEqual(['Stop now and check', 'Note from another Agent', 'Note one: check the logs first']))
  expect(rows().map(li => li.getAttribute('data-kind'))).toEqual(['interrupt', 'interrupt', 'queue'])
  expect(within(rows()[1]).queryByRole('button', { name: 'Interrupt' })).toBeNull()
  expect(screen.queryByRole('alert')).toBeNull()
})

it('409 when no longer queued: a light one-line notice, not an error; when the result is unknown, the retry reuses the original actionId', async () => {
  const bodies: Body[] = []
  server.use(http.post(actionUrl('promote-to-interrupt'), async ({ params, request }) => {
    const body = await request.json() as Body; bodies.push(body)
    if (params.messageId === m1.messageId) return HttpResponse.json(errorBody('task_conflict', 'The message has already been read', { details: { taskId, expectedNodeRunId: nodeId, currentNodeRunId: nodeId, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, taskVersion: 2, reason: 'message_read', messageId: m1.messageId! } }), { status: 409 })
    return bodies.filter(b => b.actionId === body.actionId).length === 1 ? HttpResponse.json(errorBody('internal_error', 'The receipt cannot be confirmed yet'), { status: 500 }) : HttpResponse.json(receipt('promote_inbox_message', String(params.messageId), body, m2))
  }))
  const user = userEvent.setup()
  setup(); await openQueue(user); act(() => stream().open())
  const [, first, second] = rows()
  await user.click(within(first).getByRole('button', { name: 'Interrupt' }))
  expect(await screen.findByText('This message was already read and is no longer queued')).toHaveAttribute('role', 'status')
  expect(screen.queryByRole('alert')).toBeNull()
  await user.click(within(second).getByRole('button', { name: 'Interrupt' }))
  await user.click(await within(second).findByRole('button', { name: 'Retry interrupt' }))
  await waitFor(() => expect(bodies).toHaveLength(3))
  expect(bodies[2]).toEqual(bodies[1])
  await waitFor(() => expect(within(second).getByRole('button', { name: 'Interrupt' })).toBeEnabled())
})

it('expands by itself when the result is unknown, so the interrupt retry is visible even if the list was collapsed meanwhile', async () => {
  let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
  server.use(http.post(actionUrl('promote-to-interrupt'), async () => { await gate; return HttpResponse.json(errorBody('internal_error', 'The receipt cannot be confirmed yet'), { status: 500 }) }))
  const user = userEvent.setup()
  setup(); await openQueue(user); act(() => stream().open())
  const target = rows()[2]
  await user.click(within(target).getByRole('button', { name: 'Interrupt' }))
  await user.click(toggle('Collapse'))
  expect(within(target).queryByRole('button', { name: 'Interrupting…' })).toBeNull()
  await act(async () => release())
  await waitFor(() => expect(toggle('Collapse')).toBeVisible())
  expect(within(target).getByRole('button', { name: 'Retry interrupt' })).toBeVisible()
})

it('cancels a message from someone else; editing your own message withdraws it and puts its text back in the node draft', async () => {
  const cancels: { messageId: string; body: Body }[] = []
  server.use(http.post(actionUrl('cancel'), async ({ params, request }) => {
    const body = await request.json() as Body; cancels.push({ messageId: String(params.messageId), body })
    const item = [m3, m1, m2].find(i => i.messageId === params.messageId)!
    return HttpResponse.json(receipt('cancel_inbox_message', String(params.messageId), body, item))
  }))
  const user = userEvent.setup()
  setup(); await openQueue(user); act(() => stream().open())
  await user.click(within(rows()[0]).getByRole('button', { name: 'Cancel this queued message' }))
  await waitFor(() => expect(cancels.map(c => c.messageId)).toEqual([m3.messageId]))
  expect(cancels[0].body).toEqual({ actionId: expect.stringMatching(/^[\da-f-]{36}$/), expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId })
  await user.type(screen.getByLabelText('Message draft'), 'newly written')
  await user.click(within(rows()[1]).getByRole('button', { name: 'Edit this queued message' }))
  await waitFor(() => expect(screen.getByLabelText('Message draft')).toHaveValue('newly written\nNote one: check the logs first'))
  expect(cancels.map(c => c.messageId)).toEqual([m3.messageId, m1.messageId])
  expect(screen.queryByRole('alert')).toBeNull()
})

it('with a reply in progress on the node and an empty draft, the turn can be stopped; if the turn has already ended, a light one-line notice', async () => {
  const running: Schemas['TaskMessage'] = { id: '55000000-0000-7000-8000-000000000010', nodeRunId: nodeId, runId: runQ, role: 'assistant', origin: 'task_node', text: 'Adjusting', status: 'running', createdAt: taskTime }
  const stops: { runId: string; body: Body }[] = []
  server.use(http.post(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/runs/:runId/stop`), async ({ params, request }) => {
    const body = await request.json() as Body; stops.push({ runId: String(params.runId), body })
    if (stops.length > 1) return HttpResponse.json(errorBody('task_conflict', 'The run has already ended', { details: { taskId, expectedNodeRunId: nodeId, currentNodeRunId: nodeId, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, taskVersion: 2, reason: 'run_not_active', runId: String(params.runId), runStatus: 'completed' } }), { status: 409 })
    return HttpResponse.json({ ...receipt('stop_run', null, body), runId: String(params.runId) })
  }))
  const user = userEvent.setup()
  setup({ queue: () => queueOf(), messages: [running] })
  await user.click(await screen.findByRole('button', { name: 'Stop the current reply' }))
  await waitFor(() => expect(stops).toEqual([{ runId: runQ, body: { actionId: expect.stringMatching(/^[\da-f-]{36}$/), expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId } }]))
  expect(screen.getByText('Stop the current reply only')).toBeInTheDocument()
  await user.type(screen.getByLabelText('Message draft'), 'do not stop yet')
  expect(screen.queryByRole('button', { name: 'Stop the current reply' })).toBeNull()
  await user.clear(screen.getByLabelText('Message draft'))
  await user.click(screen.getByRole('button', { name: 'Stop the current reply' }))
  expect(await screen.findByText('This reply has already ended')).toHaveAttribute('role', 'status')
  expect(screen.queryByRole('alert')).toBeNull()
})

it('the node conversation hides turns voided while queued (user rows and empty reply rows), and a turn stopped by a person is marked Stopped', async () => {
  const at = (s: number) => `2026-10-14T08:00:0${s}Z`
  const message = (id: string, role: 'user' | 'assistant', text: string, runId: string, s: number, extra: Partial<Schemas['TaskMessage']> = {}): Schemas['TaskMessage'] => ({ id, nodeRunId: nodeId, runId, role, origin: 'inbox_reminder', text, status: 'interrupted', createdAt: at(s), ...extra })
  setup({ queue: () => queueOf(), messages: [
    message('55000000-0000-7000-8000-000000000001', 'user', 'reminder text merged into an interrupt', runQ, 1, { voidReason: 'merged_into_interrupt' }),
    message('55000000-0000-7000-8000-000000000002', 'assistant', '', runQ, 2, { voidReason: 'merged_into_interrupt' }),
    message('55000000-0000-7000-8000-000000000004', 'user', 'cancelled reminder text', runQ, 3, { voidReason: 'user_cancelled' }),
    message('55000000-0000-7000-8000-000000000003', 'user', 'reminder that ended for another reason', runI, 4),
    message('55000000-0000-7000-8000-000000000005', 'assistant', 'stopped halfway', runI, 5, { origin: 'task_node', errorCode: 'user_stopped' }),
  ] })
  await screen.findByText('reminder that ended for another reason')
  expect(screen.queryByText('reminder text merged into an interrupt')).toBeNull()
  expect(screen.queryByText('cancelled reminder text')).toBeNull()
  expect(document.querySelector('[data-message-id="task:55000000-0000-7000-8000-000000000002"]')).toBeNull()
  const stopped = document.querySelector<HTMLElement>('[data-message-id="task:55000000-0000-7000-8000-000000000005"]')!
  expect(within(stopped).getByText('Stopped')).toBeInTheDocument()
  expect(within(stopped).queryByText(/Run error/)).toBeNull()
  expect(screen.queryByRole('region', { name: 'Queued messages' })).toBeNull()
})

it('queued node messages are only in the tray and enter the conversation once processing starts; cancelled ones show their final state as usual', async () => {
  const record = (id: string, text: string, extra: Partial<Schemas['InboxMessage']> = {}) => { const base = taskInboxRecord(); return { ...base, message: { ...base.message, id, threadId: id, text, ...extra } } }
  const queued = record(m1.messageId!, m1.text), cancelled = record('53000000-0000-7000-8000-000000000009', 'note that was already cancelled', { cancelledAt: taskTime })
  setup({ queue: () => queueOf(m1), inbox: [queued, cancelled] })
  await waitFor(() => expect(toggle('Expand')).toBeVisible())
  const bubble = (id: string) => document.querySelector(`[data-conversation-scroll] [data-message-id="inbox:${id}"]`)
  await waitFor(() => expect(bubble(cancelled.message.id)).not.toBeNull())
  expect(within(bubble(cancelled.message.id) as HTMLElement).getByText('Cancelled by someone')).toBeInTheDocument()
  expect(bubble(queued.message.id)).toBeNull()
  // Processing starts: it leaves the queue list and enters the conversation stream.
  act(() => stream().emit('task.node.queue.updated', queueOf(), '40'))
  await waitFor(() => expect(bubble(queued.message.id)).not.toBeNull())
  expect(screen.queryByRole('region', { name: 'Queued messages' })).toBeNull()
})

it('queued node turns (a platform reminder and an empty reply) are not in the conversation and appear only once they start', async () => {
  const turn = (id: string, role: 'user' | 'assistant', text: string, status: Schemas['TaskMessage']['status']): Schemas['TaskMessage'] => ({ id, nodeRunId: nodeId, runId: runQ, role, origin: 'inbox_reminder', text, status, createdAt: taskTime })
  const work: Schemas['TaskMessage'] = { id: '55000000-0000-7000-8000-000000000020', nodeRunId: nodeId, runId: runI, role: 'assistant', origin: 'task_node', text: 'Adjusting the ledger', status: 'running', createdAt: taskTime }
  // A queued platform reminder turn; its body is the server's placeholder
  // text (replaced with the reminder text only when it starts running).
  const prompt = turn('55000000-0000-7000-8000-000000000021', 'user', 'Platform reminder', 'queued'), reply = turn('55000000-0000-7000-8000-000000000022', 'assistant', '', 'queued')
  setup({ queue: () => queueOf(m1), messages: [work, prompt, reply] })
  await screen.findByText('Adjusting the ledger')
  const row = (id: string) => document.querySelector(`[data-conversation-scroll] [data-message-id="task:${id}"]`)
  expect(screen.queryByText('Platform reminder')).toBeNull()
  expect(row(reply.id)).toBeNull()
  // The turn starts: it is updated to running by ID and enters the conversation stream.
  act(() => stream().emit('message.created', { ...prompt, status: 'running' }, '41'))
  expect(await screen.findByText('Platform reminder')).toBeInTheDocument()
})

it('rereads the queue list once after a snapshot (first or after reconnecting); events received after the read started are not overwritten by the old read', async () => {
  let reads = 0
  setup({ queue: () => { reads++; return initial } })
  await waitFor(() => expect(toggle('Expand')).toBeVisible())
  const before = reads
  act(() => stream().emit('snapshot', { task: detail }, '30'))
  await waitFor(() => expect(reads).toBe(before + 1))
  const client = createTestQueryClient(), store = taskNodeQueueProjection(client), started = store.begin(), newer = queueOf(m3)
  expect(store.event(client, taskId, newer)).toBe(true)
  expect(store.read(started, initial)).toBe(newer)
  expect(store.read(store.begin(), initial)).toBe(initial)
  expect(store.event(client, 'other-task', newer)).toBe(false)
})
