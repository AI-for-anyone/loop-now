import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { renderWithQuery } from '@/test/render'
import { TimelineSteps } from './TimelineBody'
import { buildSteps } from './timeline-steps'
import { runtimeName } from '@/test/runtimes'

it('with only real task timeline entries does not fake the usual queued/started steps; the final tool state reads on its own', () => {
  const entry: Schemas['TimelineTool'] = { type: 'tool', at: '2026-10-18T00:00:00Z', toolCallId: 'fixture-tool', toolName: 'fixture-tool', state: 'failed', endedAt: '2026-10-18T00:00:00Z', durationMs: null }
  const steps = buildSteps(null, [entry], runtimeName)
  expect(steps).toHaveLength(1); expect(steps[0].tone).toBe('fail'); expect(steps[0].duration).toBeNull()
  renderWithQuery(<TimelineSteps steps={steps} />)
  expect(screen.getByText(/fixture-tool/)).toBeVisible(); expect(screen.queryByText('Queued')).toBeNull()
})

it('renders the command on one line with ellipsis and hover title, expands on click to keep lines and wrap, does not parse HTML', async () => {
  const user = userEvent.setup()
  const entry: Schemas['TimelineTool'] = { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: 'bash-one', toolName: 'bash', state: 'started', endedAt: null, durationMs: null }
  const command = `printf '<img src=x onerror=alert(1)>'\n${'long-path/'.repeat(80)}`
  const steps = buildSteps(null, [{ ...entry, command }], runtimeName)
  const { container } = renderWithQuery(<TimelineSteps steps={steps} />)
  const row = screen.getByText('Tool bash').closest('li')!
  const toggle = within(row).getByRole('button')
  const code = container.querySelector('code')!
  expect(code.textContent).toBe(command)
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(toggle).toHaveAttribute('title', command)
  expect(code).toHaveClass('truncate', 'max-w-full', 'min-w-0')
  expect(container.querySelector('img')).toBeNull()
  expect(row).toHaveAttribute('data-tone', 'warn')
  expect(screen.queryByText(/tools\[/)).toBeNull()

  await user.click(toggle)
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  expect(toggle).not.toHaveAttribute('title')
  expect(code).toHaveClass('whitespace-pre-wrap', '[overflow-wrap:anywhere]', 'max-w-full', 'min-w-0')

  await user.click(toggle)
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(toggle).toHaveAttribute('title', command)
  expect(code).toHaveClass('truncate')
})

it('shows the recorded command for every runtime’s shell tool name', () => {
  const entries: Schemas['TimelineTool'][] = [
    { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: 'c1', toolName: 'Bash', command: 'git status --short', state: 'succeeded', endedAt: '2026-10-22T00:00:01Z', durationMs: 868 },
    { type: 'tool', at: '2026-10-22T00:00:01Z', toolCallId: 'c2', toolName: 'execute', command: 'ls -la', state: 'succeeded', endedAt: '2026-10-22T00:00:02Z', durationMs: 120 },
    { type: 'tool', at: '2026-10-22T00:00:02Z', toolCallId: 'c3', toolName: 'shell', command: 'go test ./...', state: 'succeeded', endedAt: '2026-10-22T00:00:03Z', durationMs: 539 },
    { type: 'tool', at: '2026-10-22T00:00:03Z', toolCallId: 'c4', toolName: 'bash', command: 'make build', state: 'succeeded', endedAt: '2026-10-22T00:00:04Z', durationMs: 310 },
    { type: 'tool', at: '2026-10-22T00:00:04Z', toolCallId: 'c5', toolName: 'run_command', command: 'pwd', state: 'succeeded', endedAt: '2026-10-22T00:00:05Z', durationMs: 15 },
  ]
  renderWithQuery(<TimelineSteps steps={buildSteps(null, entries, runtimeName)} />)
  for (const entry of entries) {
    const row = screen.getByText(`Tool ${entry.toolName}`).closest('li')!
    expect(row.querySelector('code')?.textContent).toBe(entry.command)
    expect(within(row).queryByText(/Command not recorded/)).toBeNull()
  }
})

it('shows file and search targets in code style, truncates long targets on one line and expands on click', async () => {
  const user = userEvent.setup()
  const longPath = `packages/${'deep-nested-folder/'.repeat(16)}TimelineBody.tsx`
  const entries: Schemas['TimelineTool'][] = [
    { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: 'f1', toolName: 'Read', target: longPath, state: 'succeeded', endedAt: '2026-10-22T00:00:01Z', durationMs: 12 },
    { type: 'tool', at: '2026-10-22T00:00:01Z', toolCallId: 'f2', toolName: 'fileChange', target: 'a.go, b.go', state: 'succeeded', endedAt: '2026-10-22T00:00:02Z', durationMs: 40 },
    { type: 'tool', at: '2026-10-22T00:00:02Z', toolCallId: 's1', toolName: 'Grep', target: 'withToolCommand', state: 'succeeded', endedAt: '2026-10-22T00:00:03Z', durationMs: 28 },
    { type: 'tool', at: '2026-10-22T00:00:03Z', toolCallId: 's2', toolName: 'WebSearch', target: '<script>alert(1)</script>', state: 'succeeded', endedAt: '2026-10-22T00:00:04Z', durationMs: 410 },
  ]
  const { container } = renderWithQuery(<TimelineSteps steps={buildSteps(null, entries, runtimeName)} />)
  for (const entry of entries) {
    const row = screen.getByText(`Tool ${entry.toolName}`).closest('li')!
    const code = row.querySelector('code')!
    expect(code.textContent).toBe(entry.target)
    expect(code).toHaveClass('truncate')
    expect(within(row).getByRole('button')).toHaveAttribute('title', entry.target)
    expect(within(row).queryByText(/Command not recorded/)).toBeNull()
  }
  expect(container.querySelector('script')).toBeNull()

  const readRow = screen.getByText('Tool Read').closest('li')!
  const toggle = within(readRow).getByRole('button')
  await user.click(toggle)
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  expect(readRow.querySelector('code')).toHaveClass('whitespace-pre-wrap', '[overflow-wrap:anywhere]')
})

it('an old non-shell record without a target or command stays bare and does not say “Command not recorded”', () => {
  const entries: Schemas['TimelineTool'][] = [
    { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: 'read-one', toolName: 'Read', state: 'succeeded', endedAt: null, durationMs: null },
    { type: 'tool', at: '2026-10-22T00:00:01Z', toolCallId: 'grep-one', toolName: 'Grep', state: 'succeeded', endedAt: null, durationMs: null },
  ]
  const { container } = renderWithQuery(<TimelineSteps steps={buildSteps(null, entries, runtimeName)} />)
  expect(container.querySelector('code')).toBeNull()
  expect(screen.queryByText(/Command not recorded/)).toBeNull()
})

it.each(['bash', 'Bash', 'shell', 'execute'] as const)('an old %s record without a command says so plainly and keeps every state', (toolName) => {
  for (const state of ['started', 'succeeded', 'failed'] as const) {
    const entry: Schemas['TimelineTool'] = { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: `old-${toolName}-${state}`, toolName, state, endedAt: null, durationMs: null }
    const { container, unmount } = renderWithQuery(<TimelineSteps steps={buildSteps(null, [entry], runtimeName)} />)
    expect(screen.getByText(/Command not recorded/)).toBeVisible()
    expect(container.querySelector('code')).toBeNull()
    expect(screen.getByText(`Tool ${toolName}`).closest('li')).toHaveAttribute('data-tone', state === 'succeeded' ? 'ok' : state === 'failed' ? 'fail' : 'warn')
    expect(screen.queryByText(/tools\[/)).toBeNull()
    unmount()
  }
})

it('promotion to interrupt shows as a step with its time and the whenBusy change; old records without it show as usual', () => {
  const run = { queuedAt: '2026-10-24T08:00:00Z', whenBusy: 'interrupt' as const }
  const entries: Schemas['TimelineEntry'][] = [
    { type: 'queued', at: '2026-10-24T08:00:00Z', whenBusy: 'queue' },
    { type: 'promoted', at: '2026-10-24T08:00:05Z', from: 'queue', to: 'interrupt' },
    { type: 'dispatched', at: '2026-10-24T08:00:06Z' },
  ]
  const steps = buildSteps(run, entries, runtimeName)
  expect(steps.map(step => step.label)).toEqual(['Queued', 'Promoted to interrupt', 'Dispatched'])
  // The enqueue step keeps the queueing mode chosen when the message was sent.
  expect(steps[0].detail).toBe('The Agent was busy; queued after the other normal messages.')
  renderWithQuery(<TimelineSteps steps={steps} />)
  const promoted = screen.getByText('Promoted to interrupt').closest('li')!
  expect(promoted).toHaveAttribute('data-tone', 'hot')
  expect(promoted).toHaveTextContent('whenBusy=queue → interrupt')
  expect(promoted).toHaveTextContent(t.timeline.clock('2026-10-24T08:00:05Z'))
  expect(buildSteps(run, [entries[0], entries[2]], runtimeName).map(step => step.label)).toEqual(['Queued', 'Dispatched'])
})

it('an Inbox message cancelled, promoted to interrupt one by one (with the message ID) and stopped by a person each show as a step', () => {
  const messageId = '58000000-0000-7000-8000-000000000001'
  const entries: Schemas['TimelineEntry'][] = [
    { type: 'promoted', at: '2026-10-25T08:00:01Z', from: 'queue', to: 'interrupt', messageId },
    { type: 'message_cancelled', at: '2026-10-25T08:00:02Z', messageId },
    { type: 'abort_requested', at: '2026-10-25T08:00:03Z', byRunId: null, stop: true },
    { type: 'abort_requested', at: '2026-10-25T08:00:04Z', byRunId: null },
  ]
  const steps = buildSteps(null, entries, runtimeName)
  expect(steps.map(step => step.label)).toEqual(['Promoted to interrupt', 'An Inbox message was cancelled', 'Stopped by a person', 'Interrupt received'])
  expect(steps.map(step => step.field)).toEqual([`whenBusy=queue → interrupt · messageId=${messageId}`, `messageId=${messageId}`, 'abortRequestedAt · stop=true', 'abortRequestedAt · status=aborting'])
  expect(steps[2].tone).not.toBe('hot')
})

const execute = (state: Schemas['ToolState']): Schemas['TimelineTool'] => ({ type: 'tool', at: '2026-11-24T08:00:05Z', toolCallId: 'exec-1', toolName: 'execute', command: 'sleep 60', state, endedAt: state === 'started' ? null : '2026-11-24T08:01:35Z', durationMs: null })
const ended: Schemas['TimelineEnded'] = { type: 'ended', at: '2026-11-24T08:01:35Z', status: 'completed', endReason: null, errorCode: null }

it('a tool closed as no_result, or an old one still on started in a turn that has ended, reads “No result” in a neutral step', () => {
  for (const entries of [[execute('no_result')], [execute('started'), ended]]) {
    const step = buildSteps(null, entries, runtimeName)[0]
    expect(step.detail).toBe('No result')
    expect(step.tone).toBe('neutral')
  }
  // While the turn goes on, a started tool is running.
  const running = buildSteps(null, [execute('started')], runtimeName)[0]
  expect(running.detail).toBe('Running')
  expect(running.tone).toBe('warn')
})

it('the turn that ended because the runtime went quiet while background commands ran says so, with the wait in minutes', () => {
  const entry: Schemas['TimelineBackgroundWaitTimeout'] = { type: 'background_wait_timeout', at: '2026-11-24T08:11:35Z', limitMs: 10 * 60_000 }
  renderWithQuery(<TimelineSteps steps={buildSteps(null, [execute('no_result'), entry, ended], runtimeName)} />)
  expect(screen.getByText('While waiting on background commands, the runtime sent nothing for 10 min; the turn ended here')).toBeVisible()
})
