import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultAgentsPath, defaultProject, makeAgent, makeMessage, makeRun } from '@/test/api'
import { renderChat } from '@/test/chat'
import { agentParty, consoleParty, externalParty, fakeInbox, makeIdentity, makeInboxMessage, pendingOf, summaryOf } from '@/test/inbox'
import { FakeIntersectionObserver } from '@/test/intersection-observer'
import { makeAgentOverview, renderOverview } from '@/test/overview'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { makeTaskDetail, taskId, nodeId } from '@/test/tasks'
import { InboxRedirect } from './InboxRedirect'

type Message = Schemas['InboxMessage']
const open = (options: string[]): Message['question'] => ({ options: options.map((label, i) => ({ id: String(i + 1), label })), expiresAt: '2099-01-01T18:00:00Z', state: 'open', answer: null })

function chatSetup() {
  const agent = makeAgent({ name: '开发助手' })
  const run = makeRun({ queuedAt: '2026-10-12T08:00:00Z', startedAt: '2026-10-12T08:00:00Z', endedAt: '2026-10-12T08:00:30Z' })
  const turn = [makeMessage({ text: 'Change the write order first', run, createdAt: '2026-10-12T08:00:00Z' }), makeMessage({ role: 'assistant', text: 'Done, it is changed', run, createdAt: '2026-10-12T08:00:01Z' })]
  const note = makeInboxMessage({ from: agentParty(agent), to: consoleParty, text: 'server-main is merged, please confirm the expected values', whenBusy: null })
  const question = makeInboxMessage({ from: agentParty(agent), to: consoleParty, text: 'Should the expected values follow the new v3 table or the old one?', whenBusy: null, question: open(['Use the new v3 table', 'Keep the old table']) })
  const mine = makeInboxMessage({ from: consoleParty, to: agentParty(agent), text: 'Messages you send it are not repeated here' })
  const inbox = fakeInbox({ messages: [note, question, mine] })
  return { agent, turn, note, question, inbox }
}

const consoleItem = (message: Message) => {
  const el = document.querySelector<HTMLElement>(`[data-console-message-id="${message.id}"]`)
  if (!el) throw new Error('message not rendered yet')
  return el
}

