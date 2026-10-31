import { createContext, memo, useContext, useId, useRef, useState, type ComponentProps } from 'react'
import ReactMarkdown, { type ExtraProps } from 'react-markdown'
import rehypeHighlight from 'rehype-highlight'
import remarkGfm from 'remark-gfm'
import { MermaidBlock } from './MermaidBlock'
import { remarkHttpLinks } from './remark-http-links'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { t } from '@/i18n'

// react-markdown 会把语法树节点作为 node 传给自定义组件，不能透传到 DOM 上。
function withoutNode<T extends ExtraProps>({ node, ...props }: T) {
  void node
  return props
}

const MarkdownSource = createContext({ text: '', streaming: false })

function nodeText(node: ExtraProps['node']): string {
  return node?.children.map(child => child.type === 'text' ? child.value : child.type === 'element' ? nodeText(child) : '').join('') ?? ''
}

// 代码块的语言取自 rehype-highlight 保留的 language-* 类名；没有写语言时不显示。
function codeLanguage(node: ExtraProps['node']): string | null {
  const code = node?.children.find((child) => child.type === 'element' && child.tagName === 'code')
  const classes = code?.type === 'element' ? code.properties.className : undefined
  const language = Array.isArray(classes) ? classes.map(String).find((name) => name.startsWith('language-')) : undefined
  return language ? language.slice('language-'.length) : null
}

