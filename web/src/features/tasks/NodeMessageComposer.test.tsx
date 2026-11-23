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
it('queues by default, strictly bound to the original four targets, and shows only received, not replied by the Agent', async () => {
  const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />)
  await userEvent.type(screen.getByRole('textbox'), 'plain message to the original node'); await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(await screen.findByText(copy.accepted)).toBeVisible()
  expect(bodies).toHaveLength(1); expect(bodies[0]).toMatchObject({ expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, text: 'plain message to the original node', whenBusy: 'queue' })
  expect(bodies[0].actionId).toMatch(/^[\da-f-]{36}$/)
  expect(screen.getByRole('textbox')).toHaveValue('')
})
it('no longer offers a when-busy choice and always queues; IME Enter, Shift+Enter and empty messages do not submit', async () => {
  const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />)
  await userEvent.click(screen.getByRole('button', { name: 'Send' })); expect(screen.getByRole('alert')).toBeVisible(); expect(bodies).toHaveLength(0)
  const box = screen.getByRole('textbox'); await userEvent.type(box, 'IME text')
  fireEvent.keyDown(box, { key: 'Enter', isComposing: true }); fireEvent.keyDown(box, { key: 'Enter', keyCode: 229 }); fireEvent.keyDown(box, { key: 'Enter', shiftKey: true })
  expect(bodies).toHaveLength(0)
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument(); fireEvent.keyDown(box, { key: 'Enter' })
  await waitFor(() => expect(bodies).toHaveLength(1)); expect(bodies[0].whenBusy).toBe('queue')
})
it('editing continues while sending, and the original receipt does not clear the new draft', async () => {
  let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; await gate; return HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />); const box = screen.getByRole('textbox')
  await userEvent.type(box, 'submitted text'); await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(screen.getByRole('button', { name: 'Sending…' })).toBeDisabled()
  await userEvent.clear(box); await userEvent.type(box, 'new edited draft'); release()
  await screen.findByText(copy.accepted); expect(box).toHaveValue('new edited draft')
})
it('409 sends only once, keeps the original draft, and neither falls back to the old API nor resends', async () => {
  let posts = 0
  server.use(http.post(endpoint, () => { posts++; return HttpResponse.json(errorBody('task_conflict', 'The node is no longer the current run'), { status: 409 }) }))
  renderWithQuery(<Harness />); const box = screen.getByRole('textbox')
  await userEvent.type(box, 'kept on the old node'); await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(await screen.findByText(/The node is no longer the current run/)).toBeVisible(); expect(box).toHaveValue('kept on the old node')
  await new Promise(resolve => setTimeout(resolve, 100)); expect(posts).toBe(1)
})
it('an unknown result starts no new action automatically; an explicit manual retry reuses the original action, text and target, and a new draft typed meanwhile is not cleared', async () => {
  const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return bodies.length === 1 ? HttpResponse.json(errorBody('internal_error', 'The receipt cannot be confirmed yet'), { status: 500 }) : HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />); const box = screen.getByRole('textbox')
  await userEvent.type(box, 'original action text'); await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await screen.findByRole('button', { name: 'Retry checking the original message' }); expect(bodies).toHaveLength(1)
  await userEvent.clear(box); await userEvent.type(box, 'later new draft'); fireEvent.keyDown(box, { key: 'Enter' }); expect(bodies).toHaveLength(1)
  await userEvent.click(screen.getByRole('button', { name: 'Retry checking the original message' })); await screen.findByText(copy.accepted)
  expect(bodies).toHaveLength(2); expect(bodies[1]).toEqual(bodies[0]); expect(box).toHaveValue('later new draft')
})
it.each(['nodeRunId', 'agentId', 'runtimeId', 'taskId'] as const)('sending is disabled when the capability %s does not match the fixed draft target', field => {
  renderWithQuery(<Harness cap={{ ...capability, [field]: 'other-target' }} />)
  expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled()
})
it('history is read-only with no send; permission to answer the original question does not count for the plain message capability', () => {
  renderWithQuery(<Harness current={false} />)
  expect(screen.getByRole('textbox')).toHaveAttribute('readonly'); expect(screen.queryByRole('button', { name: 'Send' })).not.toBeInTheDocument()
})
it('a receipt for the wrong target does not clear the draft, claim it was received, or redirect automatically', async () => {
  server.use(http.post(endpoint, async ({ request }) => HttpResponse.json({ ...receipt(await request.json() as Schemas['TaskNodeInboxInput']), nodeRunId: 'other-node' }, { status: 201 })))
  renderWithQuery(<Harness />); const box = screen.getByRole('textbox')
  await userEvent.type(box, 'must not be cleared by mistake'); await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await screen.findByText(/The task, run, Agent, or instance returned by the Inbox or run log/); expect(box).toHaveValue('must not be cleared by mistake')
  expect(screen.queryByText(copy.accepted)).not.toBeInTheDocument()
})

it('an unknown result recovers by a GET of the original action, a 404 neither re-POSTs nor changes the ID, and history can still read the original receipt', async () => {
  const user = userEvent.setup(), bodies: Schemas['TaskNodeInboxInput'][] = []; let found = false
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return HttpResponse.json(errorBody('internal_error'), { status: 503 }) }),
    http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/inbox-actions/:actionId`), ({ params }) => { expect(params.actionId).toBe(bodies[0].actionId); return found ? HttpResponse.json(receipt(bodies[0])) : HttpResponse.json(errorBody('not_found', 'The action is not visible yet'), { status: 404 }) }))
  const env = renderWithQuery(<Harness />); await user.type(screen.getByRole('textbox'), 'keep the original action'); await user.click(screen.getByRole('button', { name: 'Send' })); await screen.findByRole('button', { name: 'Read the original send result' })
  await user.click(screen.getByRole('button', { name: 'Read the original send result' })); await screen.findByText(copy.notFound); expect(bodies).toHaveLength(1)
  env.rerender(<Harness current={false} />); found = true
  await user.click(screen.getByRole('button', { name: 'Read the original send result' })); await screen.findByText(copy.accepted); expect(bodies).toHaveLength(1); expect(screen.getByRole('textbox')).toHaveValue('')
})
it('a task node treats /resume as a plain node message, without a platform command confirmation', async () => {
  const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />)
  await userEvent.type(screen.getByRole('textbox'), '/resume full-session-id')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(bodies).toHaveLength(1))
  expect(bodies[0].text).toBe('/resume full-session-id')
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})
it('⌘/Ctrl+Enter sends to the original node as an interrupt; the keys are explained below the input', async () => {
  const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.post(endpoint, async ({ request }) => { const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body); return HttpResponse.json(receipt(body), { status: 201 }) }))
  renderWithQuery(<Harness />)
  expect(screen.getByText('to interrupt')).toBeVisible()
  const box = screen.getByRole('textbox'); await userEvent.type(box, 'stop first{Meta>}{Enter}{/Meta}')
  await waitFor(() => expect(bodies).toHaveLength(1)); expect(bodies[0]).toMatchObject({ text: 'stop first', whenBusy: 'interrupt', expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId })
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, 'Ctrl works too{Control>}{Enter}{/Control}')
  await waitFor(() => expect(bodies).toHaveLength(2)); expect(bodies[1].whenBusy).toBe('interrupt')
})
