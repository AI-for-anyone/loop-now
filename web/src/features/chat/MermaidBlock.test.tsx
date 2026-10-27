import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { Markdown } from './Markdown'
import { renderMermaid } from './mermaid-renderer'
vi.mock('./mermaid-renderer', () => ({ renderMermaid: vi.fn() }))
const draw = vi.mocked(renderMermaid)
const source = 'flowchart LR\n A-->B\n'
const fence = (code = source) => '```mermaid\n' + code + '```'
const svg = '<svg role="img" aria-label="测试图表"><text>成功</text></svg>'
beforeEach(() => { vi.useFakeTimers(); draw.mockReset() })
afterEach(() => vi.useRealTimers())
const settle = () => act(async () => { await vi.advanceTimersByTimeAsync(310) })
it('成功绘图，复制保持原始源码；多图独立', async () => {
  draw.mockResolvedValue(svg)
  render(<Markdown text={fence() + '\n\n' + fence('sequenceDiagram\n A->>B: Hi\n')} />)
  await settle()
  expect(screen.getAllByRole('img', { name: '测试图表' })).toHaveLength(2)
  expect(draw.mock.calls.map(call => call[0])).toEqual([source, 'sequenceDiagram\n A->>B: Hi\n'])
  vi.useRealTimers()
  const user = userEvent.setup()
  await user.click(screen.getAllByRole('button', { name: '复制代码' })[0])
  await expect(navigator.clipboard.readText()).resolves.toBe(source)
})
it('错误保留可复制的源码和消息其余内容', async () => {
  draw.mockRejectedValue(new Error('parse error'))
  render(<Markdown text={'前文\n\n' + fence() + '\n\n后文'} />)
  await settle()
  expect(screen.getByText('图表未能绘制，已保留源码')).toBeVisible()
  expect(screen.getByText('前文')).toBeVisible(); expect(screen.getByText('后文')).toBeVisible()
  vi.useRealTimers()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: '复制代码' }))
  await expect(navigator.clipboard.readText()).resolves.toBe(source)
})
it('流式未闭合只显示源码，闭合后绘图，其他字节不反复触发', async () => {
  draw.mockResolvedValue(svg)
  const { rerender } = render(<Markdown cursor text={'```mermaid\nflowchart LR\n A-->'} />)
  await settle(); expect(draw).not.toHaveBeenCalled()
  expect(screen.getByText('图表尚在生成，等待代码块完成')).toBeVisible()
  rerender(<Markdown cursor text={fence()} />)
  await settle(); expect(draw).toHaveBeenCalledTimes(1); expect(screen.getByRole('img')).toBeVisible()
  rerender(<Markdown cursor text={fence() + '\n\n正文继续输出'} />)
  await settle(); expect(draw).toHaveBeenCalledTimes(1)
})
it('合并快速变更，迟到请求不能覆盖新源，失败不能残留旧图', async () => {
  let resolveOld!: (svg: string) => void
  draw.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
  const { rerender, container } = render(<Markdown text={fence()} />)
  await settle()
  rerender(<Markdown text={fence('flowchart LR\n B-->C\n')} />)
  rerender(<Markdown text={fence('flowchart LR\n C-->D\n')} />)
  draw.mockRejectedValueOnce(new Error('invalid')); await settle()
  expect(draw).toHaveBeenCalledTimes(2)
  await act(async () => resolveOld(svg))
  expect(container.querySelector('svg')).toBeNull()
  expect(screen.getByText('图表未能绘制，已保留源码')).toBeVisible()
})
it('源改变立即隐藏旧图，卸载后结果不写入DOM', async () => {
  draw.mockResolvedValueOnce(svg)
  const { rerender, container, unmount } = render(<Markdown text={fence()} />)
  await settle(); expect(container.querySelector('svg')).not.toBeNull()
  let resolveLater!: (svg: string) => void
  draw.mockImplementationOnce(() => new Promise(resolve => { resolveLater = resolve }))
  rerender(<Markdown text={fence('flowchart LR\n C-->D\n')} />)
  expect(container.querySelector('svg')).toBeNull(); await settle(); unmount()
  await act(async () => resolveLater(svg)); expect(container).toBeEmptyDOMElement()
})
