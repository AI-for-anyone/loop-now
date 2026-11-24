import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, context, defaultAgentPath, errorBody, makeAgent, makeMessage, makeMessagePage, makeQueueItem, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'
import { claudeCodeList } from '@/test/commands'
import { FakeEventSource } from '@/test/fake-event-source'

function turn(text: string, reply: string, overrides: Partial<Schemas['Run']> = {}, epoch = 1) {
  const run = makeRun(overrides)
  return [makeMessage({ text, run, sessionEpoch: epoch }), makeMessage({ role: 'assistant', text: reply, run, sessionEpoch: epoch })]
}

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']>, currentText: string | null = null, queuedRuns: Schemas['Run'][] = [], queueItems: Schemas['QueueItem'][] = []) {
  return { status: makeStatus(agentId, status), currentText, queuedRuns, queueItems }
}

function acceptSend(sent: Schemas['SendMessageInput'][], runOverrides: Partial<Schemas['Run']> = {}) {
  return http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
    const body = (await request.json()) as Schemas['SendMessageInput']
    sent.push(body)
    const run = makeRun({ status: body.whenBusy ? 'queued' : 'starting', whenBusy: body.whenBusy ?? null, ...runOverrides })
    return HttpResponse.json({ message: makeMessage({ text: body.text, run }), run }, { status: 202 })
  })
}

// Send requests wait at a gate until the test releases them; when fail is true, a released
// request returns 500. After each release a new gate is set up for the next request.
function gatedSend(sent: Schemas['SendMessageInput'][], { fail = false } = {}) {
  let open = () => {}
  let gate = Promise.resolve()
  const close = () => {
    gate = new Promise<void>((resolve) => (open = resolve))
  }
  close()
  const handler = http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
    const body = (await request.json()) as Schemas['SendMessageInput']
    sent.push(body)
    await gate
    if (fail) return HttpResponse.json(errorBody('internal_error', 'Something went wrong'), { status: 500 })
    const run = makeRun({ status: body.whenBusy ? 'queued' : 'starting', whenBusy: body.whenBusy ?? null, endedAt: null })
    return HttpResponse.json({ message: makeMessage({ text: body.text, run }), run }, { status: 202 })
  })
  return {
    handler,
    release: () => {
      open()
      close()
    },
  }
}

describe('Chat history and continuing the conversation', () => {
  it('loads earlier messages when scrolling up, with nothing duplicated or missing', async () => {
    const older = turn('Question 1', 'Answer 1')
    const newer = turn('Question 2', 'Answer 2')
    let calls = 0
    const { user } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), ({ request }) => {
          calls += 1
          const before = new URL(request.url).searchParams.get('before')
          return before === newer[0].id
            ? HttpResponse.json(makeMessagePage(older, { turnsTotal: 2 }))
            : HttpResponse.json(makeMessagePage(newer, { hasMore: true, turnsBefore: 1, turnsTotal: 2 }))
        }),
      ],
    })
    expect(await screen.findByText('Question 2')).toBeInTheDocument()
    fireEvent.scroll(screen.getByRole('log'))
    expect(await screen.findByText('Question 1')).toBeInTheDocument()
    const log = screen.getByRole('log')
    const texts = within(log).getAllByText(/^(Question|Answer) .$/).map((el) => el.textContent)
    expect(texts).toEqual(['Question 1', 'Answer 1', 'Question 2', 'Answer 2'])
    expect(calls).toBe(2)
    expect(screen.queryByRole('button', { name: 'Load earlier messages' })).toBeNull()
    void user
  })

  it('history is still there after a reload and sending still works', async () => {
    const history = [...turn('Hello', 'Hi there'), ...turn('Bye', 'See you')]
    const first = await renderChat({ messages: history })
    expect(await screen.findByText('See you')).toBeInTheDocument()
    first.unmount()
    const sent: Schemas['SendMessageInput'][] = []
    const { user } = await renderChat({ agent: first.agent, messages: history, handlers: [acceptSend(sent)] })
    expect(await screen.findByText('See you')).toBeInTheDocument()
    await user.type(screen.getByLabelText('Message'), 'Third message')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(sent).toEqual([{ text: 'Third message' }]))
    expect(screen.getByLabelText('Message')).toHaveValue('')
  })

  it('shows a divider between the messages before and after a new session', async () => {
    await renderChat({ messages: [...turn('Old session', 'Old reply', {}, 1), ...turn('New session', 'New reply', {}, 2)] })
    expect(await screen.findByRole('separator')).toHaveTextContent('Session switched; page history is kept')
  })
})

