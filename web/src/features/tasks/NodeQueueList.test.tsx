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
const m3 = entry('53000000-0000-7000-8000-000000000003', '立刻停下检查', runI, 'interrupt', manager)
const m1 = entry('53000000-0000-7000-8000-000000000001', '补充一：先看日志', runQ)
const m2 = entry('53000000-0000-7000-8000-000000000002', '补充二：再看配置', runQ)
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
const queueList = () => screen.getByRole('region', { name: '排队消息' })
// One row per message; image thumbnails inside a row are list items too and are skipped.
const rows = () => within(queueList()).getAllByRole('listitem').filter(li => li.hasAttribute('data-kind'))
const rowTexts = () => rows().map(li => within(li).getByRole('button', { expanded: false }).textContent)
const toggle = (name: '展开' | '收起') => within(queueList()).getByRole('button', { name, expanded: name === '收起' })
// The node list starts collapsed to one line; open it to reach the rows.
async function openQueue(user: ReturnType<typeof userEvent.setup>) {
  await waitFor(() => expect(toggle('展开')).toBeVisible())
  await user.click(toggle('展开'))
  await waitFor(() => expect(rows()).toHaveLength(3))
}
type Body = Schemas['TaskNodeQueueActionInput']
const receipt = (operation: Schemas['TaskNodeQueueActionReceipt']['operation'], messageId: string | null, body: Body, item: Schemas['QueueItem'] | null = null): Schemas['TaskNodeQueueActionReceipt'] => ({ actionId: body.actionId, taskId, nodeRunId: nodeId, agentId: body.expectedAgentId, runtimeId: body.expectedRuntimeId, operation, status: 'accepted', messageId, runId: runI, item })