describe('Messages for you in the Agent chat', () => {
  it('sorted into the chat by time, the name row has “For you” and an unread dot; messages you sent it are not repeated; the top bar has no inbox', async () => {
    const { agent, turn, note, question, inbox } = chatSetup()
    await renderChat({ agent, messages: turn, handlers: inbox.handlers })
    const item = await waitFor(() => consoleItem(note))
    expect(within(item).getByText('For you')).toBeVisible()
    expect(within(item).getByText('Unread')).toBeInTheDocument()
    expect(within(item).getByText('server-main is merged, please confirm the expected values')).toBeVisible()
    expect(consoleItem(question)).toBeTruthy()
    expect(screen.queryByText('Messages you send it are not repeated here')).toBeNull()
    // Placed after the turn that started earlier.
    const log = screen.getByRole('log')
    const order = [...log.querySelectorAll('[data-run-id], [data-console-message-id]')].map((el) => el.getAttribute('data-console-message-id') ?? 'turn')
    expect(order).toEqual(['turn', note.id, question.id])
    expect(within(screen.getByRole('navigation', { name: 'Main navigation' })).queryByRole('link', { name: /Inbox/ })).toBeNull()
  })

  it('marked read when scrolled into view, only once, and the dot goes away', async () => {
    const { agent, turn, note, inbox } = chatSetup()
    await renderChat({ agent, messages: turn, handlers: inbox.handlers })
    const item = await waitFor(() => consoleItem(note))
    act(() => FakeIntersectionObserver.trigger(item, true))
    act(() => FakeIntersectionObserver.trigger(item, true))
    await waitFor(() => expect(within(consoleItem(note)).queryByText('Unread')).toBeNull())
    expect(inbox.readMessages).toEqual([note.id])
  })

  it('clicking an option submits it to the original question (queued, without text); after answering, the chosen option is highlighted and the others fade', async () => {
    const { agent, turn, question, inbox } = chatSetup()
    const { user } = await renderChat({ agent, messages: turn, handlers: inbox.handlers })
    const item = await waitFor(() => consoleItem(question))
    expect(within(item).getByText(/Question · Open until .+/)).toBeVisible()
    await user.click(within(item).getByRole('button', { name: 'Keep the old table' }))
    await waitFor(() => expect(inbox.sent).toEqual([{ optionId: '2', whenBusy: 'queue', replyTo: question.id }]))
    await waitFor(() => expect(within(consoleItem(question)).getByRole('button', { name: 'Keep the old table' })).toHaveAttribute('aria-pressed', 'true'))
    expect(within(consoleItem(question)).getByRole('button', { name: 'Use the new v3 table' })).toBeDisabled()
    expect(within(consoleItem(question)).getByText(/You chose "Keep the old table" at .+/)).toBeVisible()
  })

  it('a late option (the question is already closed) is submitted only once, the buttons fade, and it does not turn into a plain message', async () => {
    const { agent, turn, question, inbox } = chatSetup()
    let calls = 0
    const closed = http.post(apiUrl(`/inbox/messages/${question.id}/reply`), () => {
      calls++
      return HttpResponse.json({ error: { code: 'question_closed', message: 'The question is closed', details: { question: { ...question.question!, state: 'closed' } } } }, { status: 409 })
    })
    const { user } = await renderChat({ agent, messages: turn, handlers: [closed, ...inbox.handlers] })
    const item = await waitFor(() => consoleItem(question))
    await user.click(within(item).getByRole('button', { name: 'Use the new v3 table' }))
    expect(await within(consoleItem(question)).findByText('Closed')).toBeVisible()
    expect(within(consoleItem(question)).getByRole('button', { name: 'Keep the old table' })).toBeDisabled()
    expect(within(consoleItem(question)).queryByRole('button', { name: 'Reply to this question' })).toBeNull()
    expect(calls).toBe(1); expect(inbox.sent).toEqual([])
  })

  it('“Reply to this question” puts the input in reply mode: the text is sent as the answer to the original question, not as a plain chat message; cancelling returns to normal sending', async () => {
    const { agent, turn, question, inbox } = chatSetup()
    let chatPosts = 0
    const { user } = await renderChat({ agent, messages: turn, handlers: [...inbox.handlers, http.post(apiUrl(`/agents/${agent.id}/messages`), () => { chatPosts++; return HttpResponse.json({}, { status: 500 }) })] })
    const item = await waitFor(() => consoleItem(question))
    await user.click(within(item).getByRole('button', { name: 'Reply to this question' }))
    expect(screen.getByText('Replying to 开发助手\'s question')).toBeVisible()
    const editor = screen.getByRole('textbox', { name: 'Message' })
    expect(editor).toHaveFocus()
    expect(editor).toHaveAttribute('placeholder', 'Type your answer to this question…')
    await user.click(screen.getByRole('button', { name: 'Cancel replying to this question' }))
    expect(screen.queryByText('Replying to 开发助手\'s question')).toBeNull()
    await user.click(within(consoleItem(question)).getByRole('button', { name: 'Reply to this question' }))
    await user.type(screen.getByRole('textbox', { name: 'Message' }), 'Use the old table, leave pricing alone for now{Enter}')
    await waitFor(() => expect(inbox.sent).toEqual([{ text: 'Use the old table, leave pricing alone for now', whenBusy: 'queue', replyTo: question.id }]))
    await waitFor(() => expect(screen.queryByText('Replying to 开发助手\'s question')).toBeNull())
    expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue('')
    expect(chatPosts).toBe(0)
  })

  it('when the text is not recorded as an answer (the response answerTo is not the question), says it was saved as an ordinary note', async () => {
    const { agent, turn, question, inbox } = chatSetup()
    const ordinary = http.post(apiUrl(`/inbox/messages/${question.id}/reply`), async ({ request }) => {
      const body = (await request.json()) as Schemas['InboxReplyInput']
      return HttpResponse.json(makeInboxMessage({ from: consoleParty, to: question.from, threadId: question.threadId, inReplyTo: question.id, text: body.text!, answerTo: null }), { status: 201 })
    })
    const { user } = await renderChat({ agent, messages: turn, handlers: [ordinary, ...inbox.handlers] })
    await user.click(within(await waitFor(() => consoleItem(question))).getByRole('button', { name: 'Reply to this question' }))
    await user.type(screen.getByRole('textbox', { name: 'Message' }), 'Late text{Enter}')
    expect(await screen.findByText('Saved as an ordinary note, not recorded as an answer.')).toBeVisible()
  })
})

