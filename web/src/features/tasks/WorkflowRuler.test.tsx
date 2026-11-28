import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { forkServerNodeId, forkWebNodeId, makeForkTaskDetail, makeTaskDetail, nodeId, oldNodeId } from '@/test/tasks'
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
    expect(current).toHaveAttribute('title', 'check')
    expect(current).toHaveAttribute('aria-current', 'step')
    expect(current).toHaveAttribute('aria-pressed', 'true')
    expect(current).toHaveTextContent('01Quality checktask-checker· Blocked×2')
    // The status and the visit count stay together as one non-wrapping unit.
    expect(current.querySelector('[data-step-state]')).toHaveTextContent('· Blocked×2')
    expect(current.querySelector('[data-step-state]')).toHaveClass('whitespace-nowrap')
    expect(nav.querySelector('[data-step-dot="0"]')).toHaveClass('size-4', 'border-red-text', 'bg-tint-red')
    const todo = within(nav).getByRole('button', { name: /Submit report/ })
    expect(todo).toHaveAttribute('title', 'finish')
    expect(todo).not.toHaveAttribute('aria-current')
    expect(todo).toHaveTextContent('· not yet')
    expect(nav.querySelector('[data-step-dot="1"]')).toHaveClass('size-4', 'border-hairline-strong')
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
    detail.task = { ...detail.task, status: 'completed', currentStepId: null, currentStepTitle: null, currentNodeRunId: null }
    expect(workflowPosition(detail)).toEqual({ at: 2, total: 2, done: true })
    ruler(detail, { selected: null })
    expect(screen.getByText('2 / 2')).toHaveAttribute('title', 'all steps done')
    expect(screen.getByText('all steps done')).toBeInTheDocument()
    expect(screen.queryByRole('button', { current: 'step' })).not.toBeInTheDocument()
  })

  it('marks only steps that opted into starting with the regular conversation', () => {
    const detail = makeTaskDetail('project')
    detail.workflow.steps[0].withConversation = true
    ruler(detail)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    const check = within(nav).getByRole('button', { name: /Quality check/ })
    const finish = within(nav).getByRole('button', { name: /Submit report/ })
    expect(within(check).getByText('With conversation')).toBeVisible()
    expect(within(finish).queryByText('With conversation')).not.toBeInTheDocument()
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
    expect(check).toHaveAttribute('title', 'check')
    expect(finish).toHaveAttribute('title', 'finish')
    expect(check).toHaveAttribute('aria-current', 'step')
    expect(check).toHaveAttribute('aria-pressed', 'true')
    expect(check).toHaveClass('bg-tint-yellow')
    expect(finish).toHaveClass('border-hairline-strong', 'bg-surface-elevated')
    const visits = screen.getByRole('list', { name: 'Visits and retries' })
    const visitButtons = within(visits).getAllByRole('button')
    expect(visitButtons).toHaveLength(2)
    expect(visitButtons[1]).toHaveAttribute('aria-current', 'true')
    await userEvent.click(visitButtons[0])
    expect(onRecord).toHaveBeenCalledWith('check', oldNodeId)
    await userEvent.click(finish)
    expect(onSelect).toHaveBeenCalledWith('finish')
  })

  it('marks steps with conversation on narrow step chips too', () => {
    const detail = makeTaskDetail('project')
    detail.workflow.steps[1].withConversation = true
    render(<WorkflowChips detail={detail} selected="check" record={nodeId} onSelect={() => {}} onRecord={() => {}} />)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    const [check, finish] = within(nav).getAllByRole('button')
    expect(check.querySelector('[data-step-with-conversation]')).toBeNull()
    const mark = finish.querySelector('[data-step-with-conversation]')!
    expect(mark).not.toBeNull()
    expect(mark).toHaveTextContent('With conversation')
  })

  it('groups parallel branch chips and marks every running branch as current on narrow screens', () => {
    const detail = makeForkTaskDetail('project')
    render(<WorkflowChips detail={detail} selected="server" record={forkServerNodeId} onSelect={() => {}} onRecord={() => {}} />)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    const forkGroup = nav.querySelector<HTMLElement>('[data-chips-fork="build"]')!
    expect(forkGroup).not.toBeNull()
    const currentSteps = within(nav).getAllByRole('button', { current: 'step' })
    expect(currentSteps.map((b) => b.getAttribute('title'))).toEqual(['server', 'web'])
    expect(within(forkGroup).getByRole('button', { name: /Server/ })).toHaveClass('bg-tint-yellow')
    expect(within(forkGroup).getByRole('button', { name: /Web/ })).toHaveClass('bg-tint-yellow')
  })
})

