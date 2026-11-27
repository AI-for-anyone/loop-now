import { createContext, memo, useContext, useId, useRef, useState, type ComponentProps } from 'react'
import { ImageOffIcon } from 'lucide-react'
import ReactMarkdown, { type ExtraProps } from 'react-markdown'
import { Link as RouterLink, useInRouterContext } from 'react-router'
import rehypeHighlight from 'rehype-highlight'
import remarkCjkFriendly from 'remark-cjk-friendly/parseOnly'
import remarkCjkFriendlyGfmStrikethrough from 'remark-cjk-friendly-gfm-strikethrough/parseOnly'
import remarkGfm from 'remark-gfm'
import { ImagePreviewDialog } from '@/features/images/ImagePreview'
import { cn } from '@/lib/utils'
import { MermaidBlock } from './MermaidBlock'
import { useLocalFolderImage, useLocalFolderImages, type ReplyFolderContext } from './local-images'
import { rehypeImageRows } from './rehype-image-rows'
import { rehypeLeadLabel } from './rehype-lead-label'
import { rehypeLocalImages } from './rehype-local-images'
import { remarkHttpLinks } from './remark-http-links'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { t } from '@/i18n'
import { copyText } from '@/lib/clipboard'
import { useSkin } from '@/theme/store'

// react-markdown passes the syntax tree node to custom components as node, which must not be passed through to the DOM.
function withoutNode<T extends ExtraProps>({ node, ...props }: T) {
  void node
  return props
}

const MarkdownSource = createContext({ text: '', streaming: false })
const InsideLink = createContext(false)
const ReplyFolder = createContext<ReplyFolderContext | undefined>(undefined)

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
    <div className="min-w-0 overflow-hidden rounded-card border border-code-border bg-code-bg text-code-fg">
      <div className="flex items-center justify-between gap-3 border-b border-code-border px-4 py-2">
        <span className="font-mono text-[11.5px] font-semibold text-code-muted">{language}</span>
        <button
          type="button"
          className="inline-flex min-h-7 shrink-0 cursor-pointer items-center rounded-stamp border border-hairline-strong bg-surface-control px-2.5 text-xs font-medium text-ink hover:bg-surface-hover focus-visible:shadow-focus"
          onClick={() => void copy()}
        >
          {copied ? t.common.copied : t.chat.copyCode}
        </button>
      </div>
      <pre ref={ref} {...props} />
    </div>
  )
}

type MarkdownLinkProps = ComponentProps<'a'> & ExtraProps & { 'data-md-folder-link'?: string }

// Links: once urlTransform has emptied a dangerous protocol, the link is shown as text; only absolute http(s) and mailto links open in
// a new tab, while in-page anchors (e.g. footnotes) jump within the current page. Folder links navigate via React Router when inside
// the console router so switching to the Agent's Folder tab stays in-app.
function Link(allProps: MarkdownLinkProps) {
  const { href, children, 'data-md-folder-link': folderLinkAttr, ...props } = withoutNode(allProps)
  const inRouter = useInRouterContext()
  if (!href) return <span title={props.title}>{children}</span>
  const body = <InsideLink.Provider value={true}>{children}</InsideLink.Provider>
  if (folderLinkAttr !== undefined) {
    const title = props.title ?? t.folders.openInFolder
    if (inRouter) {
      return <RouterLink {...props} to={href} title={title} data-md-folder-link={folderLinkAttr}>{body}</RouterLink>
    }
    return <a {...props} href={href} title={title} data-md-folder-link={folderLinkAttr}>{body}</a>
  }
  if (/^(https?:\/\/|mailto:)/i.test(href)) return <a {...props} href={href} target="_blank" rel="noopener noreferrer">{body}</a>
  return <a {...props} href={href}>{body}</a>
}

type MarkdownImageProps = ComponentProps<'img'> & ExtraProps & { 'data-md-block-image'?: string; 'data-md-local-path'?: string }