describe('Sending messages', () => {
  it('does not send on Enter during IME composition', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    await renderChat({ handlers: [acceptSend(sent)] })
    const input = screen.getByLabelText('Message')
    fireEvent.change(input, { target: { value: 'nihao' } })
    fireEvent.keyDown(input, { key: 'Enter', keyCode: 229, isComposing: true })
    await new Promise((r) => setTimeout(r, 50))
    expect(sent).toEqual([])
    fireEvent.keyDown(input, { key: 'Enter' })
    await waitFor(() => expect(sent).toEqual([{ text: 'nihao' }]))
  })

  it('Claude Code shows only the general platform commands; a Pi Agent also supports the /pi: menu', async () => {
    // The server's command list decides which commands are available: on a
    // Claude Code Agent, /compact, /export and /resume are marked unavailable.
    const agent = makeAgent({ model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code' })
    const claude = await renderChat({ agent, handlers: [http.get(apiUrl(`/agents/${agent.id}/commands`), () => HttpResponse.json(claudeCodeList()))] })
    await claude.user.type(screen.getByLabelText('Message'), '/')
    await waitFor(() => expect(screen.getAllByRole('option')).toHaveLength(4))
    expect(screen.queryByText('The command list appears after the Agent has run once.')).toBeNull()
    expect(screen.getByLabelText('Message')).toHaveAttribute('aria-controls')
    expect(screen.queryByRole('option', { name: /\/compact/ })).toBeNull()
    expect(screen.getByRole('group', { name: 'Not available now' })).toHaveTextContent('/compact [instructions]')
    claude.unmount()
    const pi = await renderChat()
    await pi.user.type(screen.getByLabelText('Message'), '/pi:')
    expect(await screen.findByText('The command list appears after the Agent has run once.')).toBeInTheDocument()
  })

  it('does not send an empty message', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const { user } = await renderChat({ handlers: [acceptSend(sent)] })
    await user.type(screen.getByLabelText('Message'), '   ')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    expect(await screen.findByText('Message can\'t be empty')).toBeInTheDocument()
    expect(sent).toEqual([])
  })

  it('after a successful send, removes the sent part and keeps only the text typed while sending', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const send = gatedSend(sent)
    const { user } = await renderChat({ handlers: [send.handler] })
    const input = screen.getByLabelText('Message')
    await user.type(input, 'First message')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(sent).toEqual([{ text: 'First message' }]))
    await user.type(input, ', then a second one typed after it')
    send.release()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled())
    expect(input).toHaveValue(', then a second one typed after it')
  })

  it('when the sent part was changed while sending, keeps all the content after a successful send', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const send = gatedSend(sent)
    const { user } = await renderChat({ handlers: [send.handler] })
    const input = screen.getByLabelText('Message')
    await user.type(input, 'First message')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(sent).toEqual([{ text: 'First message' }]))
    await user.clear(input)
    await user.type(input, 'Message 1, edited')
    send.release()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled())
    expect(input).toHaveValue('Message 1, edited')
  })

  it('leaves the input as it is when sending fails', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const send = gatedSend(sent, { fail: true })
    const { user } = await renderChat({ handlers: [send.handler] })
    const input = screen.getByLabelText('Message')
    await user.type(input, 'First message')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(sent).toEqual([{ text: 'First message' }]))
    await user.type(input, ', continued')
    send.release()
    expect(await screen.findByText(/Internal server error/)).toBeInTheDocument()
    expect(input).toHaveValue('First message, continued')
  })

  it('queueing while busy also keeps only the text typed while sending', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const send = gatedSend(sent)
    const { agent, user, emit } = await renderChat({ handlers: [send.handler] })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: makeRun({ status: 'running' }) }, ''))
    const input = screen.getByLabelText('Message')
    await user.type(input, 'Queued words')
    await user.click(screen.getByRole('button', { name: 'Queue' }))
    await waitFor(() => expect(sent).toEqual([{ text: 'Queued words', whenBusy: 'queue' }]))
    await user.type(input, ', and one more line')
    send.release()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Queue' })).toBeEnabled())
    expect(input).toHaveValue(', and one more line')
  })

  it('on a network error shows that the server cannot be reached and keeps the content', async () => {
    const { user } = await renderChat({ handlers: [http.post(apiUrl('/agents/:agentId/messages'), () => HttpResponse.error())] })
    await user.type(screen.getByLabelText('Message'), 'Message sent while offline')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    // English UI issue: the global connection banner title (t.connection.unreachableTitle) and the per-request error
    // (t.errors.unreachable) are both "Can't reach the server" in English, though the copy is meant to tell them apart; the selector
    // picks the per-request error.
    expect(await screen.findByText('Can\'t reach the server', { selector: '[data-slot="alert-description"]' })).toBeInTheDocument()
    expect(screen.getByLabelText('Message')).toHaveValue('Message sent while offline')
  })

  it('when the server rejects an overlong message, shows the reason and keeps the content', async () => {
    const { user } = await renderChat({
      handlers: [http.post(apiUrl('/agents/:agentId/messages'), () => HttpResponse.json(errorBody('payload_too_large', 'The message exceeds 64 KiB'), { status: 413 }))],
    })
    await user.type(screen.getByLabelText('Message'), 'A very long message')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    expect(await screen.findByText(/Too large/)).toBeInTheDocument()
    expect(screen.getByLabelText('Message')).toHaveValue('A very long message')
  })
})