it('默认收成一行：排队总数、第一条的发送方与摘要（打断照常标明）和展开控件；展开后每条消息一行，可再收起', async () => {
  setup()
  const user = userEvent.setup()
  await waitFor(() => expect(toggle('展开')).toBeVisible())
  act(() => stream().open())
  expect(within(queueList()).getByRole('heading', { name: '排队 3' })).toBeInTheDocument()
  const summary = queueList().querySelector<HTMLElement>('[data-queue-summary]')!
  expect(summary).toHaveTextContent('下一条处理Manager立刻停下检查'); expect(within(summary).getByText('Manager')).toBeVisible()
  const list = document.getElementById(toggle('展开').getAttribute('aria-controls')!)!
  expect(list.tagName).toBe('OL'); expect(list).not.toBeVisible()
  expect(within(queueList()).queryByRole('button', { name: '打断' })).toBeNull()

  await user.click(toggle('展开'))
  expect(toggle('收起')).toHaveAttribute('aria-controls', list.id); expect(list).toBeVisible()
  expect(queueList().querySelector('[data-queue-summary]')).toBeNull()
  expect(rows().map(li => li.getAttribute('data-kind'))).toEqual(['interrupt', 'queue', 'queue'])
  expect(rowTexts()).toEqual(['立刻停下检查', '补充一：先看日志', '补充二：再看配置'])
  const [first, second, third] = rows()
  // Another Agent's message: sender chip, cancel, no edit; already an interrupt, so no 打断.
  expect(within(first).getByText('Manager')).toBeVisible(); expect(within(first).getByText('下一条处理')).toBeVisible()
  expect(within(first).queryByRole('button', { name: '打断' })).toBeNull()
  expect(within(first).queryByRole('button', { name: '编辑这条排队消息' })).toBeNull()
  expect(within(first).getByRole('button', { name: '取消这条排队消息' })).toBeEnabled()
  for (const row of [second, third]) { expect(within(row).getByText('你')).toBeVisible(); expect(within(row).getByRole('button', { name: '打断' })).toBeEnabled(); expect(within(row).getByRole('button', { name: '编辑这条排队消息' })).toBeEnabled() }
  // 托盘在节点输入框正上方。
  expect(queueList().compareDocumentPosition(screen.getByLabelText('消息草稿')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

  // 排队变化不改变展开状态；收起后回到一行。
  act(() => stream().emit('task.node.queue.updated', queueOf(m1, m2), '15'))
  await waitFor(() => expect(rows()).toHaveLength(2)); expect(toggle('收起')).toBeVisible()
  await user.click(toggle('收起'))
  expect(list).not.toBeVisible(); expect(queueList().querySelector('[data-queue-summary]')).toHaveTextContent(/^你补充一：先看日志$/)
  // 列表从有到无时整块消失。
  act(() => stream().emit('task.node.queue.updated', queueOf(), '16'))
  await waitFor(() => expect(screen.queryByRole('region', { name: '排队消息' })).toBeNull())
  expect(document.querySelector('[data-node-queue]')).toBeNull()
})

it('打断任何发件人的消息：带新 actionId 与原目标，进行中禁用；列表以 task.node.queue.updated 为准', async () => {
  const bodies: { messageId: string; body: Body }[] = []
  let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
  const other = entry('53000000-0000-7000-8000-000000000004', '别的 Agent 补充', runQ, 'queue', manager)
  server.use(http.post(actionUrl('promote-to-interrupt'), async ({ params, request }) => { const body = await request.json() as Body; bodies.push({ messageId: String(params.messageId), body }); await gate; return HttpResponse.json(receipt('promote_inbox_message', String(params.messageId), body, other)) }))
  const user = userEvent.setup()
  setup({ queue: () => queueOf(m3, m1, other) }); await openQueue(user); act(() => stream().open())
  const target = rows()[2]
  await user.click(within(target).getByRole('button', { name: '打断' }))
  expect(within(target).getByRole('button', { name: '打断中…' })).toBeDisabled()
  expect(bodies).toEqual([{ messageId: other.messageId, body: { actionId: expect.stringMatching(/^[\da-f-]{36}$/), expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId } }])
  await act(async () => release())
  act(() => stream().emit('task.node.queue.updated', queueOf(m3, { ...other, runId: runI, whenBusy: 'interrupt' }, m1), '20'))
  await waitFor(() => expect(rowTexts()).toEqual(['立刻停下检查', '别的 Agent 补充', '补充一：先看日志']))
  expect(rows().map(li => li.getAttribute('data-kind'))).toEqual(['interrupt', 'interrupt', 'queue'])
  expect(within(rows()[1]).queryByRole('button', { name: '打断' })).toBeNull()
  expect(screen.queryByRole('alert')).toBeNull()
})

it('已不在队列时 409：一行轻提示，不报错；结果不明时沿用原 actionId 重试', async () => {
  const bodies: Body[] = []
  server.use(http.post(actionUrl('promote-to-interrupt'), async ({ params, request }) => {
    const body = await request.json() as Body; bodies.push(body)
    if (params.messageId === m1.messageId) return HttpResponse.json(errorBody('task_conflict', '已读', { details: { taskId, expectedNodeRunId: nodeId, currentNodeRunId: nodeId, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, taskVersion: 2, reason: 'message_read', messageId: m1.messageId! } }), { status: 409 })
    return bodies.filter(b => b.actionId === body.actionId).length === 1 ? HttpResponse.json(errorBody('internal_error', '回执暂不可确认'), { status: 500 }) : HttpResponse.json(receipt('promote_inbox_message', String(params.messageId), body, m2))
  }))
  const user = userEvent.setup()
  setup(); await openQueue(user); act(() => stream().open())
  const [, first, second] = rows()
  await user.click(within(first).getByRole('button', { name: '打断' }))
  expect(await screen.findByText('这条消息已被读取，不在排队中了')).toHaveAttribute('role', 'status')
  expect(screen.queryByRole('alert')).toBeNull()
  await user.click(within(second).getByRole('button', { name: '打断' }))
  await user.click(await within(second).findByRole('button', { name: '重试打断' }))
  await waitFor(() => expect(bodies).toHaveLength(3))
  expect(bodies[2]).toEqual(bodies[1])
  await waitFor(() => expect(within(second).getByRole('button', { name: '打断' })).toBeEnabled())
})

it('结果不明时自动展开，期间收起了也能看到重试打断', async () => {
  let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
  server.use(http.post(actionUrl('promote-to-interrupt'), async () => { await gate; return HttpResponse.json(errorBody('internal_error', '回执暂不可确认'), { status: 500 }) }))
  const user = userEvent.setup()
  setup(); await openQueue(user); act(() => stream().open())
  const target = rows()[2]
  await user.click(within(target).getByRole('button', { name: '打断' }))
  await user.click(toggle('收起'))
  expect(within(target).queryByRole('button', { name: '打断中…' })).toBeNull()
  await act(async () => release())
  await waitFor(() => expect(toggle('收起')).toBeVisible())
  expect(within(target).getByRole('button', { name: '重试打断' })).toBeVisible()
})

it('取消别人发来的消息；编辑自己发的消息：撤回后把原文放回节点草稿', async () => {
  const cancels: { messageId: string; body: Body }[] = []
  server.use(http.post(actionUrl('cancel'), async ({ params, request }) => {
    const body = await request.json() as Body; cancels.push({ messageId: String(params.messageId), body })
    const item = [m3, m1, m2].find(i => i.messageId === params.messageId)!
    return HttpResponse.json(receipt('cancel_inbox_message', String(params.messageId), body, item))
  }))
  const user = userEvent.setup()
  setup(); await openQueue(user); act(() => stream().open())
  await user.click(within(rows()[0]).getByRole('button', { name: '取消这条排队消息' }))
  await waitFor(() => expect(cancels.map(c => c.messageId)).toEqual([m3.messageId]))
  expect(cancels[0].body).toEqual({ actionId: expect.stringMatching(/^[\da-f-]{36}$/), expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId })
  await user.type(screen.getByLabelText('消息草稿'), '新写的')
  await user.click(within(rows()[1]).getByRole('button', { name: '编辑这条排队消息' }))
  await waitFor(() => expect(screen.getByLabelText('消息草稿')).toHaveValue('新写的\n补充一：先看日志'))
  expect(cancels.map(c => c.messageId)).toEqual([m3.messageId, m1.messageId])
  expect(screen.queryByRole('alert')).toBeNull()
})

it('节点有进行中的回复、草稿为空时可以停止这一轮；这一轮已结束时一行轻提示', async () => {
  const running: Schemas['TaskMessage'] = { id: '55000000-0000-7000-8000-000000000010', nodeRunId: nodeId, runId: runQ, role: 'assistant', origin: 'task_node', text: '正在调整', status: 'running', createdAt: taskTime }
  const stops: { runId: string; body: Body }[] = []
  server.use(http.post(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/runs/:runId/stop`), async ({ params, request }) => {
    const body = await request.json() as Body; stops.push({ runId: String(params.runId), body })
    if (stops.length > 1) return HttpResponse.json(errorBody('task_conflict', '已结束', { details: { taskId, expectedNodeRunId: nodeId, currentNodeRunId: nodeId, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, taskVersion: 2, reason: 'run_not_active', runId: String(params.runId), runStatus: 'completed' } }), { status: 409 })
    return HttpResponse.json({ ...receipt('stop_run', null, body), runId: String(params.runId) })
  }))
  const user = userEvent.setup()
  setup({ queue: () => queueOf(), messages: [running] })
  await user.click(await screen.findByRole('button', { name: '停止当前回复' }))
  await waitFor(() => expect(stops).toEqual([{ runId: runQ, body: { actionId: expect.stringMatching(/^[\da-f-]{36}$/), expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId } }]))
  expect(screen.getByText('只停止当前回复')).toBeInTheDocument()
  await user.type(screen.getByLabelText('消息草稿'), '先别停')
  expect(screen.queryByRole('button', { name: '停止当前回复' })).toBeNull()
  await user.clear(screen.getByLabelText('消息草稿'))
  await user.click(screen.getByRole('button', { name: '停止当前回复' }))
  expect(await screen.findByText('这次回复已经结束')).toHaveAttribute('role', 'status')
  expect(screen.queryByRole('alert')).toBeNull()
})

it('节点对话隐藏排队中即作废的轮次（用户行与空回复行），被人停止的一轮标明已停止', async () => {
  const at = (s: number) => `2026-10-14T08:00:0${s}Z`
  const message = (id: string, role: 'user' | 'assistant', text: string, runId: string, s: number, extra: Partial<Schemas['TaskMessage']> = {}): Schemas['TaskMessage'] => ({ id, nodeRunId: nodeId, runId, role, origin: 'inbox_reminder', text, status: 'interrupted', createdAt: at(s), ...extra })
  setup({ queue: () => queueOf(), messages: [
    message('55000000-0000-7000-8000-000000000001', 'user', '已并入打断的提醒原文', runQ, 1, { voidReason: 'merged_into_interrupt' }),
    message('55000000-0000-7000-8000-000000000002', 'assistant', '', runQ, 2, { voidReason: 'merged_into_interrupt' }),
    message('55000000-0000-7000-8000-000000000004', 'user', '被取消的提醒原文', runQ, 3, { voidReason: 'user_cancelled' }),
    message('55000000-0000-7000-8000-000000000003', 'user', '其他原因结束的提醒', runI, 4),
    message('55000000-0000-7000-8000-000000000005', 'assistant', '停在一半', runI, 5, { origin: 'task_node', errorCode: 'user_stopped' }),
  ] })
  await screen.findByText('其他原因结束的提醒')
  expect(screen.queryByText('已并入打断的提醒原文')).toBeNull()
  expect(screen.queryByText('被取消的提醒原文')).toBeNull()
  expect(document.querySelector('[data-message-id="task:55000000-0000-7000-8000-000000000002"]')).toBeNull()
  const stopped = document.querySelector<HTMLElement>('[data-message-id="task:55000000-0000-7000-8000-000000000005"]')!
  expect(within(stopped).getByText('已停止')).toBeInTheDocument()
  expect(within(stopped).queryByText(/执行错误/)).toBeNull()
  expect(screen.queryByRole('region', { name: '排队消息' })).toBeNull()
})

it('排队中的节点消息只在托盘里，开始处理后才进入对话；已取消的照常显示最终状态', async () => {
  const record = (id: string, text: string, extra: Partial<Schemas['InboxMessage']> = {}) => { const base = taskInboxRecord(); return { ...base, message: { ...base.message, id, threadId: id, text, ...extra } } }
  const queued = record(m1.messageId!, m1.text), cancelled = record('53000000-0000-7000-8000-000000000009', '已经取消的补充', { cancelledAt: taskTime })
  setup({ queue: () => queueOf(m1), inbox: [queued, cancelled] })
  await waitFor(() => expect(toggle('展开')).toBeVisible())
  const bubble = (id: string) => document.querySelector(`[data-conversation-scroll] [data-message-id="inbox:${id}"]`)
  await waitFor(() => expect(bubble(cancelled.message.id)).not.toBeNull())
  expect(within(bubble(cancelled.message.id) as HTMLElement).getByText('已被人取消')).toBeInTheDocument()
  expect(bubble(queued.message.id)).toBeNull()
  // 开始处理：离开排队列表，进入对话流。
  act(() => stream().emit('task.node.queue.updated', queueOf(), '40'))
  await waitFor(() => expect(bubble(queued.message.id)).not.toBeNull())
  expect(screen.queryByRole('region', { name: '排队消息' })).toBeNull()
})

it('排队中的节点轮次（平台提醒与空回复）不在对话里，开始后才出现', async () => {
  const turn = (id: string, role: 'user' | 'assistant', text: string, status: Schemas['TaskMessage']['status']): Schemas['TaskMessage'] => ({ id, nodeRunId: nodeId, runId: runQ, role, origin: 'inbox_reminder', text, status, createdAt: taskTime })
  const work: Schemas['TaskMessage'] = { id: '55000000-0000-7000-8000-000000000020', nodeRunId: nodeId, runId: runI, role: 'assistant', origin: 'task_node', text: '正在调整账本', status: 'running', createdAt: taskTime }
  const prompt = turn('55000000-0000-7000-8000-000000000021', 'user', '排队中的平台提醒', 'queued'), reply = turn('55000000-0000-7000-8000-000000000022', 'assistant', '', 'queued')
  setup({ queue: () => queueOf(m1), messages: [work, prompt, reply] })
  await screen.findByText('正在调整账本')
  const row = (id: string) => document.querySelector(`[data-conversation-scroll] [data-message-id="task:${id}"]`)
  expect(screen.queryByText('排队中的平台提醒')).toBeNull()
  expect(row(reply.id)).toBeNull()
  // 轮次开始：按 ID 更新为进行中，进入对话流。
  act(() => stream().emit('message.created', { ...prompt, status: 'running' }, '41'))
  expect(await screen.findByText('排队中的平台提醒')).toBeInTheDocument()
})

it('收到 snapshot（首次或重连）后重新读取一次排队列表；读取开始后收到的事件不被旧读取覆盖', async () => {
  let reads = 0
  setup({ queue: () => { reads++; return initial } })
  await waitFor(() => expect(toggle('展开')).toBeVisible())
  const before = reads
  act(() => stream().emit('snapshot', { task: detail }, '30'))
  await waitFor(() => expect(reads).toBe(before + 1))
  const client = createTestQueryClient(), store = taskNodeQueueProjection(client), started = store.begin(), newer = queueOf(m3)
  expect(store.event(client, taskId, newer)).toBe(true)
  expect(store.read(started, initial)).toBe(newer)
  expect(store.read(store.begin(), initial)).toBe(initial)
  expect(store.event(client, 'other-task', newer)).toBe(false)
})
