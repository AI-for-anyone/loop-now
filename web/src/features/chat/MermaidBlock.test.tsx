import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { setSkin, setThemeMode } from '@/theme/store'
import { Markdown } from './Markdown'
import { renderMermaid } from './mermaid-renderer'
vi.mock('./mermaid-renderer', () => ({ renderMermaid: vi.fn() }))
const draw = vi.mocked(renderMermaid)
const source = 'flowchart LR\n A-->B\n'
const fence = (code = source) => '```mermaid\n' + code + '```'
const svg = '<svg role="img" aria-label="Test diagram"><text>Done</text></svg>'
beforeEach(() => { vi.useFakeTimers(); draw.mockReset() })
afterEach(() => vi.useRealTimers())
const settle = () => act(async () => { await vi.advanceTimersByTimeAsync(310) })
it('draws on success, copy keeps the original source; several diagrams are independent', async () => {
  draw.mockResolvedValue(svg)
  render(<Markdown text={fence() + '\n\n' + fence('sequenceDiagram\n A->>B: Hi\n')} />)
  await settle()
  expect(screen.getAllByRole('img', { name: 'Test diagram' })).toHaveLength(2)
  expect(draw.mock.calls.map(call => call[0])).toEqual([source, 'sequenceDiagram\n A->>B: Hi\n'])
  vi.useRealTimers()
  const user = userEvent.setup()
  await user.click(screen.getAllByRole('button', { name: 'Copy code' })[0])
  await expect(navigator.clipboard.readText()).resolves.toBe(source)
})
it('on error keeps the copyable source and the rest of the message', async () => {
  draw.mockRejectedValue(new Error('parse error'))
  render(<Markdown text={'Text before\n\n' + fence() + '\n\nText after'} />)
  await settle()
  expect(screen.getByText('Couldn\'t draw the diagram; showing the source')).toBeVisible()
  expect(screen.getByText('Text before')).toBeVisible(); expect(screen.getByText('Text after')).toBeVisible()
  vi.useRealTimers()
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Copy code' }))
  await expect(navigator.clipboard.readText()).resolves.toBe(source)
})
it('while streaming an unclosed block shows only the source, draws once closed, other bytes do not redraw', async () => {
  draw.mockResolvedValue(svg)
  const { rerender } = render(<Markdown cursor text={'```mermaid\nflowchart LR\n A-->'} />)
  await settle(); expect(draw).not.toHaveBeenCalled()
  expect(screen.getByText('Diagram still streaming; waiting for the code block to finish')).toBeVisible()
  rerender(<Markdown cursor text={fence()} />)
  await settle(); expect(draw).toHaveBeenCalledTimes(1); expect(screen.getByRole('img')).toBeVisible()
  rerender(<Markdown cursor text={fence() + '\n\nThe body keeps streaming'} />)
  await settle(); expect(draw).toHaveBeenCalledTimes(1)
})
it('merges quick changes, a late request cannot overwrite the newer source, a failure leaves no old diagram', async () => {
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
  expect(screen.getByText('Couldn\'t draw the diagram; showing the source')).toBeVisible()
})
it('a changed source hides the old diagram at once, results after unmount do not reach the DOM', async () => {
  draw.mockResolvedValueOnce(svg)
  const { rerender, container, unmount } = render(<Markdown text={fence()} />)
  await settle(); expect(container.querySelector('svg')).not.toBeNull()
  let resolveLater!: (svg: string) => void
  draw.mockImplementationOnce(() => new Promise(resolve => { resolveLater = resolve }))
  rerender(<Markdown text={fence('flowchart LR\n C-->D\n')} />)
  expect(container.querySelector('svg')).toBeNull(); await settle(); unmount()
  await act(async () => resolveLater(svg)); expect(container).toBeEmptyDOMElement()
})
it('re-renders live when theme mode or skin switches', async () => {
  draw.mockResolvedValue(svg)
  render(<Markdown text={fence()} />)
  await settle()
  expect(draw).toHaveBeenLastCalledWith(source, { skin: 'mono', resolvedTheme: 'light' })

  act(() => setThemeMode('dark'))
  await settle()
  expect(draw).toHaveBeenLastCalledWith(source, { skin: 'mono', resolvedTheme: 'dark' })

  act(() => setSkin('plate'))
  await settle()
  expect(draw).toHaveBeenLastCalledWith(source, { skin: 'plate', resolvedTheme: 'light' })
})
