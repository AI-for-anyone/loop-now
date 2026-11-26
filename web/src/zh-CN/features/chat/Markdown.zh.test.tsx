import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { Markdown } from '@/features/chat/Markdown'

// Footnotes use the UI copy instead of remark's built-in English labels. In English the copy reads the same as the built-in labels,
// so this is checked in Chinese. Moved from src/features/chat/Markdown.test.tsx, which keeps the same scenario in English.

describe('Markdown rendering and safety', () => {
  it('footnotes use the Chinese copy, jump to the note of this reply on the current page, and ids do not repeat across replies', () => {
    const text = '文字[^1]\n\n[^1]: 注释'
    const { container } = render(
      <>
        <div data-testid="first"><Markdown text={text} /></div>
        <div data-testid="second"><Markdown text={text} /></div>
      </>,
    )
    const ids = [...container.querySelectorAll('[id]')].map((el) => el.id)
    expect(ids.length).toBeGreaterThan(0)
    expect(new Set(ids).size).toBe(ids.length)
    expect(container).not.toHaveTextContent('Footnotes')
    expect(screen.getAllByText('脚注')).toHaveLength(2)

    for (const testId of ['first', 'second']) {
      const reply = screen.getByTestId(testId)
      const ref = within(reply).getByRole('link', { name: '1' })
      expect(ref).not.toHaveAttribute('target')
      const note = container.querySelector(`[id="${ref.getAttribute('href')!.slice(1)}"]`)!
      expect(reply).toContainElement(note as HTMLElement)
      expect(note).toHaveTextContent('注释')
      const label = container.querySelector(`[id="${ref.getAttribute('aria-describedby')}"]`)
      expect(reply).toContainElement(label as HTMLElement)
      const back = within(reply).getByRole('link', { name: '返回引用 1' })
      expect(back).not.toHaveAttribute('target')
      expect(container.querySelector(`[id="${back.getAttribute('href')!.slice(1)}"]`)).toBe(ref)
    }
  })

  it('reply image preview and fallback copy use Chinese UI text', async () => {
    const user = userEvent.setup()
    const { rerender } = render(<Markdown text="![候选图](https://example.com/a.png)" />)
    await user.click(screen.getByRole('button', { name: '放大图片：候选图' }))
    const dialog = await screen.findByRole('dialog', { name: '候选图' })
    expect(within(dialog).getByRole('link', { name: '在新标签页打开原图' })).toHaveAttribute('href', 'https://example.com/a.png')
    await user.keyboard('{Escape}')

    rerender(<Markdown text="![](https://example.com/broken.png)" />)
    fireEvent.error(document.querySelector('img')!)
    expect(screen.getByRole('img', { name: '图片加载失败' })).toHaveTextContent('图片加载失败')
  })
})
