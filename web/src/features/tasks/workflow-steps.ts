import type { Schemas } from '@/api/client'

type Detail = Schemas['TaskDetail']
type Node = Schemas['TaskNodeRun']
type Edge = Schemas['TaskWorkflowEdge']
type Fork = Schemas['TaskWorkflowFork']

// How a step looks on the workflow ruler: done (its latest visit completed), current (the unfinished task is at this step), visited but
// not completed, or not reached yet.
export type StepState = 'done' | 'current' | 'visited' | 'todo'

// An edge that doesn't lead to the next step (rework, skipping steps, looping back to itself): written under the step it starts from;
// rework is drawn as a pink dashed arc.
export interface StepJump {
  edge: Edge
  to: number
  back: boolean
  // How many times this edge has been taken: the number of visits that completed the starting step and chose this edge (the server
  // counts by the same rule and compares it with maxTraversals).
  used: number
}

// A secondary fork leaving a step (for example, a join step sending rework back to the same parallel branches).
export interface ForkJump {
  fork: Fork
  edges: Edge[]
  targets: number[]
  back: boolean
  used: number
  maxTraversals: number | null
}

export interface StepView {
  step: Schemas['TaskWorkflowStep']
  index: number
  number: string
  records: Node[]
  latest: Node | undefined
  state: StepState
  // Branch identifier (first step of the branch), or null for trunk steps outside every branch.
  branch: string | null
  // Labels of the edges leading to the next step (written between the two steps).
  next: Edge[]
  jumps: StepJump[]
}

export interface BranchLane {
  // Branch identifier (the first step of this branch).
  branch: string
  // Fork edge that starts this branch.
  entryEdge: Edge
  // Steps in this branch, in workflow order.
  steps: StepView[]
  // Edges leaving this branch into its join step.
  toJoin: Edge[]
  // Whether this branch currently has a current node.
  active: boolean
  // Whether this branch's current node is blocked.
  blocked: boolean
  // Whether this branch arrived at the join in the active (or latest) fork round.
  arrived: boolean
  // Matching branch workspace entry, if the task has a workspace.
  workspace: Schemas['TaskWorkspaceBranch'] | undefined
}

export interface ForkBlock {
  fork: Fork
  fromStepId: string
  joinStepId: string
  lanes: BranchLane[]
  active: boolean
  arrivedCount: number
  maxParallelBranches: number | null
}

export type WorkflowLayoutItem =
  | { kind: 'step'; view: StepView; joinsForks: ForkBlock[]; forkJumps: ForkJump[] }
  | { kind: 'fork'; block: ForkBlock }

export interface WorkflowLayout {
  hasForks: boolean
  maxLanes: number
  items: WorkflowLayoutItem[]
  views: StepView[]
}

export const stepNumber = (index: number) => String(index + 1).padStart(2, '0')

// The visits of one step, ordered by visit, then attempt.
export const sortRecords = (records: Node[]) => [...records].sort((a, b) => a.visit - b.visit || a.attempt - b.attempt || a.id.localeCompare(b.id))

export const taskEnded = (task: Schemas['TaskSummary']) => task.status === 'completed' || task.status === 'cancelled'

// Every current node of the task, in the order they became current. When several branches run at once, the single
// current* fields are null and currentNodes lists all of them; for older records without currentNodes, fall back to
// the single fields.
export function currentNodesOf(task: Schemas['TaskSummary']): Schemas['TaskCurrentNode'][] {
  if (task.currentNodes.length > 1) return task.currentNodes
  if (task.currentNodes.length === 1) {
    const only = task.currentNodes[0]
    if (task.currentStepId === null && task.currentNodeRunId === null) return []
    return [
      {
        nodeRunId: task.currentNodeRunId ?? only.nodeRunId,
        stepId: task.currentStepId ?? only.stepId,
        stepTitle: task.currentStepTitle === undefined ? only.stepTitle : (task.currentStepTitle ?? ''),
        agentId: task.currentAgentId ?? only.agentId,
        branch: only.branch,
      },
    ]
  }
  if (task.currentNodeRunId || task.currentStepId) {
    return [
      {
        nodeRunId: task.currentNodeRunId ?? '',
        stepId: task.currentStepId ?? '',
        stepTitle: task.currentStepTitle ?? '',
        agentId: task.currentAgentId ?? '',
        branch: null,
      },
    ]
  }
  return []
}

export function isCurrentNode(task: Schemas['TaskSummary'], nodeRunId: string): boolean {
  return !taskEnded(task) && currentNodesOf(task).some((c) => c.nodeRunId === nodeRunId)
}

