import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeMessage, makeMessagePage, makeQueueItem, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'
import { FakeEventSource } from '@/test/fake-event-source'
import { server } from '@/test/server'

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']>, currentText: string | null, queuedRuns: Schemas['Run'][] = [], queueItems: Schemas['QueueItem'][] = []) {
  return { status: makeStatus(agentId, status), currentText, queuedRuns, queueItems }
}

describe('resume after disconnect and snapshots', () => {
  it('shows the reconnecting state while disconnected; after resuming the text continues and events delivered twice show once', async () => {
    const run = makeRun({ status: 'running' })
    const { agent, source, emit } = await renderChat({ messages: [makeMessage({ text: 'Write a poem', run })] })
    act(() => source.open())
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, 'Roses are'), '10-0')
    await emit('run.text', { runId: run.id, delta: ' red' }, '11-0')
    expect(await screen.findByText('Roses are red')).toBeInTheDocument()

    act(() => source.interrupt())
    expect(await screen.findByText('Connection lost, reconnecting…')).toBeInTheDocument()

    // After the browser reconnects with Last-Event-ID, the server resumes
    // from after 11-0; network jitter makes 11-0 get delivered a second time.
    act(() => source.open())
    await emit('run.text', { runId: run.id, delta: ' red' }, '11-0')
    await emit('run.text', { runId: run.id, delta: ', violets' }, '12-0')
    expect(await screen.findByText('Roses are red, violets')).toBeInTheDocument()
    expect(screen.getByText('Live')).toBeInTheDocument()
  })

  it('beyond the retained range the server pushes a snapshot, and the snapshot text replaces everything', async () => {
    const run = makeRun({ status: 'running' })
    const { agent, emit } = await renderChat({ messages: [makeMessage({ text: 'Question', run })] })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, 'Old fragment'), '10-0')
    expect(await screen.findByText('Old fragment')).toBeInTheDocument()
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, 'Wrote a lot while disconnected'), '50-0')
    expect(await screen.findByText('Wrote a lot while disconnected')).toBeInTheDocument()
    expect(screen.queryByText('Old fragment')).toBeNull()
  })
})

describe('server restart and races', () => {
  it('after a restart the snapshot shows the original reply as cut off by a server restart, and the queued message starts next', async () => {
    const interrupted = makeRun({ status: 'interrupted', endReason: 'server_restart' })
    const queued = makeRun({ status: 'queued', whenBusy: 'queue' })
    let history: Schemas['Message'][] = [
      makeMessage({ text: 'Long task', run: makeRun({ ...interrupted, status: 'running', endReason: null }) }),
      makeMessage({ text: 'Queued one', run: queued }),
    ]
    const item = makeQueueItem({ runId: queued.id, text: 'Queued one' })
    const { agent, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), () => HttpResponse.json(makeMessagePage(history))),
        http.get(apiUrl('/agents/:agentId/queue'), ({ params }) => HttpResponse.json({ agentId: String(params.agentId), items: [item] })),
      ],
    })
    expect(await screen.findByText('Queued one')).toBeInTheDocument()
    // Server restart: in the history the original reply is already marked as
    // interrupted by the server restart, and the text produced so far has been saved.
    history = [
      makeMessage({ text: 'Long task', run: interrupted }),
      makeMessage({ role: 'assistant', text: 'Half-finished reply', run: interrupted }),
      makeMessage({ text: 'Queued one', run: queued }),
    ]
    await emit('snapshot', snapshot(agent.id, { busy: true, queued: { interrupt: 0, normal: 1 } }, null, [queued], [item]), '1-0')
    expect(await screen.findByText('Cut off by a server restart')).toBeInTheDocument()
    expect(screen.getByText('Half-finished reply')).toBeInTheDocument()
    await emit('run.updated', { run: { ...queued, status: 'starting', phase: 'launching' } }, '2-0')
    await emit('queue.updated', { queuedRuns: [], items: [] }, '3-0')
    // The header also shows the current phase; here we only look at the turn in the conversation.
    expect(await within(screen.getByRole('log')).findByText('Starting')).toBeInTheDocument()
    // The message that started running leaves the queue list and appears as a turn in the conversation stream.
    expect(screen.queryByRole('region', { name: 'Queued messages' })).toBeNull()
    expect(within(screen.getByRole('log')).getByText('Queued one')).toBeInTheDocument()
  })
})

describe('event stream closed for good', () => {
  it('when the Agent was deleted (404) stops reconnecting and says it does not exist', async () => {
    const { agent, source } = await renderChat()
    server.use(http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(errorBody('not_found'), { status: 404 })))
    act(() => source.fail())
    expect(await screen.findByText('This Agent doesn\'t exist or was deleted.')).toBeInTheDocument()
    expect(FakeEventSource.instances.filter((s) => s.url === source.url)).toHaveLength(1)
  })

  it('on a server error (500) reconnects after the backoff interval and shows the snapshot content once recovered', async () => {
    const run = makeRun({ status: 'running' })
    const { agent, source } = await renderChat({ messages: [makeMessage({ text: 'Question', run })] })
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      act(() => source.fail())
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1100)
      })
      await waitFor(() => expect(FakeEventSource.instances.filter((s) => s.url === source.url).length).toBe(2))
      const next = FakeEventSource.latest()
      await act(async () => {
        next.open()
        next.emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, 'Text after recovery'), '99-0')
        await vi.advanceTimersByTimeAsync(50)
      })
      expect(await screen.findByText('Text after recovery')).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })
})
