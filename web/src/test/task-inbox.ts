import type { Schemas } from '@/api/client'
import { consoleParty, makeInboxMessage } from './inbox'
import { makeTaskDetail, taskId, nodeId, runId } from './tasks'
import { defaultProject } from './api'
const node = makeTaskDetail(defaultProject.id).nodes[1]
export function taskInboxReceipt(body: Schemas['TaskNodeInboxInput']): Schemas['TaskNodeInboxReceipt'] {
  const message = makeInboxMessage({ id: '51000000-0000-7000-8000-000000000001', from: consoleParty, to: { kind: 'agent', id: body.expectedAgentId, name: node.agentName, address: '测试项目/任务检查员#任务', deleted: false, pluginId: null, channelId: null }, text: body.text, whenBusy: body.whenBusy })
  return { actionId: body.actionId, taskId, nodeRunId: nodeId, agentId: body.expectedAgentId, runtimeId: body.expectedRuntimeId, message, status: 'accepted' }
}
export function taskInboxRecord(overrides: Partial<Schemas['TaskNodeInboxRecord']> = {}): Schemas['TaskNodeInboxRecord'] {
  const receipt = taskInboxReceipt({ actionId: '51000000-0000-7000-8000-000000000002', expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId, text: '真实Inbox输入', whenBusy: 'queue' })
  return { taskId, nodeRunId: nodeId, agentId: node.agentId, runtimeId: node.runtimeId, message: receipt.message, runLinks: [], notExecuted: null, ...overrides }
}
export function taskRunTimeline(overrides: Partial<Schemas['TaskNodeRunTimeline']> = {}): Schemas['TaskNodeRunTimeline'] {
  return { taskId, nodeRunId: nodeId, agentId: node.agentId, runtimeId: node.runtimeId, run: { id: runId, origin: 'inbox_reminder', executionMode: 'task_conversation', status: 'completed', queuedAt: '2026-10-18T00:00:00Z', startedAt: '2026-10-18T00:00:01Z', endedAt: '2026-10-18T00:00:03Z', errorCode: null, errorMessage: null }, entries: [], usage: { input: 1, output: 2, cacheRead: 0, cacheWrite: 0, total: 3, calls: 1 }, nextCursor: null, ...overrides }
}
