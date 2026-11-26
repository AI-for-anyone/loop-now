import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { tasksPath } from '@/api/tasks'
import { apiUrl, defaultProject, makeBackgroundTask, projectsUrl } from '@/test/api'
import { FakeEventSource } from '@/test/fake-event-source'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { taskRunTimeline } from '@/test/task-inbox'
import { makeTaskDetail, makeTaskMessage, nodeId, runId, taskId } from '@/test/tasks'

const NOW = new Date('2026-10-14T08:02:00Z')

type Body = { actionId: string; expectedAgentId: string; expectedRuntimeId: string }

// The current node of a task, whose turn is running and waits for its background tasks; the reply's record says which tools went to the
// background and how they ended.
function setup(running: Schemas['BackgroundTask'][], entries: Schemas['TimelineEntry'][] = [], extra: HttpHandler[] = []) {
  const detail = makeTaskDetail(defaultProject.id)
  const node = detail.nodes[1]
  const reply: Schemas['TaskMessage'] = { ...makeTaskMessage(nodeId, 'Building now.'), status: 'running' }
  const stops: Array<{ path: string; body: Body }> = []
  server.use(
    // Within one call the first match wins: the test's own handlers come first.
    ...extra,
    ...baseHandlers([]),
    http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: params.node, agentId: node.agentId, runtimeId: node.runtimeId, canSend: true, reason: null, handlingHint: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), ({ params }) => HttpResponse.json({ messages: params.node === nodeId ? [reply] : [], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/inbox-messages`), () => HttpResponse.json({ items: [], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/queue`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: params.node, items: [] })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/background-tasks`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: params.node, runtimeId: node.runtimeId, tasks: params.node === nodeId ? running : [] })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/runs/:run/timeline`), () => HttpResponse.json(taskRunTimeline({ entries, run: { ...taskRunTimeline().run, status: 'running', endedAt: null } }))),
    http.post(apiUrl(`/tasks/${taskId}/nodes/:node/background-tasks/:task/stop`), async ({ request }) => {
      stops.push({ path: new URL(request.url).pathname, body: (await request.json()) as Body })
      return HttpResponse.json({ ...running[0], stopRequestedAt: NOW.toISOString() })
    }),
  )
  const user = userEvent.setup()
  renderRoutes(routes, `${tasksPath(defaultProject.name)}/${taskId}`)
  const events = () => FakeEventSource.instances.find((source) => source.url.endsWith(`/tasks/${taskId}/events`))!
  return { user, node, stops, events }
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
})
afterEach(() => vi.useRealTimers())

