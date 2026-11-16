import type { Schemas } from '@/api/client'

type Detail = Schemas['TaskDetail']
type Node = Schemas['TaskNodeRun']
type Edge = Schemas['TaskWorkflowEdge']

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

export interface StepView {
  step: Schemas['TaskWorkflowStep']
  index: number
  number: string
  records: Node[]
  latest: Node | undefined
  state: StepState
  // Labels of the edges leading to the next step (written between the two steps).
  next: Edge[]
  jumps: StepJump[]
}

export const stepNumber = (index: number) => String(index + 1).padStart(2, '0')

// The visits of one step, ordered by visit, then attempt.
export const sortRecords = (records: Node[]) => [...records].sort((a, b) => a.visit - b.visit || a.attempt - b.attempt || a.id.localeCompare(b.id))

export const taskEnded = (task: Schemas['TaskSummary']) => task.status === 'completed' || task.status === 'cancelled'

export function edgeUses(detail: Detail, edgeId: string) {
  return detail.nodes.filter((node) => node.edgeId === edgeId && node.status === 'completed').length
}

export function workflowSteps(detail: Detail): StepView[] {
  const { steps, edges } = detail.workflow
  const index = (id: string) => steps.findIndex((step) => step.id === id)
  const ended = taskEnded(detail.task)
  return steps.map((step, i) => {
    const records = sortRecords(detail.nodes.filter((node) => node.stepId === step.id))
    const latest = records.at(-1)
    const state: StepState = !ended && detail.task.currentStepId === step.id ? 'current' : !latest ? 'todo' : latest.status === 'completed' ? 'done' : 'visited'
    const outgoing = edges.filter((edge) => edge.from === step.id)
    return {
      step,
      index: i,
      number: stepNumber(i),
      records,
      latest,
      state,
      next: outgoing.filter((edge) => index(edge.to) === i + 1),
      jumps: outgoing
        .filter((edge) => index(edge.to) !== i + 1 && index(edge.to) >= 0)
        .map((edge) => ({ edge, to: index(edge.to), back: index(edge.to) <= i, used: edgeUses(detail, edge.id) })),
    }
  })
}

// The big number at the bottom of the ruler: which step it has reached (the current step while the task is unfinished, the last visited
// step once it has ended), and whether every step is done.
export function workflowPosition(detail: Detail) {
  const { steps } = detail.workflow
  const total = steps.length
  if (detail.task.status === 'completed') return { at: total, total, done: true }
  const stepId = detail.task.currentStepId ?? [...detail.nodes].sort((a, b) => (a.startedAt ?? '').localeCompare(b.startedAt ?? '')).at(-1)?.stepId
  return { at: stepId ? steps.findIndex((step) => step.id === stepId) + 1 : 0, total, done: false }
}
