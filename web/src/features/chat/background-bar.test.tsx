import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, makeBackgroundTask, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'

const NOW = new Date('2026-10-12T08:02:00Z')

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']> = {}) {
  return { status: makeStatus(agentId, status), currentText: null, queuedRuns: [], queueItems: [] }
}
const bar = () => document.querySelector<HTMLElement>('[data-chat-bar]')!
const header = () => document.querySelector('[data-agent-status]')!.closest('header')!

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
})
afterEach(() => vi.useRealTimers())

describe('The background mark in the bar under the message box', () => {
  it('shows a shell mark with the count right after the runner chip while tasks run, and goes at zero', async () => {
    const { agent, emit } = await renderChat()
    expect(within(bar()).queryByRole('button', { name: /background tasks? (is|are) running/ })).toBeNull()

    await emit('snapshot', snapshot(agent.id, { backgroundTasks: [makeBackgroundTask()] }))
    const mark = await within(bar()).findByRole('button', { name: '1 background task is running; open to see or stop them' })
    expect(mark).toHaveTextContent(/^1$/)
    expect(mark.querySelector('svg')).not.toBeNull()
    // Right after the runner chip.
    const runner = within(bar()).getByRole('button', { name: /^Runs on:/ })
    expect(runner.compareDocumentPosition(mark) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

    await emit('background_tasks.updated', { tasks: [makeBackgroundTask(), makeBackgroundTask(), makeBackgroundTask()] })
    expect(await within(bar()).findByRole('button', { name: '3 background tasks are running; open to see or stop them' })).toHaveTextContent(/^3$/)

    await emit('background_tasks.updated', { tasks: [] })
    await waitFor(() => expect(within(bar()).queryByRole('button', { name: /background tasks? (is|are) running/ })).toBeNull())
  })

  it('opens the panel from the mark, and Stop calls the existing endpoint', async () => {
    const task = makeBackgroundTask({ label: 'sleep 600' })
    const stops: string[] = []
    const { user, agent, emit } = await renderChat({
      handlers: [http.post(apiUrl('/agents/:agentId/background-tasks/:taskId/stop'), ({ request }) => { stops.push(new URL(request.url).pathname); return HttpResponse.json({ ...task, stopRequestedAt: NOW.toISOString() }) })],
    })
    await emit('snapshot', snapshot(agent.id, { backgroundTasks: [task] }))
    await user.click(await within(bar()).findByRole('button', { name: /open to see or stop them/ }))
    const panel = await screen.findByRole('dialog', { name: 'Background tasks' })
    expect(within(panel).getByText('sleep 600')).toBeInTheDocument()
    await user.click(within(panel).getByRole('button', { name: 'Stop background task: sleep 600' }))
    await waitFor(() => expect(stops).toEqual([`/api/v1/agents/${agent.id}/background-tasks/${task.id}/stop`]))
  })

  it('the Agent header has no background mark', async () => {
    const { agent, emit } = await renderChat({ status: { backgroundTasks: [makeBackgroundTask()] } })
    await emit('snapshot', snapshot(agent.id, { backgroundTasks: [makeBackgroundTask()] }))
    await within(bar()).findByRole('button', { name: /open to see or stop them/ })
    expect(within(header()).queryByRole('button', { name: /background tasks? (is|are) running/ })).toBeNull()
    expect(document.querySelectorAll('[data-background-mark]')).toHaveLength(1)
  })
})
