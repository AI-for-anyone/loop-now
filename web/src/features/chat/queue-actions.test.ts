import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/client'
import { nodeConflictNotice } from './queue-actions'

const conflict = (reason: string) => new ApiError({ status: 409, code: 'task_conflict', serverMessage: 'Task node or run conditions changed', details: { taskId: 't', expectedNodeRunId: 'n', currentNodeRunId: 'm', expectedAgentId: 'a', expectedRuntimeId: 'r', taskVersion: 2, reason } as never })

describe('nodeConflictNotice', () => {
  it.each([
    ['node_changed', 'The task has moved off this node'],
    ['task_ended', 'The task has ended'],
    ['target_mismatch', 'This node\'s Agent or instance has changed'],
    ['instance_unavailable', 'This node\'s Agent instance is no longer available'],
    ['chat_unavailable', 'This node can\'t take messages right now'],
    ['action_conflict', 'The node changed just now, so this action didn\'t take effect'],
  ])('names the node guard reason %s in plain words', (reason, text) => {
    expect(nodeConflictNotice(conflict(reason), 'cancel')).toBe(text)
  })

  it('keeps the queue-specific wording and leaves other errors to describeError', () => {
    expect(nodeConflictNotice(conflict('message_read'), 'cancel')).toBe('This message was already read and is no longer queued')
    expect(nodeConflictNotice(new ApiError({ status: 409, code: 'agent_busy' }), 'stop')).toBeNull()
  })
})
