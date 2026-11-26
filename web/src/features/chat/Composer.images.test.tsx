import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, it } from 'vitest'
import { Composer } from './Composer'
import { resetChatDraftsForTest } from './draft-store'
import { apiUrl, makeMessage, makeRun } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import type { Schemas } from '@/api/client'
afterEach(() => resetChatDraftsForTest())
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
function mount(busy = false) { renderWithQuery(<Composer agentId={agentId} agentName="dev-helper" busy={busy} disabled={false} maxMessageBytes={4096} imageLimits={limits} onSent={() => {}} />) }
function accepted(body: Schemas['SendMessageInput']) {
  const run = makeRun({ status: 'starting', endedAt: null })
  return HttpResponse.json({ run, message: { ...makeMessage({ text: body.text, run }), images: (body.imageIds ?? []).map(id => refs.get(id)!) } }, { status: 202 })
}
// Unified floating composer card: editor on top and integrated bottom toolbar holding the image plus and send arrow inside one rounded card.
it('keeps the image plus, one-line editor and send arrow inside the unified composer card', async () => {
  mount()
  const editor = screen.getByRole('textbox'), card = editor.closest('[data-chat-composer]') as HTMLElement, toolbar = card.querySelector('[data-chat-toolbar]') as HTMLElement
  expect(editor).toHaveAttribute('rows', '1')
  expect(card).toHaveAttribute('data-chat-input')
  expect(within(toolbar).getByRole('button', { name: 'Add image' })).toBeInTheDocument()
  const send = within(toolbar).getByRole('button', { name: 'Send' })
  expect(send.textContent).toBe('')
  expect(send.querySelector('svg')).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: 'Add image' })).toHaveLength(1)
  await userEvent.type(editor, 'Line one{Shift>}{Enter}{/Shift}Line two')
  expect(editor).toHaveValue('Line one\nLine two')
  expect(card).toHaveClass('rounded-2xl', '[[data-skin=plate]_&]:rounded-[22px]')
})
it('puts attached thumbnails inside the composer card, above the text row', async () => {
  mount(); await userEvent.upload(screen.getByLabelText('Choose an image file'), [file('one.png'), file('two.png')])
  const card = screen.getByRole('textbox').closest('[data-chat-input]') as HTMLElement, list = within(card).getByRole('list', { name: 'Images to send' })
  expect(card.firstElementChild).toContainElement(list)
  expect(list.compareDocumentPosition(screen.getByRole('textbox')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(card).toHaveClass('rounded-2xl', '[[data-skin=plate]_&]:rounded-[22px]')
  expect(await within(list).findByAltText('two.png')).toBeInTheDocument()
  await userEvent.click(within(list).getByRole('button', { name: 'Remove image 1' }))
  expect(within(list).queryByAltText('one.png')).not.toBeInTheDocument()
  await userEvent.click(within(card).getByRole('button', { name: 'Remove image 1' }))
  expect(screen.queryByRole('list', { name: 'Images to send' })).not.toBeInTheDocument()
  expect(card).toHaveClass('rounded-2xl', '[[data-skin=plate]_&]:rounded-[22px]')
})
it('shows upload progress and the failed image on its own thumbnail', async () => {
  let release!: () => void, uploads = 0; const gate = new Promise<void>(resolve => { release = resolve })
  server.use(http.put(apiUrl(`/agents/${agentId}/message-images/:imageId`), async ({ params }) => {
    if (++uploads === 1) return HttpResponse.json({ id: String(params.imageId), mimeType: 'image/png', size: 13, width: 2, height: 2 }, { status: 201 })
    await gate; return HttpResponse.json({ error: { code: 'payload_too_large', message: 'The image is too large' } }, { status: 413 })
  }))
  mount(); await userEvent.upload(screen.getByLabelText('Choose an image file'), [file('first.png'), file('second.png')])
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  const items = () => within(screen.getByRole('list', { name: 'Images to send' })).getAllByRole('listitem')
  await waitFor(() => expect(within(items()[0]).queryByText('Uploading')).not.toBeInTheDocument())
  expect(within(items()[1]).getByText('Uploading')).toBeInTheDocument()
  expect(screen.getByRole('status')).toHaveTextContent('Uploading images…')
  release()
  expect(await screen.findByRole('alert')).toHaveTextContent('The image is too large')
  expect(within(items()[1]).getByText('Upload failed')).toBeInTheDocument()
  expect(within(items()[0]).queryByText(/Upload/)).not.toBeInTheDocument()
})
it('sends pure images as scoped uploads plus formal IDs, with no base64 in the message', async () => {
  uploadFixture(); const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => { const body = await request.json() as Schemas['SendMessageInput']; sent.push(body); return accepted(body) }))
  mount(); await userEvent.upload(screen.getByLabelText('Choose an image file'), file('image-only.png'))
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(sent).toHaveLength(1))
  expect(sent[0]).toEqual({ text: '', imageIds: [...refs.keys()] })
  await waitFor(() => expect(screen.queryByRole('list', { name: 'Images to send' })).not.toBeInTheDocument())
})
it('a failed unsupported-model send preserves text and images and never retries automatically', async () => {
  uploadFixture(); let count = 0
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), () => { count++; return HttpResponse.json({ error: { code: 'validation_failed', message: 'The current model does not accept images' } }, { status: 422 }) }))
  mount(); await userEvent.type(screen.getByRole('textbox'), 'Please look at the image'); await userEvent.upload(screen.getByLabelText('Choose an image file'), file('kept.png'))
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('The current model does not accept images')
  expect(screen.getByRole('textbox')).toHaveValue('Please look at the image'); expect(screen.getByRole('button', { name: 'Remove image 1' })).toBeInTheDocument(); expect(screen.getByAltText('kept.png')).toBeInTheDocument(); expect(count).toBe(1)
  expect(screen.queryByText('Upload failed')).not.toBeInTheDocument()
})
it('success clears only the submitted images and retains text/new images entered during the request', async () => {
  uploadFixture(); let release!: () => void, posted = false; const gate = new Promise<void>(resolve => { release = resolve })
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => { const body = await request.json() as Schemas['SendMessageInput']; posted = true; await gate; return accepted(body) }))
  mount(); await userEvent.type(screen.getByRole('textbox'), 'Submitted'); await userEvent.upload(screen.getByLabelText('Choose an image file'), file('submitted.png'))
  await userEvent.click(screen.getByRole('button', { name: 'Send' })); await waitFor(() => expect(posted).toBe(true))
  await userEvent.clear(screen.getByRole('textbox')); await userEvent.type(screen.getByRole('textbox'), 'New text')
  await userEvent.upload(screen.getByLabelText('Choose an image file'), file('new-image.png')); release()
  await waitFor(() => expect(screen.queryByAltText('submitted.png')).not.toBeInTheDocument())
  expect(screen.getByRole('textbox')).toHaveValue('New text'); expect(await screen.findByAltText('new-image.png')).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: /^Remove image/ }).map(button => button.getAttribute('aria-label'))).toEqual(['Remove image 1'])
})