// 代码块：孔版蓝加网点底、白字、粉色印刷投影；顶部一行是语言名与复制操作。
// 复制的内容取自渲染后的文字，即代码原文，不含高亮标记。
function CodeBlock(allProps: ComponentProps<'pre'> & ExtraProps) {
  const { text, streaming } = useContext(MarkdownSource)
  const language = codeLanguage(allProps.node)
  const props = withoutNode(allProps)
  const ref = useRef<HTMLPreElement>(null)
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    await navigator.clipboard.writeText(ref.current?.querySelector('code')?.textContent ?? ref.current?.textContent ?? '')
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  if (language?.toLowerCase() === 'mermaid') {
    const node = allProps.node
    const original = text.slice(node?.position?.start.offset, node?.position?.end.offset)
    const lines = original.split(/\r?\n/)
    const opening = lines[0]?.match(/^\s*(?:>\s*)?(`{3,}|~{3,})/)
    const last = lines.at(-1)?.trim() ?? ''
    const closed = opening !== null && opening !== undefined && lines.length > 1 && new RegExp(`^${opening[1][0]}{${opening[1].length},}\\s*$`).test(last.replace(/^>\s*/, ''))
    return <MermaidBlock source={nodeText(node).replace(/\n$/, '') + '\n'} pending={streaming && !closed} />
  }
  return (
    <div className="riso-halftone min-w-0 rounded-card bg-riso-blue text-white shadow-print-pink">
      <div className="flex items-center justify-between gap-3 pt-2 pr-3 pl-4">
        <span className="font-mono text-[11.5px] font-bold text-riso-yellow">{language}</span>
        <button
          type="button"
          className="inline-flex min-h-7 shrink-0 items-center rounded-full bg-riso-yellow px-3 text-xs font-black text-ink"
          onClick={() => void copy()}
        >
          {copied ? t.common.copied : t.chat.copyCode}
        </button>
      </div>
      <pre ref={ref} {...props} />
    </div>
  )
}

// 链接：危险协议被 urlTransform 清空后按文字显示；只有绝对 http(s) 与 mailto 链接在新标签页打开，
// 页内锚点（如脚注）留在当前页跳转。
function Link(allProps: ComponentProps<'a'> & ExtraProps) {
  const { href, ...props } = withoutNode(allProps)
  if (!href) return <span title={props.title}>{props.children}</span>
  if (/^(https?:\/\/|mailto:)/i.test(href)) return <a {...props} href={href} target="_blank" rel="noopener noreferrer" />
  return <a {...props} href={href} />
}

function Image(allProps: ComponentProps<'img'> & ExtraProps) {
  return <img {...withoutNode(allProps)} loading="lazy" referrerPolicy="no-referrer" className="max-w-full" />
}

// 插件的 README 不在控制台里：相对链接没有可解析的位置，只显示文字；绝对链接与页内锚点（如脚注）同回复。
function ReadmeLink(allProps: ComponentProps<'a'> & ExtraProps) {
  const href = allProps.href
  if (href && (/^(https?:\/\/|mailto:)/i.test(href) || href.startsWith('#'))) return <Link {...allProps} />
  const { title, children } = withoutNode(allProps)
  return <span title={title}>{children}</span>
}

// README 里的图片不加载，只显示替代文字。
function AltText(allProps: ComponentProps<'img'> & ExtraProps) {
  const { alt } = withoutNode(allProps)
  return alt ? <span>{alt}</span> : null
}

// 表格用控制台的表格组件，外层可以单独横向滚动。
const tableComponents = {
  table: (props: ComponentProps<'table'> & ExtraProps) => <Table {...withoutNode(props)} />,
  thead: (props: ComponentProps<'thead'> & ExtraProps) => <TableHeader {...withoutNode(props)} />,
  tbody: (props: ComponentProps<'tbody'> & ExtraProps) => <TableBody {...withoutNode(props)} />,
  tr: (props: ComponentProps<'tr'> & ExtraProps) => <TableRow {...withoutNode(props)} />,
  th: (props: ComponentProps<'th'> & ExtraProps) => <TableHead {...withoutNode(props)} />,
  td: (props: ComponentProps<'td'> & ExtraProps) => <TableCell {...withoutNode(props)} />,
}

// 回复中的输出光标：1s 步进闪烁；减少动态效果时停止闪烁，常亮显示。
const cursorClass = 'riso-cursor ml-1 inline-block h-[1.2em] w-2.5 bg-riso-pink align-[-0.25em] motion-reduce:animate-none'

export function Cursor() {
  return <span aria-hidden="true" className={cursorClass} />
}

type HastNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HastNode[] }

// 脚注标题的 id 固定为 footnote-label，不受 clobberPrefix 影响；这里给它和引用它的 aria-describedby 加上本条回复的前缀。
function rehypePrefixFootnoteLabel(prefix: string) {
  const id = prefix + 'footnote-label'
  const visit = (node: HastNode) => {
    const properties = node.properties
    if (properties?.id === 'footnote-label') properties.id = id
    if (Array.isArray(properties?.ariaDescribedBy)) {
      properties.ariaDescribedBy = properties.ariaDescribedBy.map((v) => (v === 'footnote-label' ? id : v))
    }
    node.children?.forEach(visit)
  }
  return visit
}

const TEXT_BLOCKS = new Set(['p', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6'])
const CONTAINERS = new Set(['ul', 'ol', 'li', 'blockquote'])

// 把输出光标插在最后一段文字的末尾（沿列表、引用往里找），与文字同行；最后一块是代码块、表格等时放在它们之后。
function rehypeCursor() {
  return (tree: HastNode) => {
    let parent = tree
    for (;;) {
      const last = parent.children?.findLast((child) => child.type === 'element' || (child.type === 'text' && child.value?.trim()))
      if (last?.type !== 'element' || !last.tagName || !(TEXT_BLOCKS.has(last.tagName) || CONTAINERS.has(last.tagName))) break
      parent = last
      if (TEXT_BLOCKS.has(last.tagName)) break
    }
    const cursor: HastNode = { type: 'element', tagName: 'span', properties: { ariaHidden: 'true', className: cursorClass.split(' ') }, children: [] }
    parent.children = [...(parent.children ?? []), cursor]
  }
}

// 回复按 Markdown 渲染。不启用原始 HTML：回复中的 HTML 按文字显示，脚本不会执行；
// 链接经 react-markdown 默认的 urlTransform 过滤危险协议。
// 每条回复各自渲染，脚注 id 加上本实例的前缀，多条回复同用 [^1] 时 id 不重复。
// cursor 为真时（回复进行中）在文字末尾显示输出光标；readme 为真时按插件 README 处理相对链接与图片。
export const Markdown = memo(function Markdown({ text, cursor = false, readme = false }: { text: string; cursor?: boolean; readme?: boolean }) {
  const prefix = `md-${useId().replace(/[^\w-]/g, '')}-`
  return (
    <MarkdownSource.Provider value={{ text, streaming: cursor }}>
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkHttpLinks]}
        remarkRehypeOptions={{
          clobberPrefix: prefix,
          footnoteLabel: t.chat.footnoteLabel,
          footnoteBackLabel: (referenceIndex, rereferenceIndex) => t.chat.footnoteBackLabel(referenceIndex + 1, rereferenceIndex),
        }}
        rehypePlugins={[[rehypeHighlight, { detect: false, ignore: ['mermaid'] }], [rehypePrefixFootnoteLabel, prefix], ...(cursor ? [rehypeCursor] : [])]}
        components={{ pre: CodeBlock, a: readme ? ReadmeLink : Link, img: readme ? AltText : Image, ...tableComponents }}
      >
        {text}
      </ReactMarkdown>
    </div>
    </MarkdownSource.Provider>
  )
})