describe('Old inbox addresses', () => {
  const renderRedirect = (path: string) => renderRoutes([{ path: '/inbox/:threadId?', element: <InboxRedirect /> }, { path: '*', element: <p>Arrived</p> }], path)

  it('an Agent thread goes to its chat and an external identity thread to its page; an unreadable or missing thread goes back to the overview', async () => {
    const agent = makeAgent(), identity = makeIdentity({ name: 'ci-bot' })
    const withAgent = makeInboxMessage({ from: agentParty(agent), to: consoleParty })
    const withIdentity = makeInboxMessage({ from: externalParty(identity), to: consoleParty })
    server.use(...fakeInbox({ messages: [withAgent, withIdentity] }).handlers)
    let view = renderRedirect(`/inbox/${withAgent.threadId}`)
    await waitFor(() => expect(view.router.state.location.pathname).toBe(`/agents/${agent.id}/chat`))
    view.unmount()
    view = renderRedirect(`/inbox/${withIdentity.threadId}`)
    await waitFor(() => expect(view.router.state.location.pathname).toBe(`/identities/${identity.id}`))
    view.unmount()
    view = renderRedirect('/inbox/00000000-0000-7000-a000-999999999999')
    await waitFor(() => expect(view.router.state.location.pathname).toBe('/'))
    view.unmount()
    view = renderRedirect('/inbox')
    await waitFor(() => expect(view.router.state.location.pathname).toBe('/'))
  })

  it('a task instance thread goes to the task node the message belongs to', async () => {
    const detail = makeTaskDetail(defaultProject.id)
    const agent = makeAgent()
    const message = makeInboxMessage({ from: agentParty(agent), to: consoleParty, task: { taskId, nodeRunId: nodeId, runtimeId: detail.nodes[1].runtimeId } })
    server.use(...fakeInbox({ messages: [message] }).handlers, http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)))
    const view = renderRedirect(`/inbox/${message.threadId}`)
    await waitFor(() => expect(view.router.state.location.pathname).toBe(`/p/${encodeURIComponent(defaultProject.name)}/tasks/${taskId}`))
    expect(new URLSearchParams(view.router.state.location.search).get('record')).toBe(nodeId)
  })
})