describe('Background tasks of a task node', () => {
  it('the current node shows the tasks its turn waits for, stops one for that node, and follows the task stream', async () => {
    const build = makeBackgroundTask({ label: 'cargo build --release', startedAt: '2026-10-14T08:00:30Z' })
    const lint = makeBackgroundTask({ label: 'cargo clippy' })
    const { user, node, stops, events } = setup([build, lint])

    const mark = await screen.findByRole('button', { name: '2 background tasks are running; open to see or stop them' })
    await user.click(mark)
    expect(await screen.findByText('This step finishes only once they end; stopping one does not stop the step')).toBeInTheDocument()
    const list = screen.getByRole('list', { name: 'Background tasks' })
    expect(within(list).getAllByRole('listitem')[0]).toHaveTextContent('cargo build --release')
    expect(within(list).getByText('Running for 1:30')).toBeInTheDocument()

    await user.click(within(list).getByRole('button', { name: 'Stop background task: cargo build --release' }))
    await waitFor(() => expect(stops).toHaveLength(1))
    expect(stops[0].path).toBe(`/api/v1/tasks/${taskId}/nodes/${nodeId}/background-tasks/${build.id}/stop`)
    expect(stops[0].body).toEqual({ actionId: expect.stringMatching(/^[0-9a-f-]{36}$/), expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId })

    act(() => events().emit('task.node.background_tasks.updated', { taskId, nodeRunId: nodeId, runtimeId: node.runtimeId, tasks: [lint] }, '20'))
    expect(await screen.findByRole('button', { name: '1 background task is running; open to see or stop them' })).toBeInTheDocument()
    act(() => events().emit('task.node.background_tasks.updated', { taskId, nodeRunId: nodeId, runtimeId: node.runtimeId, tasks: [] }, '21'))
    await waitFor(() => expect(screen.queryByRole('button', { name: /background tasks? (is|are) running/ })).toBeNull())
  })

  it('an event that comes while the list is being read wins over the older read', async () => {
    const build = makeBackgroundTask({ label: 'cargo build' })
    let release = () => {}
    const gate = new Promise<void>((resolve) => (release = resolve))
    let reads = 0
    const slowRead = http.get(apiUrl(`/tasks/${taskId}/nodes/:node/background-tasks`), async ({ params }) => {
      reads += 1
      await gate
      return HttpResponse.json({ taskId, nodeRunId: params.node, runtimeId: 'old-read', tasks: params.node === nodeId ? [build] : [] })
    })
    const { node, events } = setup([build], [], [slowRead])
    await waitFor(() => expect(reads).toBe(1))
    await waitFor(() => expect(events()).toBeDefined())
    await screen.findByText('Building now.')
    act(() => events().emit('task.node.background_tasks.updated', { taskId, nodeRunId: nodeId, runtimeId: node.runtimeId, tasks: [] }, '30'))
    await act(async () => { release(); await new Promise((resolve) => setTimeout(resolve, 50)) })
    expect(screen.queryByRole('button', { name: /background tasks? (is|are) running/ })).toBeNull()
  })

  it('a refusal other than “already ended” is said under the task, and it is not shown as stopping', async () => {
    const build = makeBackgroundTask({ label: 'cargo build' })
    const { user } = setup([build])
    server.use(http.post(apiUrl(`/tasks/${taskId}/nodes/:node/background-tasks/:task/stop`), () => HttpResponse.json({ error: { code: 'task_conflict', message: 'The node has moved on.' } }, { status: 409 })))
    await user.click(await screen.findByRole('button', { name: /open to see or stop them/ }))
    await user.click(await screen.findByRole('button', { name: 'Stop background task: cargo build' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('The node has moved on.')
    expect(screen.getByRole('button', { name: 'Stop background task: cargo build' })).toBeEnabled()
  })

  it('stopping the node’s turn stops its background tasks too, and the box says so first', async () => {
    setup([makeBackgroundTask()])
    expect(await screen.findByText('Stopping or interrupting also stops 1 background task')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Stop (also stops 1 background task)' })).toBeInTheDocument()
    expect(screen.queryByText('Stop the current reply only')).toBeNull()
  })

  it('the node’s reply shows its backgrounded tools as chips from its record: running, then how they ended, details on click', async () => {
    const running = makeBackgroundTask({ id: 'bg-run', label: 'cargo test', startedAt: '2026-10-14T08:01:00Z' })
    const entries: Schemas['TimelineEntry'][] = [
      { type: 'tool', at: '2026-10-14T08:00:00Z', toolCallId: 'call_1', toolName: 'bash', command: 'cargo test', state: 'succeeded', endedAt: '2026-10-14T08:00:01Z', durationMs: 1000 },
      { type: 'background_task_started', at: '2026-10-14T08:00:01Z', taskId: 'bg-run', kind: 'command', label: 'cargo test', toolCallId: 'call_1' },
      { type: 'tool', at: '2026-10-14T08:00:02Z', toolCallId: 'call_2', toolName: 'bash', command: 'cargo fmt --check', state: 'succeeded', endedAt: '2026-10-14T08:00:03Z', durationMs: 1000 },
      { type: 'background_task_started', at: '2026-10-14T08:00:03Z', taskId: 'bg-fmt', kind: 'command', label: 'cargo fmt --check', toolCallId: 'call_2' },
      { type: 'background_task_ended', at: '2026-10-14T08:00:13Z', taskId: 'bg-fmt', status: 'failed', exitCode: 1, stopReason: null, output: 'Diff in src/main.rs' },
    ]
    const { user } = setup([running], entries)
    const tools = await screen.findByRole('list', { name: 'Tool calls' })
    const test = within(tools).getByRole('button', { name: 'bash Running in the background: cargo test' })
    expect(test.querySelector('[data-slot="tool-spinner"]')).not.toBeNull()
    const fmt = within(tools).getByRole('button', { name: 'bash Failed in the background: cargo fmt --check' })
    // The backgrounded tools are not plain chips as well.
    expect(within(tools).getAllByRole('listitem')).toHaveLength(2)
    // The live list's start time drives the clock in the details.
    await user.click(test)
    expect(await screen.findByRole('dialog', { name: 'Background task' })).toHaveTextContent('Running for 1:00')
    await user.keyboard('{Escape}')
    await user.click(fmt)
    const details = await screen.findByRole('dialog', { name: 'Background task' })
    expect(details).toHaveTextContent('exit code 1')
    expect(details).toHaveTextContent('Diff in src/main.rs')
    void runId
  })

  it('the mark is in the node’s message box bar, not in its header', async () => {
    setup([makeBackgroundTask({ label: 'cargo build' })])
    const mark = await screen.findByRole('button', { name: '1 background task is running; open to see or stop them' })
    expect(mark.closest('[data-node-status]')).toBeNull()
    const composer = document.querySelector('[data-node-composer]')!.closest('.grid')!
    expect(composer).toContainElement(mark)
    expect(document.querySelectorAll('[data-background-mark]')).toHaveLength(1)
  })
})

it('a task page whose test registers no background-task read still gets an empty list, not a failed request', async () => {
  // Without the shared default the read fails like a lost connection, and the page shows the connection banner (an alert).
  const response = await fetch(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/background-tasks`))
  expect(response.status).toBe(200)
  expect(await response.json()).toMatchObject({ taskId, nodeRunId: nodeId, tasks: [] })
})
