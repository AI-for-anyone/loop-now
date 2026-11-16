import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, context, errorBody, limits, makeMessage, makeQueueItem, makeRun, makeStatus } from '@/test/api'
import { agentParty } from '@/test/inbox'
import { renderChat } from '@/test/chat'

// Queued messages are shown one by one in the queue tray above the input, not in the
// conversation stream; the snapshot and queue.updated are authoritative for the list.

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']>, queuedRuns: Schemas['Run'][] = [], queueItems: Schemas['QueueItem'][] = []) {
  return { status: makeStatus(agentId, status), currentText: '', queuedRuns, queueItems }
}

const queueList = () => screen.getByRole('region', { name: 'Queued messages' })
// One row per queued message; image thumbnails inside a row are listitems too and don't count.
const rows = () => within(queueList()).getAllByRole('listitem').filter((item) => item.hasAttribute('data-kind'))
const texts = () => rows().map((item) => within(item).getByRole('button', { expanded: false }).textContent)
const log = () => screen.getByRole('log')
const manager = agentParty({ id: '57000000-0000-7000-8000-000000000001', name: 'test', address: 'test-project/test' })

function receipt(agentId: string, actionId: string, operation: Schemas['AgentQueueActionReceipt']['operation'], item: Schemas['QueueItem'] | null, queue: Schemas['QueueItem'][], run: Schemas['Run'] | null = null): Schemas['AgentQueueActionReceipt'] {
  return { actionId, agentId, operation, status: 'accepted', item, run, queue }
}

