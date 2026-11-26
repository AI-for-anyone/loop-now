import { screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { makeBackgroundTask, makeMessage, makeRun } from '@/test/api'
import { renderChat } from '@/test/chat'
import { buildSteps } from './timeline-steps'
import { runtimeName } from '@/test/runtimes'
import { turnPreview } from './ruler'

const NOW = new Date('2026-10-12T08:02:00Z')

function turn(text: string, reply: string, run: Schemas['Run'], origin: Schemas['MessageOrigin'] = 'console') {
  return [makeMessage({ text, run, origin }), makeMessage({ role: 'assistant', text: reply, run, origin })]
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
})
afterEach(() => vi.useRealTimers())

describe('Background work in the replies', () => {
  it('a reply the CLI wrote by itself has no person’s message above it: one line says which task it follows', async () => {
    const tests = makeBackgroundTask({ label: 'npm test', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:00Z' })
    const first = makeRun({ backgroundTasks: [tests] })
    const followUp = makeRun({ triggerBackgroundTasks: [tests], selection: null })
    await renderChat({ messages: [...turn('Run the tests', 'Running.', first), ...turn('Background task "npm test" completed with exit code 0.', 'All 128 tests passed.', followUp, 'background')] })

    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent('After background task “npm test” ended, Claude Code went on:')
    expect(note).toHaveTextContent('Finished in the background · exit code 0')
    // The generated text is not shown as a message from the person.
    expect(screen.queryByText('Background task "npm test" completed with exit code 0.')).toBeNull()
    expect(screen.getByText('All 128 tests passed.')).toBeInTheDocument()
  })

  it('several ended tasks name the first and how many more', async () => {
    const tasks = [makeBackgroundTask({ label: 'make a', status: 'completed', exitCode: 0 }), makeBackgroundTask({ label: 'make b', status: 'failed', exitCode: 2 })]
    const followUp = makeRun({ triggerBackgroundTasks: tasks })
    await renderChat({ messages: turn('generated', 'Both are done.', followUp, 'background') })
    expect(await screen.findByRole('note')).toHaveTextContent('After background task “make a” and 1 other ended, Claude Code went on:')
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
