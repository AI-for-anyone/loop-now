import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { renderWithQuery } from '@/test/render'
import { TimelineSteps } from './TimelineBody'
import { buildSteps } from './timeline-steps'

type Entry = Schemas['TimelineEntry']
const at = (seconds: number) => new Date(Date.parse('2026-11-11T08:00:00Z') + seconds * 1000).toISOString()

describe('process entries in the run log name the runtime', () => {
  it('Claude Code process entries: the start phase and the process steps all say Claude Code; an old Pi process stopped in the same turn still says Pi', () => {
    const entries: Entry[] = [
      { type: 'pi_stopped', at: at(0), pid: 100, reason: 'config_changed', uptimeMs: 60_000, runtime: 'pi' },
      { type: 'phase', at: at(1), phase: 'launching', runtime: 'claude-code' },
      { type: 'pi_started', at: at(2), pid: 200, startupMs: 900, reason: 'no_process', configVersion: 3, runtime: 'claude-code' },
      { type: 'pi_exited', at: at(5), pid: 200, uptimeMs: 3000, exit: 'signal: killed', stderrTail: '', runtime: 'claude-code' },
    ]
    const steps = buildSteps(null, entries)
    expect(steps.map((step) => step.label)).toEqual(['Pi process stopped', 'Claude Code start', 'Claude Code process exited unexpectedly'])
    expect(steps[1].detail).toBe('Started a new Claude Code process, PID 200 (no live process).')
  })

  it('reuses the resident Claude Code process; when the platform later stops it, it also says Claude Code', () => {
    const steps = buildSteps(null, [
      { type: 'phase', at: at(0), phase: 'launching', runtime: 'claude-code' },
      { type: 'pi_reused', at: at(1), pid: 300, processStartedAt: at(-600), runtime: 'claude-code' },
      { type: 'pi_stopped', at: at(9), pid: 300, reason: 'abort_timeout', uptimeMs: 609_000, runtime: 'claude-code' },
    ])
    expect(steps.map((step) => step.label)).toEqual(['Claude Code start', 'Claude Code process stopped'])
    expect(steps[0].detail).toBe('Reused the resident Claude Code process, PID 300.')
  })

  it('Pi process entries say Pi (old records without a runtime reach the console with pi filled in by the server)', () => {
    const steps = buildSteps(null, [
      { type: 'phase', at: at(0), phase: 'launching', runtime: 'pi' },
      { type: 'pi_started', at: at(1), pid: 1, startupMs: 10, reason: 'config_changed', configVersion: 1, runtime: 'pi' },
      { type: 'pi_stopped', at: at(9), pid: 1, reason: 'idle_timeout', uptimeMs: 8000, runtime: 'pi' },
    ])
    expect(steps.map((step) => step.label)).toEqual(['Pi start', 'Pi process stopped'])
    expect(steps[0].detail).toBe('Started a new Pi process, PID 1 (restart after a config change).')
  })

  it('failed before starting, with no process entry: runtime_* failures say Claude Code; other failures are treated as Pi', () => {
    const failed = (errorCode: Schemas['RunErrorCode'], runtime: Schemas['AgentRuntime']): Entry[] => [
      { type: 'phase', at: at(0), phase: 'launching', runtime },
      { type: 'ended', at: at(1), status: 'failed', endReason: null, errorCode },
    ]
    expect(buildSteps(null, failed('runtime_unavailable', 'claude-code'))[0].label).toBe('Claude Code start')
    expect(buildSteps(null, failed('pi_start_failed', 'pi'))[0].label).toBe('Pi start')
  })
})

describe('subscription limit (model_rate_limit)', () => {
  const entry = (info: Record<string, unknown>): Entry => ({ type: 'model_rate_limit', at: at(0), info: info as Record<string, never> })

  it('writes the limit type, status and reset time; the fields go as is into a monospace column', () => {
    const resetsAt = 1794382800
    const [step] = buildSteps(null, [entry({ status: 'allowed', rateLimitType: 'five_hour', resetsAt, utilization: 0.4 })])
    expect(step.label).toBe('Subscription limit')
    expect(step.detail).toBe(`5-hour limit · available · ${t.timeline.detail.rateLimit.resetsAt(new Date(resetsAt * 1000).toISOString())}`)
    expect(step.field).toBe('rateLimitType=five_hour · status=allowed')
    expect(step.tone).toBe('neutral')
    renderWithQuery(<TimelineSteps steps={[step]} />)
    expect(screen.getByText('Subscription limit').closest('li')).toHaveTextContent('5-hour limit · available')
  })

  it('used up is red, running low is yellow; unknown values show as is', () => {
    expect(buildSteps(null, [entry({ status: 'rejected', rateLimitType: 'seven_day' })])[0]).toMatchObject({ detail: '7-day limit · used up', tone: 'fail' })
    expect(buildSteps(null, [entry({ status: 'allowed_warning' })])[0]).toMatchObject({ detail: 'running low', tone: 'warn' })
    expect(buildSteps(null, [entry({ status: 'paused', rateLimitType: 'monthly' })])[0]).toMatchObject({ detail: 'monthly · paused', field: 'rateLimitType=monthly · status=paused' })
  })

  it('missing or mistyped fields cause no error: missing ones are left out; when all are missing it says there are no details', () => {
    expect(buildSteps(null, [entry({})])[0]).toMatchObject({ detail: 'No limit details given', field: null, tone: 'neutral' })
    expect(buildSteps(null, [entry({ status: 7, rateLimitType: null, resetsAt: 'not a date' })])[0]).toMatchObject({ detail: 'No limit details given', field: null })
    const iso = '2026-11-11T13:00:00Z'
    expect(buildSteps(null, [entry({ resetsAt: iso })])[0].detail).toBe(t.timeline.detail.rateLimit.resetsAt(new Date(iso).toISOString()))
    expect(buildSteps(null, [entry({ resetsAt: 1794382800000 })])[0].detail).toBe(t.timeline.detail.rateLimit.resetsAt(new Date(1794382800000).toISOString()))
  })
})

// Platform commands (/new, /reload, /resume) stop an idle process; older records also contain
// the temporary process that /export html started and stopped as soon as it was done.
describe('reasons a platform command stopped the process', () => {
  it.each([
    ['session_reset', 'Stopped after the session was reset (/new)'],
    ['console_reload', 'Stopped for a reload (/reload)'],
    ['session_resumed', 'Stopped after a session was resumed (/resume)'],
    ['console_export', 'Stopped after exporting the session'],
  ] as const)('%s is worded “%s”', (reason, wording) => {
    const [step] = buildSteps(null, [{ type: 'pi_stopped', at: at(0), pid: 100, reason, uptimeMs: 60_000, runtime: 'pi' }])
    expect(step.label).toBe('Pi process stopped')
    expect(step.detail).toContain(wording)
    expect(step.detail).not.toContain(reason)
  })
})
