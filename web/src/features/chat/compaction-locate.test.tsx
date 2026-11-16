import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultAgentPath, makeAgent, makeMessage, makeMessagePage, makeRun } from '@/test/api'
import { renderChat } from '@/test/chat'

// From “压缩失败” (Compaction failed) in the overview, click “查看压缩” (View compaction): the URL carries
// #run-<id of this compaction>. A compaction is not a turn, so the page returned by locate does not contain it;
// the chat page fetches this run through the run record endpoint and shows a note at the place where it happened.
const at = (minute: number) => new Date(Date.UTC(2026, 10, 12, 8, minute)).toISOString()
const turn = (question: string, minute: number) => {
  const run = makeRun({ queuedAt: at(minute), startedAt: at(minute), runningAt: at(minute), endedAt: at(minute) })
  return [makeMessage({ text: question, createdAt: at(minute), run }), makeMessage({ role: 'assistant', text: `Answer to ${question}`, createdAt: at(minute), run })]
}
const compactionRun = (overrides: Partial<Schemas['Run']> = {}) =>
  makeRun({
    status: 'failed',
    errorCode: 'pi_error',
    errorMessage: 'compaction failed',
    queuedAt: at(5),
    startedAt: at(5),
    runningAt: at(5),
    endedAt: at(6),
    compaction: { instructions: 'Keep the key decisions', outcome: null, skipReason: null, tokensBefore: null, estimatedTokensAfter: null },
    ...overrides,
  })

async function openCompaction(run: Schemas['Run'], timeline: () => Response = () => HttpResponse.json({ run, entries: [{ type: 'ended', at: run.endedAt, status: run.status, endReason: run.endReason, errorCode: run.errorCode }], usage: run.usage })) {
  const agent = makeAgent()
  const messages = [...turn('Question before compaction', 1), ...turn('Question after compaction', 9)]
  let timelineReads = 0
  const result = await renderChat({
    agent,
    path: `${defaultAgentPath(agent.id, 'chat')}#run-${run.id}`,
    handlers: [
      http.get(apiUrl(`/agents/${agent.id}/messages`), () => HttpResponse.json(makeMessagePage(messages, { turnsBefore: 3, turnsTotal: 5 }))),
      http.get(apiUrl(`/agents/${agent.id}/runs/${run.id}/timeline`), () => { timelineReads += 1; return timeline() }),
    ],
  })
  return { ...result, timelineReads: () => timelineReads }
}

const card = () => document.querySelector<HTMLElement>('[data-compaction-run]')

describe('Locating a manual compaction', () => {
  it('shows a highlighted note where it happened: compaction failed, the instructions, the reason, the run log expanded; it does not count as a turn', async () => {
    const run = compactionRun()
    await openCompaction(run)
    await waitFor(() => expect(card()).not.toBeNull())
    const note = card()!
    expect(note).toHaveAttribute('data-run-id', run.id)
    expect(note).toHaveAttribute('data-located')
    expect(note).toHaveClass('bg-tint-yellow')
    expect(within(note).getByText('The located manual compaction')).toHaveClass('sr-only')
    expect(note).toHaveTextContent('/compact Keep the key decisions')
    expect(note).toHaveTextContent('Manual compaction')
    expect(within(note).getByText('Compaction failed')).toBeInTheDocument()
    expect(note).toHaveTextContent('Agent reported an error: compaction failed')
    expect(note).toHaveTextContent('A manual compaction isn\'t a turn of the conversation and has no messages')
    // The run record is expanded by default and reuses the timeline from that same fetch.
    const toggle = within(note).getByRole('button', { name: /^Run log/ })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    // Placed between the two turns before and after the compaction.
    const before = screen.getByText('Question before compaction')
    const after = screen.getByText('Question after compaction')
    expect(before.compareDocumentPosition(note) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(note.compareDocumentPosition(after) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // It takes no number and no tick: there are still two turns, numbered on from 3.
    expect([...document.querySelectorAll('[data-turn-number]')].map((n) => n.textContent)).toEqual(['4', '5'])
    expect(within(screen.getByRole('navigation', { name: 'Conversation turns' })).getAllByRole('button')).toHaveLength(2)
  })

  it('when the compaction did not fail, says what happened: tokens before and after if it compacted, the reason if it did not', async () => {
    const run = compactionRun({ status: 'completed', errorCode: null, errorMessage: null, compaction: { instructions: '', outcome: 'skipped', skipReason: 'too_small', tokensBefore: null, estimatedTokensAfter: null } })
    await openCompaction(run)
    await waitFor(() => expect(card()).not.toBeNull())
    expect(card()).toHaveTextContent('The session is too short to need compacting')
    expect(card()).toHaveTextContent(/\/compact(?! )/)
    expect(within(card()!).queryByText('Compaction failed')).toBeNull()
  })

  it('does not fetch an extra run log when the located run is a normal reply', async () => {
    const agent = makeAgent()
    const messages = turn('A normal question', 1)
    let timelineReads = 0
    await renderChat({
      agent,
      path: `${defaultAgentPath(agent.id, 'chat')}#run-${messages[0].run.id}`,
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}/messages`), () => HttpResponse.json(makeMessagePage(messages))),
        http.get(apiUrl(`/agents/${agent.id}/runs/:runId/timeline`), () => { timelineReads += 1; return HttpResponse.json({ error: { code: 'not_found', message: 'x' } }, { status: 404 }) }),
      ],
    })
    await waitFor(() => expect(document.querySelector('[data-located]')).not.toBeNull())
    // A normal reply's run record, expanded by default, reads the timeline once; there must be no extra read to "find the compaction".
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(timelineReads).toBeLessThanOrEqual(1)
    expect(card()).toBeNull()
  })

  it('shows no note when the fetched run is not a manual compaction (or cannot be fetched)', async () => {
    const run = compactionRun({ compaction: undefined })
    const { timelineReads } = await openCompaction(run)
    await waitFor(() => expect(timelineReads()).toBe(1))
    await new Promise((resolve) => setTimeout(resolve, 30))
    expect(card()).toBeNull()
  })
})
