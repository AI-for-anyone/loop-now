import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import { Composer } from './Composer'
import { apiUrl, makeMessage, makeRun } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import type { Schemas } from '@/api/client'
const agentId = 'image-agent', limits = { maxCount: 4, maxImageBytes: 5242880, maxTotalBytes: 20971520 }
const refs = new Map<string, Schemas['MessageImage']>()
const file = (name: string) => new File(['synthetic-png'], name, { type: 'image/png' })
function uploadFixture() {
  refs.clear()
  server.use(http.put(apiUrl(`/agents/${agentId}/message-images/:imageId`), async ({ request, params }) => {
    expect(request.headers.get('Content-Type')).toBe('image/png')
    const size = (await request.arrayBuffer()).byteLength, id = String(params.imageId)
    const ref: Schemas['MessageImage'] = { id, mimeType: 'image/png', size, width: 2, height: 2 }; refs.set(id, ref)
    return HttpResponse.json(ref, { status: 201 })
  }))
}
function mount(busy = false) { renderWithQuery(<Composer agentId={agentId} agentName="开发助手" busy={busy} disabled={false} maxMessageBytes={4096} imageLimits={limits} onSent={() => {}} />) }
function accepted(body: Schemas['SendMessageInput']) {
  const run = makeRun({ status: 'starting', endedAt: null })
  return HttpResponse.json({ run, message: { ...makeMessage({ text: body.text, run }), images: (body.imageIds ?? []).map(id => refs.get(id)!) } }, { status: 202 })
}
it('keeps the image plus, one-line editor and send arrow in the same composer border', async () => {
  mount()
  const editor = screen.getByRole('textbox'), border = editor.closest('[data-chat-composer]')!
  expect(editor).toHaveAttribute('rows', '1')
  expect(within(border as HTMLElement).getByRole('button', { name: '添加图片' })).toBeInTheDocument()
  const send = within(border as HTMLElement).getByRole('button', { name: '发送' })
  expect(send.textContent).toBe('')
  expect(send.querySelector('svg')).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: '添加图片' })).toHaveLength(1)
  await userEvent.type(editor, '第一行{Shift>}{Enter}{/Shift}第二行')
  expect(editor).toHaveValue('第一行\n第二行')
  expect(border).toHaveClass('rounded-[32px]')
})
it('puts attached thumbnails inside the composer border, above the text row', async () => {
  mount(); await userEvent.upload(screen.getByLabelText('选择图片文件'), [file('一.png'), file('二.png')])
  const border = screen.getByRole('textbox').closest('[data-chat-composer]') as HTMLElement, list = within(border).getByRole('list', { name: '待发送图片' })
  expect(border.firstElementChild).toContainElement(list)
  expect(border.lastElementChild).toContainElement(screen.getByRole('textbox'))
  expect(border).toHaveClass('rounded-[24px]')
  expect(await within(list).findByAltText('二.png')).toBeInTheDocument()
  await userEvent.click(within(list).getByRole('button', { name: '移除图片 1' }))
  expect(within(list).queryByAltText('一.png')).not.toBeInTheDocument()
  await userEvent.click(within(border).getByRole('button', { name: '移除图片 1' }))
  expect(screen.queryByRole('list', { name: '待发送图片' })).not.toBeInTheDocument()
  expect(border).toHaveClass('rounded-[32px]')
})
it('shows upload progress and the failed image on its own thumbnail', async () => {
  let release!: () => void, uploads = 0; const gate = new Promise<void>(resolve => { release = resolve })
  server.use(http.put(apiUrl(`/agents/${agentId}/message-images/:imageId`), async ({ params }) => {
    if (++uploads === 1) return HttpResponse.json({ id: String(params.imageId), mimeType: 'image/png', size: 13, width: 2, height: 2 }, { status: 201 })
    await gate; return HttpResponse.json({ error: { code: 'payload_too_large', message: '图片大小超限' } }, { status: 413 })
  }))
  mount(); await userEvent.upload(screen.getByLabelText('选择图片文件'), [file('先传.png'), file('后传.png')])
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  const items = () => within(screen.getByRole('list', { name: '待发送图片' })).getAllByRole('listitem')
  await waitFor(() => expect(within(items()[0]).queryByText('上传中')).not.toBeInTheDocument())
  expect(within(items()[1]).getByText('上传中')).toBeInTheDocument()
  expect(screen.getByRole('status')).toHaveTextContent('正在上传图片…')
  release()
  expect(await screen.findByRole('alert')).toHaveTextContent('图片大小超限')
  expect(within(items()[1]).getByText('上传失败')).toBeInTheDocument()
  expect(within(items()[0]).queryByText(/上传/)).not.toBeInTheDocument()
})
it('sends pure images as scoped uploads plus formal IDs, with no base64 in the message', async () => {
  uploadFixture(); const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => { const body = await request.json() as Schemas['SendMessageInput']; sent.push(body); return accepted(body) }))
  mount(); await userEvent.upload(screen.getByLabelText('选择图片文件'), file('纯图片.png'))
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(sent).toHaveLength(1))
  expect(sent[0]).toEqual({ text: '', imageIds: [...refs.keys()] })
  await waitFor(() => expect(screen.queryByRole('list', { name: '待发送图片' })).not.toBeInTheDocument())
})
it('a failed unsupported-model send preserves text and images and never retries automatically', async () => {
  uploadFixture(); let count = 0
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), () => { count++; return HttpResponse.json({ error: { code: 'validation_failed', message: '当前模型不支持图片输入' } }, { status: 422 }) }))
  mount(); await userEvent.type(screen.getByRole('textbox'), '请看图片'); await userEvent.upload(screen.getByLabelText('选择图片文件'), file('保留.png'))
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('当前模型不支持图片输入')
  expect(screen.getByRole('textbox')).toHaveValue('请看图片'); expect(screen.getByRole('button', { name: '移除图片 1' })).toBeInTheDocument(); expect(screen.getByAltText('保留.png')).toBeInTheDocument(); expect(count).toBe(1)
  expect(screen.queryByText('上传失败')).not.toBeInTheDocument()
})
it('success clears only the submitted images and retains text/new images entered during the request', async () => {
  uploadFixture(); let release!: () => void, posted = false; const gate = new Promise<void>(resolve => { release = resolve })
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => { const body = await request.json() as Schemas['SendMessageInput']; posted = true; await gate; return accepted(body) }))
  mount(); await userEvent.type(screen.getByRole('textbox'), '已提交'); await userEvent.upload(screen.getByLabelText('选择图片文件'), file('已提交.png'))
  await userEvent.click(screen.getByRole('button', { name: '发送' })); await waitFor(() => expect(posted).toBe(true))
  await userEvent.clear(screen.getByRole('textbox')); await userEvent.type(screen.getByRole('textbox'), '新文字')
  await userEvent.upload(screen.getByLabelText('选择图片文件'), file('新图片.png')); release()
  await waitFor(() => expect(screen.queryByAltText('已提交.png')).not.toBeInTheDocument())
  expect(screen.getByRole('textbox')).toHaveValue('新文字'); expect(await screen.findByAltText('新图片.png')).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: /^移除图片/ }).map(button => button.getAttribute('aria-label'))).toEqual(['移除图片 1'])
})

