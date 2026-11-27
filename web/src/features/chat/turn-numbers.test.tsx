import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, makeMessage, makeMessagePage, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'

// The chat page takes its turn numbers from the message page's turnsBefore and turnsTotal (MessagePage in the contract).
const turn = (question: string, minute: number) => {
  const time = new Date(Date.UTC(2026, 10, 12, 8, minute)).toISOString()
  const run = makeRun({ queuedAt: time, startedAt: time, runningAt: time, endedAt: time })
  return [makeMessage({ text: question, createdAt: time, run }), makeMessage({ role: 'assistant', text: `Answer to ${question}`, createdAt: time, run })]
}
const numbers = () => [...document.querySelectorAll('[data-turn-number]')].map((node) => node.textContent)
const total = () => screen.getByRole('navigation', { name: 'Conversation turns' }).querySelector('[data-ruler-total]')
const idle = (agentId: string) => ({ status: makeStatus(agentId), currentText: null, queuedRuns: [], queueItems: [] })

describe('turn numbers on the chat page', () => {
  it('with 24 turns before this page numbers from 25 and shows the total at the bottom; numbers even though earlier messages are not loaded', async () => {
    await renderChat({ messages: [...turn('First question', 1), ...turn('Second question', 2)], hasMore: true, page: { turnsBefore: 24, turnsTotal: 26 } })
    await waitFor(() => expect(numbers()).toEqual(['25', '26']))
    expect(total()).toHaveTextContent('26')
  })

  it('after loading an earlier page numbers on from the count of the earliest loaded page', async () => {
    const older = [...turn('Earlier one', 1), ...turn('Earlier two', 2)]
    const newer = [...turn('Newer one', 3), ...turn('Newer two', 4)]
    const { user } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), ({ request }) =>
          new URL(request.url).searchParams.get('before')
            ? HttpResponse.json(makeMessagePage(older, { hasMore: true, turnsBefore: 10, turnsTotal: 14 }))
            : HttpResponse.json(makeMessagePage(newer, { hasMore: true, turnsBefore: 12, turnsTotal: 14 })),
        ),
      ],
    })
    await waitFor(() => expect(numbers()).toEqual(['13', '14']))
    await user.click(screen.getByRole('button', { name: 'Load earlier messages' }))
    await waitFor(() => expect(numbers()).toEqual(['11', '12', '13', '14']))
    expect(total()).toHaveTextContent('14')
  })

  it('newly sent messages and new replies are numbered on locally and the total grows, without waiting for a refetch', async () => {
    let reads = 0
    const history = [...turn('First question', 1), ...turn('Second question', 2)]
    const { agent, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), () => {
          reads += 1
          return HttpResponse.json(makeMessagePage(history, { hasMore: true, turnsBefore: 24, turnsTotal: 26 }))
        }),
      ],
    })
    await emit('snapshot', idle(agent.id))
    await waitFor(() => expect(numbers()).toEqual(['25', '26']))
    const readsBefore = reads
    const run = makeRun({ status: 'running', queuedAt: '2026-11-12T09:00:00Z', startedAt: '2026-11-12T09:00:00Z', runningAt: '2026-11-12T09:00:01Z', endedAt: null })
    await emit('message.created', { message: makeMessage({ text: 'Third question', createdAt: '2026-11-12T09:00:00Z', run }) })
    await waitFor(() => expect(numbers()).toEqual(['25', '26', '27']))
    expect(total()).toHaveTextContent('27')
    expect(within(screen.getByRole('navigation', { name: 'Conversation turns' }).querySelector('ol')!).getAllByRole('button')).toHaveLength(3)
    expect(reads).toBe(readsBefore)
  })

  it('with no conversation there are no numbers and no total; the first turn starts at 1', async () => {
    const { agent, emit } = await renderChat({ messages: [] })
    await emit('snapshot', idle(agent.id))
    expect(numbers()).toEqual([])
    const run = makeRun({ status: 'running', queuedAt: '2026-11-12T09:00:00Z', startedAt: '2026-11-12T09:00:00Z', runningAt: '2026-11-12T09:00:01Z', endedAt: null })
    await emit('message.created', { message: makeMessage({ text: 'First question', createdAt: '2026-11-12T09:00:00Z', run }) })
    await waitFor(() => expect(numbers()).toEqual(['1']))
  })
})

// A manual compaction (/compact) is in progress: the Agent's current run is this compaction, which is not a turn.
describe('manual compaction in progress', () => {
  it('adds no turn: no new turn block, number or tick, the total stays; the busy state shows as usual', async () => {
    const { agent, emit } = await renderChat({ messages: [...turn('First question', 1), ...turn('Second question', 2)], page: { turnsBefore: 24, turnsTotal: 26 }, hasMore: true })
    const compacting = makeRun({ status: 'running', queuedAt: '2026-11-12T09:00:00Z', startedAt: '2026-11-12T09:00:00Z', runningAt: '2026-11-12T09:00:01Z', endedAt: null, compaction: { instructions: '', outcome: null, skipReason: null, tokensBefore: null, estimatedTokensAfter: null } })
    await emit('snapshot', { status: makeStatus(agent.id, { busy: true, currentRun: compacting, processState: 'busy' }), currentText: '', queuedRuns: [], queueItems: [] })
    await screen.findByText(/Agent is busy/)
    expect(numbers()).toEqual(['25', '26'])
    expect(document.querySelectorAll('[data-run-id]')).toHaveLength(2)
    expect(within(screen.getByRole('navigation', { name: 'Conversation turns' }).querySelector('ol')!).getAllByRole('button')).toHaveLength(2)
    expect(total()).toHaveTextContent('26')
    expect(screen.getByRole('button', { name: 'Stop the current reply' })).toBeInTheDocument()
  })
})