export function isCurrentStep(task: Schemas['TaskSummary'], stepId: string): boolean {
  return !taskEnded(task) && currentNodesOf(task).some((c) => c.stepId === stepId)
}

export function edgeUses(detail: Detail, edgeId: string) {
  const edge = detail.workflow.edges.find((e) => e.id === edgeId)
  const match = edge?.fork ? (id: string | null) => id === edgeId || id === edge.fork : (id: string | null) => id === edgeId
  return detail.nodes.filter((node) => match(node.edgeId) && node.status === 'completed').length
}

export function forkUses(detail: Detail, forkId: string) {
  return detail.nodes.filter((node) => node.edgeId === forkId && node.status === 'completed').length
}

export function workflowSteps(detail: Detail): StepView[] {
  const { steps, edges } = detail.workflow
  const index = (id: string) => steps.findIndex((step) => step.id === id)
  const ended = taskEnded(detail.task)
  const currentStepIds = new Set(ended ? [] : currentNodesOf(detail.task).map((c) => c.stepId))
  return steps.map((step, i) => {
    const records = sortRecords(detail.nodes.filter((node) => node.stepId === step.id))
    const latest = records.at(-1)
    const state: StepState = !ended && currentStepIds.has(step.id) ? 'current' : !latest ? 'todo' : latest.status === 'completed' ? 'done' : 'visited'
    const outgoing = edges.filter((edge) => edge.from === step.id)
    return {
      step,
      index: i,
      number: stepNumber(i),
      records,
      latest,
      state,
      branch: latest?.branch ?? null,
      next: outgoing.filter((edge) => !edge.fork && index(edge.to) === i + 1),
      jumps: outgoing
        .filter((edge) => !edge.fork && index(edge.to) !== i + 1 && index(edge.to) >= 0)
        .map((edge) => ({ edge, to: index(edge.to), back: index(edge.to) <= i, used: edgeUses(detail, edge.id) })),
    }
  })
}