describe('Streaming, tool state and switching', () => {
  it('appends text deltas right away, and the final text wins at the end', async () => {
    const { agent, emit } = await renderChat()
    const run = makeRun({ status: 'running' })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, ''))
    await emit('message.created', { message: makeMessage({ text: 'Question', run }) })
    await emit('run.text', { runId: run.id, delta: 'Dropped' })
    expect(await screen.findByText('Dropped')).toBeInTheDocument()
    await emit('run.text', { runId: run.id, delta: ' text' })
    expect(await screen.findByText('Dropped text')).toBeInTheDocument()
    const ended = { ...run, status: 'completed' as const }
    await emit('run.ended', { run: ended, message: makeMessage({ role: 'assistant', text: 'Final reply', run: ended }) })
    expect(await screen.findByText('Final reply')).toBeInTheDocument()
    expect(screen.queryByText('Dropped text')).toBeNull()
  })

  it('tool calls show only their name and state', async () => {
    const { agent, emit } = await renderChat()
    const run = makeRun({ status: 'running' })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, ''))
    await emit('run.tool', { runId: run.id, toolCallId: 't1', toolName: 'bash', state: 'started', at: '2026-10-12T00:00:00Z' })
    const tools = await screen.findByRole('list', { name: 'Tool calls' })
    expect(tools).toHaveTextContent('bashRunning')
    await emit('run.tool', { runId: run.id, toolCallId: 't1', toolName: 'bash', state: 'succeeded', at: '2026-10-12T00:00:01Z' })
    await waitFor(() => expect(tools).toHaveTextContent('bashSucceeded'))
  })

  it('after switching Agents, no longer receives the events of the previous Agent', async () => {
    const other = makeAgent({ name: 'Other Agent' })
    const { router, source } = await renderChat({
      handlers: [
        http.get(apiUrl(`/agents/${other.id}`), () => HttpResponse.json(other)),
        http.get(apiUrl(`/agents/${other.id}/status`), () => HttpResponse.json(makeStatus(other.id))),
        http.get(apiUrl(`/agents/${other.id}/messages`), () => HttpResponse.json(makeMessagePage(turn('Question for B', 'Answer from B')))),
      ],
    })
    await router.navigate(defaultAgentPath(other.id, 'chat'))
    expect(await screen.findByText('Question for B')).toBeInTheDocument()
    expect(source.readyState).toBe(FakeEventSource.CLOSED)
    expect(FakeEventSource.latest().url).toContain(`/agents/${other.id}/events`)
  })
})

