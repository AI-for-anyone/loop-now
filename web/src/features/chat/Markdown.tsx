import { createContext, memo, useContext, useId, useRef, useState, type ComponentProps } from 'react'
import ReactMarkdown, { type ExtraProps } from 'react-markdown'
import rehypeHighlight from 'rehype-highlight'
import remarkGfm from 'remark-gfm'
import { MermaidBlock } from './MermaidBlock'
import { rehypeLeadLabel } from './rehype-lead-label'
import { remarkHttpLinks } from './remark-http-links'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { t } from '@/i18n'
import { copyText } from '@/lib/clipboard'

// react-markdown passes the syntax tree node to custom components as node, which must not be passed through to the DOM.
function withoutNode<T extends ExtraProps>({ node, ...props }: T) {
  void node
  return props
}

const MarkdownSource = createContext({ text: '', streaming: false })

function nodeText(node: ExtraProps['node']): string {
  return node?.children.map(child => child.type === 'text' ? child.value : child.type === 'element' ? nodeText(child) : '').join('') ?? ''
}

// The code block's language comes from the language-* class name that rehype-highlight keeps; nothing is shown when no language is given.
function codeLanguage(node: ExtraProps['node']): string | null {
  const code = node?.children.find((child) => child.type === 'element' && child.tagName === 'code')
  const classes = code?.type === 'element' ? code.properties.className : undefined
  const language = Array.isArray(classes) ? classes.map(String).find((name) => name.startsWith('language-')) : undefined
  return language ? language.slice('language-'.length) : null
}

// Code block: semantic code surface with hairline border (Plate keeps riso blue with halftone dots and pink print shadow).
// What gets copied is taken from the rendered text, i.e. the original code, without highlighting markup.
function CodeBlock(allProps: ComponentProps<'pre'> & ExtraProps) {
  const { text, streaming } = useContext(MarkdownSource)
  const language = codeLanguage(allProps.node)
  const props = withoutNode(allProps)
  const ref = useRef<HTMLPreElement>(null)
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    const ok = await copyText(ref.current?.querySelector('code')?.textContent ?? ref.current?.textContent ?? '')
    if (!ok) return
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
    <div className="min-w-0 overflow-hidden rounded-card border border-code-border bg-code-bg text-code-fg [[data-skin=plate]_&]:riso-halftone [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:shadow-print-pink">
      <div className="flex items-center justify-between gap-3 border-b border-code-border px-4 py-2 [[data-skin=plate]_&]:border-b-0 [[data-skin=plate]_&]:pt-2 [[data-skin=plate]_&]:pr-3 [[data-skin=plate]_&]:pb-0">
        <span className="font-mono text-[11.5px] font-semibold text-code-muted [[data-skin=plate]_&]:font-bold">{language}</span>
        <button
          type="button"
          className="inline-flex min-h-7 shrink-0 cursor-pointer items-center rounded-stamp border border-hairline-strong bg-surface-control px-2.5 text-xs font-medium text-ink hover:bg-surface-hover focus-visible:shadow-focus [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:px-3 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:hover:bg-riso-yellow"
          onClick={() => void copy()}
        >
          {copied ? t.common.copied : t.chat.copyCode}
        </button>
      </div>
      <pre ref={ref} {...props} />
    </div>
  )
}

// Links: once urlTransform has emptied a dangerous protocol, the link is shown as text; only absolute http(s) and mailto links open in
// a new tab, while in-page anchors (e.g. footnotes) jump within the current page.
function Link(allProps: ComponentProps<'a'> & ExtraProps) {
  const { href, ...props } = withoutNode(allProps)
  if (!href) return <span title={props.title}>{props.children}</span>
  if (/^(https?:\/\/|mailto:)/i.test(href)) return <a {...props} href={href} target="_blank" rel="noopener noreferrer" />
  return <a {...props} href={href} />
}

