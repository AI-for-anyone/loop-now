import type { Schemas } from '@/api/client'
export const taskId = '00000000-0000-7000-8000-000000000100'
export const nodeId = '00000000-0000-7000-8000-000000000101'
export const runId = '00000000-0000-7000-8000-000000000102'
export const oldNodeId = '00000000-0000-7000-8000-000000000103'
export const taskTime = '2026-10-14T08:00:00Z'
export function makeTaskDetail(projectId: string, projectName = 'test-project'): Schemas['TaskDetail'] {
  const node: Schemas['TaskNodeRun'] = {
    sleep: null, sleepRevision: 0,
    id: nodeId, taskId, stepId: 'check', visit: 2, attempt: 1, agentId: '00000000-0000-7000-8000-000000000104',
    agentName: 'task-checker', runtimeId: '00000000-0000-7000-8000-000000000105', status: 'blocked',
    inputs: { text: 'This input' }, inputVersions: [{ input: 'text', nodeRunId: oldNodeId }], output: { result: 'Result for checking only' }, edgeId: 'finish', reason: 'Something went wrong in this turn; check the result', question: null,
    runIds: [runId], startedAt: taskTime, endedAt: taskTime,
  }
  return {
    sleepingInstances: [],
    task: { id: taskId, projectId, number: 12, title: 'General task acceptance', status: 'blocked', currentNodeRunId: nodeId, currentStepId: 'check', currentAgentId: node.agentId, reason: node.reason, createdAt: taskTime, updatedAt: taskTime, endedAt: null, link: `/p/${encodeURIComponent(projectName)}/tasks/${taskId}`, version: 2 },
    request: { text: 'Original request' }, cleanup: 'none',
    workflow: { startStepId: 'check', steps: [{ id: 'check', title: 'Quality check', description: 'Check the input', agentId: node.agentId, inputFields: [{ name: 'text', type: 'string', required: true }], outputFields: [{ name: 'result', type: 'string', required: true }], inputs: { text: { kind: 'request', field: 'text' } } }, { id: 'finish', title: 'Submit report', description: 'Submit the work', agentId: node.agentId, inputFields: [], outputFields: [], inputs: {} }], edges: [{ id: 'finish', from: 'check', to: 'finish', label: 'Pass' }, { id: 'again', from: 'finish', to: 'check', label: 'Rework', maxTraversals: 3 }] },
    nodes: [{ ...node, id: oldNodeId, visit: 1, status: 'completed', inputs: { text: 'Earlier input' }, inputVersions: [], output: { result: 'Earlier successful result' }, runIds: ['00000000-0000-7000-8000-000000000106'] }, node],
    artifacts: [{ id: '00000000-0000-7000-8000-000000000107', taskId, nodeRunId: oldNodeId, name: 'saved-report.txt', contentType: 'text/plain', size: 22, sha256: 'a'.repeat(64), downloadPath: `/tasks/${taskId}/artifacts/00000000-0000-7000-8000-000000000107`, createdAt: taskTime }],
  }
}
export function makeTaskMessage(nodeRunId = nodeId, text = 'Reply from the task instance', id = '00000000-0000-7000-8000-000000000108'): Schemas['TaskMessage'] {
  return { id, nodeRunId, runId: nodeRunId === oldNodeId ? '00000000-0000-7000-8000-000000000106' : runId, role: 'assistant', origin: 'task_node', text, status: 'completed', createdAt: taskTime }
}
