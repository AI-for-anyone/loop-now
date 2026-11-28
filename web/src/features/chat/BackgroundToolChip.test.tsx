import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, type Schemas } from '@/api/client'
import { makeBackgroundTask } from '@/test/api'
import { resetThemeForTest, setSkin } from '@/theme/store'
import { BackgroundToolChip, BackgroundToolChips } from './BackgroundToolChip'
import { ToolList } from './RunStatus'

const NOW = new Date('2026-10-12T08:02:00Z')

const tool = (toolCallId: string, toolName: string): Schemas['ToolEvent'] => ({
  toolCallId,
  toolName,
  state: 'succeeded',
  at: '2026-10-12T08:00:00Z',
})

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
})

afterEach(() => {
  vi.useRealTimers()
  window.localStorage.clear()
  resetThemeForTest()
})

describe('BackgroundToolChips in mono skin', () => {
  it('joins the quiet tool line after regular tools with a dot separator and groups same name and state in start order', () => {
    const tasks = [
      makeBackgroundTask({ id: 'a3', toolCallId: 'c3', kind: 'subagent', label: 'third', startedAt: '2026-10-12T08:00:30Z', status: 'running' }),
      makeBackgroundTask({ id: 'b1', toolCallId: 'c4', kind: 'command', label: 'npm test', startedAt: '2026-10-12T08:00:20Z', status: 'failed', exitCode: 1, endedAt: '2026-10-12T08:01:00Z' }),
      makeBackgroundTask({ id: 'a1', toolCallId: 'c1', kind: 'subagent', label: 'first', startedAt: '2026-10-12T08:00:10Z', status: 'running' }),
      makeBackgroundTask({ id: 'a2', toolCallId: 'c2', kind: 'subagent', label: 'second', startedAt: '2026-10-12T08:00:25Z', status: 'running' }),
      makeBackgroundTask({ id: 's1', toolCallId: 'c5', kind: 'command', label: 'sleep 60', startedAt: '2026-10-12T08:00:40Z', status: 'stopped', stopReason: 'person', endedAt: '2026-10-12T08:01:10Z' }),
    ]
    const events = [
      tool('r1', 'Read'),
      tool('c1', 'Agent'),
      tool('c2', 'Agent'),
      tool('c3', 'Agent'),
      tool('c4', 'Bash'),
      tool('c5', 'Bash'),
    ]
    const exclude = new Set(['c1', 'c2', 'c3', 'c4', 'c5'])
    const names = new Map(events.map((event) => [event.toolCallId, event.toolName]))

    render(<ToolList tools={events} exclude={exclude} after={<BackgroundToolChips tasks={tasks} toolNames={names} />} />)

    const list = screen.getByRole('list', { name: 'Tool calls' })
    const items = within(list).getAllByRole('listitem')
    expect(items).toHaveLength(4)

    // Regular tool first, followed by background groups in earliest-start order, each preceded by "·".
    expect(items[0].querySelector('[data-tool-visual]')).toHaveTextContent('Read')
    expect(items[1].querySelector('[data-tool-separator]')).toHaveTextContent('·')
    expect(items[2].querySelector('[data-tool-separator]')).toHaveTextContent('·')
    expect(items[3].querySelector('[data-tool-separator]')).toHaveTextContent('·')

    const agentBtn = within(items[1]).getByRole('button', { name: 'Agent Running in the background, 3 tasks' })
    expect(agentBtn.querySelector('[data-tool-text]')).toHaveTextContent('Agent×3Running in the background')
    expect(agentBtn.querySelector('[data-slot="tool-spinner"]')).not.toBeNull()
    expect(agentBtn.querySelector('[data-slot="tool-static-mark"]')).toHaveTextContent('…')
    expect(agentBtn.className).not.toContain('rounded-full')
    expect(agentBtn.className).toContain('text-ink-500')

    const failedBtn = within(items[2]).getByRole('button', { name: 'Bash Failed in the background: npm test' })
    expect(failedBtn.querySelector('[data-tool-text]')).toHaveTextContent('BashFailed in the background')
    expect(failedBtn.querySelector('[data-slot="tool-spinner"]')).toBeNull()
    expect(failedBtn.className).toContain('text-red-text')

    const stoppedBtn = within(items[3]).getByRole('button', { name: 'Bash Stopped: sleep 60' })
    expect(stoppedBtn.querySelector('[data-tool-text]')).toHaveTextContent('BashStopped')
    expect(stoppedBtn.className).toContain('text-ink-500')
  })

  it('omits the leading dot separator when no regular tools precede background groups', () => {
    const tasks = [
      makeBackgroundTask({ id: 'a1', toolCallId: 'c1', kind: 'subagent', label: 'first', startedAt: '2026-10-12T08:00:10Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:00Z' }),
      makeBackgroundTask({ id: 'a2', toolCallId: 'c2', kind: 'subagent', label: 'second', startedAt: '2026-10-12T08:00:20Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:10Z' }),
      makeBackgroundTask({ id: 'b1', toolCallId: 'c3', kind: 'command', label: 'lint', startedAt: '2026-10-12T08:00:30Z', status: 'failed', exitCode: 2, endedAt: '2026-10-12T08:01:20Z' }),
    ]
    const events = [tool('c1', 'Agent'), tool('c2', 'Agent'), tool('c3', 'Bash')]
    const exclude = new Set(['c1', 'c2', 'c3'])
    const names = new Map(events.map((event) => [event.toolCallId, event.toolName]))

    render(<ToolList tools={events} exclude={exclude} after={<BackgroundToolChips tasks={tasks} toolNames={names} />} />)

    const items = within(screen.getByRole('list', { name: 'Tool calls' })).getAllByRole('listitem')
    expect(items).toHaveLength(2)
    expect(items[0].querySelector('[data-tool-separator]')).toBeNull()
    expect(items[1].querySelector('[data-tool-separator]')).toHaveTextContent('·')

    const doneBtn = within(items[0]).getByRole('button', { name: 'Agent Finished in the background, 2 tasks' })
    expect(doneBtn.querySelector('[data-tool-text]')).toHaveTextContent('Agent×2Finished in the background')
    expect(doneBtn.className).toContain('text-ink-500')
  })

  it('opens single-task details for a group of one and lists each task with Details and Stop actions for a group of several', async () => {
    const user = userEvent.setup()
    const stops: Array<{ taskId: string; actionId: string }> = []
    const onStop = vi.fn(async (taskId: string, actionId: string) => {
      stops.push({ taskId, actionId })
      if (stops.length === 1) throw new ApiError({ status: 503, code: 'internal_error', serverMessage: 'The server stopped answering.' })
      return {}
    })

    const tasks = [
      makeBackgroundTask({ id: 'a1', toolCallId: 'c1', kind: 'subagent', label: 'Inspect harness core', startedAt: '2026-10-12T08:01:00Z', status: 'running' }),
      makeBackgroundTask({ id: 'a2', toolCallId: 'c2', kind: 'subagent', label: 'Inspect sandbox model', startedAt: '2026-10-12T08:00:30Z', status: 'running' }),
      makeBackgroundTask({ id: 'b1', toolCallId: 'c3', kind: 'command', label: 'npm run lint', startedAt: '2026-10-12T08:00:00Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:00:45Z', output: 'All clean' }),
    ]
    const names = new Map([
      ['c1', 'Agent'],
      ['c2', 'Agent'],
      ['c3', 'Bash'],
    ])

    render(
      <ToolList
        tools={[]}
        after={<BackgroundToolChips tasks={tasks} toolNames={names} onStop={onStop} />}
      />,
    )

    // Group of 1 opens single-task details directly.
    await user.click(screen.getByRole('button', { name: 'Bash Finished in the background: npm run lint' }))
    const singleDialog = await screen.findByRole('dialog', { name: 'Background task' })
    expect(singleDialog).toHaveTextContent('npm run lint')
    expect(singleDialog).toHaveTextContent('exit code 0')
    expect(singleDialog).toHaveTextContent('took 45 s')
    expect(singleDialog).toHaveTextContent('All clean')
    await user.keyboard('{Escape}')

    // Group of 2 opens the group list in start order (a2 before a1) with label, status, elapsed time, Details, and Stop.
    await user.click(screen.getByRole('button', { name: 'Agent Running in the background, 2 tasks' }))
    const groupDialog = await screen.findByRole('dialog', { name: 'Agent Running in the background, 2 tasks' })
    const rows = within(groupDialog).getAllByRole('listitem')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('Inspect sandbox model')
    expect(rows[0]).toHaveTextContent('Running in the background')
    expect(rows[0]).toHaveTextContent('Running for 1:30')
    expect(rows[1]).toHaveTextContent('Inspect harness core')
    expect(rows[1]).toHaveTextContent('Running for 1:00')

    // Stop on the first task: unknown error shows alert and turns into "Try again" repeating the same actionId.
    await user.click(within(rows[0]).getByRole('button', { name: 'Stop background task: Inspect sandbox model' }))
    expect(await within(rows[0]).findByRole('alert')).toBeInTheDocument()
    const retry = within(rows[0]).getByRole('button', { name: 'Stop background task: Inspect sandbox model' })
    expect(retry).toHaveTextContent('Try again')
    await user.click(retry)
    await waitFor(() => expect(stops).toHaveLength(2))
    expect(stops[0].taskId).toBe('a2')
    expect(stops[1].taskId).toBe('a2')
    expect(stops[1].actionId).toBe(stops[0].actionId)
    expect(await within(rows[0]).findByRole('button', { name: 'Stopping…' })).toBeDisabled()

    // Details drills into one task's full details view, and Back returns to the group list.
    await user.click(within(rows[1]).getByRole('button', { name: 'View details: Inspect harness core' }))
    expect(groupDialog).toHaveTextContent('Inspect harness core')
    expect(groupDialog).toHaveTextContent('Running for 1:00')
    expect(within(groupDialog).queryByText('Inspect sandbox model')).toBeNull()

    await user.click(within(groupDialog).getByRole('button', { name: 'Back to Agent list (2)' }))
    expect(within(groupDialog).getAllByRole('listitem')).toHaveLength(2)
    expect(within(groupDialog).getByText('Inspect sandbox model')).toBeInTheDocument()

    // Drill in again, close with Escape, and reopen: starts back on the list view.
    await user.click(within(groupDialog).getByRole('button', { name: 'View details: Inspect harness core' }))
    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Agent Running in the background, 2 tasks' }))
    const reopened = await screen.findByRole('dialog', { name: 'Agent Running in the background, 2 tasks' })
    expect(within(reopened).getAllByRole('listitem')).toHaveLength(2)
  })

  it('treats background_task_ended as already stopped without showing an error alert', async () => {
    const user = userEvent.setup()
    const onStop = vi.fn(async () => {
      throw new ApiError({ status: 409, code: 'background_task_ended', serverMessage: 'The background task has already ended.' })
    })
    const tasks = [
      makeBackgroundTask({ id: 'a1', toolCallId: 'c1', kind: 'subagent', label: 'one', startedAt: '2026-10-12T08:00:10Z', status: 'running' }),
      makeBackgroundTask({ id: 'a2', toolCallId: 'c2', kind: 'subagent', label: 'two', startedAt: '2026-10-12T08:00:20Z', status: 'running' }),
    ]
    const names = new Map([['c1', 'Agent'], ['c2', 'Agent']])

    render(<ToolList tools={[]} after={<BackgroundToolChips tasks={tasks} toolNames={names} onStop={onStop} />} />)
    await user.click(screen.getByRole('button', { name: 'Agent Running in the background, 2 tasks' }))
    const dialog = await screen.findByRole('dialog', { name: 'Agent Running in the background, 2 tasks' })
    await user.click(within(dialog).getByRole('button', { name: 'Stop background task: one' }))
    expect(await within(dialog).findByRole('button', { name: 'Stopping…' })).toBeDisabled()
    expect(within(dialog).queryByRole('alert')).toBeNull()
  })

  it('aligns name, count, and state on one shared baseline without self-baseline in both mono and Plate skins', () => {
    const tasks = [
      makeBackgroundTask({ id: 'a1', toolCallId: 'c1', kind: 'subagent', label: 'first', startedAt: '2026-10-12T08:00:10Z', status: 'running' }),
      makeBackgroundTask({ id: 'a2', toolCallId: 'c2', kind: 'subagent', label: 'second', startedAt: '2026-10-12T08:00:20Z', status: 'running' }),
    ]
    const names = new Map([['c1', 'Agent'], ['c2', 'Agent']])

    const { container, unmount } = render(<ToolList tools={[]} after={<BackgroundToolChips tasks={tasks} toolNames={names} />} />)
    const monoText = container.querySelector('[data-tool-text]')!
    expect(monoText).not.toBeNull()
    expect(monoText.className).toContain('inline-flex')
    expect(monoText.className).toContain('items-baseline')
    expect(Array.from(monoText.children).map((el) => el.textContent)).toEqual(['Agent', '×2', 'Running in the background'])
    expect(container.querySelector('.self-baseline')).toBeNull()
    unmount()

    setSkin('plate')
    const plateRender = render(<BackgroundToolChip task={tasks[0]} toolName="Agent" />)
    const plateText = plateRender.container.querySelector('[data-tool-text]')!
    expect(plateText).not.toBeNull()
    expect(plateText.className).toContain('inline-flex')
    expect(plateText.className).toContain('items-baseline')
    expect(Array.from(plateText.children).map((el) => el.textContent)).toEqual(['Agent', 'Running in the background'])
    expect(plateRender.container.querySelector('.self-baseline')).toBeNull()
  })
})

