import { useState } from 'react'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import type { ImageDraft } from '@/features/images/image-drafts'
import { apiUrl, defaultProject, errorBody } from '@/test/api'
import { makeTaskDetail, nodeId, taskId } from '@/test/tasks'
import { taskInboxReceipt } from '@/test/task-inbox'
import { server } from '@/test/server'
import { renderWithQuery } from '@/test/render'
import type { NodeDraft } from './NodeRecordPanel'
import { NodeMessageComposer } from './NodeMessageComposer'
const node = makeTaskDetail(defaultProject.id).nodes[1]
const endpoint = apiUrl(`/tasks/${taskId}/nodes/${nodeId}/inbox-messages`)
const limits = { maxCount: 4, maxImageBytes: 5242880, maxTotalBytes: 20971520 }
const capability: Schemas['TaskNodeMessageCapability'] = { taskId, nodeRunId: node.id, agentId: node.agentId, runtimeId: node.runtimeId, canSend: true, reason: null, handlingHint: null }
function Harness({ current = true }: { current?: boolean }) {
  const [text, setText] = useState(''), [images, setImages] = useState<ImageDraft[]>([]), [action, setAction] = useState<NodeDraft['action']>()
  return <NodeMessageComposer taskId={taskId} draft={{ text, images, agentId: node.agentId, runtimeId: node.runtimeId, nodeRunId: node.id, agentName: node.agentName, action }} onAction={setAction} current={current} disabled={false} capability={capability} text={text} onChange={setText} onImages={setImages} imageLimits={limits} onAccepted={(sent, ids = []) => { setText(old => old === sent ? '' : old); setImages(old => old.filter(image => !ids.includes(image.id))) }} />
}
const file = (name: string) => new File(['synthetic-png'], name, { type: 'image/png' })
const imageRef = (id: string): Schemas['MessageImage'] => ({ id, mimeType: 'image/png', size: 13, width: 2, height: 2 })
function uploaded() {
  const ids: string[] = []
  server.use(http.put(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/message-images/:imageId`), ({ params }) => { const id = String(params.imageId); ids.push(id); return HttpResponse.json(imageRef(id), { status: 201 }) }))
  return ids
}
const receipt = (body: Schemas['TaskNodeInboxInput']) => { const value = taskInboxReceipt(body); value.message.images = body.imageIds?.map(imageRef); return value }
it('pure image stays on strict task Inbox; unknown action recovery clears only its images even on a historical node', async () => {
  const ids = uploaded(), bodies: Schemas['TaskNodeInboxInput'][] = []
  let normalPosts = 0
  server.use(http.post(endpoint, async ({ request }) => { bodies.push(await request.json() as Schemas['TaskNodeInboxInput']); return HttpResponse.json(errorBody('internal_error'), { status: 503 }) }), http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/inbox-actions/:actionId`), ({ params }) => { expect(params.actionId).toBe(bodies[0].actionId); return HttpResponse.json(receipt(bodies[0])) }), http.post(apiUrl(`/agents/${node.agentId}/messages`), () => { normalPosts++; return new HttpResponse(null, { status: 500 }) }))
  const view = renderWithQuery(<Harness />)
  await userEvent.upload(screen.getByLabelText('Choose an image file'), file('original-node.png'))
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await screen.findByRole('button', { name: 'Read the original send result' })
  expect(bodies).toHaveLength(1); expect(bodies[0]).toMatchObject({ text: '', imageIds: ids, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, whenBusy: 'queue' })
  await userEvent.type(screen.getByRole('textbox'), 'follow-up draft'); await userEvent.upload(screen.getByLabelText('Choose an image file'), file('follow-up.png'))
  view.rerender(<Harness current={false} />)
  expect(screen.getByRole('textbox')).toHaveAttribute('readonly')
  await userEvent.click(screen.getByRole('button', { name: 'Read the original send result' }))
  await waitFor(() => expect(screen.queryByAltText('original-node.png')).not.toBeInTheDocument())
  expect(await screen.findByAltText('follow-up.png')).toBeInTheDocument(); expect(screen.getByRole('button', { name: 'Remove image 1' })).toBeDisabled()
  expect(screen.getByRole('textbox')).toHaveValue('follow-up draft'); expect(bodies).toHaveLength(1); expect(normalPosts).toBe(0)
})
it('upload failure keeps original file and text and never posts an Inbox action', async () => {
  let posts = 0
  server.use(http.put(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/message-images/:imageId`), () => HttpResponse.json(errorBody('payload_too_large', 'The image is too large'), { status: 413 })), http.post(endpoint, () => { posts++; return new HttpResponse(null, { status: 500 }) }))
  renderWithQuery(<Harness />); await userEvent.type(screen.getByRole('textbox'), 'please keep'); await userEvent.upload(screen.getByLabelText('Choose an image file'), file('keep.png'))
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('The image is too large')
  expect(screen.getByRole('textbox')).toHaveValue('please keep'); expect(screen.getByRole('button', { name: 'Remove image 1' })).toBeInTheDocument(); expect(posts).toBe(0)
  const border = screen.getByRole('textbox').closest('[data-node-composer]') as HTMLElement
  expect(border.firstElementChild).toContainElement(screen.getByRole('list', { name: 'Images to send' }))
  expect(border).toHaveClass('rounded-[18px]')
  expect(within(border).getByText('Upload failed')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Read the original send result' })).not.toBeInTheDocument()
})
