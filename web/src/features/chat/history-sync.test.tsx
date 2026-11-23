import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, makeMessage, makeMessagePage, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'

// Events and send results that arrive while a history request is in flight (the first
// load, loading older messages, or a refetch triggered by a snapshot) must not be lost.

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((r) => (resolve = r))
  return { promise, resolve }
}

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']>, currentText: string | null = null) {
  return { status: makeStatus(agentId, status), currentText, queuedRuns: [], queueItems: [] }
}

function turn(text: string, reply: string) {
  const run = makeRun()
  return [makeMessage({ text, run }), makeMessage({ role: 'assistant', text: reply, run })]
}

// A turn whose reply is in progress, plus the run and reply that the event stream delivers when it ends.
function replyingTurn(text: string, reply: string) {
  const run = makeRun({ status: 'running', endedAt: null })
  const ended: Schemas['Run'] = { ...run, status: 'completed', endedAt: '2026-10-12T08:00:30Z' }
  return {
    run,
    question: makeMessage({ text, run }),
    ended,
    reply: makeMessage({ role: 'assistant', text: reply, run: ended }),
  }
}

const settle = () => act(() => new Promise((resolve) => setTimeout(resolve, 50)))

describe('Events that arrive while a history request is in flight', () => {
  it('a reply ends while older messages load: the request result does not overwrite the end event', async () => {
    const { question, ended, reply } = replyingTurn('New question', 'Final reply')
    const gate = deferred()
    const { emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
          if (!new URL(request.url).searchParams.get('before')) return HttpResponse.json(makeMessagePage([question], { hasMore: true, turnsBefore: 1, turnsTotal: 2 }))
          await gate.promise
          return HttpResponse.json(makeMessagePage(turn('Old question', 'Old answer'), { turnsTotal: 2 }))
        }),
      ],
    })
    expect(await screen.findByText('New question')).toBeInTheDocument()
    fireEvent.scroll(screen.getByRole('log'))
    expect(await screen.findByText('Loading earlier messages…')).toBeInTheDocument()
    await emit('run.ended', { run: ended, message: reply })
    gate.resolve()
    expect(await screen.findByText('Old question')).toBeInTheDocument()
    expect(screen.getByText('Final reply')).toBeInTheDocument()
    expect(screen.queryByText('Replying')).toBeNull()
  })

  it('a reply ends while a snapshot-triggered refetch is in flight: the end event wins', async () => {
    const { run, question, ended, reply } = replyingTurn('Question', 'Final reply')
    const gate = deferred()
    let calls = 0
    const { agent, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async () => {
          calls += 1
          // The refetch read the history before the run ended, but its response arrives after the end event.
          if (calls > 1) await gate.promise
          return HttpResponse.json(makeMessagePage([question]))
        }),
      ],
    })
    expect(await screen.findByText('Question')).toBeInTheDocument()
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, 'Writing'))
    await waitFor(() => expect(calls).toBe(2))
    await emit('run.ended', { run: ended, message: reply })
    gate.resolve()
    await settle()
    expect(screen.getByText('Final reply')).toBeInTheDocument()
    expect(screen.queryByText('Replying')).toBeNull()
  })

  it('events that arrive before the first load completes are not lost', async () => {
    const { question, ended, reply } = replyingTurn('Question', 'Final reply')
    const gate = deferred()
    const { emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async () => {
          await gate.promise
          return HttpResponse.json(makeMessagePage([question]))
        }),
      ],
    })
    // The queue tray only follows the server's queue list; here the next turn, already started, is used to check that no event is lost.
    const later = makeMessage({ text: 'Later question', run: makeRun({ status: 'starting', phase: 'launching', startedAt: '2026-10-12T09:00:00Z', endedAt: null }) })
    await emit('run.ended', { run: ended, message: reply })
    await emit('message.created', { message: later })
    gate.resolve()
    expect(await screen.findByText('Question')).toBeInTheDocument()
    expect(screen.getByText('Final reply')).toBeInTheDocument()
    expect(screen.getByText('Later question')).toBeInTheDocument()
    expect(screen.queryByText('Replying')).toBeNull()
  })

  it('a snapshot arrives during the first load: the history is fetched again after the snapshot', async () => {
    const { question, ended, reply } = replyingTurn('Question', 'Final reply')
    const gate = deferred()
    let calls = 0
    const { agent, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async () => {
          calls += 1
          if (calls === 1) {
            // The first load reads before the run ends; the run ends before the
            // snapshot, so the event stream will not send the end event again.
            await gate.promise
            return HttpResponse.json(makeMessagePage([question]))
          }
          return HttpResponse.json(makeMessagePage([{ ...question, run: ended }, reply]))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, {}))
    gate.resolve()
    expect(await screen.findByText('Final reply')).toBeInTheDocument()
    await settle()
    expect(calls).toBe(2)
    expect(screen.getByText('Final reply')).toBeInTheDocument()
    expect(screen.queryByText('Replying')).toBeNull()
  })

  it('a send result that arrives while a refetch is in flight is not lost', async () => {
    const gate = deferred()
    let calls = 0
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async () => {
          calls += 1
          if (calls > 1) await gate.promise
          return HttpResponse.json(makeMessagePage(turn('Earlier question', 'Earlier answer')))
        }),
        http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
          const body = (await request.json()) as Schemas['SendMessageInput']
          const run = makeRun({ status: 'starting', phase: 'launching', endedAt: null })
          return HttpResponse.json({ message: makeMessage({ text: body.text, run }), run }, { status: 202 })
        }),
      ],
    })
    expect(await screen.findByText('Earlier answer')).toBeInTheDocument()
    // While the event stream is disconnected (so message.created is not
    // received), a snapshot triggers a refetch, and a message is sent meanwhile.
    await emit('snapshot', snapshot(agent.id, {}))
    await waitFor(() => expect(calls).toBe(2))
    await user.type(screen.getByLabelText('Message'), 'New message')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(screen.getByLabelText('Message')).toHaveValue(''))
    gate.resolve()
    await settle()
    expect(screen.getByText('New message')).toBeInTheDocument()
  })
})