function Image(allProps: ComponentProps<'img'> & ExtraProps) {
  return <img {...withoutNode(allProps)} loading="lazy" referrerPolicy="no-referrer" className="max-w-full" />
}

// A plugin's README doesn't live in the console: relative links have nowhere to resolve to, so only their text is shown; absolute
// links and in-page anchors (e.g. footnotes) behave as in replies.
function ReadmeLink(allProps: ComponentProps<'a'> & ExtraProps) {
  const href = allProps.href
  if (href && (/^(https?:\/\/|mailto:)/i.test(href) || href.startsWith('#'))) return <Link {...allProps} />
  const { title, children } = withoutNode(allProps)
  return <span title={title}>{children}</span>
}

// Images in a README aren't loaded; only their alt text is shown.
function AltText(allProps: ComponentProps<'img'> & ExtraProps) {
  const { alt } = withoutNode(allProps)
  return alt ? <span>{alt}</span> : null
}

// Tables use the console's table components, whose wrapper can scroll horizontally on its own.
const tableComponents = {
  table: (props: ComponentProps<'table'> & ExtraProps) => <Table {...withoutNode(props)} />,
  thead: (props: ComponentProps<'thead'> & ExtraProps) => <TableHeader {...withoutNode(props)} />,
  tbody: (props: ComponentProps<'tbody'> & ExtraProps) => <TableBody {...withoutNode(props)} />,
  tr: (props: ComponentProps<'tr'> & ExtraProps) => <TableRow {...withoutNode(props)} />,
  th: (props: ComponentProps<'th'> & ExtraProps) => <TableHead {...withoutNode(props)} />,
  td: (props: ComponentProps<'td'> & ExtraProps) => <TableCell {...withoutNode(props)} />,
}

// The output cursor while replying: blinks in 1s steps; with reduced motion it stops blinking and stays on.
const cursorClass = 'riso-cursor ml-1 inline-block h-[1.15em] w-2 bg-ink align-[-0.2em] motion-reduce:animate-none [[data-skin=plate]_&]:h-[1.2em] [[data-skin=plate]_&]:w-2.5 [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:align-[-0.25em]'

export function Cursor() {
  return <span aria-hidden="true" className={cursorClass} />
}

type HastNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HastNode[] }

// The footnote heading's id is always footnote-label and isn't affected by clobberPrefix; here it and the aria-describedby that
// references it get this reply's prefix.
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

// Insert the output cursor at the end of the last paragraph of text (searching inward through lists and blockquotes), on the same
// line as the text; when the last block is a code block, table or the like, put it after that block.
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

// Replies are rendered as Markdown. Raw HTML is not enabled: HTML in a reply is shown as text and scripts never run;
// links go through react-markdown's default urlTransform, which filters out dangerous protocols.
// Each reply renders separately, and footnote ids get this instance's prefix, so ids don't collide when several replies all use [^1].
// When cursor is true (reply in progress), the output cursor shows at the end of the text; when readme is true, relative links and
// images are handled as for a plugin README.
// leadLabels: the bold word at the start of a list item becomes a small yellow label; used for replies and messages, not for documents
// (plugin READMEs, Markdown files in folders).
export const Markdown = memo(function Markdown({ text, cursor = false, readme = false, leadLabels = !readme }: { text: string; cursor?: boolean; readme?: boolean; leadLabels?: boolean }) {
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
        // Labels are processed after the cursor: the cursor gets wrapped together with the text after the label and stays at the end
        // of the text.
        rehypePlugins={[[rehypeHighlight, { detect: false, ignore: ['mermaid'] }], [rehypePrefixFootnoteLabel, prefix], ...(cursor ? [rehypeCursor] : []), ...(leadLabels ? [rehypeLeadLabel] : [])]}
        components={{ pre: CodeBlock, a: readme ? ReadmeLink : Link, img: readme ? AltText : Image, ...tableComponents }}
      >
        {text}
      </ReactMarkdown>
    </div>
    </MarkdownSource.Provider>
  )
})