describe('queue tray above the input box', () => {
  it('lists one by one: interrupts first, marked up next; your own can be edited, Inbox messages show the sender; every row can be cancelled, normal queued rows can interrupt', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const sent: Schemas['SendMessageInput'][] = []
    const interrupt = makeQueueItem({ text: 'Cut in line', whenBusy: 'interrupt' })
    const own = makeQueueItem({ text: 'Run lint while you are at it' })
    const inbox = makeQueueItem({ kind: 'inbox', messageId: '58000000-0000-7000-8000-000000000001', from: manager, text: 'The fixture is updated; pull server-main first' })
    const { agent, user, emit } = await renderChat({
      messages: [makeMessage({ text: 'Current', run: current })],
      handlers: [
        http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
          const body = (await request.json()) as Schemas['SendMessageInput']
          sent.push(body)
          const run = makeRun({ status: 'queued', whenBusy: body.whenBusy ?? null, startedAt: null, endedAt: null })
          return HttpResponse.json({ message: makeMessage({ text: body.text, run }), run }, { status: 202 })
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [], [interrupt, own, inbox]))
    expect(within(queueList()).getByRole('heading', { name: 'Queued · 3' })).toBeInTheDocument()
    expect(within(queueList()).getByText('Handled in order after the current reply')).toBeInTheDocument()
    expect(rows().map((item) => item.getAttribute('data-kind'))).toEqual(['interrupt', 'queue', 'queue'])
    expect(texts()).toEqual(['Cut in line', 'Run lint while you are at it', 'The fixture is updated; pull server-main first'])
    const [first, second, third] = rows()
    expect(within(first).getByText('Up next')).toBeInTheDocument()
    expect(within(first).queryByRole('button', { name: 'Interrupt' })).toBeNull()
    expect(within(first).getByRole('button', { name: 'Edit this queued message' })).toBeEnabled()
    expect(within(second).getByRole('button', { name: 'Interrupt' })).toBeEnabled()
    expect(within(second).getByRole('button', { name: 'Edit this queued message' })).toBeEnabled()
    // An Inbox message sent by someone else: it names the sender and can be promoted to interrupt or cancelled, but not edited.
    expect(within(third).getByText('test')).toBeInTheDocument()
    expect(within(third).getByRole('button', { name: 'Interrupt' })).toBeEnabled()
    expect(within(third).queryByRole('button', { name: 'Edit this queued message' })).toBeNull()
    for (const row of rows()) expect(within(row).getByRole('button', { name: 'Cancel this queued message' })).toBeEnabled()
    expect(within(log()).queryByText(/^(Cut in line|Run lint while you are at it)$/)).toBeNull()

    // When busy, a message is queued directly without asking for a choice; the tray updates with queue.updated.
    expect(screen.getByLabelText('Message')).toHaveAttribute('placeholder', 'dev-helper is replying; new messages will queue')
    await user.type(screen.getByLabelText('Message'), 'One more thing{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: 'One more thing', whenBusy: 'queue' }]))
    expect(screen.queryByRole('dialog')).toBeNull()
    await emit('queue.updated', { queuedRuns: [], items: [interrupt, own, inbox, makeQueueItem({ text: 'One more thing' })] })
    await waitFor(() => expect(within(queueList()).getByRole('heading', { name: 'Queued · 4' })).toBeInTheDocument())
    expect(texts().at(-1)).toBe('One more thing')
    // The tray sits right above the input; the key hints are below the input, and the busy status does not repeat the queue count.
    expect(queueList().compareDocumentPosition(screen.getByLabelText('Message')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByText('Agent is busy (Replying)')).toBeInTheDocument()
    expect(screen.getByText('Stop the current reply only')).toBeInTheDocument()
  })

  it('the text is cut to one line, the full text shows on hover, a click expands it; images show as thumbnails', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const text = 'First line explains\nSecond line adds'
    const image: Schemas['MessageImage'] = { id: '00000000-0000-7000-8000-000000000301', mimeType: 'image/png', size: 1024, width: 640, height: 480 }
    const { agent, user, emit } = await renderChat()
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [], [makeQueueItem({ text, images: [image] })]))
    const body = within(queueList()).getByRole('button', { name: /First line explains/ })
    expect(body).toHaveAttribute('title', text)
    expect(body).toHaveAttribute('aria-expanded', 'false')
    await user.click(body)
    expect(body).toHaveAttribute('aria-expanded', 'true')
    expect(body).not.toHaveAttribute('title')
    expect(within(queueList()).getByRole('img', { name: 'Image 1 · 640×480' })).toBeInTheDocument()
  })

  it('no queued messages: no tray, the conversation shows as usual', async () => {
    const run = makeRun()
    const { agent, emit } = await renderChat({
      messages: [makeMessage({ text: 'Hello', run }), makeMessage({ role: 'assistant', text: 'Hi there', run })],
    })
    await emit('snapshot', snapshot(agent.id, {}))
    expect(screen.queryByRole('region', { name: 'Queued messages' })).toBeNull()
    expect(within(log()).getByText('Hi there')).toBeInTheDocument()
  })

  it('before the snapshot arrives the tray comes from the queue API; after it the snapshot wins', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const { agent, emit } = await renderChat({
      status: { busy: true, currentRun: current },
      handlers: [http.get(apiUrl('/agents/:agentId/queue'), ({ params }) => HttpResponse.json({ agentId: String(params.agentId), items: [makeQueueItem({ text: 'Queued from the API' })] }))],
    })
    expect(await within(queueList()).findByText('Queued from the API')).toBeInTheDocument()
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }))
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Queued messages' })).toBeNull())
  })

  it('interrupt: one click with an actionId takes effect, disabled while in flight; moves to the front by the receipt list; Inbox messages are promoted by message', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const runA = makeRun({ status: 'queued', whenBusy: 'queue', startedAt: null, endedAt: null })
    const a = makeQueueItem({ runId: runA.id, text: 'Normal one' })
    const b = makeQueueItem({ kind: 'inbox', messageId: '58000000-0000-7000-8000-000000000002', from: manager, text: 'From test' })
    const posts: { url: string; body: Schemas['AgentQueueActionInput'] }[] = []
    let release!: () => void
    const gate = new Promise<void>((resolve) => (release = resolve))
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.post(apiUrl('/agents/:agentId/inbox-messages/:messageId/promote-to-interrupt'), async ({ request, params }) => {
          const body = (await request.json()) as Schemas['AgentQueueActionInput']
          posts.push({ url: `inbox:${String(params.messageId)}`, body })
          await gate
          return HttpResponse.json(receipt(String(params.agentId), body.actionId, 'promote_inbox_message', b, [{ ...b, whenBusy: 'interrupt' }, a]))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [runA], [a, b]))
    expect(texts()).toEqual(['Normal one', 'From test'])
    const row = rows()[1]
    await user.click(within(row).getByRole('button', { name: 'Interrupt' }))
    const busyButton = within(row).getByRole('button', { name: 'Interrupting…' })
    expect(busyButton).toBeDisabled()
    expect(within(row).getByRole('button', { name: 'Cancel this queued message' })).toBeDisabled()
    await user.click(busyButton)
    expect(posts).toEqual([{ url: `inbox:${b.messageId}`, body: { actionId: expect.stringMatching(/^[\da-f-]{36}$/) } }])
    expect(screen.queryByRole('dialog')).toBeNull()

    await act(async () => release())
    await waitFor(() => expect(texts()).toEqual(['From test', 'Normal one']))
    expect(rows()[0]).toHaveAttribute('data-kind', 'interrupt')
    expect(within(rows()[0]).getByText('Up next')).toBeInTheDocument()
    expect(within(rows()[0]).queryByRole('button', { name: 'Interrupt' })).toBeNull()
  })

  it('queue.updated replaces the whole tray; a receipt arriving later does not overwrite it with its old list', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const a = makeQueueItem({ text: 'Normal one' }), b = makeQueueItem({ text: 'Normal two' })
    let release!: () => void
    const gate = new Promise<void>((resolve) => (release = resolve))
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.post(apiUrl('/agents/:agentId/runs/:runId/promote-to-interrupt'), async ({ request, params }) => {
          const body = (await request.json()) as Schemas['AgentQueueActionInput']
          await gate
          return HttpResponse.json(receipt(String(params.agentId), body.actionId, 'promote_run', b, [{ ...b, whenBusy: 'interrupt' }, a]))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [], [a, b]))
    await user.click(within(rows()[1]).getByRole('button', { name: 'Interrupt' }))
    // Before the receipt arrives: “Normal one” has started and the server has pushed a new list.
    await emit('queue.updated', { queuedRuns: [], items: [{ ...b, whenBusy: 'interrupt' }] })
    await waitFor(() => expect(texts()).toEqual(['Normal two']))
    await act(async () => release())
    await waitFor(() => expect(within(rows()[0]).queryByRole('button', { name: 'Interrupting…' })).toBeNull())
    expect(texts()).toEqual(['Normal two'])
  })

  it('409 when no longer queued: a light one-line notice (already started, already read), no dialog, no error, and a reread', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const own = makeQueueItem({ text: 'Normal one' })
    const inbox = makeQueueItem({ kind: 'inbox', messageId: '58000000-0000-7000-8000-000000000003', from: manager, text: 'Just read' })
    let statusReads = 0
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/status'), ({ params }) => {
          statusReads++
          return HttpResponse.json(makeStatus(String(params.agentId), { busy: true, currentRun: current }))
        }),
        http.post(apiUrl('/agents/:agentId/runs/:runId/promote-to-interrupt'), ({ params }) =>
          HttpResponse.json(errorBody('run_not_queued', 'The run has already started', { details: { runId: String(params.runId), status: 'starting', whenBusy: 'queue' } }), { status: 409 }),
        ),
        http.post(apiUrl('/agents/:agentId/inbox-messages/:messageId/cancel'), ({ params }) =>
          HttpResponse.json(errorBody('run_not_queued', 'The message has already been read', { details: { runId: null, status: null, whenBusy: 'queue', messageId: String(params.messageId), messageState: 'read' } }), { status: 409 }),
        ),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [], [own, inbox]))
    const before = statusReads
    await user.click(within(rows()[0]).getByRole('button', { name: 'Interrupt' }))
    expect(await screen.findByText('Already started; can\'t promote to interrupt')).toHaveAttribute('role', 'status')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByRole('dialog')).toBeNull()
    await waitFor(() => expect(statusReads).toBeGreaterThan(before))
    expect(within(rows()[0]).getByRole('button', { name: 'Interrupt' })).toBeEnabled()
    await user.click(within(rows()[1]).getByRole('button', { name: 'Cancel this queued message' }))
    expect(await screen.findByText('This message was already read and is no longer queued')).toHaveAttribute('role', 'status')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('cancel: when the result is unknown the button turns into retry, which reuses the same actionId; on success the row goes by the receipt', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const runA = makeRun({ status: 'queued', whenBusy: 'queue', startedAt: null, endedAt: null })
    const a = makeQueueItem({ runId: runA.id, text: 'Not needed' }), b = makeQueueItem({ text: 'Keep this' })
    const bodies: Schemas['AgentQueueActionInput'][] = []
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.post(apiUrl('/agents/:agentId/runs/:runId/cancel'), async ({ request, params }) => {
          const body = (await request.json()) as Schemas['AgentQueueActionInput']
          bodies.push(body)
          if (bodies.length === 1) return HttpResponse.json(errorBody('internal_error', 'The receipt cannot be confirmed yet'), { status: 500 })
          const cancelled = { ...runA, status: 'interrupted' as const, endReason: 'user_cancelled' as const, endedAt: '2026-10-12T08:01:00Z' }
          return HttpResponse.json(receipt(String(params.agentId), body.actionId, 'cancel_run', a, [b], cancelled))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [runA], [a, b]))
    await user.click(within(rows()[0]).getByRole('button', { name: 'Cancel this queued message' }))
    await user.click(await within(rows()[0]).findByRole('button', { name: 'Retry cancelling this queued message' }))
    await waitFor(() => expect(texts()).toEqual(['Keep this']))
    expect(bodies).toHaveLength(2)
    expect(bodies[1]).toEqual(bodies[0])
    // The withdrawn turn never ran, so it does not enter the conversation stream.
    expect(within(log()).queryByText('Not needed')).toBeNull()
  })

  it('edit: fetches the images first, then withdraws the message and puts its text and images back into the input box, keeping text already there in front', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const image: Schemas['MessageImage'] = { id: '00000000-0000-7000-8000-000000000302', mimeType: 'image/png', size: 8, width: 2, height: 2 }
    const item = makeQueueItem({ text: 'Original words', images: [image] })
    const order: string[] = []
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/context'), () => HttpResponse.json({ ...context, limits: { ...limits, maxMessageImages: 4, maxMessageImageBytes: 5 * 1024 * 1024 } })),
        http.get(apiUrl('/message-images/:imageId'), ({ params }) => {
          order.push(`image:${String(params.imageId)}`)
          return new HttpResponse(new Blob(['png-data'], { type: 'image/png' }), { headers: { 'Content-Type': 'image/png' } })
        }),
        http.post(apiUrl('/agents/:agentId/runs/:runId/cancel'), async ({ request, params }) => {
          const body = (await request.json()) as Schemas['AgentQueueActionInput']
          order.push('cancel')
          return HttpResponse.json(receipt(String(params.agentId), body.actionId, 'cancel_run', item, []))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [], [item]))
    await user.type(screen.getByLabelText('Message'), 'Written first')
    await user.click(within(rows()[0]).getByRole('button', { name: 'Edit this queued message' }))
    await waitFor(() => expect(screen.getByLabelText('Message')).toHaveValue('Written first\nOriginal words'))
    expect(order).toEqual([`image:${image.id}`, 'cancel'])
    expect(await screen.findByRole('list', { name: 'Images to send' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Remove image 1' })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Queued messages' })).toBeNull()
  })

  it('stop: when busy with an empty input the send button becomes stop and stops only this reply; a light one-line notice when it has already ended', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const bodies: { runId: string; body: Schemas['AgentQueueActionInput'] }[] = []
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.post(apiUrl('/agents/:agentId/runs/:runId/stop'), async ({ request, params }) => {
          const body = (await request.json()) as Schemas['AgentQueueActionInput']
          bodies.push({ runId: String(params.runId), body })
          if (bodies.length > 1) return HttpResponse.json(errorBody('run_not_active', 'The run has already ended', { details: { runId: String(params.runId), status: 'completed' } }), { status: 409 })
          return HttpResponse.json(receipt(String(params.agentId), body.actionId, 'stop_run', null, [], { ...current, status: 'aborting' }))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }))
    await user.click(screen.getByRole('button', { name: 'Stop the current reply' }))
    await waitFor(() => expect(bodies).toEqual([{ runId: current.id, body: { actionId: expect.stringMatching(/^[\da-f-]{36}$/) } }]))
    // With text in the input, the button queues the message.
    await user.type(screen.getByLabelText('Message'), 'New message')
    expect(screen.queryByRole('button', { name: 'Stop the current reply' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Queue' })).toBeEnabled()
    await user.clear(screen.getByLabelText('Message'))
    await user.click(screen.getByRole('button', { name: 'Stop the current reply' }))
    expect(await screen.findByText('This reply has already ended')).toHaveAttribute('role', 'status')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('conversation flow: a reply stopped by a person is marked stopped; turns voided while queued are not shown', async () => {
    const stopped = makeRun({ status: 'interrupted', endReason: 'user_stopped', endedAt: '2026-10-12T08:01:00Z' })
    const merged = makeRun({ status: 'interrupted', endReason: 'merged_into_interrupt', startedAt: null, endedAt: '2026-10-12T08:01:00Z' })
    const { agent, emit } = await renderChat({
      messages: [
        makeMessage({ text: 'Run the tests', run: stopped }),
        makeMessage({ role: 'assistant', text: 'Running them', run: stopped }),
        makeMessage({ origin: 'inbox_reminder', text: 'Reminder merged into an interrupt', run: merged }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, {}))
    expect(within(log()).getByText('Stopped')).toBeInTheDocument()
    expect(within(log()).queryByText('Reminder merged into an interrupt')).toBeNull()
    expect(within(log()).getAllByRole('article')).toHaveLength(1)
  })
})
