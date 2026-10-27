import type { Schemas } from '@/api/client'
export const taskId = '00000000-0000-7000-8000-000000000100'
export const nodeId = '00000000-0000-7000-8000-000000000101'
export const runId = '00000000-0000-7000-8000-000000000102'
export const oldNodeId = '00000000-0000-7000-8000-000000000103'
export const taskTime = '2026-10-14T08:00:00Z'
export function makeTaskDetail(projectId: string, projectName = '测试项目'): Schemas['TaskDetail'] {
  const node: Schemas['TaskNodeRun'] = {
    sleep: null, sleepRevision: 0,
    id: nodeId, taskId, stepId: 'check', visit: 2, attempt: 1, agentId: '00000000-0000-7000-8000-000000000104',
    agentName: '任务检查员', runtimeId: '00000000-0000-7000-8000-000000000105', status: 'blocked',
    inputs: { text: '本次输入' }, inputVersions: [{ input: 'text', nodeRunId: oldNodeId }], output: { result: '仅供检查的结果' }, edgeId: 'finish', reason: '本轮异常，请检查结果', question: null,
    runIds: [runId], startedAt: taskTime, endedAt: taskTime,
  }
  return {
    sleepingInstances: [],
    task: { id: taskId, projectId, title: '通用任务验收', status: 'blocked', currentNodeRunId: nodeId, currentStepId: 'check', currentAgentId: node.agentId, reason: node.reason, createdAt: taskTime, updatedAt: taskTime, endedAt: null, link: `/p/${encodeURIComponent(projectName)}/tasks/${taskId}`, version: 2 },
    request: { text: '原始要求' }, cleanup: 'none',
    workflow: { startStepId: 'check', steps: [{ id: 'check', title: '质量检查', description: '检查输入', agentId: node.agentId, inputFields: [{ name: 'text', type: 'string', required: true }], outputFields: [{ name: 'result', type: 'string', required: true }], inputs: { text: { kind: 'request', field: 'text' } } }, { id: 'finish', title: '提交报告', description: '提交成果', agentId: node.agentId, inputFields: [], outputFields: [], inputs: {} }], edges: [{ id: 'finish', from: 'check', to: 'finish', label: '通过' }, { id: 'again', from: 'finish', to: 'check', label: '返工', maxTraversals: 3 }] },
    nodes: [{ ...node, id: oldNodeId, visit: 1, status: 'completed', inputs: { text: '历史输入' }, inputVersions: [], output: { result: '历史成功结果' }, runIds: ['00000000-0000-7000-8000-000000000106'] }, node],
    artifacts: [{ id: '00000000-0000-7000-8000-000000000107', taskId, nodeRunId: oldNodeId, name: '已保存报告.txt', contentType: 'text/plain', size: 22, sha256: 'a'.repeat(64), downloadPath: `/tasks/${taskId}/artifacts/00000000-0000-7000-8000-000000000107`, createdAt: taskTime }],
  }
}
export function makeTaskMessage(nodeRunId = nodeId, text = '任务实例的回复', id = '00000000-0000-7000-8000-000000000108'): Schemas['TaskMessage'] {
  return { id, nodeRunId, runId: nodeRunId === oldNodeId ? '00000000-0000-7000-8000-000000000106' : runId, role: 'assistant', origin: 'task_node', text, status: 'completed', createdAt: taskTime }
}