describe('BackgroundToolChips in Plate skin', () => {
  it('keeps individual ungrouped pills unchanged apart from baseline alignment', async () => {
    setSkin('plate')
    const user = userEvent.setup()
    const tasks = [
      makeBackgroundTask({ id: 'a1', toolCallId: 'c1', kind: 'subagent', label: 'first agent', startedAt: '2026-10-12T08:00:10Z', status: 'running' }),
      makeBackgroundTask({ id: 'a2', toolCallId: 'c2', kind: 'subagent', label: 'second agent', startedAt: '2026-10-12T08:00:20Z', status: 'running' }),
    ]
    const names = new Map([['c1', 'Agent'], ['c2', 'Agent']])

    render(
      <ToolList
        tools={[tool('r1', 'Read'), tool('c1', 'Agent'), tool('c2', 'Agent')]}
        exclude={new Set(['c1', 'c2'])}
        after={<BackgroundToolChips tasks={tasks} toolNames={names} />}
      />,
    )

    const list = screen.getByRole('list', { name: 'Tool calls' })
    const items = within(list).getAllByRole('listitem')
    expect(items).toHaveLength(3)
    expect(list.querySelector('[data-tool-separator]')).toBeNull()
    expect(list.querySelector('.self-baseline')).toBeNull()

    const firstChip = within(items[1]).getByRole('button', { name: 'Agent Running in the background: first agent' })
    const secondChip = within(items[2]).getByRole('button', { name: 'Agent Running in the background: second agent' })
    expect(firstChip.className).toContain('rounded-full')
    expect(firstChip.className).toContain('bg-tint-blue')
    expect(firstChip.querySelector('[data-tool-text]')?.className).toContain('items-baseline')
    expect(secondChip.className).toContain('rounded-full')

    await user.click(firstChip)
    const dialog = await screen.findByRole('dialog', { name: 'Background task' })
    expect(dialog).toHaveTextContent('first agent')
    expect(dialog).not.toHaveTextContent('second agent')
  })
})