it('busy image send queues directly without a choice dialog', async () => {
  uploadFixture(); const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => { const body = await request.json() as Schemas['SendMessageInput']; sent.push(body); return accepted(body) }))
  mount(true); await userEvent.upload(screen.getByLabelText('选择图片文件'), file('忙时图片.png'))
  await userEvent.click(screen.getByRole('button', { name: '排队发送' }))
  await waitFor(() => expect(sent).toHaveLength(1))
  expect(sent[0]).toEqual({ text: '', imageIds: [...refs.keys()], whenBusy: 'queue' })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('slash command text with an image is an ordinary message and does not confirm /new', async () => {
  uploadFixture(); const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => { const body = await request.json() as Schemas['SendMessageInput']; sent.push(body); return accepted(body) }))
  mount(); await userEvent.type(screen.getByRole('textbox'), '/new')
  await userEvent.upload(screen.getByLabelText('选择图片文件'), file('图文.png'))
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(sent).toHaveLength(1))
  expect(sent[0]).toEqual({ text: '/new', imageIds: [...refs.keys()] })
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

it('slash command text with an image retries agent_busy as an ordinary queued message', async () => {
  uploadFixture(); const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']; sent.push(body)
    if (sent.length === 1) return HttpResponse.json({ error: { code: 'agent_busy', message: 'Agent正忙', details: { currentRunId: crypto.randomUUID(), queued: { interrupt: 0, normal: 0 } } } }, { status: 409 })
    return accepted(body)
  }))
  mount(); await userEvent.type(screen.getByRole('textbox'), '/reload')
  await userEvent.upload(screen.getByLabelText('选择图片文件'), file('忙时图文.png'))
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(sent).toHaveLength(2))
  expect(sent[0]).toEqual({ text: '/reload', imageIds: [...refs.keys()] })
  expect(sent[1]).toEqual({ text: '/reload', imageIds: [...refs.keys()], whenBusy: 'queue' })
})
