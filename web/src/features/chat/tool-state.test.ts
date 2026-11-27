import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { groupTools, shownState, unfinishedTool } from './tool-state'

const ev = (toolCallId: string, toolName: string, state: Schemas['ToolState'], at = '2026-10-12T00:00:00Z'): Schemas['ToolEvent'] => ({
  toolCallId,
  toolName,
  state,
  at,
})

describe('tool grouping', () => {
  it('merges state transitions by toolCallId and groups calls with the same name and state in order of first appearance', () => {
    const events: Schemas['ToolEvent'][] = [
      ev('c1', 'execute', 'started'),
      ev('c1', 'execute', 'succeeded'),
      ev('c2', 'read', 'started'),
      ev('c2', 'read', 'succeeded'),
      ev('c3', 'execute', 'started'),
      ev('c3', 'execute', 'succeeded'),
      ev('c4', 'edit', 'succeeded'),
      ev('c5', 'execute', 'succeeded'),
    ]
    expect(groupTools(events, 'running')).toEqual([
      { key: 'execute\0succeeded', toolName: 'execute', state: 'succeeded', count: 3 },
      { key: 'read\0succeeded', toolName: 'read', state: 'succeeded', count: 1 },
      { key: 'edit\0succeeded', toolName: 'edit', state: 'succeeded', count: 1 },
    ])
  })

  it('keeps different states of the same tool as separate groups in order of first appearance', () => {
    const events: Schemas['ToolEvent'][] = [
      ev('c1', 'execute', 'succeeded'),
      ev('c2', 'execute', 'failed'),
      ev('c3', 'execute', 'succeeded'),
      ev('c4', 'execute', 'failed'),
      ev('c5', 'execute', 'started'),
    ]
    expect(groupTools(events, 'running')).toEqual([
      { key: 'execute\0succeeded', toolName: 'execute', state: 'succeeded', count: 2 },
      { key: 'execute\0failed', toolName: 'execute', state: 'failed', count: 2 },
      { key: 'execute\0started', toolName: 'execute', state: 'started', count: 1 },
    ])
  })

  it('resolves unfinished calls according to turn end state and groups them together', () => {
    const events: Schemas['ToolEvent'][] = [
      ev('c1', 'execute', 'started'),
      ev('c2', 'execute', 'failed'),
      ev('c3', 'execute', 'no_result'),
    ]
    expect(groupTools(events, 'no_result')).toEqual([
      { key: 'execute\0no_result', toolName: 'execute', state: 'no_result', count: 2 },
      { key: 'execute\0failed', toolName: 'execute', state: 'failed', count: 1 },
    ])
    expect(groupTools(events, 'interrupted')).toEqual([
      { key: 'execute\0interrupted', toolName: 'execute', state: 'interrupted', count: 2 },
      { key: 'execute\0failed', toolName: 'execute', state: 'failed', count: 1 },
    ])
  })

  it('omits excluded toolCallIds before grouping', () => {
    const events: Schemas['ToolEvent'][] = [
      ev('bg1', 'execute', 'succeeded'),
      ev('c1', 'execute', 'succeeded'),
      ev('c2', 'read', 'succeeded'),
    ]
    expect(groupTools(events, 'running', new Set(['bg1']))).toEqual([
      { key: 'execute\0succeeded', toolName: 'execute', state: 'succeeded', count: 1 },
      { key: 'read\0succeeded', toolName: 'read', state: 'succeeded', count: 1 },
    ])
  })

  it('exports shownState and unfinishedTool helpers consistently', () => {
    expect(shownState('started', 'running')).toBe('started')
    expect(shownState('started', 'no_result')).toBe('no_result')
    expect(shownState('no_result', 'interrupted')).toBe('interrupted')
    expect(shownState('failed', 'interrupted')).toBe('failed')
    expect(unfinishedTool({ status: 'running' })).toBe('running')
    expect(unfinishedTool({ status: 'interrupted', endReason: 'user_interrupt' })).toBe('interrupted')
    expect(unfinishedTool({ status: 'completed' })).toBe('no_result')
  })
})