// Shared clickable thumbnail + lightbox preview used by both external Markdown images and local folder images.
function ReplyImageThumbnail({
  src,
  alt,
  title,
  className,
  block = false,
  originalHref,
  onError,
  imgProps,
}: {
  src: string
  alt: string
  title?: string
  className?: string
  block?: boolean
  originalHref?: string
  onError?: () => void
  imgProps?: Omit<ComponentProps<'img'>, 'src' | 'alt' | 'title' | 'className' | 'loading' | 'referrerPolicy' | 'onError'>
}) {
  const insideLink = useContext(InsideLink)
  const [open, setOpen] = useState(false)
  const tooltip = alt || title || undefined

  const image = (
    <img
      {...imgProps}
      src={src}
      alt={alt}
      title={tooltip}
      loading="lazy"
      referrerPolicy="no-referrer"
      onError={onError}
      className={cn(
        'block h-auto max-h-[240px] w-auto max-w-full',
        insideLink && 'max-w-[min(100%,360px)] rounded-input border border-ink/20 bg-white',
        insideLink && block && 'mt-1.5',
        className,
      )}
    />
  )

  if (insideLink) return image

  return (
    <>
      <button
        type="button"
        aria-label={alt ? t.images.enlarge(alt) : t.images.enlargeUnnamed}
        title={tooltip}
        onClick={() => setOpen(true)}
        data-md-image
        data-md-block-image={block ? '' : undefined}
        className={cn(
          block ? 'mt-1.5 block' : 'inline-block align-top',
          'max-w-[min(100%,360px)] cursor-zoom-in overflow-hidden rounded-input border border-ink/20 bg-white p-0 outline-none hover:border-ink/40 focus-visible:shadow-focus',
        )}
      >
        {image}
      </button>
      <ImagePreviewDialog
        open={open}
        onOpenChange={setOpen}
        src={src}
        alt={alt}
        title={alt || t.images.unnamed}
        referrerPolicy="no-referrer"
        showOriginal
        originalHref={originalHref}
      />
    </>
  )
}

// Local Markdown image loaded through the Agent or task node folder content endpoint: missing files, outside paths, or files over the
// preview limit show no thumbnail (keeping alt text only when present).
function LocalFolderImage({
  relativePath,
  alt,
  title,
  className,
  block,
}: {
  relativePath: string
  alt: string
  title?: string
  className?: string
  block: boolean
}) {
  const folder = useContext(ReplyFolder)
  const { status, src, contentURL } = useLocalFolderImage(folder, relativePath)
  const [brokenSrc, setBrokenSrc] = useState<string | null>(null)

  if (status === 'loading') return null
  if (status === 'error' || !src || brokenSrc === src) {
    return alt ? <span>{alt}</span> : null
  }
  return (
    <ReplyImageThumbnail
      src={src}
      alt={alt}
      title={title}
      className={className}
      block={block}
      originalHref={contentURL ?? undefined}
      onError={() => setBrokenSrc(src)}
    />
  )
}