it('busy image send queues directly without a choice dialog', async () => {
  uploadFixture(); const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => { const body = await request.json() as Schemas['SendMessageInput']; sent.push(body); return accepted(body) }))
  mount(true); await userEvent.upload(screen.getByLabelText('Choose an image file'), file('busy-image.png'))
  await userEvent.click(screen.getByRole('button', { name: 'Queue' }))
  await waitFor(() => expect(sent).toHaveLength(1))
  expect(sent[0]).toEqual({ text: '', imageIds: [...refs.keys()], whenBusy: 'queue' })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('slash command text with an image is an ordinary message and does not confirm /new', async () => {
  uploadFixture(); const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => { const body = await request.json() as Schemas['SendMessageInput']; sent.push(body); return accepted(body) }))
  mount(); await userEvent.type(screen.getByRole('textbox'), '/new')
  expect(screen.getByRole('listbox', { name: 'Commands' })).toBeInTheDocument()
  await userEvent.upload(screen.getByLabelText('Choose an image file'), file('text-and-image.png'))
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(sent).toHaveLength(1))
  expect(sent[0]).toEqual({ text: '/new', imageIds: [...refs.keys()] })
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

it('slash command text with an image retries agent_busy as an ordinary queued message', async () => {
  uploadFixture(); const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']; sent.push(body)
    if (sent.length === 1) return HttpResponse.json({ error: { code: 'agent_busy', message: 'The Agent is replying; interrupt it or queue the message', details: { currentRunId: crypto.randomUUID(), queued: { interrupt: 0, normal: 0 } } } }, { status: 409 })
    return accepted(body)
  }))
  mount(); await userEvent.type(screen.getByRole('textbox'), '/reload')
  await userEvent.upload(screen.getByLabelText('Choose an image file'), file('busy-text-and-image.png'))
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(sent).toHaveLength(2))
  expect(sent[0]).toEqual({ text: '/reload', imageIds: [...refs.keys()] })
  expect(sent[1]).toEqual({ text: '/reload', imageIds: [...refs.keys()], whenBusy: 'queue' })
})
it("doesn't take images for a model that takes none: the plus is disabled and says why, and a pasted image is refused", async () => {
  renderWithQuery(<Composer agentId={agentId} agentName="dev-helper" busy={false} disabled={false} maxMessageBytes={4096} imageLimits={limits} imagesRefused onSent={() => {}} />)
  const add = screen.getByRole('button', { name: 'Add image' })
  expect(add).toBeDisabled()
  expect(add).toHaveAttribute('title', "This model doesn't take images")
  const editor = screen.getByRole('textbox')
  editor.focus()
  await userEvent.paste({ files: [file('shot.png')] } as unknown as DataTransfer)
  expect(await screen.findByRole('alert')).toHaveTextContent("This model doesn't take images")
  expect(screen.queryByRole('list', { name: 'Images to send' })).toBeNull()
})
it('preserves pending images per Agent across unmount and remount, and clears them after sending', async () => {
  uploadFixture()
  server.use(http.post(apiUrl(`/agents/${agentId}/messages`), async ({ request }) => accepted(await request.json() as Schemas['SendMessageInput'])))
  const first = renderWithQuery(<Composer agentId={agentId} agentName="dev-helper" busy={false} disabled={false} maxMessageBytes={4096} imageLimits={limits} onSent={() => {}} />)
  await userEvent.upload(screen.getByLabelText('Choose an image file'), file('persisted.png'))
  expect(await screen.findByAltText('persisted.png')).toBeInTheDocument()
  first.unmount()

  const second = renderWithQuery(<Composer agentId={agentId} agentName="dev-helper" busy={false} disabled={false} maxMessageBytes={4096} imageLimits={limits} onSent={() => {}} />)
  expect(await screen.findByAltText('persisted.png')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(screen.queryByRole('list', { name: 'Images to send' })).toBeNull())
  second.unmount()

  renderWithQuery(<Composer agentId={agentId} agentName="dev-helper" busy={false} disabled={false} maxMessageBytes={4096} imageLimits={limits} onSent={() => {}} />)
  expect(screen.queryByRole('list', { name: 'Images to send' })).toBeNull()
})
