import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { apiUrl } from '@/test/api'
import { server } from '@/test/server'
import type { ReplyFolderContext } from './local-images'
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

describe('Markdown images in a reply', () => {
  it('caps a reply image as a rounded thumbnail with its alt text as tooltip', () => {
    render(<Markdown text="![Architecture diagram](https://example.com/big.png)" />)
    const trigger = screen.getByRole('button', { name: 'Enlarge image: Architecture diagram' })
    expect(trigger).toHaveAttribute('title', 'Architecture diagram')
    expect(trigger.className).toContain('rounded-input')
    expect(trigger.className).toContain('max-w-[min(100%,360px)]')
    const img = within(trigger).getByRole('img', { name: 'Architecture diagram' })
    expect(img).toHaveAttribute('src', 'https://example.com/big.png')
    expect(img).toHaveAttribute('referrerpolicy', 'no-referrer')
    expect(img).toHaveAttribute('title', 'Architecture diagram')
    expect(img.className).toContain('max-h-[240px]')
    expect(img.className).toContain('h-auto')
    expect(img.className).toContain('w-auto')
  })

  it('opens the preview dialog on click and Enter, shows a link to the original in a new tab, and closes on Escape', async () => {
    const user = userEvent.setup()
    render(<Markdown text="![Hero shot](https://example.com/hero.png)" />)
    const trigger = screen.getByRole('button', { name: 'Enlarge image: Hero shot' })

    await user.click(trigger)
    const dialog = await screen.findByRole('dialog', { name: 'Hero shot' })
    const large = within(dialog).getByRole('img', { name: 'Hero shot' })
    expect(large).toHaveAttribute('src', 'https://example.com/hero.png')
    expect(large).toHaveAttribute('referrerpolicy', 'no-referrer')
    const link = within(dialog).getByRole('link', { name: 'Open original in new tab' })
    expect(link).toHaveAttribute('href', 'https://example.com/hero.png')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')

    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    trigger.focus()
    await user.keyboard('{Enter}')
    expect(await screen.findByRole('dialog', { name: 'Hero shot' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('uses fallback accessible labels when an image has no alt text', async () => {
    const user = userEvent.setup()
    render(<Markdown text="![](https://example.com/plain.png)" />)
    const trigger = screen.getByRole('button', { name: 'Enlarge image' })
    expect(trigger).not.toHaveAttribute('title')
    await user.click(trigger)
    expect(await screen.findByRole('dialog', { name: 'Image' })).toBeInTheDocument()
  })

  it('keeps an image between two text paragraphs inline as a thumbnail', () => {
    const { container } = render(<Markdown text={'Before the chart.\n\n![Latency chart](https://example.com/chart.png)\n\nAfter the chart.'} />)
    expect(container.querySelector('[data-md-image-row]')).toBeNull()
    const paragraphs = [...container.querySelectorAll('p')]
    expect(paragraphs).toHaveLength(3)
    expect(paragraphs[0]).toHaveTextContent('Before the chart.')
    expect(within(paragraphs[1]).getByRole('button', { name: 'Enlarge image: Latency chart' })).toBeInTheDocument()
    expect(paragraphs[2]).toHaveTextContent('After the chart.')
  })

  it('renders an image inside a link as a capped thumbnail without nesting a button', () => {
    const { container } = render(<Markdown text="[![Build status](https://example.com/badge.png)](https://example.com/ci)" />)
    const link = screen.getByRole('link', { name: 'Build status' })
    expect(link).toHaveAttribute('href', 'https://example.com/ci')
    expect(within(link).queryByRole('button')).toBeNull()
    const img = within(link).getByRole('img', { name: 'Build status' })
    expect(img.className).toContain('max-h-[240px]')
    expect(container.querySelector('a button')).toBeNull()
  })

  it('shows the alt text in a small box when an image fails to load or has a blocked URL', () => {
    const { rerender } = render(<Markdown text="![Broken chart](https://example.com/missing.png)" />)
    const img = screen.getByRole('img', { name: 'Broken chart' })
    fireEvent.error(img)
    expect(screen.queryByRole('button', { name: /Enlarge image/ })).toBeNull()
    const box = screen.getByRole('img', { name: 'Broken chart' })
    expect(box.tagName).not.toBe('IMG')
    expect(box).toHaveTextContent('Broken chart')

    rerender(<Markdown text="![Blocked image](javascript:alert(1))" />)
    const blocked = screen.getByRole('img', { name: 'Blocked image' })
    expect(blocked.tagName).not.toBe('IMG')
    expect(blocked).toHaveTextContent('Blocked image')

    rerender(<Markdown text="![](https://example.com/missing-no-alt.png)" />)
    fireEvent.error(document.querySelector('img')!)
    expect(screen.getByRole('img', { name: 'Image failed to load' })).toHaveTextContent('Image failed to load')
  })

  it('keeps README images as alt text only', () => {
    const { container } = render(<Markdown readme text={'![Architecture](https://example.com/a.png)\n\n![Details](https://example.com/b.png)'} />)
    expect(container.querySelector('img')).toBeNull()
    expect(screen.queryByRole('button')).toBeNull()
    expect(container.querySelector('[data-md-image-row]')).toBeNull()
    expect(container).toHaveTextContent('Architecture')
    expect(container).toHaveTextContent('Details')
  })

  it('places consecutive images in one paragraph in a single row', () => {
    const { container } = render(
      <Markdown text={'![One](https://example.com/1.png) ![Two](https://example.com/2.png)\n![Three](https://example.com/3.png) ![Four](https://example.com/4.png)'} />,
    )
    const row = container.querySelector('[data-md-image-row]')!
    expect(row).not.toBeNull()
    expect(row).toHaveAttribute('data-count', '4')
    expect(row.querySelectorAll('.md-image-item')).toHaveLength(4)
    expect(row.querySelectorAll('figure')).toHaveLength(0)
    expect(within(row as HTMLElement).getAllByRole('img').map((img) => img.getAttribute('alt'))).toEqual(['One', 'Two', 'Three', 'Four'])
  })

  it('merges consecutive image-only paragraphs into one row without pulling in an intro paragraph', () => {
    const { container } = render(
      <Markdown
        text={[
          'Here are four candidates:',
          '',
          '![One](https://example.com/1.png)',
          '',
          '![Two](https://example.com/2.png)',
          '',
          '![Three](https://example.com/3.png)',
          '',
          '![Four](https://example.com/4.png)',
          '',
          'Pick your favorite.',
        ].join('\n')}
      />,
    )
    const paragraphs = [...container.querySelectorAll('p')]
    expect(paragraphs.map((p) => p.textContent)).toEqual(['Here are four candidates:', 'Pick your favorite.'])
    const row = container.querySelector('[data-md-image-row]')!
    expect(row).toHaveAttribute('data-count', '4')
    expect(row.querySelectorAll('.md-image-item')).toHaveLength(4)
    expect(row.querySelectorAll('figure')).toHaveLength(0)
  })

  it('groups caption lines with their following images in one row across blank-line, single-newline, and heading forms', () => {
    const separated = [
      'Four options:',
      '',
      '**2D-1**: bright blue poster',
      '',
      '![2D-1](https://example.com/1.png)',
      '',
      '**2D-2**: warm yellow print',
      '',
      '![2D-2](https://example.com/2.png)',
      '',
      'Which one works best?',
    ].join('\n')
    const view1 = render(<Markdown text={separated} />)
    expect([...view1.container.querySelectorAll('p')].map((p) => p.textContent)).toEqual(['Four options:', 'Which one works best?'])
    const row1 = view1.container.querySelector('[data-md-image-row]')!
    expect(row1).toHaveAttribute('data-count', '2')
    const figures1 = [...row1.querySelectorAll<HTMLElement>('figure.md-image-item')]
    expect(figures1).toHaveLength(2)
    expect(figures1[0].firstElementChild!.tagName).toBe('FIGCAPTION')
    expect(figures1[0].firstElementChild).toHaveTextContent('2D-1: bright blue poster')
    expect(within(figures1[0]).getByRole('img', { name: '2D-1' })).toBeInTheDocument()
    expect(figures1[1].firstElementChild).toHaveTextContent('2D-2: warm yellow print')
    expect(within(figures1[1]).getByRole('img', { name: '2D-2' })).toBeInTheDocument()
    view1.unmount()

    const singleNewline = [
      '2D-1: bright blue poster\n![2D-1](https://example.com/1.png)',
      '',
      '2D-2: warm yellow print\n![2D-2](https://example.com/2.png)',
    ].join('\n')
    const view2 = render(<Markdown text={singleNewline} />)
    const figures2 = [...view2.container.querySelectorAll('[data-md-image-row] figure.md-image-item')]
    expect(figures2).toHaveLength(2)
    expect(figures2[0].firstElementChild).toHaveTextContent('2D-1: bright blue poster')
    expect(figures2[1].firstElementChild).toHaveTextContent('2D-2: warm yellow print')
    view2.unmount()

    const compact = '2D-1: bright blue\n![2D-1](https://example.com/1.png)\n2D-2: warm yellow\n![2D-2](https://example.com/2.png)'
    const view3 = render(<Markdown text={compact} />)
    const figures3 = [...view3.container.querySelectorAll('[data-md-image-row] figure.md-image-item')]
    expect(figures3).toHaveLength(2)
    expect(figures3[0].firstElementChild).toHaveTextContent('2D-1: bright blue')
    expect(figures3[1].firstElementChild).toHaveTextContent('2D-2: warm yellow')
    view3.unmount()

    const headings = '### 2D-1\n\n![2D-1](https://example.com/1.png)\n\n### 2D-2\n\n![2D-2](https://example.com/2.png)'
    const view4 = render(<Markdown text={headings} />)
    const figures4 = [...view4.container.querySelectorAll('[data-md-image-row] figure.md-image-item')]
    expect(figures4).toHaveLength(2)
    expect(figures4[0].firstElementChild).toHaveTextContent('2D-1')
    expect(figures4[1].firstElementChild).toHaveTextContent('2D-2')
  })

  it('does not turn long prose paragraphs or a single captioned image into an image row', () => {
    const prose = [
      'After refactoring the scheduler we ran the full benchmark suite across three machines and verified that tail latency dropped steadily.',
      '',
      '![Latency](https://example.com/latency.png)',
      '',
      'Next we checked garbage collection pause times under the same steady workload because earlier versions spiked whenever compaction ran.',
      '',
      '![Pauses](https://example.com/pauses.png)',
    ].join('\n')
    const view1 = render(<Markdown text={prose} />)
    expect(view1.container.querySelector('[data-md-image-row]')).toBeNull()
    expect(view1.container.querySelectorAll('figure')).toHaveLength(0)
    view1.unmount()

    const single = render(<Markdown text={'2D-1: bright blue\n![2D-1](https://example.com/1.png)\n\nClosing note.'} />)
    expect(single.container.querySelector('[data-md-image-row]')).toBeNull()
    expect(single.container.querySelectorAll('p')).toHaveLength(2)
  })

  it('keeps consecutive images in one row while streaming with the output cursor after the row', () => {
    const { container } = render(<Markdown cursor text={'![One](https://example.com/1.png)\n\n![Two](https://example.com/2.png)'} />)
    const row = container.querySelector('[data-md-image-row]')!
    expect(row).toHaveAttribute('data-count', '2')
    const cursor = container.querySelector('.riso-cursor')!
    expect(cursor).not.toBeNull()
    expect(row.contains(cursor)).toBe(false)
  })

  it('groups multi-line captions (up to 3 lines with hard/soft breaks and inline links) into rows while rejecting 4+ lines', () => {
    const twoSections = [
      'Intro summary for all four candidates.',
      '',
      '### 2D sticker style',
      '',
      '**2D-1**: cute smile, one thin white line near the top corner.  ',
      'Local file: [mascot-2d-1.png](file:///workspace/mascot-2d-1.png)',
      '![Mascot 2D-1](https://example.com/2d-1.png)',
      '',
      '**2D-2 (pick)**: clean floating yellow block and matching cover colors.  ',
      'Local file: [mascot-2d-2.png](https://example.com/2d-2.png)',
      '![Mascot 2D-2](https://example.com/2d-2.png)',
      '',
      '---',
      '',
      '### 3D vinyl toy style',
      '',
      '**3D-1**: smooth material, slightly smaller corner opening.  ',
      'Local file: [mascot-3d-1.png](https://example.com/3d-1.png)',
      '![Mascot 3D-1](https://example.com/3d-1.png)',
      '',
      '**3D-2 (pick)**: crisp logo silhouette, warm expression and gold accent.  ',
      'Local file: [mascot-3d-2.png](https://example.com/3d-2.png)',
      '![Mascot 3D-2](https://example.com/3d-2.png)',
    ].join('\n')
    const view = render(<Markdown text={twoSections} />)
    const rows = [...view.container.querySelectorAll<HTMLElement>('[data-md-image-row]')]
    expect(rows).toHaveLength(2)
    expect(rows.map((row) => row.getAttribute('data-count'))).toEqual(['2', '2'])
    expect(view.container.querySelectorAll('h3')).toHaveLength(2)
    expect(view.container.querySelectorAll('hr')).toHaveLength(1)
    const firstCaption = rows[0].querySelector('figure figcaption')!
    expect(firstCaption.querySelector('br')).not.toBeNull()
    expect(firstCaption).toHaveTextContent('2D-1: cute smile, one thin white line near the top corner.')
    expect(firstCaption).toHaveTextContent('Local file: mascot-2d-1.png')
    // The blocked file:// link stays visible as a span inside the caption.
    expect(firstCaption.querySelector('span')).toHaveTextContent('mascot-2d-1.png')
    view.unmount()

    const tooManyLines = [
      'Line 1  \nLine 2  \nLine 3  \nLine 4\n![One](https://example.com/1.png)',
      '',
      'Line 1  \nLine 2  \nLine 3  \nLine 4\n![Two](https://example.com/2.png)',
    ].join('\n')
    const view2 = render(<Markdown text={tooManyLines} />)
    expect(view2.container.querySelector('[data-md-image-row]')).toBeNull()
  })

  it('starts a trailing image after a soft or hard line break on its own line even when not in a row, while keeping inline images inline', () => {
    const softBreak = render(<Markdown text={'Local file: mascot.png\n![Mascot](https://example.com/a.png)'} />)
    const softButton = screen.getByRole('button', { name: 'Enlarge image: Mascot' })
    expect(softButton).toHaveAttribute('data-md-block-image')
    expect(softButton.className).toContain('block')
    expect(softButton.className).not.toContain('inline-block')
    softBreak.unmount()

    const hardBreak = render(<Markdown text={'Local file: mascot.png  \n![Mascot](https://example.com/a.png)'} />)
    const hardButton = screen.getByRole('button', { name: 'Enlarge image: Mascot' })
    expect(hardButton).toHaveAttribute('data-md-block-image')
    expect(hardButton.className).toContain('block')
    expect(hardBreak.container.querySelector('p br')).toBeNull()
    hardBreak.unmount()

    const failedTrailing = render(<Markdown text={'Local file: missing.png\n![Missing](https://example.com/missing.png)'} />)
    fireEvent.error(screen.getByRole('img', { name: 'Missing' }))
    const errorBox = screen.getByRole('img', { name: 'Missing' })
    expect(errorBox).toHaveAttribute('data-md-block-image')
    expect(errorBox.className).toContain('flex')
    failedTrailing.unmount()

    render(<Markdown text="Look at ![Icon](https://example.com/icon.png) right here" />)
    const inlineButton = screen.getByRole('button', { name: 'Enlarge image: Icon' })
    expect(inlineButton).not.toHaveAttribute('data-md-block-image')
    expect(inlineButton.className).toContain('inline-block')
  })
})

describe('Local folder images in a reply', () => {
  const ROOT = '/var/work/agents/a1/workspace'
  const REAL_ROOT = '/private/var/work/agents/a1/workspace'
  const PNG_BYTES = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
  const SVG_BYTES = '<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><script>window.__svgRan=true</script><rect width="32" height="32" fill="#2443b5"/></svg>'

  function makeFolder(overrides: Partial<ReplyFolderContext> = {}): ReplyFolderContext {
    return {
      roots: [ROOT, REAL_ROOT],
      contentURL: (rel) => apiUrl(`/agents/a1/folder/content?${new URLSearchParams({ path: rel })}`),
      folderLink: (rel) => `/p/demo/agents/a1/folder?${new URLSearchParams({ path: rel })}`,
      maxBytes: 1024,
      ...overrides,
    }
  }

  function mockFolderFiles(files: Record<string, Uint8Array | string>) {
    const requested: string[] = []
    server.use(
      http.get(apiUrl('/agents/a1/folder/content'), ({ request }) => {
        const rel = new URL(request.url).searchParams.get('path') ?? ''
        requested.push(rel)
        const body = files[rel]
        if (body === undefined) return new HttpResponse(null, { status: 404 })
        return new HttpResponse(body, { headers: { 'Content-Type': 'application/octet-stream' } })
      }),
    )
    return requested
  }

  it('loads absolute, realPath, relative, and file:// Markdown image sources through the folder content endpoint and opens the preview dialog', async () => {
    const user = userEvent.setup()
    const requested = mockFolderFiles({
      'assets/abs.png': PNG_BYTES,
      'assets/rel.JPG': PNG_BYTES,
      'assets/file.webp': PNG_BYTES,
      'assets/real.svg': SVG_BYTES,
    })
    const folder = makeFolder()
    const md = [
      `![Abs](${ROOT}/assets/abs.png)`,
      '',
      '![Rel](./assets/rel.JPG)',
      '',
      `![File](file://${ROOT}/assets/file.webp)`,
      '',
      `![Real](${REAL_ROOT}/assets/real.svg)`,
    ].join('\n')

    const { container } = render(<Markdown folder={folder} text={md} />)
    const row = container.querySelector('[data-md-image-row]')!
    expect(row).toHaveAttribute('data-count', '4')

    for (const label of ['Abs', 'Rel', 'File', 'Real']) {
      expect(await screen.findByRole('button', { name: `Enlarge image: ${label}` })).toBeInTheDocument()
    }
    expect(requested.sort()).toEqual(['assets/abs.png', 'assets/file.webp', 'assets/real.svg', 'assets/rel.JPG'])

    // SVG is rendered inside <img>, never inlined, and its preview dialog never links to a blob: URL.
    const svgImg = screen.getByRole('img', { name: 'Real' })
    expect(svgImg.tagName).toBe('IMG')
    expect(container.querySelector('svg script')).toBeNull()

    await user.click(screen.getByRole('button', { name: 'Enlarge image: Real' }))
    const dialog = await screen.findByRole('dialog', { name: 'Real' })
    expect(within(dialog).getByRole('img', { name: 'Real' }).tagName).toBe('IMG')
    const original = within(dialog).getByRole('link', { name: 'Open original in new tab' })
    expect(original.getAttribute('href')).toBe(folder.contentURL('assets/real.svg'))
    expect(original.getAttribute('href')).not.toMatch(/^blob:/i)
  })

  it('keeps inline-code and bare absolute paths as folder links and shows thumbnails right after the paragraph or as one row after a list', async () => {
    mockFolderFiles({
      'assets/preview-2x2.png': PNG_BYTES,
      'assets/bare.jpg': PNG_BYTES,
      'assets/a.svg': SVG_BYTES,
      'assets/b.svg': SVG_BYTES,
      'assets/c.svg': SVG_BYTES,
    })
    const folder = makeFolder()
    const md = [
      `Preview image \`${ROOT}/assets/preview-2x2.png\` and bare (${ROOT}/assets/bare.jpg).`,
      '',
      `- **Option A**: \`${ROOT}/assets/a.svg\``,
      `- **Option B**: \`${ROOT}/assets/b.svg\``,
      `- **Option C**: \`${ROOT}/assets/c.svg\``,
    ].join('\n')

    const { container } = render(<Markdown folder={folder} text={md} />)
    // Every path text becomes a link to the Agent's Folder tab.
    const inlineLink = screen.getByRole('link', { name: `${ROOT}/assets/preview-2x2.png` })
    expect(inlineLink).toHaveAttribute('href', folder.folderLink!('assets/preview-2x2.png'))
    expect(inlineLink).toHaveAttribute('title', 'Open in folder')
    expect(screen.getByRole('link', { name: `${ROOT}/assets/bare.jpg` })).toHaveAttribute('href', folder.folderLink!('assets/bare.jpg'))
    expect(screen.getByRole('link', { name: `${ROOT}/assets/a.svg` })).toHaveAttribute('href', folder.folderLink!('assets/a.svg'))

    // Both the paragraph thumbnails and the list thumbnails appear as rows directly after their blocks.
    await waitFor(() => expect(container.querySelectorAll('[data-md-local-images] [data-md-image-row]')).toHaveLength(2))
    const rows = [...container.querySelectorAll<HTMLElement>('[data-md-local-images] [data-md-image-row]')]
    expect(rows[0]).toHaveAttribute('data-count', '2')
    expect(within(rows[0]).getAllByRole('img').map((img) => img.getAttribute('alt'))).toEqual(['preview-2x2.png', 'bare.jpg'])
    expect(rows[1]).toHaveAttribute('data-count', '3')
    expect(within(rows[1]).getAllByRole('img').map((img) => img.getAttribute('alt'))).toEqual(['a.svg', 'b.svg', 'c.svg'])

    // The list itself kept lead-label formatting because the thumbnail row sits after the <ul>.
    const list = container.querySelector('ul')!
    expect(list).toHaveClass('md-lead-list')
    expect(list.nextElementSibling).toHaveAttribute('data-md-local-images')
  })

  it('places a single-item list thumbnail right after that list item', async () => {
    mockFolderFiles({ 'shots/one.png': PNG_BYTES })
    const folder = makeFolder()
    const md = [
      `- First item: \`${ROOT}/shots/one.png\``,
      '- Second item without image',
    ].join('\n')

    const { container } = render(<Markdown folder={folder} text={md} />)
    const img = await screen.findByRole('img', { name: 'one.png' })
    const items = container.querySelectorAll('li')
    expect(items[0].contains(img)).toBe(true)
    expect(items[1].contains(img)).toBe(false)
  })

  it('shows no thumbnail and keeps text intact for outside paths, missing files, and files above the preview limit', async () => {
    const requested = mockFolderFiles({
      'assets/huge.png': new Uint8Array(2048),
      'assets/ok.png': PNG_BYTES,
    })
    const folder = makeFolder({ maxBytes: 512 })
    const md = [
      'Outside markdown: ![Outside](/etc/secret.png) ![Escape](../secret.png)',
      '',
      'Outside inline: `/etc/secret.png` and bare /tmp/other.jpg.',
      '',
      `Missing: \`${ROOT}/assets/missing.png\``,
      '',
      `Too large: \`${ROOT}/assets/huge.png\``,
      '',
      'Missing markdown: ![Missing](assets/missing.png)',
      '',
      'Huge markdown: ![Huge](assets/huge.png)',
      '',
      `Valid: \`${ROOT}/assets/ok.png\``,
    ].join('\n')

    const { container } = render(<Markdown folder={folder} text={md} />)
    // Wait until the valid image finishes loading so every missing/too-large fetch has settled too.
    expect(await screen.findByRole('img', { name: 'ok.png' })).toBeInTheDocument()

    // Outside paths were never requested and never linked.
    expect(requested.every((path) => path.startsWith('assets/'))).toBe(true)
    expect(screen.queryByRole('link', { name: '/etc/secret.png' })).toBeNull()
    expect(screen.queryByRole('link', { name: '/tmp/other.jpg' })).toBeNull()
    expect(container).toHaveTextContent('/etc/secret.png')
    expect(container).toHaveTextContent('/tmp/other.jpg')

    // Only the single valid file produced a thumbnail; missing, too-large, and outside paths show no thumbnail or error box.
    expect(screen.getAllByRole('img')).toHaveLength(1)
    expect(container.querySelector('[data-md-image-error]')).toBeNull()
    expect(container).toHaveTextContent('Outside')
    expect(container).toHaveTextContent('Missing')
    expect(container).toHaveTextContent('Huge')
  })

  it('renders thumbnails without folder links when folderLink is omitted (task node conversations)', async () => {
    mockFolderFiles({ 'assets/node.png': PNG_BYTES })
    const folder = makeFolder({ folderLink: undefined })
    render(<Markdown folder={folder} text={`Saved to \`${ROOT}/assets/node.png\`.`} />)
    expect(await screen.findByRole('img', { name: 'node.png' })).toBeInTheDocument()
    expect(screen.queryByRole('link')).toBeNull()
    expect(screen.getByText(`${ROOT}/assets/node.png`)).toBeInTheDocument()
  })
})
