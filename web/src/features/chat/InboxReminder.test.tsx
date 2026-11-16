import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import { apiUrl, errorBody, makeAgent, makeMessage, makeRun, makeStatus } from '@/test/api'
import { makeInboxMessage, consoleParty, externalParty, makeIdentity, agentParty } from '@/test/inbox'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { keys } from '@/api/queries'
import { InboxReminder, InboxReminderLine } from './InboxReminder'
import { renderChat } from '@/test/chat'

const at = '2026-10-22T12:00:00Z'
it('renders a single plain-text, whitespace-normalized and bounded preview with the real sender', () => {
  const preview = `Start\n\t <img src=x> ${'🟦'.repeat(120)}`
  const { container } = render(<InboxReminderLine at={at} sender={consoleParty} preview={preview} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent('From Console')
  expect(line).toHaveTextContent('Start <img src=x>')
  expect(line.lastElementChild?.textContent).toMatch(/…$/u)
  expect(Array.from(line.lastElementChild?.textContent ?? '')).toHaveLength(101)
  expect(container.querySelector('img')).toBeNull()
  expect(line.className).not.toMatch(/riso-tilt|bg-tint-yellow/)
  expect(container.querySelector('button')).toBeNull()
})

it('marks an Inbox message that woke a sleep early, with the sleep ID only in the tooltip', () => {
  render(<InboxReminderLine at={at} sender={consoleParty} preview="New task" wakeSleepId="00000000-0000-7000-8000-000000005003" />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(/Woke earlyFrom ConsoleNew task$/u)
  expect(line).not.toHaveTextContent('00000000')
  expect(line.querySelector('[data-early-wake]')).toHaveAttribute('title', 'Sleep 00000000-0000-7000-8000-000000005003')
})

it('reads the exact historical message once across rerenders, without unread lookup or writes', async () => {
  const agent = makeAgent(), sender = externalParty(makeIdentity({ name: 'original-sender' }))
  const message = makeInboxMessage({ from: sender, to: agentParty(agent), text: 'Full original message', preview: 'Original summary' })
  const requests: string[] = []
  server.use(http.all(apiUrl('/*'), ({ request }) => {
    requests.push(`${request.method} ${new URL(request.url).pathname}`)
    if (request.method === 'GET' && new URL(request.url).pathname.endsWith(`/${message.id}`)) return HttpResponse.json(message)
    return HttpResponse.json(errorBody('not_found'), { status: 404 })
  }))
  const view = renderWithQuery(<InboxReminder agentId={agent.id} messageId={message.id} at={at} />)
  expect(screen.getByRole('note')).toHaveTextContent('Received an Inbox message')
  expect(await screen.findByText('From original-sender')).toBeInTheDocument()
  expect(screen.getByRole('note')).toHaveTextContent('Original summary')
  view.rerender(<InboxReminder agentId={agent.id} messageId={message.id} at={at} />)
  expect(requests).toEqual([`GET /api/v1/agents/${agent.id}/inbox/${message.id}`])
})

it('has a neutral fallback without a trigger ID and makes no Inbox request', () => {
  renderWithQuery(<InboxReminder agentId="agent-without-trigger" at={at} />)
  expect(screen.getByRole('note')).toHaveTextContent('Received an Inbox message')
  expect(screen.getByRole('note')).not.toHaveTextContent('From')
})

it('keeps the fallback for a missing original, without guessing another sender', async () => {
  const agent = makeAgent()
  server.use(http.get(apiUrl(`/agents/${agent.id}/inbox/missing`), () => HttpResponse.json(errorBody('not_found'), { status: 404 })))
  const view = renderWithQuery(<InboxReminder agentId={agent.id} messageId="missing" at={at} />)
  await waitFor(() => expect(view.client.getQueryState(keys.agentInboxMessage(agent.id, 'missing'))?.status).toBe('error'))
  expect(screen.getByRole('note')).toHaveTextContent('Received an Inbox message')
  expect(screen.getByRole('note')).not.toHaveTextContent('From')
})

it('normal chat uses its run trigger and does not refetch the summary for streamed text', async () => {
  const agent = makeAgent(), sender = agentParty(makeAgent({ name: 'Real sender Agent' }))
  const original = makeInboxMessage({ from: sender, to: agentParty(agent), text: 'Actual message', preview: 'Exact summary of the original message' })
  const run = makeRun({ triggerInboxMessageId: original.id, status: 'running', endedAt: null })
  let gets = 0, unreadGets = 0, posts = 0
  const { emit } = await renderChat({ agent, messages: [makeMessage({ origin: 'inbox_reminder', text: 'Instructions not to show: loopctl inbox list --unread', run: { ...run, triggerInboxMessageId: undefined } })], handlers: [
    http.get(apiUrl(`/agents/${agent.id}/inbox/${original.id}`), () => { gets++; return HttpResponse.json(original) }),
    http.get(apiUrl(`/agents/${agent.id}/inbox`), () => { unreadGets++; return HttpResponse.json({ messages: [], nextCursor: null }) }),
    http.post(apiUrl('/*'), () => { posts++; return new HttpResponse(null, { status: 500 }) }),
  ] })
  expect(screen.getByRole('note')).toHaveTextContent('Received an Inbox message')
  await emit('snapshot', { status: makeStatus(agent.id, { busy: true, currentRun: run }), currentText: '', queuedRuns: [], queueItems: [] })
  expect(await screen.findByText('From Real sender Agent')).toBeInTheDocument()
  await emit('run.text', { runId: run.id, delta: 'Streamed part one' })
  await emit('run.text', { runId: run.id, delta: ' and part two' })
  expect(await screen.findByText('Streamed part one and part two')).toBeInTheDocument()
  expect(screen.getByRole('note')).toHaveTextContent('Exact summary of the original message')
  expect(screen.getByRole('note')).not.toHaveTextContent('loopctl')
  expect({ gets, unreadGets, posts }).toEqual({ gets: 1, unreadGets: 0, posts: 0 })
})

it('keeps the deleted sender label and its original parentheses', () => {
  render(<InboxReminderLine at={at} sender={externalParty(makeIdentity({ name: 'original-sender' }), true)} preview="Original summary" />)
  expect(screen.getByRole('note')).toHaveTextContent('From original-sender (Deleted identity)')
})
