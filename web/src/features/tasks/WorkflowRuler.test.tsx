import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { makeTaskDetail, nodeId, oldNodeId } from '@/test/tasks'
import { WorkflowChips, WorkflowRuler } from './WorkflowRuler'
import { edgeUses, workflowPosition, workflowSteps } from './workflow-steps'

// A two-step workflow (“质量检查” (quality check) → “提交报告” (submit report), where submit report
// can send rework back to quality check), currently stopped at the 2nd visit of quality check.
// Add one more completed submit-report visit that took the rework edge, so the rework edge has been used once.
function detailWithRework(): Schemas['TaskDetail'] {
  const detail = makeTaskDetail('project')
  const report: Schemas['TaskNodeRun'] = { ...detail.nodes[0], id: '00000000-0000-7000-8000-000000000301', stepId: 'finish', visit: 1, status: 'completed', edgeId: 'again' }
  // A visit that chose the rework edge when it failed (blocked) does not count as a use.
  const failed: Schemas['TaskNodeRun'] = { ...report, id: '00000000-0000-7000-8000-000000000302', visit: 2, status: 'blocked' }
  return { ...detail, nodes: [...detail.nodes, report, failed] }
}
const ruler = (detail: Schemas['TaskDetail'], props: Partial<Parameters<typeof WorkflowRuler>[0]> = {}) =>
  render(<WorkflowRuler detail={detail} selected="check" record={nodeId} onSelect={() => {}} onRecord={() => {}} agentName={(id) => id} {...props} />)

describe('workflow ruler', () => {
  it('each step is a dot on the ruler: the current step is a big yellow dot with the step number (aria-current=step), visited steps show their latest status, and steps not reached say “not yet”', () => {
    const detail = makeTaskDetail('project')
    ruler(detail)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    const current = within(nav).getByRole('button', { name: /Quality check/ })
    expect(current).toHaveAttribute('aria-current', 'step')
    expect(current).toHaveAttribute('aria-pressed', 'true')
    expect(current).toHaveTextContent('01Quality checktask-checker· Blocked×2')
    // The status and the visit count are wrapped together, so they move together when the line wraps.
    expect(current.querySelector('[data-step-state]')).toHaveTextContent('· Blocked×2')
    expect(current.querySelector('[data-step-state]')).toHaveClass('whitespace-nowrap')
    expect(nav.querySelector('[data-step-dot="0"]')).toHaveTextContent('01')
    expect(nav.querySelector('[data-step-dot="0"]')).toHaveClass('bg-riso-yellow')
    const todo = within(nav).getByRole('button', { name: /Submit report/ })
    expect(todo).not.toHaveAttribute('aria-current')
    expect(todo).toHaveTextContent('· not yet')
    expect(nav.querySelector('[data-step-dot="1"]')).toHaveClass('border-dashed')
    // Between steps, the edge's condition is written out; the bottom shows which step the task has reached.
    expect(within(nav).getByText('↓ Pass')).toBeInTheDocument()
    expect(within(nav).getByText('at step 1')).toBeInTheDocument()
    expect(within(nav).getByText('at step 1').previousElementSibling).toHaveTextContent('1 / 2')
  })

  it('a rework edge is written under the step it starts from: “<label> → 01 · used 1/3”, counting only completed visits that took this edge', () => {
    const detail = detailWithRework()
    expect(edgeUses(detail, 'again')).toBe(1)
    expect(workflowSteps(detail)[1].jumps).toMatchObject([{ to: 0, back: true, used: 1 }])
    ruler(detail)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    expect(within(nav).getByText('Rework → 01 · used 1/3').closest('p')).toHaveClass('text-pink-text')
  })

  it('the selected step lists its visits below it and clicking one jumps to it; the one being viewed is marked “viewing”', async () => {
    const onRecord = vi.fn(), onSelect = vi.fn()
    ruler(makeTaskDetail('project'), { onRecord, onSelect })
    const visits = screen.getByRole('list', { name: 'Visits and retries' })
    const items = within(visits).getAllByRole('button')
    expect(items.map((item) => item.textContent)).toEqual(['Visit 1 · Completed', 'Visit 2 · Blocked · viewing'])
    expect(items[1]).toHaveAttribute('aria-current', 'true')
    await userEvent.click(items[0])
    expect(onRecord).toHaveBeenCalledWith('check', oldNodeId)
    await userEvent.click(screen.getByRole('button', { name: /Submit report/ }))
    expect(onSelect).toHaveBeenCalledWith('finish')
  })

  it('once the task is completed there is no current step, and the bottom says “all steps done”', () => {
    const detail = makeTaskDetail('project')
    detail.task = { ...detail.task, status: 'completed', currentStepId: null, currentNodeRunId: null }
    expect(workflowPosition(detail)).toEqual({ at: 2, total: 2, done: true })
    ruler(detail, { selected: null })
    expect(screen.getByText('all steps done')).toBeInTheDocument()
    expect(screen.queryByRole('button', { current: 'step' })).not.toBeInTheDocument()
  })
})

describe('small step tags on narrow screens', () => {
  it('a row of small step tags: the current step yellow, done steps navy with a check mark, steps not reached dashed; the step being viewed is pressed (aria-pressed), and a click selects it', async () => {
    const onSelect = vi.fn()
    const detail = detailWithRework()
    render(<WorkflowChips detail={detail} selected="check" onSelect={onSelect} />)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    const [check, finish] = within(nav).getAllByRole('button')
    expect(check).toHaveAttribute('aria-current', 'step')
    expect(check).toHaveAttribute('aria-pressed', 'true')
    expect(check).toHaveClass('bg-riso-yellow')
    expect(finish).toHaveClass('border-ink', 'bg-paper-raised')
    await userEvent.click(finish)
    expect(onSelect).toHaveBeenCalledWith('finish')
  })
})
