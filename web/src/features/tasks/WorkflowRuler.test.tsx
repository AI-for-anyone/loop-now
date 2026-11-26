import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { makeTaskDetail, nodeId, oldNodeId } from '@/test/tasks'
import { WorkflowChips, WorkflowRuler } from './WorkflowRuler'
import { edgeUses, workflowPosition, workflowSteps } from './workflow-steps'

// A two-step workflow (quality check -> submit report, where submit report can send rework back to quality check),
// currently stopped at the 2nd visit of quality check. Add one completed submit-report visit that took the rework edge,
// so the rework edge has been used once.
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
  it('renders a compact 216px left step rail with 16px status indicators, header step counter, agent/status subline, and edge labels', () => {
    const detail = makeTaskDetail('project')
    ruler(detail)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    expect(nav).toHaveClass('w-[216px]', 'border-r', 'max-lg:hidden')
    const current = within(nav).getByRole('button', { name: /Quality check/ })
    expect(current).toHaveAttribute('aria-current', 'step')
    expect(current).toHaveAttribute('aria-pressed', 'true')
    expect(current).toHaveTextContent('01Quality checktask-checker· Blocked×2')
    // The status and the visit count stay together as one non-wrapping unit.
    expect(current.querySelector('[data-step-state]')).toHaveTextContent('· Blocked×2')
    expect(current.querySelector('[data-step-state]')).toHaveClass('whitespace-nowrap')
    expect(nav.querySelector('[data-step-dot="0"]')).toHaveClass('size-4', 'border-red-text', 'bg-tint-red', '[[data-skin=plate]_&]:bg-riso-yellow')
    const todo = within(nav).getByRole('button', { name: /Submit report/ })
    expect(todo).not.toHaveAttribute('aria-current')
    expect(todo).toHaveTextContent('· not yet')
    expect(nav.querySelector('[data-step-dot="1"]')).toHaveClass('size-4', 'border-hairline-strong', '[[data-skin=plate]_&]:border-dashed')
    // Between steps, the edge condition is written out; the rail header shows the step counter and screen-reader position.
    expect(within(nav).getByText('↓ Pass')).toBeInTheDocument()
    expect(within(nav).getByText('1 / 2')).toHaveAttribute('title', 'at step 1')
    expect(within(nav).getByText('at step 1')).toHaveClass('sr-only')
  })

  it('a rework edge is written as an inline badge under the step it starts from, counting only completed visits that took this edge', () => {
    const detail = detailWithRework()
    expect(edgeUses(detail, 'again')).toBe(1)
    expect(workflowSteps(detail)[1].jumps).toMatchObject([{ to: 0, back: true, used: 1 }])
    ruler(detail)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    expect(within(nav).getByText('Rework → 01 · used 1/3').closest('p')).toHaveClass('text-pink-text')
  })

  it('the selected step lists its visits below it and clicking one jumps to it; the one being viewed is marked viewing', async () => {
    const onRecord = vi.fn(),
      onSelect = vi.fn()
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

  it('once the task is completed there is no current step, and the rail header reports all steps done', () => {
    const detail = makeTaskDetail('project')
    detail.task = { ...detail.task, status: 'completed', currentStepId: null, currentNodeRunId: null }
    expect(workflowPosition(detail)).toEqual({ at: 2, total: 2, done: true })
    ruler(detail, { selected: null })
    expect(screen.getByText('2 / 2')).toHaveAttribute('title', 'all steps done')
    expect(screen.getByText('all steps done')).toBeInTheDocument()
    expect(screen.queryByRole('button', { current: 'step' })).not.toBeInTheDocument()
  })
})

describe('small step tags on narrow screens', () => {
  it('renders step chips plus visit/attempt selector pills so narrow screens support switching visits', async () => {
    const onSelect = vi.fn(),
      onRecord = vi.fn()
    const detail = detailWithRework()
    render(<WorkflowChips detail={detail} selected="check" record={nodeId} onSelect={onSelect} onRecord={onRecord} />)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    const [check, finish] = within(nav).getAllByRole('button')
    expect(check).toHaveAttribute('aria-current', 'step')
    expect(check).toHaveAttribute('aria-pressed', 'true')
    expect(check).toHaveClass('bg-tint-yellow', '[[data-skin=plate]_&]:bg-riso-yellow')
    expect(finish).toHaveClass('border-hairline-strong', 'bg-surface-elevated', '[[data-skin=plate]_&]:border-ink', '[[data-skin=plate]_&]:bg-paper-raised')
    const visits = screen.getByRole('list', { name: 'Visits and retries' })
    const visitButtons = within(visits).getAllByRole('button')
    expect(visitButtons).toHaveLength(2)
    expect(visitButtons[1]).toHaveAttribute('aria-current', 'true')
    await userEvent.click(visitButtons[0])
    expect(onRecord).toHaveBeenCalledWith('check', oldNodeId)
    await userEvent.click(finish)
    expect(onSelect).toHaveBeenCalledWith('finish')
  })
})