describe('Loading older messages and refetching', () => {
  it('does not load older messages while a refetch requested by a snapshot is in flight, so the reconciliation is not cancelled', async () => {
    const gate = deferred()
    let calls = 0
    let olderCalls = 0
    const first = turn('Old question', 'Old answer')
    const missed = turn('Question while disconnected', 'Answer while disconnected')
    const { agent, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
          if (new URL(request.url).searchParams.get('before')) {
            olderCalls += 1
            return HttpResponse.json(makeMessagePage(turn('Older question', 'Older answer'), { turnsTotal: 3 }))
          }
          calls += 1
          if (calls === 1) return HttpResponse.json(makeMessagePage(first, { hasMore: true, turnsBefore: 1, turnsTotal: 2 }))
          await gate.promise
          return HttpResponse.json(makeMessagePage([...first, ...missed], { hasMore: true, turnsBefore: 1, turnsTotal: 3 }))
        }),
      ],
    })
    expect(await screen.findByText('Old answer')).toBeInTheDocument()
    await emit('snapshot', snapshot(agent.id, {}))
    await waitFor(() => expect(calls).toBe(2))
    fireEvent.scroll(screen.getByRole('log'))
    await settle()
    gate.resolve()
    expect(await screen.findByText('Answer while disconnected')).toBeInTheDocument()
    expect(olderCalls).toBe(0)
    // Once the refetch has completed, older messages can be loaded again.
    fireEvent.scroll(screen.getByRole('log'))
    expect(await screen.findByText('Older answer')).toBeInTheDocument()
    expect(screen.getByText('Answer while disconnected')).toBeInTheDocument()
    expect(olderCalls).toBe(1)
  })
})
