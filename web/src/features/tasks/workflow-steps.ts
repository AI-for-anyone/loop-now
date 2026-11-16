import type { Schemas } from '@/api/client'

type Detail = Schemas['TaskDetail']
type Node = Schemas['TaskNodeRun']
type Edge = Schemas['TaskWorkflowEdge']

// 一步在流程尺上的样子：已完成（最近一次访问已完成）、当前步（任务还没结束时停在这一步）、走过但没完成、还没到。
export type StepState = 'done' | 'current' | 'visited' | 'todo'

// 不是走向下一步的连线（返工、跳过几步、回到自己）：写在出发的那一步下面，返工画成粉色虚线弧。
export interface StepJump {
  edge: Edge
  to: number
  back: boolean
  // 这条线已经走过几次：从出发步完成、选了这条线的访问数（服务端按同样的规则数，与 maxTraversals 比较）。
  used: number
}

export interface StepView {
  step: Schemas['TaskWorkflowStep']
  index: number
  number: string
  records: Node[]
  latest: Node | undefined
  state: StepState
  // 走向下一步的连线说明（写在两步之间）。
  next: Edge[]
  jumps: StepJump[]
}

export const stepNumber = (index: number) => String(index + 1).padStart(2, '0')

// 同一步的各次访问按访问、尝试的先后排。
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

// 尺子底部的大数字：走到第几步（任务没结束时是当前步，结束后是最后访问的那一步），以及是否已经全部走完。
export function workflowPosition(detail: Detail) {
  const { steps } = detail.workflow
  const total = steps.length
  if (detail.task.status === 'completed') return { at: total, total, done: true }
  const stepId = detail.task.currentStepId ?? [...detail.nodes].sort((a, b) => (a.startedAt ?? '').localeCompare(b.startedAt ?? '')).at(-1)?.stepId
  return { at: stepId ? steps.findIndex((step) => step.id === stepId) + 1 : 0, total, done: false }
}
