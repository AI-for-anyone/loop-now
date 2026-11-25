import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeBackgroundTask, makeMessage, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'

// Two minutes past the hour; tasks start at fixed times before it, so their running clocks are known.
const NOW = new Date('2026-10-12T08:02:00Z')

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']> = {}) {
  return { status: makeStatus(agentId, status), currentText: null, queuedRuns: [], queueItems: [] }
}

function turn(text: string, reply: string, run = makeRun()) {
  return [makeMessage({ text, run }), makeMessage({ role: 'assistant', text: reply, run })]
}

type StopCall = { path: string; actionId: string }

// Answers the two stop endpoints with the given replies, in order, and records each call.
function stopEndpoints(answers: Array<(path: string) => Response>) {
  const calls: StopCall[] = []
  const answer = async ({ request }: { request: Request }) => {
    const body = (await request.json()) as { actionId: string }
    const path = new URL(request.url).pathname
    calls.push({ path, actionId: body.actionId })
    return (answers.shift() ?? (() => HttpResponse.json(errorBody('internal_error', 'no answer'), { status: 500 })))(path)
  }
  return { calls, handlers: [http.post(apiUrl('/agents/:agentId/background-tasks/:taskId/stop'), answer), http.post(apiUrl('/agents/:agentId/background-tasks/stop'), answer)] }
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
})
afterEach(() => vi.useRealTimers())

