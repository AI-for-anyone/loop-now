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

describe('流程尺', () => {
  it('每一步是尺子上的一个点：当前步是写着步号的黄色大圆点（aria-current=step），走过的写最近一次的状态，没到的写“还没到”', () => {
    const detail = makeTaskDetail('project')
    ruler(detail)
    const nav = screen.getByRole('navigation', { name: '固定流程' })
    const current = within(nav).getByRole('button', { name: /质量检查/ })
    expect(current).toHaveAttribute('aria-current', 'step')
    expect(current).toHaveAttribute('aria-pressed', 'true')
    expect(current).toHaveTextContent('01质量检查任务检查员· 阻塞×2')
    // The status and the visit count are wrapped together, so they move together when the line wraps.
    expect(current.querySelector('[data-step-state]')).toHaveTextContent('· 阻塞×2')
    expect(current.querySelector('[data-step-state]')).toHaveClass('whitespace-nowrap')
    expect(nav.querySelector('[data-step-dot="0"]')).toHaveTextContent('01')
    expect(nav.querySelector('[data-step-dot="0"]')).toHaveClass('bg-riso-yellow')
    const todo = within(nav).getByRole('button', { name: /提交报告/ })
    expect(todo).not.toHaveAttribute('aria-current')
    expect(todo).toHaveTextContent('· 还没到')
    expect(nav.querySelector('[data-step-dot="1"]')).toHaveClass('border-dashed')
    // Between steps, the edge's condition is written out; the bottom shows which step the task has reached.
    expect(within(nav).getByText('↓ 通过')).toBeInTheDocument()
    expect(within(nav).getByText('走到第 1 步')).toBeInTheDocument()
    expect(within(nav).getByText('走到第 1 步').previousElementSibling).toHaveTextContent('1 / 2')
  })

  it('返工线写在出发的那一步下面：“返工到 01 · 已用 1/3 次”，次数只算选了这条线并且已完成的访问', () => {
    const detail = detailWithRework()
    expect(edgeUses(detail, 'again')).toBe(1)
    expect(workflowSteps(detail)[1].jumps).toMatchObject([{ to: 0, back: true, used: 1 }])
    ruler(detail)
    const nav = screen.getByRole('navigation', { name: '固定流程' })
    expect(within(nav).getByText('返工到 01 · 已用 1/3 次').closest('p')).toHaveClass('text-pink-text')
  })

  it('选中的一步下面列出各次访问，点哪次跳到哪次；正在看的那次标着“在看”', async () => {
    const onRecord = vi.fn(), onSelect = vi.fn()
    ruler(makeTaskDetail('project'), { onRecord, onSelect })
    const visits = screen.getByRole('list', { name: '访问与重试记录' })
    const items = within(visits).getAllByRole('button')
    expect(items.map((item) => item.textContent)).toEqual(['第 1 次 · 已完成', '第 2 次 · 阻塞 · 在看'])
    expect(items[1]).toHaveAttribute('aria-current', 'true')
    await userEvent.click(items[0])
    expect(onRecord).toHaveBeenCalledWith('check', oldNodeId)
    await userEvent.click(screen.getByRole('button', { name: /提交报告/ }))
    expect(onSelect).toHaveBeenCalledWith('finish')
  })

  it('任务完成后没有当前步，底部写“已全部走完”', () => {
    const detail = makeTaskDetail('project')
    detail.task = { ...detail.task, status: 'completed', currentStepId: null, currentNodeRunId: null }
    expect(workflowPosition(detail)).toEqual({ at: 2, total: 2, done: true })
    ruler(detail, { selected: null })
    expect(screen.getByText('已全部走完')).toBeInTheDocument()
    expect(screen.queryByRole('button', { current: 'step' })).not.toBeInTheDocument()
  })
})

describe('窄屏的步骤小标签', () => {
  it('一排步骤小标签：当前步黄色、走完的藏青带对勾、没到的虚线；正在看的一步按下（aria-pressed），点一下选中', async () => {
    const onSelect = vi.fn()
    const detail = detailWithRework()
    render(<WorkflowChips detail={detail} selected="check" onSelect={onSelect} />)
    const nav = screen.getByRole('navigation', { name: '固定流程' })
    const [check, finish] = within(nav).getAllByRole('button')
    expect(check).toHaveAttribute('aria-current', 'step')
    expect(check).toHaveAttribute('aria-pressed', 'true')
    expect(check).toHaveClass('bg-riso-yellow')
    expect(finish).toHaveClass('border-ink', 'bg-paper-raised')
    await userEvent.click(finish)
    expect(onSelect).toHaveBeenCalledWith('finish')
  })
})
