import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { apiUrl, errorBody, makeAgent, makeMessage, makeRun } from '@/test/api'
import { renderChat } from '@/test/chat'
import { renderRoutes } from '@/test/render'

// A conversation history of 300 messages (150 turns), paged by before, after, aroundRun and limit, following the server's rules.
function history(agentId: string) {
  const messages: Schemas['Message'][] = []
  for (let i = 0; i < 150; i++) {
    const time = new Date(Date.UTC(2026, 9, 1) + i * 60_000).toISOString()
    const run = makeRun({ queuedAt: time, startedAt: time, runningAt: time, endedAt: time })
    messages.push(makeMessage({ text: `Question ${i}`, createdAt: time, run }), makeMessage({ role: 'assistant', text: `Answer ${i}`, createdAt: time, run }))
  }
  // Two messages per turn: the turn count before this page is the number of complete
  // turns in front of it (a turn cut by the page boundary counts toward this page).
  const page = (start: number, end: number): Schemas['MessagePage'] => ({
    messages: messages.slice(start, end),
    hasMore: start > 0,
    hasMoreAfter: end < messages.length,
    turnsBefore: Math.floor(start / 2),
    turnsTotal: messages.length / 2,
  })
  const handler = http.get(apiUrl(`/agents/${agentId}/messages`), ({ request }) => {
    const params = new URL(request.url).searchParams
    const limit = Number(params.get('limit') ?? 50)
    const before = params.get('before')
    const after = params.get('after')
    const aroundRun = params.get('aroundRun')
    if (aroundRun) {
      const index = messages.findIndex((m) => m.run.id === aroundRun)
      if (index < 0) return HttpResponse.json(errorBody('not_found'), { status: 404 })
      const start = Math.max(0, index - limit / 2)
      return HttpResponse.json(page(start, Math.min(messages.length, start + limit)))
    }
    if (before) {
      const index = messages.findIndex((m) => m.id === before)
      return HttpResponse.json(page(Math.max(0, index - limit), index))
    }
    if (after) {
      const index = messages.findIndex((m) => m.id === after) + 1
      return HttpResponse.json(page(index, Math.min(messages.length, index + limit)))
    }
    return HttpResponse.json(page(Math.max(0, messages.length - limit), messages.length))
  })
  const timeline = http.get(apiUrl(`/agents/${agentId}/runs/:runId/timeline`), ({ params }) => {
    const run = messages.find((m) => m.run.id === params.runId)!.run
    return HttpResponse.json({ run, entries: [{ type: 'ended', at: run.endedAt, status: 'completed', endReason: null, errorCode: null }], usage: run.usage })
  })
  return { messages, handlers: [handler, timeline] }
}

const turnIds = () => [...document.querySelectorAll('[data-run-id]')].map((el) => el.getAttribute('data-run-id'))
const located = () => document.querySelector<HTMLElement>('[data-located]')

async function openLocated(index: number) {
  const agent = makeAgent()
  const data = history(agent.id)
  // The reply belonging to message number index (counting from 1).
  const target = data.messages[index - 1].run.id
  const result = await renderChat({ agent, handlers: data.handlers, path: `/agents/${agent.id}/chat#run-${target}` })
  await waitFor(() => expect(located()).not.toBeNull())
  return { ...result, data, target }
}