describe('The background tasks mark and list in the Agent header', () => {
  it('shows each running task with its label, the reply that started it and how long it has run, and follows the live list', async () => {
    const first = makeRun()
    const second = makeRun()
    const build = makeBackgroundTask({ label: 'npm run build', runId: second.id, startedAt: '2026-10-12T08:00:37Z' })
    const devServer = makeBackgroundTask({ label: 'npm run dev -- --port 5173', kind: 'subagent', runId: 'not-loaded', startedAt: '2026-10-12T07:00:00Z' })
    const { user, agent, emit } = await renderChat({ messages: [...turn('Start the build', 'Started it.', first), ...turn('And the dev server', 'Running.', second)] })
    expect(screen.queryByRole('button', { name: /background tasks? (is|are) running/ })).toBeNull()

    await emit('snapshot', snapshot(agent.id, { backgroundTasks: [build, devServer] }))
    const mark = await screen.findByRole('button', { name: '2 background tasks are running; open to see or stop them' })
    expect(mark).toHaveTextContent('Background 2')

    await user.click(mark)
    const list = await screen.findByRole('list', { name: 'Background tasks' })
    const [buildRow, devRow] = within(list).getAllByRole('listitem')
    expect(buildRow).toHaveTextContent('npm run build')
    expect(buildRow).toHaveTextContent('Command')
    expect(within(buildRow).getByText('Running for 1:23')).toBeInTheDocument()
    // The reply is loaded: its number; the link locates it in the conversation.
    expect(within(buildRow).getByRole('link', { name: 'Go to reply #2' })).toHaveAttribute('href', expect.stringMatching(new RegExp(`/chat#run-${second.id}$`)))
    expect(devRow).toHaveTextContent('Sub-agent')
    expect(within(devRow).getByText('Running for 1:02:00')).toBeInTheDocument()
    // Not loaded: named by its time instead.
    expect(within(devRow).getByRole('link', { name: /^Go to the reply at / })).toHaveAttribute('href', expect.stringMatching(/#run-not-loaded$/))
    expect(screen.getByRole('button', { name: 'Stop all (2)' })).toBeInTheDocument()

    await emit('background_tasks.updated', { tasks: [build] })
    expect(await screen.findByRole('button', { name: '1 background task is running; open to see or stop them' })).toHaveTextContent('Background 1')
    expect(within(screen.getByRole('list', { name: 'Background tasks' })).getAllByRole('listitem')).toHaveLength(1)
    // One task: no Stop all.
    expect(screen.queryByRole('button', { name: /^Stop all/ })).toBeNull()

    await emit('background_tasks.updated', { tasks: [] })
    await waitFor(() => expect(screen.queryByRole('button', { name: /background tasks? (is|are) running/ })).toBeNull())
  })

  it('before the live stream says anything, the header shows the tasks from the status', async () => {
    const task = makeBackgroundTask({ label: 'pytest -x' })
    const { user } = await renderChat({ status: { backgroundTasks: [task] } })
    await user.click(await screen.findByRole('button', { name: '1 background task is running; open to see or stop them' }))
    expect(await screen.findByText('pytest -x')).toBeInTheDocument()
  })

  it('Stop asks to stop that task with an actionId and says it is stopping until the list drops it', async () => {
    const task = makeBackgroundTask({ label: 'sleep 600' })
    const stop = stopEndpoints([() => HttpResponse.json({ ...task, stopRequestedAt: NOW.toISOString() })])
    const { user, agent, emit } = await renderChat({ handlers: stop.handlers })
    await emit('snapshot', snapshot(agent.id, { backgroundTasks: [task] }))
    await user.click(await screen.findByRole('button', { name: /open to see or stop them/ }))
    await user.click(await screen.findByRole('button', { name: 'Stop background task: sleep 600' }))

    await waitFor(() => expect(stop.calls).toHaveLength(1))
    expect(stop.calls[0].path).toBe(`/api/v1/agents/${agent.id}/background-tasks/${task.id}/stop`)
    expect(stop.calls[0].actionId).toMatch(/^[0-9a-f-]{36}$/)
    expect(await screen.findByRole('button', { name: 'Stopping…' })).toBeDisabled()

    await emit('background_tasks.updated', { tasks: [] })
    await waitFor(() => expect(screen.queryByText('sleep 600')).toBeNull())
  })

  it('Stop all asks to stop every task of the Agent', async () => {
    const tasks = [makeBackgroundTask({ label: 'a' }), makeBackgroundTask({ label: 'b' })]
    const stop = stopEndpoints([() => HttpResponse.json({ tasks: tasks.map((task) => ({ ...task, status: 'stopped', stopReason: 'person', endedAt: NOW.toISOString() })) })])
    const { user, agent, emit } = await renderChat({ handlers: stop.handlers })
    await emit('snapshot', snapshot(agent.id, { backgroundTasks: tasks }))
    await user.click(await screen.findByRole('button', { name: /open to see or stop them/ }))
    await user.click(await screen.findByRole('button', { name: 'Stop all (2)' }))
    await waitFor(() => expect(stop.calls.map((call) => call.path)).toEqual([`/api/v1/agents/${agent.id}/background-tasks/stop`]))
    expect(await screen.findAllByRole('button', { name: 'Stopping…' })).toHaveLength(2)
  })

  it('a task that had already ended is no error; when the outcome is unknown, Try again repeats the same actionId', async () => {
    const ended = makeBackgroundTask({ label: 'make test' })
    const flaky = makeBackgroundTask({ label: 'make lint' })
    const stop = stopEndpoints([
      () => HttpResponse.json(errorBody('background_task_ended', 'The background task has already ended.', { details: { task: { ...ended, status: 'completed' } } }), { status: 409 }),
      () => HttpResponse.json(errorBody('internal_error', 'The server stopped answering.'), { status: 503 }),
      () => HttpResponse.json({ ...flaky, stopRequestedAt: NOW.toISOString() }),
    ])
    const { user, agent, emit } = await renderChat({ handlers: stop.handlers })
    await emit('snapshot', snapshot(agent.id, { backgroundTasks: [ended, flaky] }))
    await user.click(await screen.findByRole('button', { name: /open to see or stop them/ }))

    await user.click(await screen.findByRole('button', { name: 'Stop background task: make test' }))
    await waitFor(() => expect(stop.calls).toHaveLength(1))
    expect(await screen.findByRole('button', { name: 'Stopping…' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()

    await user.click(screen.getByRole('button', { name: 'Stop background task: make lint' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('The server stopped answering.')
    const retry = screen.getByRole('button', { name: 'Stop background task: make lint' })
    expect(retry).toHaveTextContent('Try again')
    await user.click(retry)
    await waitFor(() => expect(stop.calls).toHaveLength(3))
    expect(stop.calls[2].actionId).toBe(stop.calls[1].actionId)
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Stopping…' })).toHaveLength(2))
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
