import type { Schemas } from '@/api/client'
export const taskId = '00000000-0000-7000-8000-000000000100'
export const nodeId = '00000000-0000-7000-8000-000000000101'
export const runId = '00000000-0000-7000-8000-000000000102'
export const oldNodeId = '00000000-0000-7000-8000-000000000103'
export const taskTime = '2026-10-14T08:00:00Z'
export function makeTaskDetail(projectId: string, projectName = 'test-project'): Schemas['TaskDetail'] {
  const node: Schemas['TaskNodeRun'] = {
    branch: null,
    sleep: null, sleepRevision: 0,
    id: nodeId, taskId, stepId: 'check', visit: 2, attempt: 1, agentId: '00000000-0000-7000-8000-000000000104',
    agentName: 'task-checker', runtimeId: '00000000-0000-7000-8000-000000000105', status: 'blocked',
    inputs: { text: 'This input' }, inputVersions: [{ input: 'text', nodeRunId: oldNodeId }], output: { result: 'Result for checking only' }, edgeId: 'finish', reason: 'Something went wrong in this turn; check the result', question: null,
    runIds: [runId], startedAt: taskTime, endedAt: taskTime,
  }
  return {
    sleepingInstances: [],
    task: {
      id: taskId, projectId, number: 12, title: 'General task acceptance', status: 'blocked',
      currentNodeRunId: nodeId, currentStepId: 'check', currentStepTitle: 'Quality check', currentAgentId: node.agentId,
      currentNodes: [{ nodeRunId: nodeId, stepId: 'check', stepTitle: 'Quality check', agentId: node.agentId, branch: null }],
      reason: node.reason, createdAt: taskTime, updatedAt: taskTime, endedAt: null,
      link: `/p/${encodeURIComponent(projectName)}/tasks/${taskId}`, version: 2, branch: null, workspaceMode: null,
    },
    request: { text: 'Original request' }, cleanup: 'none', workspace: null,
    workflow: { startStepId: 'check', steps: [{ id: 'check', title: 'Quality check', description: 'Check the input', agentId: node.agentId, inputFields: [{ name: 'text', type: 'string', required: true }], outputFields: [{ name: 'result', type: 'string', required: true }], inputs: { text: { kind: 'request', field: 'text' } } }, { id: 'finish', title: 'Submit report', description: 'Submit the work', agentId: node.agentId, inputFields: [], outputFields: [], inputs: {} }], edges: [{ id: 'finish', from: 'check', to: 'finish', label: 'Pass' }, { id: 'again', from: 'finish', to: 'check', label: 'Rework', maxTraversals: 3 }] },
    nodes: [{ ...node, id: oldNodeId, visit: 1, status: 'completed', inputs: { text: 'Earlier input' }, inputVersions: [], output: { result: 'Earlier successful result' }, runIds: ['00000000-0000-7000-8000-000000000106'] }, node],
    artifacts: [{ id: '00000000-0000-7000-8000-000000000107', taskId, nodeRunId: oldNodeId, name: 'saved-report.txt', contentType: 'text/plain', size: 22, sha256: 'a'.repeat(64), downloadPath: `/tasks/${taskId}/artifacts/00000000-0000-7000-8000-000000000107`, createdAt: taskTime }],
  }
}
export function makeTaskMessage(nodeRunId = nodeId, text = 'Reply from the task instance', id = '00000000-0000-7000-8000-000000000108'): Schemas['TaskMessage'] {
  return { id, nodeRunId, runId: nodeRunId === oldNodeId ? '00000000-0000-7000-8000-000000000106' : runId, role: 'assistant', origin: 'task_node', text, status: 'completed', createdAt: taskTime }
}
type WorkspaceOverrides = Partial<Omit<Schemas['TaskWorkspace'], 'git' | 'copy'>> & { git?: Partial<Schemas['TaskWorkspaceGit']> }
// A task's git-mode workspace (a worktree) as the server reports it; by default active, created from main.
export function makeTaskWorkspace({ git, ...overrides }: WorkspaceOverrides = {}): Schemas['TaskWorkspace'] {
  return {
    mode: 'git', folder: '/Users/someone/projects/demo', path: `/Users/someone/Library/Application Support/Loop Now/default/worktrees/${taskId}/demo`,
    state: 'active', kept: null, modeNote: null, copy: null, branches: [],
    ...overrides,
    git: { repository: '/Users/someone/projects/demo', branch: 'task/12-general-task-acceptance', baseBranch: 'main', baseCommit: '4f2c9a1d8e7b6c5a4f3e2d1c0b9a8f7e6d5c4b3a', ...git },
  }
}
// A task's copy-mode workspace: a copy of a plain folder; by default active, 42 files and 1.5 MiB.
export function makeCopyWorkspace(overrides: Partial<Omit<Schemas['TaskWorkspace'], 'git'>> = {}): Schemas['TaskWorkspace'] {
  return {
    mode: 'copy', folder: '/Users/someone/Documents/reports', path: `/Users/someone/Library/Application Support/Loop Now/default/worktrees/${taskId}/reports`,
    state: 'active', kept: null, modeNote: null, copy: { files: 42, bytes: 1_572_864 }, branches: [],
    ...overrides, git: null,
  }
}