describe('Sending while busy queues directly', () => {
  it('clicking send while busy queues directly, after the messages already queued', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const current = makeRun({ status: 'running' })
    const existing = makeRun({ status: 'queued', whenBusy: 'queue' })
    const { agent, user, emit } = await renderChat({
      messages: [makeMessage({ text: 'Working on this', run: current }), makeMessage({ text: 'Queued earlier', run: existing })],
      handlers: [acceptSend(sent)],
    })
    const existingItem = makeQueueItem({ runId: existing.id, text: 'Queued earlier' })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current, queued: { interrupt: 0, normal: 1 } }, '', [existing], [existingItem]))
    // With an empty input the button stops the current reply; with text in the input it queues the message.
    expect(screen.getByRole('button', { name: 'Stop the current reply' })).toBeEnabled()
    await user.type(screen.getByLabelText('Message'), 'Queued now')
    await user.click(screen.getByRole('button', { name: 'Queue' }))
    await waitFor(() => expect(sent).toEqual([{ text: 'Queued now', whenBusy: 'queue' }]))
    expect(screen.queryByRole('dialog')).toBeNull()
    // Queued messages appear in the queue tray above the input (the list
    // pushed by the server is authoritative), not in the conversation stream.
    await emit('queue.updated', { queuedRuns: [existing], items: [existingItem, makeQueueItem({ text: 'Queued now' })] })
    const log = screen.getByRole('log')
    const queue = screen.getByRole('region', { name: 'Queued messages' })
    await waitFor(() => {
      const order = within(queue).getAllByText(/^(Queued earlier|Queued now)$/).map((el) => el.textContent)
      expect(order).toEqual(['Queued earlier', 'Queued now'])
    })
    expect(within(log).getByText('Working on this')).toBeInTheDocument()
    expect(within(log).queryByText(/^(Queued earlier|Queued now)$/)).toBeNull()
  })

  it('pressing Enter while busy queues directly without asking', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const current = makeRun({ status: 'running' })
    const { agent, user, emit } = await renderChat({ handlers: [acceptSend(sent)] })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, ''))
    await user.type(screen.getByLabelText('Message'), 'Queued with Enter{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: 'Queued with Enter', whenBusy: 'queue' }]))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByLabelText('Message')).toHaveValue('')
  })

  it('finding the Agent busy only when sending: does not ask, sends again as queued and the message goes into the queue', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
          const body = (await request.json()) as Schemas['SendMessageInput']
          sent.push(body)
          if (!body.whenBusy) {
            return HttpResponse.json(errorBody('agent_busy', 'busy', { details: { currentRunId: 'r1', queued: { interrupt: 1, normal: 2 } } }), { status: 409 })
          }
          const run = makeRun({ status: 'queued', whenBusy: body.whenBusy })
          return HttpResponse.json({ message: makeMessage({ text: body.text, run }), run }, { status: 202 })
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, {}))
    await user.type(screen.getByLabelText('Message'), 'Late message')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(sent).toEqual([{ text: 'Late message' }, { text: 'Late message', whenBusy: 'queue' }]))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    await emit('queue.updated', { queuedRuns: [], items: [makeQueueItem({ text: 'Late message' })] })
    expect(await within(screen.getByRole('region', { name: 'Queued messages' })).findByText('Late message')).toBeInTheDocument()
    expect(within(screen.getByRole('log')).queryByText('Late message')).toBeNull()
    expect(screen.getByLabelText('Message')).toHaveValue('')
  })
})

