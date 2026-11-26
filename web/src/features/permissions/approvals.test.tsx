import { render, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeAgent, makeMessage, makePermissionRequest, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'
import { agentParty, consoleParty, makeInboxMessage } from '@/test/inbox'
import { createTestQueryClient } from '@/test/render'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { buildSteps } from '@/features/chat/timeline-steps'
import { runtimeName, withCodex } from '@/test/runtimes'
import { QuestionChoices } from '@/features/console-inbox/ConsoleMessage'

const agent = makeAgent({ name: 'Coordinator', model: { provider: 'claude-code', modelId: 'opus' }, runtime: 'claude-code', permissionMode: 'supervised' })
const running = makeRun({ status: 'running', endedAt: null })
const push = makePermissionRequest({ agentId: agent.id, runId: running.id, title: 'git push origin main', detail: 'git push origin main --force-with-lease' })
const edit = makePermissionRequest({ agentId: agent.id, runId: running.id, kind: 'file_change', title: 'Edit MAIN.md', detail: '-old\n+new' })

const snapshot = (requests: Schemas['PermissionRequest'][]) => ({ status: makeStatus(agent.id, { busy: true, currentRun: running, permissionRequests: requests }), currentText: 'Pushing first.', queuedRuns: [], queueItems: [] })

// Records answers; each answers as the given function says.
function answers(reply: (body: Schemas['PermissionAnswer'], id: string) => Response) {
  const calls: { id: string; body: Schemas['PermissionAnswer'] }[] = []
  return {
    calls,
    handler: http.post(apiUrl('/permission-requests/:id/answer'), async ({ params, request }) => {
      const body = (await request.json()) as Schemas['PermissionAnswer']
      calls.push({ id: String(params.id), body })
      return reply(body, String(params.id))
    }),
  }
}

const card = (title: string) => screen.getByText(title).closest('[data-permission-request]') as HTMLElement

describe('Approving in the chat', () => {
  it('each waiting request is a card in the reply that asked, with the command; one line above the box says the reply waits', async () => {
    const { emit } = await renderChat({ agent, messages: [makeMessage({ text: 'Push and update MAIN.md', run: running })] })
    await emit('snapshot', snapshot([push, edit]))
    const pushCard = card('git push origin main')
    expect(within(pushCard).getByRole('heading', { name: 'Coordinator wants to run a command' })).toBeInTheDocument()
    expect(within(pushCard).getByText('git push origin main --force-with-lease')).toBeInTheDocument()
    expect(within(card('Edit MAIN.md')).getByRole('heading', { name: 'Coordinator wants to change a file' })).toBeInTheDocument()
    expect(screen.getByText('2 requests are waiting for your approval; the reply waits here')).toBeInTheDocument()
    expect(screen.getByText('Stopping the reply rejects every request still waiting')).toBeInTheDocument()
  })

  it('Approve answers the request and the card says so; a typed reason rejects it with that reason', async () => {
    const api = answers((body, id) => HttpResponse.json({ ...(id === push.id ? push : edit), state: body.decision === 'approve' ? 'approved' : 'rejected', reason: body.reason ?? null, answeredAt: '2026-10-12T08:01:00Z' }))
    const { user, emit } = await renderChat({ agent, messages: [makeMessage({ text: 'Go', run: running })], handlers: [api.handler] })
    await emit('snapshot', snapshot([push, edit]))
    await user.click(within(card('git push origin main')).getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(api.calls).toEqual([{ id: push.id, body: { decision: 'approve' } }]))
    expect(await within(card('git push origin main')).findByRole('status')).toHaveTextContent('Approved')

    const editCard = card('Edit MAIN.md')
    expect(within(editCard).queryByRole('textbox', { name: 'Reason to reject' })).toBeNull()
    await user.click(within(editCard).getByRole('button', { name: 'Reject with reason…' }))
    await user.type(within(editCard).getByRole('textbox', { name: 'Reason to reject' }), 'Wait until I read the diff')
    await user.click(within(editCard).getByRole('button', { name: 'Reject with reason' }))
    await waitFor(() => expect(api.calls[1]).toEqual({ id: edit.id, body: { decision: 'reject', reason: 'Wait until I read the diff' } }))
    expect(await within(editCard).findByRole('status')).toHaveTextContent('Rejected · Reason: Wait until I read the diff')
  })

  it('Reject takes a typed reason too, so the Agent is told why', async () => {
    const api = answers((body) => HttpResponse.json({ ...push, state: 'rejected', reason: body.reason ?? null, answeredAt: '2026-10-12T08:01:00Z' }))
    const { user, emit } = await renderChat({ agent, messages: [makeMessage({ text: 'Go', run: running })], handlers: [api.handler] })
    await emit('snapshot', snapshot([push]))
    const pushCard = card('git push origin main')
    await user.click(within(pushCard).getByRole('button', { name: 'Reject with reason…' }))
    await user.type(within(pushCard).getByRole('textbox', { name: 'Reason to reject' }), 'Not on main')
    await user.click(within(pushCard).getByRole('button', { name: 'Reject' }))
    await waitFor(() => expect(api.calls).toEqual([{ id: push.id, body: { decision: 'reject', reason: 'Not on main' } }]))
    expect(await within(pushCard).findByRole('status')).toHaveFocus()
  })

  it('a runtime that is not told the reason says so under the field; Claude Code is told it', async () => {
    const codex = makePermissionRequest({ agentId: agent.id, runId: running.id, runtime: 'codex', title: 'npm publish' })
    const { user, emit } = await renderChat({ agent, messages: [makeMessage({ text: 'Go', run: running })], handlers: [http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(withCodex()))] })
    await emit('snapshot', snapshot([push, codex]))
    await user.click(within(card('git push origin main')).getByRole('button', { name: 'Reject with reason…' }))
    expect(within(card('git push origin main')).getByRole('textbox', { name: 'Reason to reject' })).toHaveAttribute('placeholder', 'A reason rejects the request and tells the Agent why')
    expect(within(card('git push origin main')).queryByText(/isn't told the reason/)).toBeNull()
    await user.click(within(card('npm publish')).getByRole('button', { name: 'Reject with reason…' }))
    expect(within(card('npm publish')).getByRole('textbox', { name: 'Reason to reject' })).toHaveAttribute('placeholder', 'A typed reason rejects the request')
    expect(within(card('npm publish')).getByText("Codex isn't told the reason; it is kept with the request here")).toBeInTheDocument()
  })

  it('answered elsewhere first (the Inbox): 409 shows how it was answered, not an error', async () => {
    const api = answers(() => HttpResponse.json(errorBody('permission_request_answered', 'Already answered.', { details: { request: { ...push, state: 'rejected', reason: 'Not now', answeredAt: '2026-10-12T08:01:00Z' } } }), { status: 409 }))
    const { user, emit } = await renderChat({ agent, messages: [makeMessage({ text: 'Go', run: running })], handlers: [api.handler] })
    await emit('snapshot', snapshot([push]))
    await user.click(within(card('git push origin main')).getByRole('button', { name: 'Approve' }))
    expect(await within(card('git push origin main')).findByRole('status')).toHaveTextContent('Answered elsewhere: Rejected · Reason: Not now')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('a request answered or cancelled leaves the list, and its card goes', async () => {
    const { emit } = await renderChat({ agent, messages: [makeMessage({ text: 'Go', run: running })] })
    await emit('snapshot', snapshot([push, edit]))
    await emit('permission_requests.updated', { requests: [edit] })
    await waitFor(() => expect(screen.queryByText('git push origin main')).toBeNull())
    expect(screen.getByText('1 request is waiting for your approval; the reply waits here')).toBeInTheDocument()
    await emit('permission_requests.updated', { requests: [] })
    await waitFor(() => expect(screen.queryByText(/waiting for your approval/)).toBeNull())
  })

  it('the approval question the Agent sent the console is the card, not a second message in the chat', async () => {
    const asked = makeInboxMessage({ from: agentParty(agent), to: consoleParty, text: 'git push origin main', question: { kind: 'permission', permissionRequestId: push.id, options: [], expiresAt: '2026-12-01T00:00:00Z', state: 'open', answer: null } })
    const plain = makeInboxMessage({ from: agentParty(agent), to: consoleParty, text: 'FYI: the build is green' })
    const { emit } = await renderChat({
      agent,
      messages: [makeMessage({ text: 'Go', run: running })],
      handlers: [http.get(apiUrl(`/agents/${agent.id}/console-messages`), () => HttpResponse.json({ messages: [asked, plain], nextCursor: null }))],
    })
    await emit('snapshot', snapshot([push]))
    expect(await screen.findByText('FYI: the build is green')).toBeInTheDocument()
    expect(screen.getAllByText('git push origin main')).toHaveLength(1)
  })
})

describe('Imported history in the chat', () => {
  it('the turns copied from the terminal come first under their divider, without a run record; the Agent’s own follow', async () => {
    const copied = (text: string, reply: string) => {
      const run = makeRun({ usageReported: false })
      return [makeMessage({ text, run, origin: 'imported' }), makeMessage({ role: 'assistant', text: reply, run, origin: 'imported' })]
    }
    const own = makeRun()
    await renderChat({
      agent,
      messages: [...copied('Read MAIN.md', 'Done reading.'), ...copied('Is it merged?', 'Yes, PR #83.'), makeMessage({ text: 'Start the import work', run: own }), makeMessage({ role: 'assistant', text: 'Dispatched.', run: own })],
    })
    const log = screen.getByRole('log')
    const separators = within(log).getAllByRole('separator')
    expect(separators.map((line) => line.getAttribute('aria-label'))).toEqual(['Imported history, copied from the terminal conversation', 'Continued in Loop Now from here'])
    const order = (el: Element) => Array.from(log.querySelectorAll('[role=separator], [data-run-id]')).indexOf(el.closest('[role=separator], [data-run-id]')!)
    expect(order(separators[0])).toBeLessThan(order(screen.getByText('Read MAIN.md')))
    expect(order(screen.getByText('Yes, PR #83.'))).toBeLessThan(order(separators[1]))
    expect(order(separators[1])).toBeLessThan(order(screen.getByText('Dispatched.')))
    // Only the Agent's own turn has a run record.
    expect(within(log).getAllByRole('button', { name: /^Run log/ })).toHaveLength(1)
  })
})

describe('Imported history at a page edge', () => {
  it('an imported turn whose prompt is on an older page still counts as imported', async () => {
    const run = makeRun({ usageReported: false })
    const own = makeRun()
    await renderChat({ agent, hasMore: true, messages: [makeMessage({ role: 'assistant', text: 'Yes, PR #83.', run, origin: 'imported' }), makeMessage({ text: 'Go on', run: own }), makeMessage({ role: 'assistant', text: 'Going.', run: own })] })
    const log = screen.getByRole('log')
    expect(within(log).getAllByRole('separator').map((line) => line.getAttribute('aria-label'))).toEqual(['Continued in Loop Now from here'])
    expect(within(log).getAllByRole('button', { name: /^Run log/ })).toHaveLength(1)
  })
})

describe('Approvals elsewhere', () => {
  it('the Inbox marks an approval question', () => {
    const message = makeInboxMessage({ from: agentParty(agent), to: consoleParty, text: 'git push', question: { kind: 'permission', permissionRequestId: push.id, options: [{ id: 'approve', label: 'Approve' }, { id: 'reject', label: 'Reject' }], expiresAt: '2026-12-01T00:00:00Z', state: 'open', answer: null } })
    render(
      <QueryClientProvider client={createTestQueryClient()}>
        <MemoryRouter>
          <QuestionChoices message={message} />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    expect(screen.getByText('Asks for approval: answering approves or rejects; a typed reason rejects it')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Approve' })).toBeEnabled()
  })

  it('an approval question whose request was cancelled reads as cancelled, not as expired at a time', () => {
    const message = makeInboxMessage({ from: agentParty(agent), to: consoleParty, text: 'git push', question: { kind: 'permission', permissionRequestId: push.id, options: [{ id: '1', label: 'Approve' }, { id: '2', label: 'Reject' }], expiresAt: '2026-12-01T00:00:00Z', state: 'expired', answer: null } })
    render(
      <QueryClientProvider client={createTestQueryClient()}>
        <MemoryRouter>
          <QuestionChoices message={message} />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    expect(screen.getByText('Cancelled: the reply ended before it was answered')).toBeInTheDocument()
    expect(screen.queryByText(/^Expires/)).toBeNull()
    expect(screen.getByRole('button', { name: 'Approve' })).toBeDisabled()
  })

  it('the run record keeps what was asked and how it was answered', () => {
    const steps = buildSteps(null, [
      { type: 'permission_request', at: '2026-10-12T08:00:00Z', requestId: 'r1', kind: 'command', title: 'git push', state: 'rejected', answeredAt: '2026-10-12T08:00:30Z', reason: 'Not now' },
      { type: 'permission_request', at: '2026-10-12T08:01:00Z', requestId: 'r2', kind: 'file_change', title: 'Edit MAIN.md', state: 'cancelled', answeredAt: '2026-10-12T08:02:00Z', reason: 'reply_stopped' },
    ], runtimeName)
    expect(steps.map((step) => [step.label, step.detail, step.lines, step.tone])).toEqual([
      ['Asked for approval to run a command', 'git push', ['Rejected · Reason: Not now'], 'fail'],
      ['Asked for approval to change a file', 'Edit MAIN.md', ['Cancelled · the reply was stopped'], 'neutral'],
    ])
  })
})
