import { describe, expect, it } from 'vitest'
import { makeBackgroundTask } from '@/test/api'
import { backgroundStatusText, backgroundTaskName, groupBackgroundTasks, shownBackgroundState } from './background-text'

describe('groupBackgroundTasks', () => {
  it('groups tasks with the same tool name and state in order of earliest start time', () => {
    const t1 = makeBackgroundTask({ id: 't1', toolCallId: 'c1', kind: 'subagent', label: 'third agent', startedAt: '2026-10-12T08:00:03Z', status: 'running' })
    const t2 = makeBackgroundTask({ id: 't2', toolCallId: 'c2', kind: 'subagent', label: 'first agent', startedAt: '2026-10-12T08:00:01Z', status: 'running' })
    const t3 = makeBackgroundTask({ id: 't3', toolCallId: 'c3', kind: 'subagent', label: 'second agent', startedAt: '2026-10-12T08:00:02Z', status: 'running' })
    const names = new Map([
      ['c1', 'Agent'],
      ['c2', 'Agent'],
      ['c3', 'Agent'],
    ])

    const groups = groupBackgroundTasks([t1, t2, t3], names)
    expect(groups).toHaveLength(1)
    expect(groups[0]).toMatchObject({ name: 'Agent', state: 'running', status: 'running' })
    expect(groups[0].tasks.map((task) => task.id)).toEqual(['t2', 't3', 't1'])
  })

  it('keeps different states and tool names in separate groups ordered by first start', () => {
    const bashDone1 = makeBackgroundTask({ id: 'b1', toolCallId: 'cb1', kind: 'command', label: 'npm test', startedAt: '2026-10-12T08:00:01Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:00:10Z' })
    const agentRun1 = makeBackgroundTask({ id: 'a1', toolCallId: 'ca1', kind: 'subagent', label: 'explore A', startedAt: '2026-10-12T08:00:02Z', status: 'running' })
    const agentFail = makeBackgroundTask({ id: 'a2', toolCallId: 'ca2', kind: 'subagent', label: 'explore B', startedAt: '2026-10-12T08:00:03Z', status: 'failed', exitCode: 1, endedAt: '2026-10-12T08:00:09Z' })
    const agentStopping = makeBackgroundTask({ id: 'a3', toolCallId: 'ca3', kind: 'subagent', label: 'explore C', startedAt: '2026-10-12T08:00:04Z', status: 'running', stopRequestedAt: '2026-10-12T08:00:05Z' })
    const agentStopped = makeBackgroundTask({ id: 'a4', toolCallId: 'ca4', kind: 'subagent', label: 'explore D', startedAt: '2026-10-12T08:00:05Z', status: 'stopped', stopReason: 'person', endedAt: '2026-10-12T08:00:06Z' })
    const agentUnknown = makeBackgroundTask({ id: 'a5', toolCallId: 'ca5', kind: 'subagent', label: 'explore E', startedAt: '2026-10-12T08:00:06Z', status: 'unknown', endedAt: '2026-10-12T08:00:07Z' })
    const agentRun2 = makeBackgroundTask({ id: 'a6', toolCallId: 'ca6', kind: 'subagent', label: 'explore F', startedAt: '2026-10-12T08:00:07Z', status: 'running' })
    const bashDone2 = makeBackgroundTask({ id: 'b2', toolCallId: 'cb2', kind: 'command', label: 'npm lint', startedAt: '2026-10-12T08:00:08Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:00:12Z' })
    const names = new Map([
      ['cb1', 'Bash'],
      ['cb2', 'Bash'],
      ['ca1', 'Agent'],
      ['ca2', 'Agent'],
      ['ca3', 'Agent'],
      ['ca4', 'Agent'],
      ['ca5', 'Agent'],
      ['ca6', 'Agent'],
    ])

    const groups = groupBackgroundTasks([bashDone2, agentRun2, agentUnknown, agentStopped, agentStopping, agentFail, agentRun1, bashDone1], names)
    expect(groups.map((group) => [group.name, group.state, group.tasks.map((task) => task.id)])).toEqual([
      ['Bash', 'completed', ['b1', 'b2']],
      ['Agent', 'running', ['a1', 'a6']],
      ['Agent', 'failed', ['a2']],
      ['Agent', 'stopping', ['a3']],
      ['Agent', 'stopped', ['a4']],
      ['Agent', 'unknown', ['a5']],
    ])
  })

  it('falls back to the task kind name when no tool name is mapped', () => {
    const t1 = makeBackgroundTask({ id: 't1', toolCallId: null, kind: 'subagent', startedAt: '2026-10-12T08:00:01Z' })
    const t2 = makeBackgroundTask({ id: 't2', toolCallId: 'missing', kind: 'subagent', startedAt: '2026-10-12T08:00:02Z' })
    const t3 = makeBackgroundTask({ id: 't3', toolCallId: null, kind: 'command', startedAt: '2026-10-12T08:00:03Z' })

    expect(backgroundTaskName(t1)).toBe('Sub-agent')
    expect(backgroundTaskName(t3)).toBe('Command')
    const groups = groupBackgroundTasks([t1, t2, t3], new Map())
    expect(groups.map((group) => [group.name, group.tasks.length])).toEqual([
      ['Sub-agent', 2],
      ['Command', 1],
    ])
  })

  it('maps shown state and status text consistently for stopping and all task statuses', () => {
    expect(shownBackgroundState({ status: 'running', stopRequestedAt: null })).toBe('running')
    expect(shownBackgroundState({ status: 'running', stopRequestedAt: '2026-10-12T08:01:00Z' })).toBe('stopping')
    expect(shownBackgroundState({ status: 'completed', stopRequestedAt: '2026-10-12T08:01:00Z' })).toBe('completed')

    expect(backgroundStatusText('running')).toBe('Running in the background')
    expect(backgroundStatusText('stopping')).toBe('Stopping…')
    expect(backgroundStatusText('completed')).toBe('Finished in the background')
    expect(backgroundStatusText('failed')).toBe('Failed in the background')
    expect(backgroundStatusText('stopped')).toBe('Stopped')
    expect(backgroundStatusText('unknown')).toBe('Outcome unknown')
  })
})