function parseStripPaths(raw: string): string[] {
  try {
    const parsed: unknown = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}

// Thumbnails attached right after a paragraph or list that named local image paths in inline code or bare text.
// Files that are missing, outside, or over the preview limit are omitted; when nothing loads, nothing is rendered.
function LocalImageStrip({ paths, inline }: { paths: string[]; inline: boolean }) {
  const folder = useContext(ReplyFolder)
  const loaded = useLocalFolderImages(folder, paths)
  const [broken, setBroken] = useState<ReadonlySet<string>>(() => new Set())
  const visible = loaded.filter((item) => !broken.has(item.src))
  if (visible.length === 0) return null

  const markBroken = (src: string) => setBroken((prev) => (prev.has(src) ? prev : new Set([...prev, src])))

  if (visible.length === 1) {
    const item = visible[0]!
    return (
      <div data-md-local-images className={inline ? 'mt-2' : undefined}>
        <ReplyImageThumbnail
          src={item.src}
          alt={item.alt}
          originalHref={item.contentURL}
          onError={() => markBroken(item.src)}
        />
      </div>
    )
  }

  return (
    <div data-md-local-images className={cn('md-image-row-wrap', inline && 'mt-2')}>
      <div className="md-image-row" data-md-image-row="" data-count={String(visible.length)}>
        {visible.map((item) => (
          <div key={item.relativePath} className="md-image-item">
            <ReplyImageThumbnail
              src={item.src}
              alt={item.alt}
              originalHref={item.contentURL}
              onError={() => markBroken(item.src)}
            />
          </div>
        ))}
      </div>
    </div>
  )
}

type MarkdownDivProps = ComponentProps<'div'> & ExtraProps & { 'data-md-local-strip'?: string; 'data-md-local-strip-inline'?: string }

function MarkdownDiv(allProps: MarkdownDivProps) {
  const { 'data-md-local-strip': strip, 'data-md-local-strip-inline': inlineAttr, ...props } = withoutNode(allProps)
  if (strip !== undefined) {
    return <LocalImageStrip paths={parseStripPaths(strip)} inline={inlineAttr !== undefined} />
  }
  return <div {...props} />
}

// Reply images render as rounded thumbnails capped at 240px high and 360px wide, keeping aspect ratio; click or Enter opens the shared
// preview dialog. Images that fail to load (or have a blocked URL) show their alt text in a small box instead of an empty frame.
function Image(allProps: MarkdownImageProps) {
  const { src, alt = '', title, className, 'data-md-block-image': blockAttr, 'data-md-local-path': localPath, ...props } = withoutNode(allProps)
  const block = blockAttr !== undefined
  const [failedSrc, setFailedSrc] = useState<string | null>(null)

  if (localPath !== undefined) {
    return <LocalFolderImage relativePath={localPath} alt={alt} title={title} className={className} block={block} />
  }

  const failed = !src || failedSrc === src
  const tooltip = alt || title || undefined

  if (failed) {
    return (
      <span
        role="img"
        aria-label={alt || t.images.loadFailed}
        title={tooltip}
        data-md-image-error
        data-md-block-image={block ? '' : undefined}
        className={cn(
          block ? 'mt-1.5 flex w-fit' : 'inline-flex align-top',
          'max-w-[min(100%,360px)] items-center gap-2 rounded-input border border-dashed border-ink/25 bg-paper px-3 py-2 text-[13px] leading-snug text-ink-500',
        )}
      >
        <ImageOffIcon aria-hidden="true" className="size-4 shrink-0" />
        <span className="min-w-0 break-words">{alt || t.images.loadFailed}</span>
      </span>
    )
  }

  return (
    <ReplyImageThumbnail
      src={src}
      alt={alt}
      title={title}
      className={className}
      block={block}
      onError={() => setFailedSrc(src)}
      imgProps={props}
    />
  )
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
const cursorClass = 'riso-cursor ml-1 inline-block h-[1.15em] w-2 bg-ink align-[-0.2em] motion-reduce:animate-none'

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
// leadLabels: in the Plate skin, the bold word at the start of a list item becomes a small label; in the default mono skin, bold stays
// plain inline text with its punctuation intact. Disabled for documents (plugin READMEs, Markdown files in folders).
export const Markdown = memo(function Markdown({
  text,
  cursor = false,
  readme = false,
  leadLabels = !readme,
  folder,
}: {
  text: string
  cursor?: boolean
  readme?: boolean
  leadLabels?: boolean
  folder?: ReplyFolderContext
}) {
  const skin = useSkin()
  const useLeadLabels = leadLabels && skin === 'plate'
  const prefix = `md-${useId().replace(/[^\w-]/g, '')}-`
  const localFolder = readme ? undefined : folder
  return (
    <MarkdownSource.Provider value={{ text, streaming: cursor }}>
    <ReplyFolder.Provider value={localFolder}>
    <div className="markdown">
      <ReactMarkdown
        // remarkCjkFriendlyGfmStrikethrough must run after remarkGfm so it overrides GFM's strikethrough tokenizer.
        remarkPlugins={[remarkGfm, remarkCjkFriendly, remarkCjkFriendlyGfmStrikethrough, remarkHttpLinks]}
        remarkRehypeOptions={{
          clobberPrefix: prefix,
          footnoteLabel: t.chat.footnoteLabel,
          footnoteBackLabel: (referenceIndex, rereferenceIndex) => t.chat.footnoteBackLabel(referenceIndex + 1, rereferenceIndex),
        }}
        // Local image resolution runs before image rows so local Markdown images participate in wrapping rows; image rows run before
        // the cursor so streaming doesn't break a row; labels run after the cursor so the cursor stays at the end of the text.
        rehypePlugins={[
          [rehypeHighlight, { detect: false, ignore: ['mermaid'] }],
          [rehypePrefixFootnoteLabel, prefix],
          ...(localFolder ? [[rehypeLocalImages, localFolder] as [typeof rehypeLocalImages, ReplyFolderContext]] : []),
          ...(readme ? [] : [rehypeImageRows]),
          ...(cursor ? [rehypeCursor] : []),
          ...(useLeadLabels ? [rehypeLeadLabel] : []),
        ]}
        components={{ pre: CodeBlock, a: readme ? ReadmeLink : Link, img: readme ? AltText : Image, div: MarkdownDiv, ...tableComponents }}
      >
        {text}
      </ReactMarkdown>
    </div>
    </ReplyFolder.Provider>
    </MarkdownSource.Provider>
  )
})
