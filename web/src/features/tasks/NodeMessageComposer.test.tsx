import { useState } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject, errorBody } from '@/test/api'
import { makeTaskDetail, nodeId, taskId } from '@/test/tasks'
import { t } from '@/i18n'
import { taskInboxReceipt } from '@/test/task-inbox'
import { server } from '@/test/server'
import { renderWithQuery } from '@/test/render'
import type { NodeDraft } from './NodeRecordPanel'
import { NodeMessageComposer } from './NodeMessageComposer'

const copy = t.taskInbox

const node = makeTaskDetail(defaultProject.id).nodes[1]
const draft = { text: '', agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName }
const capability: Schemas['TaskNodeMessageCapability'] = { taskId, nodeRunId: node.id, agentId: node.agentId, runtimeId: node.runtimeId, canSend: true, reason: null, handlingHint: null }
const endpoint = apiUrl(`/tasks/${taskId}/nodes/${nodeId}/inbox-messages`)
const receipt = taskInboxReceipt

function Harness({ cap = capability, current = true, disabled = false }: { cap?: Schemas['TaskNodeMessageCapability']; current?: boolean; disabled?: boolean }) {
  const [text, setText] = useState('')
  const [action, setAction] = useState<NodeDraft['action']>()
  return <NodeMessageComposer taskId={taskId} draft={{ ...draft, action }} onAction={setAction} current={current} disabled={disabled} capability={cap} text={text} onChange={setText} onAccepted={sent => setText(old => old === sent ? '' : old)} />
}
it('默认queue严格绑定原四目标，只显示已接收而非Agent已回复', async () => {
  const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />)
  await userEvent.type(screen.getByRole('textbox'), '原节点普通消息'); await userEvent.click(screen.getByRole('button', { name: '发送' }))
  expect(await screen.findByText(copy.accepted)).toBeVisible()
  expect(bodies).toHaveLength(1); expect(bodies[0]).toMatchObject({ expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, text: '原节点普通消息', whenBusy: 'queue' })
  expect(bodies[0].actionId).toMatch(/^[\da-f-]{36}$/)
  expect(screen.getByRole('textbox')).toHaveValue('')
})
it('不再提供忙时处理选择，一律排队；输入法回车/ShiftEnter和空消息不会提交', async () => {
  const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />)
  await userEvent.click(screen.getByRole('button', { name: '发送' })); expect(screen.getByRole('alert')).toBeVisible(); expect(bodies).toHaveLength(0)
  const box = screen.getByRole('textbox'); await userEvent.type(box, '输入法内容')
  fireEvent.keyDown(box, { key: 'Enter', isComposing: true }); fireEvent.keyDown(box, { key: 'Enter', keyCode: 229 }); fireEvent.keyDown(box, { key: 'Enter', shiftKey: true })
  expect(bodies).toHaveLength(0)
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument(); fireEvent.keyDown(box, { key: 'Enter' })
  await waitFor(() => expect(bodies).toHaveLength(1)); expect(bodies[0].whenBusy).toBe('queue')
})
it('发送中继续编辑，原回执不会清掉新草稿', async () => {
  let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; await gate; return HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />); const box = screen.getByRole('textbox')
  await userEvent.type(box, '已提交内容'); await userEvent.click(screen.getByRole('button', { name: '发送' }))
  expect(screen.getByRole('button', { name: '正在发送…' })).toBeDisabled()
  await userEvent.clear(box); await userEvent.type(box, '新的编辑草稿'); release()
  await screen.findByText(copy.accepted); expect(box).toHaveValue('新的编辑草稿')
})
it('409只发送一次、保原草稿且不降级旧接口或重新发', async () => {
  let posts = 0
  server.use(http.post(endpoint, () => { posts++; return HttpResponse.json(errorBody('task_conflict', '原节点已不是当前执行'), { status: 409 }) }))
  renderWithQuery(<Harness />); const box = screen.getByRole('textbox')
  await userEvent.type(box, '保留在旧节点'); await userEvent.click(screen.getByRole('button', { name: '发送' }))
  expect(await screen.findByText(/原节点已不是当前执行/)).toBeVisible(); expect(box).toHaveValue('保留在旧节点')
  await new Promise(resolve => setTimeout(resolve, 100)); expect(posts).toBe(1)
})
it('结果未知不自动新动作，明确手动重试沿用原action/内容/目标，期间新草稿不清', async () => {
  const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return bodies.length === 1 ? HttpResponse.json(errorBody('internal_error', '回执暂不可确认'), { status: 500 }) : HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />); const box = screen.getByRole('textbox')
  await userEvent.type(box, '原动作内容'); await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await screen.findByRole('button', { name: '重试确认原消息' }); expect(bodies).toHaveLength(1)
  await userEvent.clear(box); await userEvent.type(box, '之后的新草稿'); fireEvent.keyDown(box, { key: 'Enter' }); expect(bodies).toHaveLength(1)
  await userEvent.click(screen.getByRole('button', { name: '重试确认原消息' })); await screen.findByText(copy.accepted)
  expect(bodies).toHaveLength(2); expect(bodies[1]).toEqual(bodies[0]); expect(box).toHaveValue('之后的新草稿')
})
it.each(['nodeRunId', 'agentId', 'runtimeId', 'taskId'] as const)('能力%s不匹配固定草稿目标时禁发', field => {
  renderWithQuery(<Harness cap={{ ...capability, [field]: 'other-target' }} />)
  expect(screen.getByRole('button', { name: '发送' })).toBeDisabled()
})
it('历史只读，无发送；原题回答权限不参与普通消息能力', () => {
  renderWithQuery(<Harness current={false} />)
  expect(screen.getByRole('textbox')).toHaveAttribute('readonly'); expect(screen.queryByRole('button', { name: '发送' })).not.toBeInTheDocument()
})
it('错误目标回执不清稿、不宣称接收或自动改投', async () => {
  server.use(http.post(endpoint, async ({ request }) => HttpResponse.json({ ...receipt(await request.json() as Schemas['TaskNodeInboxInput']), nodeRunId: 'other-node' }, { status: 201 })))
  renderWithQuery(<Harness />); const box = screen.getByRole('textbox')
  await userEvent.type(box, '不能错误清除'); await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await screen.findByText(/Inbox 或运行记录返回的原任务/); expect(box).toHaveValue('不能错误清除')
  expect(screen.queryByText(copy.accepted)).not.toBeInTheDocument()
})

