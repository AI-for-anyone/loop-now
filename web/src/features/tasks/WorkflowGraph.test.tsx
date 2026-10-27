import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { makeTaskDetail } from '@/test/tasks'
import { WorkflowGraph } from './WorkflowGraph'

afterEach(() => vi.restoreAllMocks())
function dimensions() {
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(640)
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(400)
}
describe('横向流程视角', () => {
  it('普通初入按当前节点以scale1居中，历史入口按所选节点居中', () => {
    dimensions(); const detail = makeTaskDetail('project')
    const first = render(<WorkflowGraph detail={detail} selected={null} onSelect={() => {}} />)
    expect(first.container.querySelector('[data-task-camera]')).toHaveStyle({ transform: 'translate(168px, 56px) scale(1)' })
    first.unmount()
    const history = render(<WorkflowGraph detail={detail} selected="finish" onSelect={() => {}} />)
    expect(history.container.querySelector('[data-task-camera]')).toHaveStyle({ transform: 'translate(-152px, 56px) scale(1)' })
    expect(screen.getByRole('button', { name: /提交报告/ })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: /质量检查/ })).toHaveTextContent('当前节点')
  })
  it('方向键平移后SSE投影/resize不重置；定位当前是显式复位，缩放控件不缩放', async () => {
    dimensions(); const detail = makeTaskDetail('project'), select = vi.fn()
    const rendered = render(<WorkflowGraph detail={detail} selected="finish" onSelect={select} />)
    const camera = rendered.container.querySelector('[data-task-camera]')!
    const canvas = screen.getByLabelText('流程画布，可拖动或使用方向键平移')
    canvas.focus(); await userEvent.keyboard('{ArrowRight}')
    expect(camera).toHaveStyle({ transform: 'translate(-192px, 56px) scale(1)' })
    rendered.rerender(<WorkflowGraph detail={{ ...detail, task: { ...detail.task, currentStepId: 'finish', version: 7 } }} selected="finish" onSelect={select} />)
    fireEvent(window, new Event('resize'))
    expect(camera).toHaveStyle({ transform: 'translate(-192px, 56px) scale(1)' })
    await userEvent.click(screen.getByRole('button', { name: '定位当前' }))
    expect(camera).toHaveStyle({ transform: 'translate(-152px, 56px) scale(1)' })
    const button = screen.getByRole('button', { name: '放大画布' })
    await userEvent.click(button)
    expect(camera.getAttribute('style')).toContain('scale(1.2)')
    expect(button.closest('[data-task-camera]')).toBeNull()
    const node = screen.getByRole('button', { name: /质量检查/ }); node.focus(); await userEvent.keyboard('{Enter}')
    expect(select).toHaveBeenCalledWith('check')
  })
})


it('现有合法bounded自环使用本卡两个独立端点，不退化为重叠竖线', () => {
  dimensions(); const detail = makeTaskDetail('project')
  detail.workflow.edges.push({ id: 'self', from: 'check', to: 'check', label: '再次检查', maxTraversals: 2 })
  const { container } = render(<WorkflowGraph detail={detail} selected="check" onSelect={() => {}} />)
  expect(container.querySelector('[data-edge-id="self"] path')).toHaveAttribute('d', 'M 204 212 V 310 H 100 V 216')
  expect(screen.getByText('再次检查 · 回路上限 2')).toBeInTheDocument()
})