describe('parallel branches in the workflow ruler', () => {
  it('draws parallel branches side by side in a 2-column grid, marks every running branch as current, and shows the active count and join badge', async () => {
    const detail = makeForkTaskDetail('project')
    const onSelect = vi.fn()
    render(<WorkflowRuler detail={detail} selected="server" record={forkServerNodeId} onSelect={onSelect} onRecord={() => {}} agentName={(id) => id} />)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    expect(nav).toHaveAttribute('data-has-forks', 'true')
    expect(nav).toHaveClass('w-[344px]')

    // Header shows how many steps are running in parallel.
    expect(within(nav).getByText('2 active')).toHaveAttribute('title', 'running in parallel: Server, Web')

    // Fork block renders both branches side by side in a 2-column grid.
    const forkBlock = nav.querySelector<HTMLElement>('[data-fork-block="build"]')!
    expect(forkBlock).not.toBeNull()
    const lanesGrid = forkBlock.querySelector<HTMLElement>('[data-fork-lanes]')!
    expect(lanesGrid).toHaveClass('grid', 'grid-cols-2')
    const lanes = [...forkBlock.querySelectorAll<HTMLElement>('[data-branch-lane]')]
    expect(lanes.map((el) => [el.dataset.branchLane, el.dataset.branchState])).toEqual([
      ['server', 'active'],
      ['web', 'active'],
    ])
    expect(within(lanes[0]).getByText('Backend')).toBeVisible()
    expect(within(lanes[1]).getByText('Frontend')).toBeVisible()

    // Both running branch steps carry aria-current="step".
    const currentButtons = within(nav).getAllByRole('button', { current: 'step' })
    expect(currentButtons.map((b) => b.getAttribute('title'))).toEqual(['server', 'web'])

    // Join step carries a join badge showing arrived count while the fork is active.
    const joinStep = nav.querySelector<HTMLElement>('[data-join-step="verify"]')!
    expect(within(joinStep).getByText('0/2 arrived')).toBeVisible()

    // Clicking the other parallel branch selects it.
    await userEvent.click(within(lanes[1]).getByRole('button', { name: /Web/ }))
    expect(onSelect).toHaveBeenCalledWith('web')
  })

  it('shows blocked state on a blocked branch, arrived state on the finished branch, merge conflict tags, and maxParallelBranches cap', () => {
    const detail = makeForkTaskDetail('project')
    detail.workflow.maxParallelBranches = 2
    detail.nodes[1] = { ...detail.nodes[1], status: 'completed', edgeId: 'server_done', endedAt: '2026-10-14T08:04:00Z' }
    detail.nodes[2] = { ...detail.nodes[2], status: 'blocked', reason: 'Build failed' }
    detail.task.status = 'blocked'
    detail.task.currentNodes = [detail.task.currentNodes[1]]
    detail.task.currentNodeRunId = forkWebNodeId
    detail.task.currentStepId = 'web'
    detail.task.currentStepTitle = 'Web'
    detail.task.currentAgentId = detail.nodes[2].agentId
    detail.workspace!.branches[0].merge = 'merged'
    detail.workspace!.branches[1].merge = 'conflict'

    render(<WorkflowRuler detail={detail} selected="web" record={forkWebNodeId} onSelect={() => {}} onRecord={() => {}} agentName={(id) => id} />)
    const nav = screen.getByRole('navigation', { name: 'Workflow' })
    const forkBlock = nav.querySelector<HTMLElement>('[data-fork-block="build"]')!
    expect(within(forkBlock).getByText('max 2 at once')).toBeVisible()
    expect(within(forkBlock).getAllByText('1/2 arrived').length).toBeGreaterThanOrEqual(1)

    const serverLane = forkBlock.querySelector<HTMLElement>('[data-branch-lane="server"]')!
    const webLane = forkBlock.querySelector<HTMLElement>('[data-branch-lane="web"]')!
    expect(serverLane).toHaveAttribute('data-branch-state', 'arrived')
    expect(webLane).toHaveAttribute('data-branch-state', 'blocked')
    expect(within(serverLane).getByText('merged')).toBeVisible()
    expect(within(webLane).getByText('merge conflict')).toBeVisible()
  })
})