export const forkPlanNodeId = '00000000-0000-7000-8000-000000000201'
export const forkServerNodeId = '00000000-0000-7000-8000-000000000202'
export const forkWebNodeId = '00000000-0000-7000-8000-000000000203'
export const forkLeadAgentId = '00000000-0000-7000-8000-000000000211'
export const forkServerAgentId = '00000000-0000-7000-8000-000000000212'
export const forkWebAgentId = '00000000-0000-7000-8000-000000000213'

// A task inside a parallel fork: plan completed and took fork "build", starting "server" and "web" at once, which will
// meet at "verify" before "finish". Both branches are currently running in their own worktrees.
export function makeForkTaskDetail(projectId: string, projectName = 'test-project'): Schemas['TaskDetail'] {
  const planNode: Schemas['TaskNodeRun'] = {
    branch: null, sleep: null, sleepRevision: 0,
    id: forkPlanNodeId, taskId, stepId: 'plan', visit: 1, attempt: 1,
    agentId: forkLeadAgentId, agentName: 'lead', runtimeId: '00000000-0000-7000-8000-000000000221',
    status: 'completed', inputs: { goal: 'Ship feature' }, inputVersions: [], output: { contract: 'v1' },
    edgeId: 'build', reason: null, question: null, runIds: ['00000000-0000-7000-8000-000000000231'],
    startedAt: '2026-10-14T08:00:00Z', endedAt: '2026-10-14T08:02:00Z',
  }
  const serverNode: Schemas['TaskNodeRun'] = {
    branch: 'server', sleep: null, sleepRevision: 0,
    id: forkServerNodeId, taskId, stepId: 'server', visit: 1, attempt: 1,
    agentId: forkServerAgentId, agentName: 'backend-dev', runtimeId: '00000000-0000-7000-8000-000000000222',
    status: 'running', inputs: { contract: 'v1' }, inputVersions: [{ input: 'contract', nodeRunId: forkPlanNodeId }],
    output: null, edgeId: null, reason: null, question: null, runIds: ['00000000-0000-7000-8000-000000000232'],
    startedAt: '2026-10-14T08:02:05Z', endedAt: null,
  }
  const webNode: Schemas['TaskNodeRun'] = {
    branch: 'web', sleep: null, sleepRevision: 0,
    id: forkWebNodeId, taskId, stepId: 'web', visit: 1, attempt: 1,
    agentId: forkWebAgentId, agentName: 'frontend-dev', runtimeId: '00000000-0000-7000-8000-000000000223',
    status: 'running', inputs: { contract: 'v1' }, inputVersions: [{ input: 'contract', nodeRunId: forkPlanNodeId }],
    output: null, edgeId: null, reason: null, question: null, runIds: ['00000000-0000-7000-8000-000000000233'],
    startedAt: '2026-10-14T08:02:06Z', endedAt: null,
  }
  const workspace = makeTaskWorkspace({
    branches: [
      {
        step: 'server',
        path: `/Users/someone/Library/Application Support/Loop Now/default/worktrees/${taskId}/demo--server`,
        state: 'active',
        merge: null,
        git: { branch: 'task/12-general-task-acceptance--server', baseCommit: '4f2c9a1d8e7b6c5a4f3e2d1c0b9a8f7e6d5c4b3a' },
      },
      {
        step: 'web',
        path: `/Users/someone/Library/Application Support/Loop Now/default/worktrees/${taskId}/demo--web`,
        state: 'active',
        merge: null,
        git: { branch: 'task/12-general-task-acceptance--web', baseCommit: '4f2c9a1d8e7b6c5a4f3e2d1c0b9a8f7e6d5c4b3a' },
      },
    ],
  })
  return {
    sleepingInstances: [],
    task: {
      id: taskId, projectId, number: 12, title: 'Build parallel feature', status: 'running',
      currentNodeRunId: null, currentStepId: null, currentStepTitle: null, currentAgentId: null,
      currentNodes: [
        { nodeRunId: forkServerNodeId, stepId: 'server', stepTitle: 'Server', agentId: forkServerAgentId, branch: 'server' },
        { nodeRunId: forkWebNodeId, stepId: 'web', stepTitle: 'Web', agentId: forkWebAgentId, branch: 'web' },
      ],
      reason: null, createdAt: taskTime, updatedAt: '2026-10-14T08:02:06Z', endedAt: null,
      link: `/p/${encodeURIComponent(projectName)}/tasks/${taskId}`, version: 4,
      branch: workspace.git!.branch, workspaceMode: 'git',
    },
    request: { goal: 'Ship feature' },
    cleanup: 'none',
    workspace,
    workflow: {
      startStepId: 'plan',
      steps: [
        { id: 'plan', title: 'Plan', description: 'Agree on the contract', agentId: forkLeadAgentId, inputFields: [{ name: 'goal', type: 'string', required: true }], outputFields: [{ name: 'contract', type: 'string', required: true }], inputs: { goal: { kind: 'request', field: 'goal' } } },
        { id: 'server', title: 'Server', description: 'Implement backend', agentId: forkServerAgentId, inputFields: [{ name: 'contract', type: 'string', required: true }], outputFields: [{ name: 'summary', type: 'string', required: true }], inputs: { contract: { kind: 'node', nodeId: 'plan', field: 'contract' } } },
        { id: 'web', title: 'Web', description: 'Implement console', agentId: forkWebAgentId, inputFields: [{ name: 'contract', type: 'string', required: true }], outputFields: [{ name: 'summary', type: 'string', required: true }], inputs: { contract: { kind: 'node', nodeId: 'plan', field: 'contract' } } },
        { id: 'verify', title: 'Verify', description: 'Check merged result', agentId: forkLeadAgentId, inputFields: [], outputFields: [], inputs: {} },
        { id: 'finish', title: 'Finish', description: 'Wrap up', agentId: forkLeadAgentId, inputFields: [], outputFields: [], inputs: {} },
      ],
      edges: [
        { id: 'to_server', from: 'plan', to: 'server', label: 'Backend', fork: 'build' },
        { id: 'to_web', from: 'plan', to: 'web', label: 'Frontend', fork: 'build' },
        { id: 'server_done', from: 'server', to: 'verify', label: 'Done' },
        { id: 'web_done', from: 'web', to: 'verify', label: 'Done' },
        { id: 'passed', from: 'verify', to: 'finish', label: 'Passed' },
      ],
      forks: [{ id: 'build', join: 'verify' }],
    },
    nodes: [planNode, serverNode, webNode],
    artifacts: [],
  }
}
