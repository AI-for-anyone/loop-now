import { screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { makeAgent, makeBackgroundTask, makeMessage, makeRun } from '@/test/api'
import { renderChat } from '@/test/chat'
import { resetThemeForTest, setSkin } from '@/theme/store'
import { buildSteps } from './timeline-steps'
import { runtimeName } from '@/test/runtimes'
import { turnPreview } from './ruler'

const NOW = new Date('2026-10-12T08:02:00Z')

function turn(text: string, reply: string, run: Schemas['Run'], origin: Schemas['MessageOrigin'] = 'console', createdAt?: string) {
  return [makeMessage({ text, run, origin, ...(createdAt ? { createdAt } : {}) }), makeMessage({ role: 'assistant', text: reply, run, origin })]
}

const tool = (toolCallId: string, toolName: string): Schemas['ToolEvent'] => ({ toolCallId, toolName, state: 'succeeded', at: '2026-10-12T08:00:00Z' })

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
})
afterEach(() => {
  vi.useRealTimers()
  window.localStorage.clear()
  resetThemeForTest()
})

describe('Background work in the replies', () => {
  it('a reply the CLI wrote by itself shows one compact chip instead of the command, with its details on click and the time at the end', async () => {
    const command = 'cd /Users/dev/project && sleep 30; for i in $(seq 1 60); do\n  gh pr view 95 --json statusCheckRollup\ndone'
    const first = makeRun({ tools: [tool('toolu_bash', 'Bash')] })
    const tests = makeBackgroundTask({
      label: command,
      runId: first.id,
      toolCallId: 'toolu_bash',
      startedAt: '2026-10-12T08:00:00Z',
      status: 'completed',
      exitCode: 0,
      endedAt: '2026-10-12T08:01:12Z',
      output: 'PASS src/a.test.ts (128 passed)',
    })
    first.backgroundTasks = [tests]
    const followUp = makeRun({ triggerBackgroundTasks: [tests], selection: null })
    const followUpAt = '2026-10-12T08:01:15Z'
    const { user } = await renderChat({
      messages: [
        ...turn('Run the tests', 'Running.', first),
        ...turn(`Background task "${command}" completed with exit code 0.`, 'All 128 tests passed.', followUp, 'background', followUpAt),
      ],
    })

    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent('After background work ended, Claude Code went on:')
    // The command text never appears in the heading itself (or its tooltip).
    expect(note).not.toHaveTextContent(/cd \/Users/)
    expect(note).not.toHaveTextContent(/gh pr view/)
    expect(note).not.toHaveAttribute('title')

    const chip = within(note).getByRole('button', { name: /^Bash Finished in the background: cd \/Users\/dev\/project/ })
    expect(chip).toHaveTextContent(/^BashFinished in the background$/)
    expect(within(note).getAllByRole('button')).toHaveLength(1)

    const time = note.querySelector('time')
    expect(time).not.toBeNull()
    expect(time).toHaveAttribute('datetime', followUpAt)
    expect(time).toHaveTextContent(t.chat.time(followUpAt))

    await user.click(chip)
    const details = await screen.findByRole('dialog', { name: 'Background task' })
    expect(details).toHaveTextContent(/cd \/Users\/dev\/project/)
    expect(details).toHaveTextContent('exit code 0')
    expect(details).toHaveTextContent('took 1 min 12 s')
    expect(within(details).getByText('Output (the last part)')).toBeInTheDocument()
    expect(within(details).getByText('PASS src/a.test.ts (128 passed)')).toBeVisible()

    // The generated text is not shown as a message from the person.
    expect(screen.queryByText(/Background task "cd \/Users/)).toBeNull()
    expect(screen.getByText('All 128 tests passed.')).toBeInTheDocument()
  })

  it('two ended tasks show two chips without +N and keep their commands out of the heading', async () => {
    const tasks = [
      makeBackgroundTask({ label: 'make build', kind: 'command', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:00Z' }),
      makeBackgroundTask({ label: 'inspect logs', kind: 'subagent', status: 'failed', exitCode: 2, endedAt: '2026-10-12T08:01:10Z' }),
    ]
    const followUp = makeRun({ triggerBackgroundTasks: tasks })
    await renderChat({ messages: turn('generated', 'Both are done.', followUp, 'background') })

    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent('After background work ended, Claude Code went on:')
    expect(note).not.toHaveTextContent('make build')
    expect(note).not.toHaveTextContent('inspect logs')
    expect(within(note).getAllByRole('button')).toHaveLength(2)
    expect(within(note).getByRole('button', { name: 'Command Finished in the background: make build' })).toBeInTheDocument()
    expect(within(note).getByRole('button', { name: 'Sub-agent Failed in the background: inspect logs' })).toBeInTheDocument()
    expect(within(note).queryByText(/^\+/)).toBeNull()
  })

  it('more than two ended tasks show at most two chips and a +N button that opens the rest', async () => {
    const tasks = [
      makeBackgroundTask({ label: 'make a', kind: 'command', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:00:30Z' }),
      makeBackgroundTask({ label: 'make b', kind: 'command', status: 'failed', exitCode: 2, endedAt: '2026-10-12T08:00:40Z' }),
      makeBackgroundTask({ label: 'make c --verbose', kind: 'command', status: 'stopped', stopReason: 'person', endedAt: '2026-10-12T08:00:50Z', output: 'Stopped midway' }),
      makeBackgroundTask({ label: 'make d --check', kind: 'subagent', status: 'unknown', endedAt: '2026-10-12T08:01:00Z' }),
    ]
    const followUp = makeRun({ triggerBackgroundTasks: tasks })
    const { user } = await renderChat({ messages: turn('generated', 'All four ended.', followUp, 'background') })

    const note = await screen.findByRole('note')
    for (const command of ['make a', 'make b', 'make c', 'make d']) expect(note).not.toHaveTextContent(command)
    expect(within(note).getAllByRole('button')).toHaveLength(3)
    expect(within(note).getByRole('button', { name: 'Command Finished in the background: make a' })).toBeInTheDocument()
    expect(within(note).getByRole('button', { name: 'Command Failed in the background: make b' })).toBeInTheDocument()

    const more = within(note).getByRole('button', { name: 'Show 2 more ended background tasks' })
    expect(more).toHaveTextContent('+2')
    expect(more.className).not.toContain('rounded-full')
    expect(more.className).toContain('focus-visible:ring-inset')
    await user.click(more)
    const rest = await screen.findByRole('dialog', { name: '2 more background tasks' })
    expect(rest).toHaveTextContent('make c --verbose')
    expect(rest).toHaveTextContent('stopped by you')
    expect(rest).toHaveTextContent('Stopped midway')
    expect(rest).toHaveTextContent('make d --check')
    expect(rest).toHaveTextContent('Outcome unknown')
    expect(rest).not.toHaveTextContent('make a')
    expect(rest).not.toHaveTextContent('make b')
  })

  it('a follow-up with no task data keeps the unknown heading and time without chips', async () => {
    const followUpAt = '2026-10-12T08:01:30Z'
    const followUp = makeRun({ triggerBackgroundTasks: [], selection: null })
    await renderChat({
      agent: makeAgent({ runtime: 'claude-code' }),
      messages: turn('generated', 'Back again.', followUp, 'background', followUpAt),
    })

    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent('After a background task ended, Claude Code went on:')
    expect(within(note).queryAllByRole('button')).toHaveLength(0)
    const time = note.querySelector('time')
    expect(time).toHaveAttribute('datetime', followUpAt)
    expect(time).toHaveTextContent(t.chat.time(followUpAt))
  })

  it('uses the quiet tool-line style and groups same-name tasks with ×N in the default mono skin', async () => {
    const first = makeRun({
      tools: [tool('toolu_a1', 'Agent'), tool('toolu_a2', 'Agent'), tool('toolu_a3', 'Agent'), tool('toolu_b1', 'Bash')],
    })
    const tasks = [
      makeBackgroundTask({ id: 'bg-a1', kind: 'subagent', label: 'Wait A', runId: first.id, toolCallId: 'toolu_a1', startedAt: '2026-10-12T08:00:10Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:00Z', output: 'done A' }),
      makeBackgroundTask({ id: 'bg-a2', kind: 'subagent', label: 'Wait B', runId: first.id, toolCallId: 'toolu_a2', startedAt: '2026-10-12T08:00:20Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:05Z', output: 'done B' }),
      makeBackgroundTask({ id: 'bg-a3', kind: 'subagent', label: 'Wait C', runId: first.id, toolCallId: 'toolu_a3', startedAt: '2026-10-12T08:00:30Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:10Z', output: 'done C' }),
      makeBackgroundTask({ id: 'bg-b1', kind: 'command', label: 'npm run lint', runId: first.id, toolCallId: 'toolu_b1', startedAt: '2026-10-12T08:00:40Z', status: 'failed', exitCode: 1, endedAt: '2026-10-12T08:01:15Z', output: 'lint failed' }),
    ]
    first.backgroundTasks = tasks
    const followUp = makeRun({ triggerBackgroundTasks: tasks })
    const { user } = await renderChat({
      messages: [
        ...turn('Start work', 'Started.', first),
        ...turn('generated', 'All finished.', followUp, 'background'),
      ],
    })

    const note = await screen.findByRole('note')
    const buttons = within(note).getAllByRole('button')
    expect(buttons).toHaveLength(2)

    const agentGroup = within(note).getByRole('button', { name: 'Agent Finished in the background, 3 tasks' })
    expect(agentGroup.querySelector('[data-tool-text]')).toHaveTextContent('Agent×3Finished in the background')
    expect(agentGroup.querySelector('[data-tool-text] span')?.className).toContain('font-mono')
    expect(agentGroup.className).not.toContain('rounded-full')
    expect(agentGroup.className).not.toContain('bg-tint-green')
    expect(agentGroup.className).toContain('text-ink-500')
    expect(agentGroup.className).toContain('focus-visible:ring-inset')

    const bashItem = within(note).getByRole('button', { name: 'Bash Failed in the background: npm run lint' })
    expect(bashItem.querySelector('[data-tool-text]')).toHaveTextContent('BashFailed in the background')
    expect(bashItem.className).not.toContain('rounded-full')
    expect(bashItem.className).toContain('text-red-text')
    expect(note.querySelectorAll('[data-tool-separator]')).toHaveLength(1)

    // Clicking the grouped entry opens the group list; Details -> Back restores focus to that task's Details button.
    await user.click(agentGroup)
    const dialog = await screen.findByRole('dialog', { name: 'Agent Finished in the background, 3 tasks' })
    expect(within(dialog).getAllByRole('listitem')).toHaveLength(3)
    await user.click(within(dialog).getByRole('button', { name: 'View details: Wait B' }))
    expect(dialog).toHaveTextContent('done B')
    await user.click(within(dialog).getByRole('button', { name: 'Back to Agent list (3)' }))
    expect(within(dialog).getByRole('button', { name: 'View details: Wait B' })).toHaveFocus()
  })

  it('keeps individual pills and the pill +N button in the follow-up heading in Plate skin', async () => {
    setSkin('plate')
    const first = makeRun({
      tools: [tool('toolu_a1', 'Agent'), tool('toolu_a2', 'Agent'), tool('toolu_a3', 'Agent')],
    })
    const tasks = [
      makeBackgroundTask({ id: 'bg-a1', kind: 'subagent', label: 'Wait A', runId: first.id, toolCallId: 'toolu_a1', startedAt: '2026-10-12T08:00:10Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:00Z' }),
      makeBackgroundTask({ id: 'bg-a2', kind: 'subagent', label: 'Wait B', runId: first.id, toolCallId: 'toolu_a2', startedAt: '2026-10-12T08:00:20Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:05Z' }),
      makeBackgroundTask({ id: 'bg-a3', kind: 'subagent', label: 'Wait C', runId: first.id, toolCallId: 'toolu_a3', startedAt: '2026-10-12T08:00:30Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:10Z' }),
    ]
    first.backgroundTasks = tasks
    const followUp = makeRun({ triggerBackgroundTasks: tasks })
    const { user } = await renderChat({
      messages: [
        ...turn('Start work', 'Started.', first),
        ...turn('generated', 'All finished.', followUp, 'background'),
      ],
    })

    const note = await screen.findByRole('note')
    const buttons = within(note).getAllByRole('button')
    expect(buttons).toHaveLength(3)
    expect(note.querySelector('[data-tool-separator]')).toBeNull()

    const firstChip = within(note).getByRole('button', { name: 'Agent Finished in the background: Wait A' })
    const secondChip = within(note).getByRole('button', { name: 'Agent Finished in the background: Wait B' })
    const moreChip = within(note).getByRole('button', { name: 'Show 1 more ended background task' })
    expect(firstChip.className).toContain('rounded-full')
    expect(firstChip.className).toContain('bg-tint-green')
    expect(secondChip.className).toContain('rounded-full')
    expect(moreChip.className).toContain('rounded-full')

    await user.click(firstChip)
    const dialog = await screen.findByRole('dialog', { name: 'Background task' })
    expect(dialog).toHaveTextContent('Wait A')
    expect(dialog).not.toHaveTextContent('Wait B')
  })
})

describe('Background work in the run record and the ruler', () => {
  it('the run record has a step when a tool goes to the background and one when it ends, with its output', () => {
    const steps = buildSteps(null, [
      { type: 'background_task_started', at: '2026-10-12T08:00:01Z', taskId: 'bg1', kind: 'command', label: 'npm run build', toolCallId: 'toolu_1' },
      { type: 'background_task_started', at: '2026-10-12T08:00:02Z', taskId: 'bg2', kind: 'subagent', label: 'Explore', toolCallId: null },
      { type: 'background_task_ended', at: '2026-10-12T08:05:00Z', taskId: 'bg1', status: 'failed', exitCode: 2, stopReason: null, output: 'error TS2322' },
      { type: 'background_task_ended', at: '2026-10-12T08:06:00Z', taskId: 'bg2', status: 'stopped', exitCode: null, stopReason: 'person', output: null },
    ], runtimeName)
    expect(steps.map((step) => [step.label, step.command, step.detail, step.tone])).toEqual([
      ['Went on in the background', 'npm run build', null, 'warn'],
      ['Went on in the background', null, 'Sub-agent · Explore', 'warn'],
      ['A background task ended', 'npm run build', 'Failed in the background · exit code 2', 'fail'],
      ['A background task ended', null, 'Stopped', 'neutral'],
    ])
    expect(steps[2].output).toBe('error TS2322')
    expect(steps[3].lines).toEqual(['stopped by you'])
  })

  it('a node reply’s task with no end in the loaded record is running only while it can be', async () => {
    const { tasksFromTimeline } = await import('./background-text')
    const entries: Schemas['TimelineEntry'][] = [{ type: 'background_task_started', at: '2026-10-12T08:00:01Z', taskId: 'bg1', kind: 'command', label: 'make', toolCallId: 'call_1' }]
    const live = makeBackgroundTask({ id: 'bg1', stopRequestedAt: '2026-10-12T08:01:00Z' })
    expect(tasksFromTimeline(entries, 'run', undefined, false).map((task) => task.status)).toEqual(['running'])
    expect(tasksFromTimeline(entries, 'run', [live], false)[0].stopRequestedAt).toBe('2026-10-12T08:01:00Z')
    // Its end is on a page not loaded yet: the reply has ended, or the node's list no longer has it.
    expect(tasksFromTimeline(entries, 'run', undefined, true)).toEqual([])
    expect(tasksFromTimeline(entries, 'run', [], false)).toEqual([])
  })

  it('the ruler names a follow-up reply by the task it follows', () => {
    const tests = makeBackgroundTask({ label: 'npm test', status: 'completed', exitCode: 0 })
    const run = makeRun({ triggerBackgroundTasks: [tests] })
    const user = makeMessage({ origin: 'background', text: 'Background task "npm test" completed.', run })
    expect(turnPreview({ key: run.id, run, user, reply: null, sessionEpoch: 1 } as Parameters<typeof turnPreview>[0])).toBe('After background work: npm test')
  })
})