describe('Queue display and reply state', () => {
  it('lists interrupt messages before normal queued messages and marks them', async () => {
    const current = makeRun({ status: 'running' })
    const n1 = makeRun({ status: 'queued', whenBusy: 'queue' })
    const n2 = makeRun({ status: 'queued', whenBusy: 'queue' })
    const i1 = makeRun({ status: 'queued', whenBusy: 'interrupt' })
    const { agent, emit } = await renderChat({
      messages: [makeMessage({ text: 'Current', run: current }), makeMessage({ text: 'Normal one', run: n1 }), makeMessage({ text: 'Normal two', run: n2 }), makeMessage({ text: 'Cutting in', run: i1 })],
    })
    const items = [makeQueueItem({ runId: i1.id, text: 'Cutting in', whenBusy: 'interrupt' }), makeQueueItem({ runId: n1.id, text: 'Normal one' }), makeQueueItem({ runId: n2.id, text: 'Normal two' })]
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, '', [i1, n1, n2], items))
    const log = screen.getByRole('log')
    const queue = screen.getByRole('region', { name: 'Queued messages' })
    await waitFor(() => {
      const order = within(queue).getAllByText(/^(Normal one|Normal two|Cutting in)$/).map((el) => el.textContent)
      expect(order).toEqual(['Cutting in', 'Normal one', 'Normal two'])
    })
    expect(within(queue).getAllByText('Up next')).toHaveLength(1)
    expect(within(queue).getAllByRole('button', { name: 'Interrupt' })).toHaveLength(2)
    expect(within(queue).getByRole('heading', { name: 'Queued · 3' })).toBeInTheDocument()
    expect(within(log).getByText('Current')).toBeInTheDocument()
    expect(within(log).queryByText(/^(Normal one|Normal two|Cutting in)$/)).toBeNull()
  })

  it('shows cut off by a server restart, the failure reason and waiting for a run slot', async () => {
    const restarted = turn('Question one', 'Halfway through', { status: 'interrupted', endReason: 'server_restart' })
    const failed = turn('Question two', '', { status: 'failed', errorCode: 'credential_unavailable', errorMessage: 'Cannot decrypt the gh credential' })
    const interrupted = turn('Question three', 'Cut short', { status: 'interrupted', endReason: 'user_interrupt' })
    const waiting = makeRun({ status: 'starting', phase: 'waiting_slot' })
    const { agent, emit } = await renderChat({ messages: [...restarted, ...failed, ...interrupted, makeMessage({ text: 'Question four', run: waiting })] })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: waiting }, ''))
    expect(await screen.findByText('Cut off by a server restart')).toBeInTheDocument()
    expect(screen.getByText('Halfway through')).toBeInTheDocument()
    expect(screen.getByText('Failed: Credential unavailable (Cannot decrypt the gh credential)')).toBeInTheDocument()
    expect(screen.getByText('Interrupted')).toBeInTheDocument()
    expect(screen.getByText('Cut short')).toBeInTheDocument()
    // The header also shows the current phase; here we only look at the turn in the conversation.
    expect(await within(screen.getByRole('log')).findByText('Waiting for a run slot')).toBeInTheDocument()
  })

  it('shows Interrupting while interrupting, then Interrupted after the end, and keeps the text', async () => {
    const run = makeRun({ status: 'running' })
    const { agent, emit } = await renderChat({ messages: [makeMessage({ text: 'Long task', run })] })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, 'Half done'))
    await emit('run.updated', { run: { ...run, status: 'aborting' } })
    expect(await within(screen.getByRole('log')).findByText('Interrupting')).toBeInTheDocument()
    const ended = { ...run, status: 'interrupted' as const, endReason: 'user_interrupt' as const }
    await emit('run.ended', { run: ended, message: makeMessage({ role: 'assistant', text: 'Half done', run: ended }) })
    expect(await screen.findByText('Interrupted')).toBeInTheDocument()
    expect(screen.getByText('Half done')).toBeInTheDocument()
  })
})

