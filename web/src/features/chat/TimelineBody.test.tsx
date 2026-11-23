import { screen } from '@testing-library/react'
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

it('renders the command as code text, keeps multiple lines and lets long commands wrap, does not parse HTML', () => {
  const entry: Schemas['TimelineTool'] = { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: 'bash-one', toolName: 'bash', state: 'started', endedAt: null, durationMs: null }
  const command = `printf '<img src=x onerror=alert(1)>'\n${'long-path/'.repeat(80)}`
  const steps = buildSteps(null, [{ ...entry, command }], runtimeName)
  const { container } = renderWithQuery(<TimelineSteps steps={steps} />)
  const code = container.querySelector('code')!
  expect(code.textContent).toBe(command)
  expect(code).toHaveClass('whitespace-pre-wrap', '[overflow-wrap:anywhere]', 'max-w-full', 'min-w-0')
  expect(container.querySelector('img')).toBeNull()
  expect(screen.getByText('Tool bash').closest('li')).toHaveAttribute('data-tone', 'warn')
  expect(screen.queryByText(/tools\[/)).toBeNull()
})

it('does not pass off another tool\'s field as a bash command', () => {
  const entry: Schemas['TimelineTool'] = { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: 'read-one', toolName: 'read', command: 'not a bash command', state: 'succeeded', endedAt: null, durationMs: null }
  const { container } = renderWithQuery(<TimelineSteps steps={buildSteps(null, [entry], runtimeName)} />)
  expect(container.querySelector('code')).toBeNull()
  expect(screen.queryByText(/Command not recorded/)).toBeNull()
})

it.each(['started', 'succeeded', 'failed'] as const)('an old bash record without a command says so plainly and keeps the %s state', state => {
  const entry: Schemas['TimelineTool'] = { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: 'old-bash', toolName: 'bash', state, endedAt: null, durationMs: null }
  const { container } = renderWithQuery(<TimelineSteps steps={buildSteps(null, [entry], runtimeName)} />)
  expect(screen.getByText(/Command not recorded/)).toBeVisible()
  expect(container.querySelector('code')).toBeNull()
  expect(screen.getByText('Tool bash').closest('li')).toHaveAttribute('data-tone', state === 'succeeded' ? 'ok' : state === 'failed' ? 'fail' : 'warn')
  expect(screen.queryByText(/tools\[/)).toBeNull()
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
