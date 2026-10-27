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
  await userEvent.upload(screen.getByLabelText('选择图片文件'), file('原节点.png'))
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await screen.findByRole('button', { name: '读取原发送结果' })
  expect(bodies).toHaveLength(1); expect(bodies[0]).toMatchObject({ text: '', imageIds: ids, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, whenBusy: 'queue' })
  await userEvent.type(screen.getByRole('textbox'), '后续草稿'); await userEvent.upload(screen.getByLabelText('选择图片文件'), file('后续.png'))
  view.rerender(<Harness current={false} />)
  expect(screen.getByRole('textbox')).toHaveAttribute('readonly')
  await userEvent.click(screen.getByRole('button', { name: '读取原发送结果' }))
  await waitFor(() => expect(screen.queryByAltText('原节点.png')).not.toBeInTheDocument())
  expect(await screen.findByAltText('后续.png')).toBeInTheDocument(); expect(screen.getByRole('button', { name: '移除图片 1' })).toBeDisabled()
  expect(screen.getByRole('textbox')).toHaveValue('后续草稿'); expect(bodies).toHaveLength(1); expect(normalPosts).toBe(0)
})
it('upload failure keeps original file and text and never posts an Inbox action', async () => {
  let posts = 0
  server.use(http.put(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/message-images/:imageId`), () => HttpResponse.json(errorBody('payload_too_large', '图片大小超限'), { status: 413 })), http.post(endpoint, () => { posts++; return new HttpResponse(null, { status: 500 }) }))
  renderWithQuery(<Harness />); await userEvent.type(screen.getByRole('textbox'), '请保留'); await userEvent.upload(screen.getByLabelText('选择图片文件'), file('保留.png'))
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('图片大小超限')
  expect(screen.getByRole('textbox')).toHaveValue('请保留'); expect(screen.getByRole('button', { name: '移除图片 1' })).toBeInTheDocument(); expect(posts).toBe(0)
  const border = screen.getByRole('textbox').closest('[data-node-composer]') as HTMLElement
  expect(border.firstElementChild).toContainElement(screen.getByRole('list', { name: '待发送图片' }))
  expect(border).toHaveClass('rounded-[18px]')
  expect(within(border).getByText('上传失败')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '读取原发送结果' })).not.toBeInTheDocument()
})