it('after confirming a resume from the list, restores the text draft and neither uploads nor clears existing images', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  const { user } = await renderChat({ handlers: [
    http.get(apiUrl('/context'), () => HttpResponse.json({ ...context, limits: { ...context.limits, maxMessageImages: 4, maxMessageImageBytes: 5 * 1024 * 1024 } })),
    http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
      const body = await request.json() as Schemas['SendMessageInput']
      sent.push(body)
      return HttpResponse.json(body.text === '/resume'
        ? { handled: true, source: 'platform', plugin: '', reply: null, command: 'resume', resumeSessions: [
          { id: 'target-full-session-id', title: 'Topic from last time', preview: 'Last user message', updatedAt: '2026-10-31T01:00:00Z', current: false },
          { id: 'current-full-session-id', title: 'Current topic', preview: 'Current topic', updatedAt: '2026-10-30T01:00:00Z', current: true },
        ] }
        : { handled: true, source: 'platform', plugin: '', reply: null, command: 'resume', resumedSession: { state: 'active', epoch: 2 } })
    }),
  ] })
  const input = await screen.findByLabelText('Message')
  await user.type(input, '/resume')
  await user.click(screen.getByRole('button', { name: 'Send' }))
  expect(await screen.findByText('Topic from last time')).toBeInTheDocument()
  expect(screen.getByText('Last user message')).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: 'Resume' })).toHaveLength(1)

  await user.type(input, 'Draft not sent yet')
  await user.upload(screen.getByLabelText('Choose an image file'), new File(['synthetic-png'], 'kept.png', { type: 'image/png' }))
  expect(await screen.findByAltText('kept.png')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Resume' }))
  expect(await screen.findByRole('alertdialog')).toHaveTextContent('The workspace, model configuration, Prompt, Skills, and permissions are not rolled back')
  expect(input).toHaveValue('/resume target-full-session-id')
  expect(sent).toEqual([{ text: '/resume' }])
  await user.click(screen.getByRole('button', { name: 'Resume session' }))
  await waitFor(() => expect(sent).toEqual([{ text: '/resume' }, { text: '/resume target-full-session-id' }]))
  expect(screen.getByAltText('kept.png')).toBeInTheDocument()
  expect(input).toHaveValue('Draft not sent yet')
})

it('after cancelling a resume from the list, restores the text draft and sends no resume request', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  const { user } = await renderChat({ handlers: [http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']
    sent.push(body)
    return HttpResponse.json({ handled: true, source: 'platform', plugin: '', reply: null, command: 'resume', resumeSessions: [
      { id: 'target-full-session-id', title: 'Topic from last time', preview: 'Last user message', updatedAt: '2026-10-31T01:00:00Z', current: false },
    ] })
  })] })
  const input = await screen.findByLabelText('Message')
  await user.type(input, '/resume')
  await user.click(screen.getByRole('button', { name: 'Send' }))
  expect(await screen.findByText('Topic from last time')).toBeInTheDocument()
  await user.type(input, 'Draft to keep after cancelling')
  await user.click(screen.getByRole('button', { name: 'Resume' }))
  expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
  expect(input).toHaveValue('/resume target-full-session-id')
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(input).toHaveValue('Draft to keep after cancelling')
  expect(sent).toEqual([{ text: '/resume' }])
})

it('a final sleep state only reloads the original history and makes up no message; without message.created the original timer source still shows', async () => {
  const agent = makeAgent()
  const sleepId = '00000000-0000-7000-8000-000000005001'
  const run = makeRun({ wakeSleepId: sleepId })
  const timer = makeMessage({ origin: 'sleep_wake', text: 'Original timer reminder from the server', run })
  let messages: Schemas['Message'][] = [], calls = 0
  const { emit } = await renderChat({ agent, handlers: [http.get(apiUrl('/agents/:agentId/messages'), () => {
    calls++
    return HttpResponse.json(makeMessagePage(messages))
  })] })
  await emit('snapshot', snapshot(agent.id, {}))
  const before = calls
  messages = [timer]
  await emit('sleep.updated', { scopeKind: 'normal', agentId: agent.id, taskId: null, runtimeId: null, nodeRunId: null, taskVersion: null, sleepRevision: 3, sleep: null, endedSleepId: sleepId })
  await waitFor(() => expect(calls).toBeGreaterThan(before))
  expect(await screen.findByRole('note')).toHaveTextContent('Woke up on schedule')
  expect(screen.getByRole('note')).not.toHaveTextContent('Original timer reminder from the server')
  expect(screen.getByRole('note')).toHaveAttribute('title', `Original timer reminder from the server\nSleep ${sleepId}`)
})
