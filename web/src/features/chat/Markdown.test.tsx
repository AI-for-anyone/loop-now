import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Markdown } from './Markdown'

describe('Markdown rendering and safety', () => {
  it('the closing marker of a bold bare URL and the Chinese explanation after it do not go into the link', () => {
    const { container } = render(<Markdown text="已改为 **http://127.0.0.1:3111/app**，确认可访问。 3000 端口已停止监听。" />) // check-english: zh (where a bare URL ends before Chinese text and full-width punctuation)
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', 'http://127.0.0.1:3111/app')
    expect(link.textContent).toBe('http://127.0.0.1:3111/app')
    expect(link.parentElement?.tagName).toBe('STRONG')
    expect(container).toHaveTextContent('，确认可访问。 3000 端口已停止监听。') // check-english: zh (where a bare URL ends before Chinese text and full-width punctuation)
    expect(container).not.toHaveTextContent('**')
  })

  it('a plain bare URL ends at Chinese punctuation and keeps its query, fragment and Unicode path', () => {
    render(<Markdown text="地址：https://example.com/目录/文件?q=中文&next=%2Fapp#段落，确认可访问。" />) // check-english: zh (where a bare URL ends before Chinese text and full-width punctuation)
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', new URL('https://example.com/目录/文件?q=中文&next=%2Fapp#段落').href) // check-english: zh (a Unicode path, query and fragment)
    expect(link.textContent).toBe('https://example.com/目录/文件?q=中文&next=%2Fapp#段落') // check-english: zh (a Unicode path, query and fragment)
    expect(link.nextSibling?.textContent).toBe('，确认可访问。') // check-english: zh (where a bare URL ends before Chinese text and full-width punctuation)
  })

  it('explicit and angle-bracket links keep the full address, including punctuation, query and fragment', () => {
    const url = 'https://example.com/目录，文件?q=a,b&next=%2Fapp#段落' // check-english: zh (full-width punctuation inside an explicit link address)
    render(<Markdown text={`[完整地址](${url})\n\n**<${url}>**，说明`} />) // check-english: zh (full-width punctuation right after a link)
    const links = screen.getAllByRole('link')
    expect(links).toHaveLength(2)
    for (const link of links) {
      expect(link).toHaveAttribute('href', new URL(url).href)
      expect(link).toHaveAttribute('target', '_blank')
      expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    }
    expect(links[1].parentElement?.tagName).toBe('STRONG')
  })

  it('URLs in inline and block code do not become links; ordinary parentheses and parameters are not cut off crudely', () => {
    const url = 'https://example.com/a_(b)?q=a,b&next=%2Fx#片段' // check-english: zh (a Unicode fragment)
    const { container } = render(<Markdown text={`\`${url}，说明\`\n\n\`\`\`text\n${url}，说明\n\`\`\`\n\n${url}。后文`} />) // check-english: zh (where a bare URL ends before Chinese text and full-width punctuation)
    expect(container.querySelectorAll('code')).toHaveLength(2)
    expect(screen.getAllByRole('link')).toHaveLength(1)
    expect(screen.getByRole('link')).toHaveAttribute('href', new URL(url).href)
    expect(screen.getByText('。后文')).toBeInTheDocument() // check-english: zh (where a bare URL ends before Chinese text and full-width punctuation)
  })

  it('a plain bare link keeps asterisks in a valid query and encoded punctuation', () => {
    const url = 'https://example.com/a%EF%BC%8Cb?q=*value*&x=1#part'
    render(<Markdown text={`${url}，说明`} />) // check-english: zh (where a bare URL ends before Chinese text and full-width punctuation)
    expect(screen.getByRole('link')).toHaveAttribute('href', url)
    expect(screen.getByRole('link').textContent).toBe(url)
  })
  it('HTML and scripts in a reply are shown as text and not run', () => {
    const { container } = render(<Markdown text={'<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>'} />)
    expect(container.querySelector('script')).toBeNull()
    expect(container.querySelector('[onerror]')).toBeNull()
    expect(container).toHaveTextContent('<script>alert(1)</script>')
    expect(container).toHaveTextContent('<img src=x onerror=alert(1)>')
  })

  it('links with dangerous protocols are shown as text that cannot be clicked', () => {
    const { container } = render(<Markdown text="[Click me](javascript:alert(1))" />)
    expect(screen.getByText('Click me').closest('a')).toBeNull()
    expect(container.querySelector('a')).toBeNull()
  })

  it('only absolute http(s) and mailto links open in a new tab', () => {
    render(<Markdown text={'[External](https://example.com) [Email](mailto:a@example.com) [Anchor](#part) [Relative](docs/a.md)'} />)
    for (const name of ['External', 'Email']) {
      const link = screen.getByRole('link', { name })
      expect(link).toHaveAttribute('target', '_blank')
      expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    }
    for (const name of ['Anchor', 'Relative']) {
      const link = screen.getByRole('link', { name })
      expect(link).not.toHaveAttribute('target')
      expect(link).not.toHaveAttribute('rel')
    }
  })

  it('links, images and code blocks carry no node attribute', () => {
    const { container } = render(<Markdown text={'[External](https://example.com)\n\n![Image](https://example.com/a.png)\n\n```\nx\n```'} />)
    expect(container.querySelectorAll('a, img, pre')).toHaveLength(3)
    expect(container.querySelector('[node]')).toBeNull()
  })

  it('footnotes use the UI copy, jump to the note of this reply on the current page, and ids do not repeat across replies', () => {
    const text = 'Text[^1]\n\n[^1]: Note'
    const { container } = render(
      <>
        <div data-testid="first"><Markdown text={text} /></div>
        <div data-testid="second"><Markdown text={text} /></div>
      </>,
    )
    const ids = [...container.querySelectorAll('[id]')].map((el) => el.id)
    expect(ids.length).toBeGreaterThan(0)
    expect(new Set(ids).size).toBe(ids.length)
    // The English copy reads the same as remark's built-in “Footnotes”; that the UI copy replaces the built-in label is checked in
    // src/zh-CN/features/chat/Markdown.zh.test.tsx.
    expect(screen.getAllByText('Footnotes')).toHaveLength(2)

    for (const testId of ['first', 'second']) {
      const reply = screen.getByTestId(testId)
      const ref = within(reply).getByRole('link', { name: '1' })
      expect(ref).not.toHaveAttribute('target')
      const note = container.querySelector(`[id="${ref.getAttribute('href')!.slice(1)}"]`)!
      expect(reply).toContainElement(note as HTMLElement)
      expect(note).toHaveTextContent('Note')
      const label = container.querySelector(`[id="${ref.getAttribute('aria-describedby')}"]`)
      expect(reply).toContainElement(label as HTMLElement)
      const back = within(reply).getByRole('link', { name: 'Back to reference 1' })
      expect(back).not.toHaveAttribute('target')
      expect(container.querySelector(`[id="${back.getAttribute('href')!.slice(1)}"]`)).toBe(ref)
    }
  })

  it('copying a code block gives the original code', async () => {
    const user = userEvent.setup()
    const { container } = render(<Markdown text={'```bash\necho "hello" && ls -la\n```'} />)
    expect(container.querySelector('code.hljs')).not.toBeNull()
    await user.click(screen.getByRole('button', { name: 'Copy code' }))
    await expect(navigator.clipboard.readText()).resolves.toBe('echo "hello" && ls -la\n')
  })

  it('handles clipboard failure cleanly without throwing or falsely showing Copied', async () => {
    const user = userEvent.setup()
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValueOnce(new Error('denied'))
    render(<Markdown text={'```ts\nconst x = 1\n```'} />)
    const button = screen.getByRole('button', { name: 'Copy code' })
    await user.click(button)
    expect(button).toHaveTextContent('Copy code')
  })

  it('while streaming, an unclosed code block is shown as a code block', () => {
    const { container, rerender } = render(<Markdown text={'Notes:\n\n```ts\nconst a = 1'} />)
    expect(container.querySelector('pre code')).toHaveTextContent('const a = 1')
    rerender(<Markdown text={'Notes:\n\n```ts\nconst a = 1\n```\n\nThe end'} />)
    expect(container.querySelector('pre code')).toHaveTextContent('const a = 1')
    expect(screen.getByText('The end')).toBeInTheDocument()
  })

  it('shows images in a reply directly', () => {
    render(<Markdown text="![Architecture diagram](https://example.com/a.png)" />)
    const img = screen.getByRole('img', { name: 'Architecture diagram' })
    expect(img).toHaveAttribute('src', 'https://example.com/a.png')
    expect(img).toHaveAttribute('referrerpolicy', 'no-referrer')
  })

  it('renders GFM tables and task lists', () => {
    const { container } = render(<Markdown text={'| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] Done'} />)
    expect(container.querySelector('table')).not.toBeNull()
    expect(container.querySelector('input[type="checkbox"]')).toBeChecked()
  })

  it('puts tables in a container that scrolls horizontally on its own', () => {
    const { container } = render(<Markdown text={'| a | b |\n|---|---|\n| 1 | 2 |'} />)
    const wrapper = container.querySelector('table')!.parentElement!
    expect(wrapper).toHaveAttribute('data-slot', 'table-container')
    expect(wrapper.className).toContain('overflow-x-auto')
  })

  it('shows the language name at the top of a code block, and none when no language is given', () => {
    const { container, rerender } = render(<Markdown text={'```bash\nls\n```'} />)
    expect(screen.getByText('bash')).toBeInTheDocument()
    rerender(<Markdown text={'```\nls\n```'} />)
    expect(screen.queryByText('bash')).toBeNull()
    expect(container.querySelector('pre code')).toHaveTextContent('ls')
  })

  it('while a reply is in progress, shows the output cursor at the end of the last text, and stops blinking with reduced motion', () => {
    const { container, rerender } = render(<Markdown text={'First paragraph\n\n- List item'} cursor />)
    const cursor = container.querySelector('.riso-cursor')!
    expect(cursor).toHaveAttribute('aria-hidden', 'true')
    expect(cursor.className).toContain('motion-reduce:animate-none')
    expect(cursor.parentElement).toHaveTextContent('List item')
    expect(cursor.parentElement!.tagName).toBe('LI')
    rerender(<Markdown text={'First paragraph\n\n- List item'} />)
    expect(container.querySelector('.riso-cursor')).toBeNull()
  })
})

