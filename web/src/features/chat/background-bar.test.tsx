import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, makeBackgroundTask, makeMessage, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'

const NOW = new Date('2026-10-12T08:02:00Z')

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']> = {}) {
  return { status: makeStatus(agentId, status), currentText: null, queuedRuns: [], queueItems: [] }
}
function turn(text: string, reply: string, run: Schemas['Run']) {
  return [makeMessage({ text, run }), makeMessage({ role: 'assistant', text: reply, run })]
}
const tool = (toolCallId: string, toolName: string): Schemas['ToolEvent'] => ({ toolCallId, toolName, state: 'succeeded', at: '2026-10-12T08:00:00Z' })
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
    const runner = bar().querySelector<HTMLElement>('[data-composer-menu-trigger="model"]')!
    expect(runner).not.toBeNull()
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

describe('A background tool in a reply is a compact chip', () => {
  it('runs as “Bash Running in the background” with a spinner and no clock, then shows how it ended; the details are on click', async () => {
    const task = makeBackgroundTask({ label: 'npm test', toolCallId: 'toolu_test', startedAt: '2026-10-12T08:00:37Z' })
    const run = makeRun({ tools: [tool('toolu_read', 'Read'), tool('toolu_test', 'Bash')] })
    const running = { ...task, runId: run.id }
    run.backgroundTasks = [running]
    const { user, emit } = await renderChat({ messages: turn('Run the tests', 'They are running.', run) })

    const tools = screen.getByRole('list', { name: 'Tool calls' })
    const chip = within(tools).getByRole('button', { name: 'Bash Running in the background: npm test' })
    expect(chip).toHaveTextContent(/BashRunning in the background$/)
    expect(chip.querySelector('[data-slot="tool-spinner"]')).not.toBeNull()
    // A chip, not a card: no clock in the reply, and one chip for the tool.
    expect(within(tools).queryByText(/1:23/)).toBeNull()
    expect(within(tools).getAllByText('Bash')).toHaveLength(1)

    await user.click(chip)
    const details = await screen.findByRole('dialog', { name: 'Background task' })
    expect(within(details).getByText('npm test')).toBeInTheDocument()
    expect(within(details).getByText('Running for 1:23')).toBeInTheDocument()
    await user.keyboard('{Escape}')

    const ended: Schemas['BackgroundTask'] = { ...running, status: 'failed', exitCode: 1, endedAt: '2026-10-12T08:01:49Z', output: 'FAIL src/a.test.ts\n1 failed' }
    await emit('run.updated', { run: { ...run, backgroundTasks: [ended] } })
    const failed = await within(tools).findByRole('button', { name: 'Bash Failed in the background: npm test' })
    expect(failed.querySelector('[data-slot="tool-spinner"]')).toBeNull()
    await user.click(failed)
    const after = await screen.findByRole('dialog', { name: 'Background task' })
    expect(after).toHaveTextContent('exit code 1')
    expect(after).toHaveTextContent('took 1 min 12 s')
    expect(within(after).getByText('Output (the last part)')).toBeInTheDocument()
    expect(within(after).getByText(/FAIL src\/a\.test\.ts/)).toBeVisible()
  })

  it.each([
    ['completed', { status: 'completed', exitCode: 0 }, 'Finished in the background', 'exit code 0'],
    ['stopped', { status: 'stopped', stopReason: 'settings_changed' }, 'Stopped', 'stopped when a settings change restarted the CLI'],
    ['unknown', { status: 'unknown' }, 'Outcome unknown', 'Loop Now restarted, so how it ended is unknown'],
  ] as const)('an ended task (%s) says how it ended on the chip and why in its details', async (_, change, status, detail) => {
    const task = makeBackgroundTask({ label: 'tail -f app.log', ...change, endedAt: '2026-10-12T08:01:00Z' })
    const { user } = await renderChat({ messages: turn('Go', 'Started.', makeRun({ backgroundTasks: [task] })) })
    // No tool name known: the chip names the kind.
    const chip = screen.getByRole('button', { name: `Command ${status}: tail -f app.log` })
    await user.click(chip)
    expect(await screen.findByRole('dialog', { name: 'Background task' })).toHaveTextContent(detail)
  })

  it('a task asked to stop says it is stopping', async () => {
    const task = makeBackgroundTask({ label: 'make', stopRequestedAt: '2026-10-12T08:01:00Z' })
    await renderChat({ messages: turn('Go', 'Started.', makeRun({ backgroundTasks: [task] })) })
    expect(screen.getByRole('button', { name: 'Command Stopping…: make' })).toBeInTheDocument()
  })

  it('groups several running background tasks of the same tool on the quiet tool line and stops one from the group popover', async () => {
    const run = makeRun({
      tools: [
        tool('toolu_read', 'Read'),
        tool('toolu_a1', 'Agent'),
        tool('toolu_a2', 'Agent'),
        tool('toolu_a3', 'Agent'),
      ],
    })
    const tasks = [
      makeBackgroundTask({ id: 'bg-a1', kind: 'subagent', label: 'Audit harness', runId: run.id, toolCallId: 'toolu_a1', startedAt: '2026-10-12T08:00:30Z' }),
      makeBackgroundTask({ id: 'bg-a2', kind: 'subagent', label: 'Audit SDK', runId: run.id, toolCallId: 'toolu_a2', startedAt: '2026-10-12T08:00:40Z' }),
      makeBackgroundTask({ id: 'bg-a3', kind: 'subagent', label: 'Audit sandbox', runId: run.id, toolCallId: 'toolu_a3', startedAt: '2026-10-12T08:00:50Z' }),
    ]
    run.backgroundTasks = tasks
    const stops: string[] = []
    const { user, agent } = await renderChat({
      messages: turn('Investigate', 'Started three sub-agents.', run),
      handlers: [
        http.post(apiUrl('/agents/:agentId/background-tasks/:taskId/stop'), ({ request }) => {
          stops.push(new URL(request.url).pathname)
          return HttpResponse.json({ ...tasks[1], stopRequestedAt: NOW.toISOString() })
        }),
      ],
    })

    const tools = screen.getByRole('list', { name: 'Tool calls' })
    const groupBtn = within(tools).getByRole('button', { name: 'Agent Running in the background, 3 tasks' })
    expect(groupBtn.querySelector('[data-tool-text]')).toHaveTextContent('Agent×3Running in the background')

    await user.click(groupBtn)
    const dialog = await screen.findByRole('dialog', { name: 'Agent Running in the background, 3 tasks' })
    expect(within(dialog).getAllByRole('listitem')).toHaveLength(3)
    await user.click(within(dialog).getByRole('button', { name: 'Stop background task: Audit SDK' }))
    await waitFor(() => expect(stops).toEqual([`/api/v1/agents/${agent.id}/background-tasks/bg-a2/stop`]))
  })
})