describe('Locating a reply from the overview', () => {
  it('numbers turns as usual when locating (using the turn count before this page); before the latest is loaded, the total at the bottom of the ruler is the total turn count from the API', async () => {
    await openLocated(40)
    // Turn n reads “Question n-1” and “Answer n-1”. This page starts in the middle of a turn
    // (the first block has only a reply) and ends in the middle of a turn, and the numbers still line up.
    const blocks = [...document.querySelectorAll<HTMLElement>('[data-run-id]')]
    expect(blocks.length).toBeLessThan(150)
    for (const block of [blocks[0], blocks[blocks.length - 1]]) {
      const index = Number(/(?:Question|Answer) (\d+)/.exec(block.textContent ?? '')![1])
      expect(block.querySelector('[data-turn-number]')).toHaveTextContent(String(index + 1))
    }
    expect(screen.getByRole('navigation', { name: 'Conversation turns' }).querySelector('[data-ruler-total]')).toHaveTextContent('150')
  })

  it('locating a very early reply: shows it with its run log expanded; loading downward reaches the latest messages with nothing duplicated or missing', async () => {
    const { data, target } = await openLocated(20)
    const turn = located()!
    expect(turn).toHaveAttribute('data-run-id', target)
    expect(turn).toHaveTextContent('Answer 9')
    expect(within(turn).getByText('Located reply')).toBeInTheDocument()
    const toggle = within(turn).getByRole('button', { name: /^Run log/ })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(await within(turn).findByText('The reply finished normally.')).toBeInTheDocument()
    expect(screen.getByText('Viewing the located reply')).toBeInTheDocument()

    for (let i = 0; i < 10 && screen.queryByRole('button', { name: 'Load newer messages' }); i++) {
      // With hundreds of turns on the page, user-event's pointer checks are very slow, so fire the click directly.
      fireEvent.click(screen.getByRole('button', { name: 'Load newer messages' }))
      await waitFor(() => expect(screen.queryByText('Loading newer messages…')).toBeNull())
    }
    expect(screen.queryByRole('button', { name: 'Load newer messages' })).toBeNull()
    const ids = turnIds()
    expect(ids).toHaveLength(150)
    expect(new Set(ids).size).toBe(150)
    expect(ids).toEqual(data.messages.filter((m) => m.role === 'user').map((m) => m.run.id))
    // Loading page by page up to 150 turns, rendering Markdown and a run
    // record for every turn, is slow when the whole suite runs in parallel.
  }, 20_000)

  it('back to latest: shows the latest page, and then behaves as usual', async () => {
    const { user, data, router } = await openLocated(20)
    await user.click(screen.getByRole('button', { name: 'Back to latest' }))
    await waitFor(() => expect(router.state.location.hash).toBe(''))
    await waitFor(() => expect(turnIds().at(-1)).toBe(data.messages.at(-1)!.run.id))
    expect(turnIds()).toHaveLength(25)
    expect(located()).toBeNull()
    expect(screen.queryByText('Viewing the located reply')).toBeNull()
  })

  it('the reply in the URL does not belong to this Agent: says it cannot be found and shows the latest conversation', async () => {
    const agent = makeAgent()
    const data = history(agent.id)
    await renderChat({ agent, handlers: data.handlers, path: `/agents/${agent.id}/chat#run-01890a5d-ac96-774b-bcce-b302099a8057` })
    expect(await screen.findByText('Couldn\'t find that reply; showing the latest conversation.')).toBeInTheDocument()
    await waitFor(() => expect(turnIds().at(-1)).toBe(data.messages.at(-1)!.run.id))
    expect(located()).toBeNull()
    expect(screen.queryByRole('alert', { name: /Couldn't load/ })).toBeNull()
  })

  it('a malformed anchor also says it cannot be found, without sending a locate request', async () => {
    const agent = makeAgent()
    const data = history(agent.id)
    await renderChat({ agent, handlers: data.handlers, path: `/agents/${agent.id}/chat#run-not-a-run` })
    expect(await screen.findByText('Couldn\'t find that reply; showing the latest conversation.')).toBeInTheDocument()
  })

  it('before the latest is loaded, new messages are only announced, not inserted into the conversation', async () => {
    const { emit } = await openLocated(20)
    const before = turnIds()
    const run = makeRun({ status: 'running', endedAt: null })
    await emit('message.created', { message: makeMessage({ text: 'A message just sent', run }) })
    await emit('run.updated', { run })
    expect(await screen.findByText('New messages')).toBeInTheDocument()
    expect(screen.queryByText('A message just sent')).toBeNull()
    expect(turnIds()).toEqual(before)
  })

  it('still locates the same reply after a reload', async () => {
    const { target, agent, data, unmount } = await openLocated(40)
    unmount()
    renderRoutes(routes, `/agents/${agent.id}/chat#run-${target}`)
    await waitFor(() => expect(located()).toHaveAttribute('data-run-id', target))
    expect(located()).toHaveTextContent('Answer 19')
    expect(data.messages[39].text).toBe('Answer 19')
  })
})