// "Printed sheet" redesign: a bold word at the start of a list item becomes a small yellow label.
describe('A bold word at the start of a list item', () => {
  const items = (container: HTMLElement) => [...container.querySelectorAll('li')]

  it('in an unordered list a leading bold word is a label with the following text to its right, and a colon right after the label is no longer shown', () => {
    const { container } = render(<Markdown text={'归好了：\n\n- **导出** 大文件导出到一半会停。\n- **搜索**：中文分词不准，见 `search.go`。\n- **通知**: 同一件事提醒两次。'} />) // check-english: zh (a Chinese reply with a full-width colon after the label)
    const [first, second, third] = items(container)
    expect(container.querySelector('ul')).toHaveClass('md-lead-list')
    for (const item of [first, second, third]) {
      expect(item).toHaveClass('md-lead-item')
      expect(item.firstElementChild).toHaveClass('md-lead')
      expect(item.firstElementChild!.tagName).toBe('STRONG')
      expect(item.children).toHaveLength(2)
      expect(item.lastElementChild).toHaveClass('md-lead-text')
    }
    expect(first.firstElementChild).toHaveTextContent('导出') // check-english: zh (a Chinese reply with a full-width colon after the label)
    expect(first.lastElementChild!.textContent).toBe(' 大文件导出到一半会停。') // check-english: zh (a Chinese reply with a full-width colon after the label)
    expect(second.lastElementChild!.textContent).toBe('中文分词不准，见 search.go。') // check-english: zh (a Chinese reply with a full-width colon after the label)
    expect(second.lastElementChild!.querySelector('code')).toHaveTextContent('search.go')
    expect(third.lastElementChild!.textContent).toBe('同一件事提醒两次。') // check-english: zh (a Chinese reply with a full-width colon after the label)
  })

  it('a list is laid out side by side only when every item is “bold word + inline text”; if one is not, the whole list keeps its bullets and the bold words are still labels', () => {
    const all = render(<Markdown text={'- **Export** stops\n- **Search** is off'} />)
    expect(all.container.querySelector('ul')).toHaveClass('md-lead-list')
    all.unmount()
    const mixed = render(<Markdown text={'- **Export**: stops\n- A plain item\n- **Search**: is off'} />)
    expect(mixed.container.querySelector('ul')).not.toHaveClass('md-lead-list')
    expect(mixed.container.querySelector('.md-lead-item')).toBeNull()
    expect(mixed.container.querySelector('.md-lead-text')).toBeNull()
    expect(mixed.container.querySelectorAll('strong.md-lead')).toHaveLength(2)
    expect(items(mixed.container)[0].textContent).toBe('Exportstops')
    mixed.unmount()
    // One item has a sublist and the other doesn't: both must use the same layout, and neither is laid out horizontally.
    const nested = render(<Markdown text={'- **Export**: two problems\n  - large files stop\n- **Search**: is off'} />)
    expect(nested.container.querySelector('.md-lead-item')).toBeNull()
    expect(nested.container.querySelectorAll('strong.md-lead')).toHaveLength(2)
  })

  it('when a sublist in a plain list item has only label items, the sublist itself is laid out side by side (indentation unchanged)', () => {
    const { container } = render(<Markdown text={'- Problems this week\n  - **Export** stops\n  - **Search** is off'} />)
    const [outer, inner] = container.querySelectorAll('ul')
    expect(outer).not.toHaveClass('md-lead-list')
    expect(inner).toHaveClass('md-lead-list')
    expect(inner.querySelectorAll('li.md-lead-item')).toHaveLength(2)
  })

  it('removes only a colon used as a separator: a full-width colon, or an ASCII colon followed by whitespace or the line end; colons in ports, times, drive letters and :: are kept', () => {
    const rest = (text: string) => {
      const view = render(<Markdown text={text} />)
      const value = view.container.querySelector('li')!.textContent
      view.unmount()
      return value
    }
    expect(rest('- **地址**：见下')).toBe('地址见下') // check-english: zh (the full-width colon of Chinese text)
    expect(rest('- **Address**: see below')).toBe('Addresssee below')
    expect(rest('- **Address**:')).toBe('Address')
    expect(rest('- **localhost**:3000 is up')).toBe('localhost:3000 is up')
    expect(rest('- **12**:30 lunch')).toBe('12:30 lunch')
    expect(rest('- **C**:\\Users')).toBe('C:\\Users')
    expect(rest('- **std**::vector')).toBe('std::vector')
  })

  it('a bold word that is not at the start, or comes after something else (a task list checkbox), is not made a label', () => {
    const { container } = render(<Markdown text={'- First look at the **Export** problem\n- [ ] **Search** not done yet\n- A plain item'} />)
    expect(container.querySelector('.md-lead')).toBeNull()
    expect(container.querySelector('.md-lead-item')).toBeNull()
    expect(container.querySelector('strong')).toHaveTextContent('Export')
  })

  it('a whole bold sentence at the start stays bold and is not made a label', () => {
    const { container } = render(<Markdown text={'- **This whole sentence is bold, and it is not a word but a complete explanation** and more\n- **Short label** ok'} />)
    const [long, short] = items(container)
    expect(long.querySelector('.md-lead')).toBeNull()
    expect(long).not.toHaveClass('md-lead-item')
    expect(short.firstElementChild).toHaveClass('md-lead')
  })

  it('ordered lists keep their numbers: the bold word is a label, but the item is not laid out side by side', () => {
    const { container } = render(<Markdown text={'1. **Export** fix this first\n2. **Search** fix this next'} />)
    const [first] = items(container)
    expect(first.querySelector('strong')).toHaveClass('md-lead')
    expect(first).not.toHaveClass('md-lead-item')
    expect(first.querySelector('.md-lead-text')).toBeNull()
  })

  it('when a list item also has a sublist or paragraphs, the bold word is a label and the rest is laid out as is', () => {
    const { container } = render(<Markdown text={'- **Export** two problems\n  - large files stop\n  - it only works after a retry\n\nAnother list:\n\n- **Search** first paragraph\n\n  second paragraph\n- **Notify** also in paragraphs'} />)
    const [nested, , , loose] = items(container)
    expect(nested.firstElementChild).toHaveClass('md-lead')
    expect(nested).not.toHaveClass('md-lead-item')
    expect(nested.querySelectorAll('li')).toHaveLength(2)
    expect(loose.querySelector('p > strong')).toHaveClass('md-lead')
    expect(loose).not.toHaveClass('md-lead-item')
    expect(loose.querySelectorAll('p')).toHaveLength(2)
  })

  it('a lone bold word with no text after it shows only the label', () => {
    const { container } = render(<Markdown text={'- **Export**\n- **Search**'} />)
    const [first] = items(container)
    expect(first).toHaveClass('md-lead-item')
    expect(first.children).toHaveLength(1)
    expect(first.firstElementChild).toHaveClass('md-lead')
  })

  it('while a reply is in progress, the output cursor follows the end of the text after the label', () => {
    const { container } = render(<Markdown cursor text={'- **Export** large files\n- **Search** word spl'} />)
    const last = items(container)[1]
    const cursor = container.querySelector('.riso-cursor')!
    expect(last.lastElementChild).toHaveClass('md-lead-text')
    expect(last.lastElementChild).toContainElement(cursor as HTMLElement)
    expect(last.lastElementChild!.lastElementChild).toBe(cursor)
  })
})

it('a leading bold word in documents (plugin READMEs, Markdown files in folders) is not made a label', () => {
  const text = '- **Export** large file exports stop halfway.'
  const readme = render(<Markdown readme text={text} />)
  expect(readme.container.querySelector('.md-lead')).toBeNull()
  readme.unmount()
  const file = render(<Markdown leadLabels={false} text={text} />)
  expect(file.container.querySelector('.md-lead')).toBeNull()
  expect(file.container.querySelector('strong')).toHaveTextContent('Export')
})
