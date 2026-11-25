import { screen, waitFor, within } from '@testing-library/react'
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

const tool = (toolCallId: string, toolName: string): Schemas['ToolEvent'] => ({ toolCallId, toolName, state: 'succeeded', at: '2026-10-12T08:00:00Z' })

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
})
afterEach(() => vi.useRealTimers())

describe('Background work in the replies', () => {
  it('a tool that went on in the background is a card that runs, then says how it ended, with its output', async () => {
    const task = makeBackgroundTask({ label: 'npm test', toolCallId: 'toolu_test', startedAt: '2026-10-12T08:00:37Z' })
    const run = makeRun({ tools: [tool('toolu_read', 'Read'), tool('toolu_test', 'Bash')], backgroundTasks: [{ ...task, runId: '' }] })
    const withRun = { ...task, runId: run.id }
    run.backgroundTasks = [withRun]
    const { user, emit } = await renderChat({ messages: turn('Run the tests', 'They are running.', run) })

    const tools = screen.getByRole('list', { name: 'Tool calls' })
    expect(within(tools).getByText('Read')).toBeInTheDocument()
    // The backgrounded tool is not a chip as well.
    expect(within(tools).queryByText('Bash')).toBeNull()
    const card = screen.getByText('npm test').closest('[data-background-tool]') as HTMLElement
    expect(within(card).getByText('Bash')).toBeInTheDocument()
    expect(within(card).getByRole('status')).toHaveTextContent('Running in the background · 1:23')

    const ended: Schemas['BackgroundTask'] = { ...withRun, status: 'failed', exitCode: 1, endedAt: '2026-10-12T08:01:49Z', output: 'FAIL src/a.test.ts\n1 failed' }
    await emit('run.updated', { run: { ...run, backgroundTasks: [ended] } })
    await waitFor(() => expect(within(card).getByRole('status')).toHaveTextContent('Failed in the background'))
    expect(card).toHaveTextContent('exit code 1')
    expect(card).toHaveTextContent('took 1 min 12 s')
    await user.click(within(card).getByText('Output (the last part)'))
    expect(within(card).getByText(/FAIL src\/a\.test\.ts/)).toBeVisible()
  })

  it('a stopped task says why, and one whose end is unknown says so without a duration', async () => {
    const stopped = makeBackgroundTask({ label: 'tail -f app.log', status: 'stopped', stopReason: 'settings_changed', endedAt: '2026-10-12T08:01:00Z' })
    const unknown = makeBackgroundTask({ label: 'Explore the code', kind: 'subagent', status: 'unknown', endedAt: '2026-10-12T08:01:30Z' })
    const run = makeRun({ backgroundTasks: [stopped, unknown] })
    await renderChat({ messages: turn('Go', 'Started.', run) })
    const stoppedCard = screen.getByText('tail -f app.log').closest('[data-background-tool]') as HTMLElement
    expect(stoppedCard).toHaveTextContent('Stopped')
    expect(stoppedCard).toHaveTextContent('stopped when a settings change restarted the CLI')
    const unknownCard = screen.getByText('Explore the code').closest('[data-background-tool]') as HTMLElement
    expect(unknownCard).toHaveTextContent('Outcome unknown')
    expect(unknownCard).toHaveTextContent('Loop Now restarted, so how it ended is unknown')
    expect(unknownCard).not.toHaveTextContent('took')
  })

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

  it('the ruler names a follow-up reply by the task it follows', () => {
    const tests = makeBackgroundTask({ label: 'npm test', status: 'completed', exitCode: 0 })
    const run = makeRun({ triggerBackgroundTasks: [tests] })
    const user = makeMessage({ origin: 'background', text: 'Background task "npm test" completed.', run })
    expect(turnPreview({ key: run.id, run, user, reply: null, sessionEpoch: 1 } as Parameters<typeof turnPreview>[0])).toBe('After background work: npm test')
  })
})
