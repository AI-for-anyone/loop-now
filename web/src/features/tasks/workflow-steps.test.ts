import { describe, expect, it } from 'vitest'
import {
  forkLeadAgentId,
  forkPlanNodeId,
  forkServerAgentId,
  forkServerNodeId,
  forkWebAgentId,
  forkWebNodeId,
  makeForkTaskDetail,
  makeTaskDetail,
  nodeId,
} from '@/test/tasks'
import { currentNodesOf, edgeUses, forkUses, isCurrentNode, isCurrentStep, workflowLayout, workflowSteps } from './workflow-steps'

describe('workflow-steps fork and multi-current helpers', () => {
  it('recognizes every running branch as current while single current* fields are null', () => {
    const detail = makeForkTaskDetail('project')
    expect(detail.task.currentNodeRunId).toBeNull()
    expect(currentNodesOf(detail.task).map((c) => c.stepId)).toEqual(['server', 'web'])
    expect(isCurrentNode(detail.task, forkServerNodeId)).toBe(true)
    expect(isCurrentNode(detail.task, forkWebNodeId)).toBe(true)
    expect(isCurrentNode(detail.task, forkPlanNodeId)).toBe(false)
    expect(isCurrentStep(detail.task, 'server')).toBe(true)
    expect(isCurrentStep(detail.task, 'web')).toBe(true)
    expect(isCurrentStep(detail.task, 'plan')).toBe(false)

    const steps = workflowSteps(detail)
    expect(steps.map((s) => [s.step.id, s.state])).toEqual([
      ['plan', 'done'],
      ['server', 'current'],
      ['web', 'current'],
      ['verify', 'todo'],
      ['finish', 'todo'],
    ])
  })

  it('falls back to the single current* fields for records stored before currentNodes existed', () => {
    const detail = makeTaskDetail('project')
    detail.task.currentNodes = []
    expect(currentNodesOf(detail.task)).toEqual([
      { nodeRunId: nodeId, stepId: 'check', stepTitle: 'Quality check', agentId: detail.task.currentAgentId, branch: null },
    ])
    expect(isCurrentNode(detail.task, nodeId)).toBe(true)
  })

  it('lays out a sequential workflow without forks as plain steps', () => {
    const layout = workflowLayout(makeTaskDetail('project'))
    expect(layout.hasForks).toBe(false)
    expect(layout.maxLanes).toBe(1)
    expect(layout.items.map((item) => item.kind)).toEqual(['step', 'step'])
  })

  it('lays out a fork/join workflow into trunk steps and a side-by-side fork block with lane arrival state', () => {
    const detail = makeForkTaskDetail('project')
    const layout = workflowLayout(detail)
    expect(layout.hasForks).toBe(true)
    expect(layout.maxLanes).toBe(2)
    expect(layout.items.map((item) => (item.kind === 'step' ? `step:${item.view.step.id}` : `fork:${item.block.fork.id}`))).toEqual([
      'step:plan',
      'fork:build',
      'step:verify',
      'step:finish',
    ])
    const forkItem = layout.items[1]
    expect(forkItem.kind).toBe('fork')
    if (forkItem.kind !== 'fork') return
    expect(forkItem.block.active).toBe(true)
    expect(forkItem.block.arrivedCount).toBe(0)
    expect(forkItem.block.lanes.map((l) => [l.branch, l.active, l.arrived, l.toJoin.map((e) => e.id)])).toEqual([
      ['server', true, false, ['server_done']],
      ['web', true, false, ['web_done']],
    ])
    // Branch edges into the join are tracked in toJoin, not duplicated as jump badges on branch steps.
    expect(forkItem.block.lanes[0].steps[0].jumps).toEqual([])
    expect(forkItem.block.lanes[0].workspace?.git?.branch).toBe('task/12-general-task-acceptance--server')
    const verifyItem = layout.items[2]
    expect(verifyItem.kind === 'step' && verifyItem.joinsForks.map((b) => b.fork.id)).toEqual(['build'])
    expect(forkUses(detail, 'build')).toBe(1)
    expect(edgeUses(detail, 'to_server')).toBe(1)
  })

  it('tracks one arrived branch while the slower branch is still running or blocked', () => {
    const detail = makeForkTaskDetail('project')
    detail.nodes[1] = { ...detail.nodes[1], status: 'completed', edgeId: 'server_done', endedAt: '2026-10-14T08:05:00Z' }
    detail.nodes[2] = { ...detail.nodes[2], status: 'blocked', reason: 'Compile error' }
    detail.task.status = 'blocked'
    detail.task.currentNodes = [detail.task.currentNodes[1]]
    detail.task.currentNodeRunId = forkWebNodeId
    detail.task.currentStepId = 'web'
    detail.task.currentStepTitle = 'Web'
    detail.task.currentAgentId = forkWebAgentId

    const layout = workflowLayout(detail)
    const forkItem = layout.items[1]
    if (forkItem.kind !== 'fork') throw new Error('expected fork item')
    expect(forkItem.block.active).toBe(true)
    expect(forkItem.block.arrivedCount).toBe(1)
    expect(forkItem.block.lanes[0]).toMatchObject({ branch: 'server', active: false, arrived: true, blocked: false })
    expect(forkItem.block.lanes[1]).toMatchObject({ branch: 'web', active: true, arrived: false, blocked: true })
  })

  it('keeps a rework fork from the join step as a back-jump without duplicating lanes, and resets arrival counts on the new round', () => {
    const detail = makeForkTaskDetail('project')
    detail.workflow.forks = [
      { id: 'build', join: 'verify' },
      { id: 'rework', join: 'verify' },
    ]
    detail.workflow.edges.push(
      { id: 'fix_server', from: 'verify', to: 'server', label: 'Rework', fork: 'rework', maxTraversals: 2 },
      { id: 'fix_web', from: 'verify', to: 'web', label: 'Rework', fork: 'rework', maxTraversals: 2 },
    )
    const verifyNode = {
      branch: null, sleep: null, sleepRevision: 0,
      id: '00000000-0000-7000-8000-000000000204', taskId: detail.task.id, stepId: 'verify', visit: 1, attempt: 1,
      agentId: forkLeadAgentId, agentName: 'lead', runtimeId: '00000000-0000-7000-8000-000000000221',
      status: 'completed' as const, inputs: {}, inputVersions: [], output: {}, edgeId: 'rework', reason: null, question: null,
      runIds: ['00000000-0000-7000-8000-000000000234'], startedAt: '2026-10-14T08:10:00Z', endedAt: '2026-10-14T08:12:00Z',
    }
    const serverV2 = {
      ...detail.nodes[1],
      id: '00000000-0000-7000-8000-000000000205', visit: 2, status: 'running' as const, edgeId: null, endedAt: null,
      startedAt: '2026-10-14T08:12:05Z',
    }
    detail.nodes = [
      detail.nodes[0],
      { ...detail.nodes[1], status: 'completed', edgeId: 'server_done', endedAt: '2026-10-14T08:05:00Z' },
      { ...detail.nodes[2], status: 'completed', edgeId: 'web_done', endedAt: '2026-10-14T08:06:00Z' },
      verifyNode,
      serverV2,
    ]
    detail.workflow.maxParallelBranches = 1
    detail.task.currentNodes = [{ nodeRunId: serverV2.id, stepId: 'server', stepTitle: 'Server', agentId: forkServerAgentId, branch: 'server' }]
    detail.task.currentNodeRunId = serverV2.id
    detail.task.currentStepId = 'server'
    detail.task.currentStepTitle = 'Server'
    detail.task.currentAgentId = forkServerAgentId

    const layout = workflowLayout(detail)
    // Only one ForkBlock (build); rework is attached as a ForkJump on verify.
    expect(layout.items.filter((i) => i.kind === 'fork')).toHaveLength(1)
    const forkItem = layout.items[1]
    if (forkItem.kind !== 'fork') throw new Error('expected fork item')
    // Web has NOT arrived in round 2 yet (its visit 1 was before verify took rework).
    expect(forkItem.block.arrivedCount).toBe(0)
    expect(forkItem.block.lanes[1].arrived).toBe(false)
    expect(forkItem.block.maxParallelBranches).toBe(1)

    const verifyItem = layout.items[2]
    if (verifyItem.kind !== 'step') throw new Error('expected verify step')
    expect(verifyItem.forkJumps).toHaveLength(1)
    expect(verifyItem.forkJumps[0]).toMatchObject({ back: true, targets: [1, 2], used: 1, maxTraversals: 2 })
  })

  it('supports multi-step branches with internal edges and internal rework loops', () => {
    const detail = makeForkTaskDetail('project')
    detail.workflow.steps.splice(2, 0, {
      id: 'server_test', title: 'Server tests', description: 'Run tests', agentId: forkServerAgentId,
      inputFields: [], outputFields: [], inputs: {},
    })
    detail.workflow.edges = [
      { id: 'to_server', from: 'plan', to: 'server', label: 'Backend', fork: 'build' },
      { id: 'to_web', from: 'plan', to: 'web', label: 'Frontend', fork: 'build' },
      { id: 'server_to_test', from: 'server', to: 'server_test', label: 'Built' },
      { id: 'test_retry', from: 'server_test', to: 'server', label: 'Fix tests', maxTraversals: 2 },
      { id: 'server_done', from: 'server_test', to: 'verify', label: 'Passed' },
      { id: 'web_done', from: 'web', to: 'verify', label: 'Done' },
      { id: 'passed', from: 'verify', to: 'finish', label: 'Passed' },
    ]
    const layout = workflowLayout(detail)
    const forkItem = layout.items[1]
    if (forkItem.kind !== 'fork') throw new Error('expected fork item')
    expect(forkItem.block.lanes[0].steps.map((s) => s.step.id)).toEqual(['server', 'server_test'])
    expect(forkItem.block.lanes[0].steps[0].next.map((e) => e.id)).toEqual(['server_to_test'])
    expect(forkItem.block.lanes[0].steps[1].jumps).toMatchObject([{ edge: { id: 'test_retry' }, to: 1, back: true }])
    expect(forkItem.block.lanes[0].toJoin.map((e) => e.id)).toEqual(['server_done'])
  })
})