// Compute the structured layout of a workflow: sequential steps when there are no forks, or trunk steps interleaved with
// side-by-side parallel ForkBlocks when workflow.forks is present. Mirrors internal/server/workflow/fork.go.
export function workflowLayout(detail: Detail): WorkflowLayout {
  const views = workflowSteps(detail)
  const forks = detail.workflow.forks ?? []
  if (forks.length === 0) {
    return {
      hasForks: false,
      maxLanes: 1,
      items: views.map((view) => ({ kind: 'step', view, joinsForks: [], forkJumps: [] })),
      views,
    }
  }

  const { steps, edges } = detail.workflow
  const stepIndex = new Map(steps.map((s, i) => [s.id, i]))
  const viewById = new Map(views.map((v) => [v.step.id, v]))
  const outgoingOf = (id: string) => edges.filter((e) => e.from === id)
  const forkEdgesOf = (forkId: string) => edges.filter((e) => e.fork === forkId)

  // Collect steps reachable from each fork edge without crossing the fork's join.
  const collectBranch = (firstStep: string, joinStep: string): Set<string> => {
    const seen = new Set<string>()
    const queue = [firstStep]
    while (queue.length > 0) {
      const id = queue.shift()!
      if (id === joinStep || seen.has(id)) continue
      seen.add(id)
      for (const edge of outgoingOf(id)) queue.push(edge.to)
    }
    return seen
  }

  // Prefer forward forks (fromStep before joinStep) as primary owners of a branch set so a rework fork from the join
  // step back to the same branches renders as a rework badge instead of duplicating the parallel lanes.
  const orderedForks = [...forks].sort((a, b) => {
    const aFrom = forkEdgesOf(a.id)[0]?.from
    const bFrom = forkEdgesOf(b.id)[0]?.from
    const aForward = aFrom !== undefined && (stepIndex.get(aFrom) ?? 0) < (stepIndex.get(a.join) ?? Infinity) ? 0 : 1
    const bForward = bFrom !== undefined && (stepIndex.get(bFrom) ?? 0) < (stepIndex.get(b.join) ?? Infinity) ? 0 : 1
    return aForward - bForward
  })

  const branchOwner = new Map<string, string>() // stepId -> primary forkId
  const branchEntry = new Map<string, string>() // stepId -> branch first step
  const blocksByForkId = new Map<string, { fork: Fork; fromStepId: string; entries: Edge[]; branchStepSets: Map<string, Set<string>>; allForkIds: Set<string> }>()
  const secondaryForksByFrom = new Map<string, ForkJump[]>()

  for (const fork of orderedForks) {
    const fe = forkEdgesOf(fork.id)
    if (fe.length === 0) continue
    const fromStepId = fe[0].from
    const existingForkId = branchOwner.get(fe[0].to)
    if (existingForkId) {
      blocksByForkId.get(existingForkId)?.allForkIds.add(fork.id)
      const fromIdx = stepIndex.get(fromStepId) ?? 0
      const targets = fe.map((e) => stepIndex.get(e.to) ?? -1).filter((i) => i >= 0)
      const limits = fe.map((e) => e.maxTraversals).filter((n): n is number => typeof n === 'number')
      const jump: ForkJump = {
        fork,
        edges: fe,
        targets,
        back: targets.some((t) => t <= fromIdx),
        used: forkUses(detail, fork.id),
        maxTraversals: limits.length > 0 ? Math.min(...limits) : null,
      }
      const list = secondaryForksByFrom.get(fromStepId) ?? []
      list.push(jump)
      secondaryForksByFrom.set(fromStepId, list)
      continue
    }
    const branchStepSets = new Map<string, Set<string>>()
    for (const edge of fe) {
      const set = collectBranch(edge.to, fork.join)
      branchStepSets.set(edge.to, set)
      for (const sid of set) {
        branchOwner.set(sid, fork.id)
        branchEntry.set(sid, edge.to)
      }
    }
    blocksByForkId.set(fork.id, { fork, fromStepId, entries: fe, branchStepSets, allForkIds: new Set([fork.id]) })
  }

  const ended = taskEnded(detail.task)
  const currentList = ended ? [] : currentNodesOf(detail.task)
  const currentNodeIds = new Set(currentList.map((c) => c.nodeRunId))
  const currentByStep = new Map(currentList.map((c) => [c.stepId, detail.nodes.find((n) => n.id === c.nodeRunId)]))
  const workspacesByBranch = new Map((detail.workspace?.branches ?? []).map((b) => [b.step, b]))

  // Build ForkBlocks with lane-scoped step views and accurate current-round arrival status.
  const blocks: ForkBlock[] = []
  const blocksByFrom = new Map<string, ForkBlock[]>()
  const blocksByJoin = new Map<string, ForkBlock[]>()
  const updatedViews = new Map<string, StepView>()

  for (const raw of blocksByForkId.values()) {
    // Latest completed node that triggered this block (either its primary fork or a rework fork targeting the same lanes).
    let roundStartIdx = -1
    for (let i = detail.nodes.length - 1; i >= 0; i--) {
      const n = detail.nodes[i]
      if (n.status === 'completed' && n.edgeId && raw.allForkIds.has(n.edgeId)) {
        roundStartIdx = i
        break
      }
    }
    const roundNodes = roundStartIdx >= 0 ? detail.nodes.slice(roundStartIdx + 1) : detail.nodes

    const lanes: BranchLane[] = raw.entries.map((entryEdge) => {
      const branch = entryEdge.to
      const set = raw.branchStepSets.get(branch) ?? new Set([branch])
      const laneStepIds = steps.filter((s) => set.has(s.id)).map((s) => s.id)
      const laneIdx = new Map(laneStepIds.map((id, i) => [id, i]))
      const toJoin: Edge[] = []

      const laneSteps: StepView[] = laneStepIds.map((sid, pos) => {
        const base = viewById.get(sid)!
        const outgoing = outgoingOf(sid).filter((e) => !e.fork)
        const joinEdges = outgoing.filter((e) => e.to === raw.fork.join)
        toJoin.push(...joinEdges)
        const internal = outgoing.filter((e) => e.to !== raw.fork.join)
        const nextStepId = laneStepIds[pos + 1]
        const stepView: StepView = {
          ...base,
          branch,
          next: nextStepId ? internal.filter((e) => e.to === nextStepId) : [],
          jumps: internal
            .filter((e) => e.to !== nextStepId && stepIndex.has(e.to))
            .map((e) => {
              const targetLanePos = laneIdx.get(e.to)
              return {
                edge: e,
                to: stepIndex.get(e.to)!,
                back: targetLanePos !== undefined ? targetLanePos <= pos : (stepIndex.get(e.to)! <= base.index),
                used: edgeUses(detail, e.id),
              }
            }),
        }
        updatedViews.set(sid, stepView)
        return stepView
      })

      const activeStep = laneStepIds.find((sid) => {
        const cur = currentByStep.get(sid)
        return cur ? currentNodeIds.has(cur.id) : false
      })
      const activeNode = activeStep ? currentByStep.get(activeStep) : undefined
      const active = Boolean(activeStep)
      const blocked = activeNode?.status === 'blocked'
      const joinEdgeIds = new Set(toJoin.map((e) => e.id))
      const arrived =
        !active &&
        roundNodes.some((n) => set.has(n.stepId) && n.status === 'completed' && n.edgeId !== null && joinEdgeIds.has(n.edgeId))

      return {
        branch,
        entryEdge,
        steps: laneSteps,
        toJoin,
        active,
        blocked,
        arrived,
        workspace: workspacesByBranch.get(branch),
      }
    })

    const block: ForkBlock = {
      fork: raw.fork,
      fromStepId: raw.fromStepId,
      joinStepId: raw.fork.join,
      lanes,
      active: lanes.some((l) => l.active),
      arrivedCount: lanes.filter((l) => l.arrived).length,
      maxParallelBranches: detail.workflow.maxParallelBranches ?? null,
    }
    blocks.push(block)
    const fromList = blocksByFrom.get(raw.fromStepId) ?? []
    fromList.push(block)
    blocksByFrom.set(raw.fromStepId, fromList)
    const joinList = blocksByJoin.get(raw.fork.join) ?? []
    joinList.push(block)
    blocksByJoin.set(raw.fork.join, joinList)
  }

  // Build trunk step sequence and interleave ForkBlocks immediately after their originating step.
  const trunkSteps = steps.filter((s) => !branchOwner.has(s.id))
  const trunkIdx = new Map(trunkSteps.map((s, i) => [s.id, i]))
  const items: WorkflowLayoutItem[] = []
  const placedBlocks = new Set<string>()

  for (let ti = 0; ti < trunkSteps.length; ti++) {
    const step = trunkSteps[ti]
    const base = viewById.get(step.id)!
    const nextTrunk = trunkSteps[ti + 1]
    const outgoing = outgoingOf(step.id).filter((e) => !e.fork)
    const ownBlocks = blocksByFrom.get(step.id) ?? []
    // If a fork block sits right after this trunk step, an ordinary edge directly to the next trunk step bypasses the
    // fork and should read as a jump rather than a straight-down arrow through the fork.
    const directNextId = ownBlocks.length === 0 ? nextTrunk?.id : undefined
    const trunkView: StepView = {
      ...base,
      branch: null,
      next: directNextId ? outgoing.filter((e) => e.to === directNextId) : [],
      jumps: outgoing
        .filter((e) => e.to !== directNextId && stepIndex.has(e.to))
        .map((e) => {
          const targetTrunkPos = trunkIdx.get(e.to)
          return {
            edge: e,
            to: stepIndex.get(e.to)!,
            back: targetTrunkPos !== undefined ? targetTrunkPos <= ti : (stepIndex.get(e.to)! <= base.index),
            used: edgeUses(detail, e.id),
          }
        }),
    }
    updatedViews.set(step.id, trunkView)
    items.push({
      kind: 'step',
      view: trunkView,
      joinsForks: blocksByJoin.get(step.id) ?? [],
      forkJumps: secondaryForksByFrom.get(step.id) ?? [],
    })
    for (const block of ownBlocks) {
      if (!placedBlocks.has(block.fork.id)) {
        placedBlocks.add(block.fork.id)
        items.push({ kind: 'fork', block })
      }
    }
  }
  for (const block of blocks) {
    if (!placedBlocks.has(block.fork.id)) {
      items.push({ kind: 'fork', block })
    }
  }

  return {
    hasForks: blocks.length > 0,
    maxLanes: blocks.reduce((max, b) => Math.max(max, b.lanes.length), 1),
    items,
    views: views.map((v) => updatedViews.get(v.step.id) ?? { ...v, branch: branchEntry.get(v.step.id) ?? null }),
  }
}

// The big number at the bottom of the ruler: which step it has reached (the current step while the task is unfinished, the last visited
// step once it has ended), and whether every step is done.
export function workflowPosition(detail: Detail) {
  const { steps } = detail.workflow
  const total = steps.length
  if (detail.task.status === 'completed') return { at: total, total, done: true }
  const current = taskEnded(detail.task) ? [] : currentNodesOf(detail.task)
  const stepId = current[0]?.stepId ?? [...detail.nodes].sort((a, b) => (a.startedAt ?? '').localeCompare(b.startedAt ?? '')).at(-1)?.stepId
  return { at: stepId ? steps.findIndex((step) => step.id === stepId) + 1 : 0, total, done: false }
}