it('未知结果按原action GET恢复，404不重POST/换ID；历史仍可读原回执', async () => {
  const user = userEvent.setup(), bodies: Schemas['TaskNodeInboxInput'][] = []; let found = false
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return HttpResponse.json(errorBody('internal_error'), { status: 503 }) }),
    http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/inbox-actions/:actionId`), ({ params }) => { expect(params.actionId).toBe(bodies[0].actionId); return found ? HttpResponse.json(receipt(bodies[0])) : HttpResponse.json(errorBody('not_found', '原动作尚不可见'), { status: 404 }) }))
  const env = renderWithQuery(<Harness />); await user.type(screen.getByRole('textbox'), '保持原动作'); await user.click(screen.getByRole('button', { name: '发送' })); await screen.findByRole('button', { name: '读取原发送结果' })
  await user.click(screen.getByRole('button', { name: '读取原发送结果' })); await screen.findByText(copy.notFound); expect(bodies).toHaveLength(1)
  env.rerender(<Harness current={false} />); found = true
  await user.click(screen.getByRole('button', { name: '读取原发送结果' })); await screen.findByText(copy.accepted); expect(bodies).toHaveLength(1); expect(screen.getByRole('textbox')).toHaveValue('')
})
it('任务节点把 /resume 当普通节点消息，不弹平台命令确认', async () => {
  const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />)
  await userEvent.type(screen.getByRole('textbox'), '/resume full-session-id')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(bodies).toHaveLength(1))
  expect(bodies[0].text).toBe('/resume full-session-id')
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})
it('⌘／Ctrl+Enter 作为打断发到原节点；输入框下方说明按键', async () => {
  const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />)
  expect(screen.getByText('立即打断')).toBeVisible()
  const box = screen.getByRole('textbox'); await userEvent.type(box, '先停下{Meta>}{Enter}{/Meta}')
  await waitFor(() => expect(bodies).toHaveLength(1)); expect(bodies[0]).toMatchObject({ text: '先停下', whenBusy: 'interrupt', expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId })
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, '也可以用 Ctrl{Control>}{Enter}{/Control}')
  await waitFor(() => expect(bodies).toHaveLength(2)); expect(bodies[1].whenBusy).toBe('interrupt')
})