describe('Needs you', () => {
  it('the Agent list shows the number waiting for your reply, and screen readers hear how many are questions and how many new messages', async () => {
    const { agent, note, question } = chatSetup()
    const other = makeAgent({ name: 'Testing assistant' })
    server.use(http.get(apiUrl('/inbox/pending'), () => HttpResponse.json(pendingOf([note, question]))), ...baseHandlers([agent, other]))
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    const row = await within(list).findByRole('link', { name: /开发助手/ })
    expect(await within(row).findByText('Needs your reply: 1 question, 1 new message')).toBeInTheDocument()
    expect(within(row).getByText('2')).toBeVisible()
    expect(within(within(list).getByRole('link', { name: /Testing assistant/ })).queryByText(/Needs your reply/)).toBeNull()
  })

  it('the top of the overview lists the questions and new messages for you, questions first, each linking to its chat; the block is hidden when there are none', async () => {
    const { agent, note, question } = chatSetup()
    const { snapshot } = await renderOverview({ handlers: [http.get(apiUrl('/inbox/pending'), () => HttpResponse.json(pendingOf([note, question])))] })
    await snapshot([makeAgentOverview({ agentId: agent.id, name: agent.name })])
    const section = await screen.findByRole('region', { name: 'Needs you' })
    const rows = within(section).getAllByRole('link')
    expect(rows.map((row) => row.textContent)).toEqual([expect.stringMatching(/Question.*开发助手.*Should the expected values follow the new v3 table or the old one\?.*Answer/), expect.stringMatching(/New message.*开发助手.*server-main is merged.*View/)])
    expect(rows[0]).toHaveAttribute('href', `/agents/${agent.id}/chat`)
  })

  it('redesign: “Needs you” sits at the very top of the overview sheet as a card with a pink shadow, unnumbered; Run status is still 01', async () => {
    const { agent, note, question } = chatSetup()
    const { snapshot } = await renderOverview({ handlers: [http.get(apiUrl('/inbox/pending'), () => HttpResponse.json(pendingOf([note, question])))] })
    await snapshot([makeAgentOverview({ agentId: agent.id, name: agent.name })])
    const pending = await screen.findByRole('region', { name: 'Needs you' })
    const run = screen.getByRole('region', { name: 'Run status' })
    expect(pending.closest('[data-sheet]')).toBe(run.closest('[data-sheet]'))
    expect(pending.compareDocumentPosition(run) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(pending).toHaveClass('border-2', 'border-ink', 'bg-tint-pink-2', 'shadow-print-pink')
    expect(pending.querySelector('[data-section-number]')).toBeNull()
    expect(run.querySelector('[data-section-number]')).toHaveTextContent('01')
  })

  it('with nothing needing you, the overview has no such block', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    await screen.findByRole('heading', { name: 'Run status' })
    expect(screen.queryByRole('region', { name: 'Needs you' })).toBeNull()
  })
})

describe('Exchanges between an external identity and you', () => {
  function identitySetup() {
    const identity = makeIdentity({ name: 'ci-bot' }), agent = makeAgent()
    const fromIt = makeInboxMessage({ from: externalParty(identity), to: consoleParty, text: 'The build failed, run it again?', whenBusy: null })
    const toAgent = makeInboxMessage({ from: externalParty(identity), to: agentParty(agent), text: 'Messages to Agents are not here' })
    const inbox = fakeInbox({ messages: [fromIt, toAgent], recipients: { '@ci-bot': externalParty(identity) } })
    server.use(
      ...inbox.handlers, ...baseHandlers([]),
      http.get(apiUrl('/external-identities'), () => HttpResponse.json({ identities: [identity] })),
      http.get(apiUrl('/external-identities/:id'), () => HttpResponse.json(identity)),
      http.get(apiUrl('/external-identities/:id/tokens'), () => HttpResponse.json({ tokens: [] })),
      http.get(apiUrl('/external-identities/:id/inbox'), () => HttpResponse.json({ messages: [fromIt, toAgent].map(summaryOf), nextCursor: null })),
    )
    return { identity, fromIt, inbox }
  }

  it('shows the messages between it and you and can reply to the latest one; a new message starts a new thread to @name', async () => {
    const { identity, fromIt, inbox } = identitySetup()
    const user = userEvent.setup()
    renderRoutes(routes, `/identities/${identity.id}`)
    const section = await screen.findByRole('region', { name: 'Conversation with you' })
    expect(await within(section).findByText('The build failed, run it again?')).toBeVisible()
    expect(within(section).queryByText('Messages to Agents are not here')).toBeNull()
    await user.type(within(section).getByRole('textbox', { name: 'Reply to ci-bot…' }), 'Run it once more')
    await user.click(within(section).getByRole('button', { name: 'Reply' }))
    await waitFor(() => expect(inbox.sent).toEqual([{ text: 'Run it once more', replyTo: fromIt.id }]))
    await user.click(within(section).getByRole('button', { name: 'New message' }))
    await user.type(within(section).getByRole('textbox', { name: 'New message to ci-bot' }), 'Something else')
    await user.click(within(section).getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(inbox.sent.at(-1)).toEqual({ to: '@ci-bot', text: 'Something else' }))
  })
})
