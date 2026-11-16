import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/client'
import { nodeConflictNotice } from './queue-actions'

const conflict = (reason: string) => new ApiError({ status: 409, code: 'task_conflict', serverMessage: 'Task node or run conditions changed', details: { taskId: 't', expectedNodeRunId: 'n', currentNodeRunId: 'm', expectedAgentId: 'a', expectedRuntimeId: 'r', taskVersion: 2, reason } as never })

describe('nodeConflictNotice', () => {
  it.each([
    ['node_changed', '任务已经不在这个节点上'],
    ['task_ended', '任务已经结束'],
    ['target_mismatch', '这个节点的 Agent 或实例已经换了'],
    ['instance_unavailable', '这个节点的 Agent 实例已不可用'],
    ['chat_unavailable', '现在不能给这个节点发消息'],
    ['action_conflict', '节点状态刚刚变了，这次操作没有生效'],
  ])('names the node guard reason %s in Chinese', (reason, text) => {
    expect(nodeConflictNotice(conflict(reason), 'cancel')).toBe(text)
  })

  it('keeps the queue-specific wording and leaves other errors to describeError', () => {
    expect(nodeConflictNotice(conflict('message_read'), 'cancel')).toBe('这条消息已被读取，不在排队中了')
    expect(nodeConflictNotice(new ApiError({ status: 409, code: 'agent_busy' }), 'stop')).toBeNull()
  })
})
