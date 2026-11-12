import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { Markdown } from './Markdown'

describe('Markdown 渲染与安全', () => {
  it('加粗裸网址的闭合符和后续中文说明不进入链接', () => {
    const { container } = render(<Markdown text="已改为 **http://127.0.0.1:3111/app**，确认可访问。 3000 端口已停止监听。" />)
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', 'http://127.0.0.1:3111/app')
    expect(link.textContent).toBe('http://127.0.0.1:3111/app')
    expect(link.parentElement?.tagName).toBe('STRONG')
    expect(container).toHaveTextContent('，确认可访问。 3000 端口已停止监听。')
    expect(container).not.toHaveTextContent('**')
  })

  it('普通裸网址在中文句读处结束，保留查询参数、片段和 Unicode 路径', () => {
    render(<Markdown text="地址：https://example.com/目录/文件?q=中文&next=%2Fapp#段落，确认可访问。" />)
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', new URL('https://example.com/目录/文件?q=中文&next=%2Fapp#段落').href)
    expect(link.textContent).toBe('https://example.com/目录/文件?q=中文&next=%2Fapp#段落')
    expect(link.nextSibling?.textContent).toBe('，确认可访问。')
  })

  it('显式链接和尖括号链接保留完整地址，包括句读、查询参数和片段', () => {
    const url = 'https://example.com/目录，文件?q=a,b&next=%2Fapp#段落'
    render(<Markdown text={`[完整地址](${url})\n\n**<${url}>**，说明`} />)
    const links = screen.getAllByRole('link')
    expect(links).toHaveLength(2)
    for (const link of links) {
      expect(link).toHaveAttribute('href', new URL(url).href)
      expect(link).toHaveAttribute('target', '_blank')
      expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    }
    expect(links[1].parentElement?.tagName).toBe('STRONG')
  })

  it('行内和块代码内的网址不生成链接；普通括号和参数不被粗暴截断', () => {
    const url = 'https://example.com/a_(b)?q=a,b&next=%2Fx#片段'
    const { container } = render(<Markdown text={`\`${url}，说明\`\n\n\`\`\`text\n${url}，说明\n\`\`\`\n\n${url}。后文`} />)
    expect(container.querySelectorAll('code')).toHaveLength(2)
    expect(screen.getAllByRole('link')).toHaveLength(1)
    expect(screen.getByRole('link')).toHaveAttribute('href', new URL(url).href)
    expect(screen.getByText('。后文')).toBeInTheDocument()
  })

  it('普通裸链接保留合法查询中的星号和编码句读', () => {
    const url = 'https://example.com/a%EF%BC%8Cb?q=*value*&x=1#part'
    render(<Markdown text={`${url}，说明`} />)
    expect(screen.getByRole('link')).toHaveAttribute('href', url)
    expect(screen.getByRole('link').textContent).toBe(url)
  })
  it('回复中的 HTML 与脚本按文字显示，不执行', () => {
    const { container } = render(<Markdown text={'<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>'} />)
    expect(container.querySelector('script')).toBeNull()
    expect(container.querySelector('[onerror]')).toBeNull()
    expect(container).toHaveTextContent('<script>alert(1)</script>')
    expect(container).toHaveTextContent('<img src=x onerror=alert(1)>')
  })

  it('危险协议的链接按不可点击的文字显示', () => {
    const { container } = render(<Markdown text="[点我](javascript:alert(1))" />)
    expect(screen.getByText('点我').closest('a')).toBeNull()
    expect(container.querySelector('a')).toBeNull()
  })

  it('只有绝对 http(s) 与 mailto 链接在新标签页打开', () => {
    render(<Markdown text={'[外链](https://example.com) [邮件](mailto:a@example.com) [锚点](#part) [相对](docs/a.md)'} />)
    for (const name of ['外链', '邮件']) {
      const link = screen.getByRole('link', { name })
      expect(link).toHaveAttribute('target', '_blank')
      expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    }
    for (const name of ['锚点', '相对']) {
      const link = screen.getByRole('link', { name })
      expect(link).not.toHaveAttribute('target')
      expect(link).not.toHaveAttribute('rel')
    }
  })

  it('链接、图片与代码块不带 node 属性', () => {
    const { container } = render(<Markdown text={'[外链](https://example.com)\n\n![图](https://example.com/a.png)\n\n```\nx\n```'} />)
    expect(container.querySelectorAll('a, img, pre')).toHaveLength(3)
    expect(container.querySelector('[node]')).toBeNull()
  })

  it('脚注使用中文文案，在当前页跳到本条回复的注释，多条回复的 id 不重复', () => {
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

  it('复制代码块时得到代码原文', async () => {
    const user = userEvent.setup()
    const { container } = render(<Markdown text={'```bash\necho "你好" && ls -la\n```'} />)
    expect(container.querySelector('code.hljs')).not.toBeNull()
    await user.click(screen.getByRole('button', { name: '复制代码' }))
    await expect(navigator.clipboard.readText()).resolves.toBe('echo "你好" && ls -la\n')
  })

  it('流式中代码块尚未闭合时按代码块显示', () => {
    const { container, rerender } = render(<Markdown text={'说明：\n\n```ts\nconst a = 1'} />)
    expect(container.querySelector('pre code')).toHaveTextContent('const a = 1')
    rerender(<Markdown text={'说明：\n\n```ts\nconst a = 1\n```\n\n结束'} />)
    expect(container.querySelector('pre code')).toHaveTextContent('const a = 1')
    expect(screen.getByText('结束')).toBeInTheDocument()
  })

  it('直接显示回复中的图片', () => {
    render(<Markdown text="![架构图](https://example.com/a.png)" />)
    const img = screen.getByRole('img', { name: '架构图' })
    expect(img).toHaveAttribute('src', 'https://example.com/a.png')
    expect(img).toHaveAttribute('referrerpolicy', 'no-referrer')
  })

  it('渲染 GFM 表格与任务列表', () => {
    const { container } = render(<Markdown text={'| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] 完成'} />)
    expect(container.querySelector('table')).not.toBeNull()
    expect(container.querySelector('input[type="checkbox"]')).toBeChecked()
  })

  it('表格放在可以单独横向滚动的容器里', () => {
    const { container } = render(<Markdown text={'| a | b |\n|---|---|\n| 1 | 2 |'} />)
    const wrapper = container.querySelector('table')!.parentElement!
    expect(wrapper).toHaveAttribute('data-slot', 'table-container')
    expect(wrapper.className).toContain('overflow-x-auto')
  })

  it('代码块顶部显示语言名；没有写语言时不显示', () => {
    const { container, rerender } = render(<Markdown text={'```bash\nls\n```'} />)
    expect(screen.getByText('bash')).toBeInTheDocument()
    rerender(<Markdown text={'```\nls\n```'} />)
    expect(screen.queryByText('bash')).toBeNull()
    expect(container.querySelector('pre code')).toHaveTextContent('ls')
  })

  it('回复进行中时在最后一段文字末尾显示输出光标，减少动态效果时停止闪烁', () => {
    const { container, rerender } = render(<Markdown text={'第一段\n\n- 列表项'} cursor />)
    const cursor = container.querySelector('.riso-cursor')!
    expect(cursor).toHaveAttribute('aria-hidden', 'true')
    expect(cursor.className).toContain('motion-reduce:animate-none')
    expect(cursor.parentElement).toHaveTextContent('列表项')
    expect(cursor.parentElement!.tagName).toBe('LI')
    rerender(<Markdown text={'第一段\n\n- 列表项'} />)
    expect(container.querySelector('.riso-cursor')).toBeNull()
  })
})

// 改版“一张印出来的纸”：列表项开头的加粗词做成黄色小标签。
describe('列表项开头的加粗词', () => {
  const items = (container: HTMLElement) => [...container.querySelectorAll('li')]

  it('无序列表里开头的加粗词是标签，后面的文字排在标签右边；标签后面紧跟的冒号不再显示', () => {
    const { container } = render(<Markdown text={'归好了：\n\n- **导出** 大文件导出到一半会停。\n- **搜索**：中文分词不准，见 `search.go`。\n- **通知**: 同一件事提醒两次。'} />)
    const [first, second, third] = items(container)
    expect(container.querySelector('ul')).toHaveClass('md-lead-list')
    for (const item of [first, second, third]) {
      expect(item).toHaveClass('md-lead-item')
      expect(item.firstElementChild).toHaveClass('md-lead')
      expect(item.firstElementChild!.tagName).toBe('STRONG')
      expect(item.children).toHaveLength(2)
      expect(item.lastElementChild).toHaveClass('md-lead-text')
    }
    expect(first.firstElementChild).toHaveTextContent('导出')
    expect(first.lastElementChild!.textContent).toBe(' 大文件导出到一半会停。')
    expect(second.lastElementChild!.textContent).toBe('中文分词不准，见 search.go。')
    expect(second.lastElementChild!.querySelector('code')).toHaveTextContent('search.go')
    expect(third.lastElementChild!.textContent).toBe('同一件事提醒两次。')
  })

  it('整张列表每一项都是“加粗词 + 行内文字”时才改成横排；有一项不是，整张列表保留圆点，加粗词仍是标签', () => {
    const all = render(<Markdown text={'- **导出** 会停\n- **搜索** 不准'} />)
    expect(all.container.querySelector('ul')).toHaveClass('md-lead-list')
    all.unmount()
    const mixed = render(<Markdown text={'- **导出**：会停\n- 普通的一项\n- **搜索**：不准'} />)
    expect(mixed.container.querySelector('ul')).not.toHaveClass('md-lead-list')
    expect(mixed.container.querySelector('.md-lead-item')).toBeNull()
    expect(mixed.container.querySelector('.md-lead-text')).toBeNull()
    expect(mixed.container.querySelectorAll('strong.md-lead')).toHaveLength(2)
    expect(items(mixed.container)[0].textContent).toBe('导出会停')
    mixed.unmount()
    // 一项带子列表、另一项不带：两项排法要一致，都不横排。
    const nested = render(<Markdown text={'- **导出**：两个问题\n  - 大文件会停\n- **搜索**：不准'} />)
    expect(nested.container.querySelector('.md-lead-item')).toBeNull()
    expect(nested.container.querySelectorAll('strong.md-lead')).toHaveLength(2)
  })

  it('普通列表项里的子列表整张都是标签项时，子列表自己横排（缩进不变）', () => {
    const { container } = render(<Markdown text={'- 这周的问题\n  - **导出** 会停\n  - **搜索** 不准'} />)
    const [outer, inner] = container.querySelectorAll('ul')
    expect(outer).not.toHaveClass('md-lead-list')
    expect(inner).toHaveClass('md-lead-list')
    expect(inner.querySelectorAll('li.md-lead-item')).toHaveLength(2)
  })

  it('只去掉当分隔用的冒号：全角冒号，或后面是空白、行尾的半角冒号；端口、时间、盘符、:: 里的冒号保留', () => {
    const rest = (text: string) => {
      const view = render(<Markdown text={text} />)
      const value = view.container.querySelector('li')!.textContent
      view.unmount()
      return value
    }
    expect(rest('- **地址**：见下')).toBe('地址见下')
    expect(rest('- **地址**: 见下')).toBe('地址见下')
    expect(rest('- **地址**:')).toBe('地址')
    expect(rest('- **localhost**:3000 已启动')).toBe('localhost:3000 已启动')
    expect(rest('- **12**:30 午饭')).toBe('12:30 午饭')
    expect(rest('- **C**:\\Users')).toBe('C:\\Users')
    expect(rest('- **std**::vector')).toBe('std::vector')
  })

  it('加粗词不在开头、或者开头是别的内容（任务列表的勾选框）时不做成标签', () => {
    const { container } = render(<Markdown text={'- 先看 **导出** 的问题\n- [ ] **搜索** 还没做\n- 普通的一项'} />)
    expect(container.querySelector('.md-lead')).toBeNull()
    expect(container.querySelector('.md-lead-item')).toBeNull()
    expect(container.querySelector('strong')).toHaveTextContent('导出')
  })

  it('开头加粗的是一整句话时照常加粗，不做成标签', () => {
    const { container } = render(<Markdown text={'- **这一整句话都加粗了，它不是一个词而是一句完整的说明** 后面还有\n- **Short label** ok'} />)
    const [long, short] = items(container)
    expect(long.querySelector('.md-lead')).toBeNull()
    expect(long).not.toHaveClass('md-lead-item')
    expect(short.firstElementChild).toHaveClass('md-lead')
  })

  it('有序列表保留序号：加粗词是标签，但这一项不改成横排', () => {
    const { container } = render(<Markdown text={'1. **导出** 先修这个\n2. **搜索** 再修这个'} />)
    const [first] = items(container)
    expect(first.querySelector('strong')).toHaveClass('md-lead')
    expect(first).not.toHaveClass('md-lead-item')
    expect(first.querySelector('.md-lead-text')).toBeNull()
  })

  it('列表项里还有子列表或分段时，加粗词是标签，其余内容照原样排', () => {
    const { container } = render(<Markdown text={'- **导出** 两个问题\n  - 大文件会停\n  - 重试才成功\n\n另一个列表：\n\n- **搜索** 第一段\n\n  第二段\n- **通知** 也是分段的'} />)
    const [nested, , , loose] = items(container)
    expect(nested.firstElementChild).toHaveClass('md-lead')
    expect(nested).not.toHaveClass('md-lead-item')
    expect(nested.querySelectorAll('li')).toHaveLength(2)
    expect(loose.querySelector('p > strong')).toHaveClass('md-lead')
    expect(loose).not.toHaveClass('md-lead-item')
    expect(loose.querySelectorAll('p')).toHaveLength(2)
  })

  it('只有一个加粗词、后面没有文字时只显示标签', () => {
    const { container } = render(<Markdown text={'- **导出**\n- **搜索**'} />)
    const [first] = items(container)
    expect(first).toHaveClass('md-lead-item')
    expect(first.children).toHaveLength(1)
    expect(first.firstElementChild).toHaveClass('md-lead')
  })

  it('回复进行中时输出光标跟在标签后面的文字末尾', () => {
    const { container } = render(<Markdown cursor text={'- **导出** 大文件\n- **搜索** 中文分'} />)
    const last = items(container)[1]
    const cursor = container.querySelector('.riso-cursor')!
    expect(last.lastElementChild).toHaveClass('md-lead-text')
    expect(last.lastElementChild).toContainElement(cursor as HTMLElement)
    expect(last.lastElementChild!.lastElementChild).toBe(cursor)
  })
})

it('文档（插件 README、文件夹里的 Markdown 文件）里开头的加粗词不做成标签', () => {
  const text = '- **导出** 大文件导出到一半会停。'
  const readme = render(<Markdown readme text={text} />)
  expect(readme.container.querySelector('.md-lead')).toBeNull()
  readme.unmount()
  const file = render(<Markdown leadLabels={false} text={text} />)
  expect(file.container.querySelector('.md-lead')).toBeNull()
  expect(file.container.querySelector('strong')).toHaveTextContent('导出')
})
