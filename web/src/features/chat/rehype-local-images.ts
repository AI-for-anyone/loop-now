import {
  classifyMarkdownImageSource,
  resolveAbsoluteLocalImage,
  splitBareLocalImagePaths,
  type ReplyFolderContext,
} from './local-images'

type HastNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HastNode[] }

const HEADINGS = new Set(['h1', 'h2', 'h3', 'h4', 'h5', 'h6'])
const LISTS = new Set(['ul', 'ol'])

const isElement = (node: HastNode, tag?: string) => node.type === 'element' && (tag ? node.tagName === tag : Boolean(node.tagName))

function textOf(node: HastNode): string {
  if (node.type === 'text') return node.value ?? ''
  return (node.children ?? []).map(textOf).join('')
}

function makeFolderLink(folder: ReplyFolderContext, relativePath: string, children: HastNode[]): HastNode {
  return {
    type: 'element',
    tagName: 'a',
    properties: {
      href: folder.folderLink!(relativePath),
      dataMdFolderLink: relativePath,
    },
    children,
  }
}

function makeStrip(paths: string[], inline = false): HastNode {
  return {
    type: 'element',
    tagName: 'div',
    properties: {
      dataMdLocalStrip: JSON.stringify(paths),
      ...(inline ? { dataMdLocalStripInline: '' } : {}),
    },
    children: [],
  }
}

function uniqueNew(paths: string[], exclude: ReadonlySet<string>): string[] {
  const seen = new Set(exclude)
  const out: string[] = []
  for (const path of paths) {
    if (seen.has(path)) continue
    seen.add(path)
    out.push(path)
  }
  return out
}

interface InlineCollection {
  // Local image paths named in inline code or bare text (candidates for trailing thumbnails).
  textPaths: string[]
  // Local image paths already rendered as Markdown <img> elements inside this block.
  markdownImagePaths: Set<string>
}

function transformInlineChildren(
  children: HastNode[],
  folder: ReplyFolderContext,
  collected: InlineCollection,
  inLink: boolean,
): HastNode[] {
  const out: HastNode[] = []
  for (const child of children) {
    if (child.type === 'text') {
      if (inLink || !child.value) {
        out.push(child)
        continue
      }
      const parts = splitBareLocalImagePaths(child.value, folder.roots)
      if (parts.length === 1 && parts[0]!.kind === 'text') {
        out.push(child)
        continue
      }
      for (const part of parts) {
        if (part.kind === 'text') {
          out.push({ type: 'text', value: part.value })
        } else {
          collected.textPaths.push(part.relativePath)
          if (folder.folderLink) {
            out.push(makeFolderLink(folder, part.relativePath, [{ type: 'text', value: part.raw }]))
          } else {
            out.push({ type: 'text', value: part.raw })
          }
        }
      }
      continue
    }

    if (!isElement(child)) {
      out.push(child)
      continue
    }

    const tag = child.tagName ?? ''
    if (tag === 'pre') {
      out.push(child)
      continue
    }

    if (tag === 'img') {
      const rawSrc = typeof child.properties?.src === 'string' ? child.properties.src : ''
      const classified = classifyMarkdownImageSource(rawSrc, folder.roots)
      if (classified.kind === 'local') {
        collected.markdownImagePaths.add(classified.relativePath)
        child.properties = { ...(child.properties ?? {}), src: '', dataMdLocalPath: classified.relativePath }
        out.push(child)
      } else if (classified.kind === 'blocked') {
        const alt = typeof child.properties?.alt === 'string' ? child.properties.alt : ''
        if (alt) out.push({ type: 'element', tagName: 'span', properties: {}, children: [{ type: 'text', value: alt }] })
      } else {
        out.push(child)
      }
      continue
    }

    if (tag === 'code') {
      if (!inLink) {
        const rel = resolveAbsoluteLocalImage(textOf(child), folder.roots)
        if (rel) {
          collected.textPaths.push(rel)
          out.push(folder.folderLink ? makeFolderLink(folder, rel, [child]) : child)
          continue
        }
      }
      out.push(child)
      continue
    }

    if (tag === 'a') {
      const href = typeof child.properties?.href === 'string' ? child.properties.href : ''
      if (folder.folderLink && href) {
        const rel = resolveAbsoluteLocalImage(href, folder.roots)
        if (rel) {
          child.properties = { ...(child.properties ?? {}), href: folder.folderLink(rel), dataMdFolderLink: rel }
        }
      }
      if (child.children) {
        child.children = transformInlineChildren(child.children, folder, collected, true)
      }
      out.push(child)
      continue
    }

    if (child.children) {
      child.children = transformInlineChildren(child.children, folder, collected, inLink)
    }
    out.push(child)
  }
  return out
}

function transformListItem(item: HastNode, folder: ReplyFolderContext): string[] {
  const collected: InlineCollection = { textPaths: [], markdownImagePaths: new Set() }
  const children = item.children ?? []
  const out: HastNode[] = []
  for (const child of children) {
    if (isElement(child, 'p')) {
      if (child.children) child.children = transformInlineChildren(child.children, folder, collected, false)
      out.push(child)
    } else if (isElement(child) && (LISTS.has(child.tagName ?? '') || child.tagName === 'blockquote' || child.tagName === 'table')) {
      transformBlockContainer(child, folder)
      out.push(child)
    } else if (isElement(child, 'pre')) {
      out.push(child)
    } else {
      out.push(...transformInlineChildren([child], folder, collected, false))
    }
  }
  item.children = out
  return uniqueNew(collected.textPaths, collected.markdownImagePaths)
}

function transformTable(table: HastNode, folder: ReplyFolderContext): string[] {
  const collected: InlineCollection = { textPaths: [], markdownImagePaths: new Set() }
  const walk = (node: HastNode) => {
    if (isElement(node, 'td') || isElement(node, 'th')) {
      if (node.children) node.children = transformInlineChildren(node.children, folder, collected, false)
      return
    }
    for (const child of node.children ?? []) walk(child)
  }
  walk(table)
  return uniqueNew(collected.textPaths, collected.markdownImagePaths)
}

function transformBlockContainer(parent: HastNode, folder: ReplyFolderContext) {
  const children = parent.children
  if (!children?.length) return
  const out: HastNode[] = []

  for (const node of children) {
    if (!isElement(node)) {
      out.push(node)
      continue
    }
    const tag = node.tagName ?? ''

    if (tag === 'pre') {
      out.push(node)
      continue
    }

    if (tag === 'p' || HEADINGS.has(tag)) {
      const collected: InlineCollection = { textPaths: [], markdownImagePaths: new Set() }
      if (node.children) node.children = transformInlineChildren(node.children, folder, collected, false)
      out.push(node)
      const paths = uniqueNew(collected.textPaths, collected.markdownImagePaths)
      if (paths.length > 0) out.push(makeStrip(paths))
      continue
    }

    if (LISTS.has(tag)) {
      const listChildren = node.children ?? []
      const perItem: { item: HastNode; paths: string[] }[] = []
      for (const child of listChildren) {
        if (isElement(child, 'li')) {
          perItem.push({ item: child, paths: transformListItem(child, folder) })
        }
      }
      const withPaths = perItem.filter((entry) => entry.paths.length > 0)
      out.push(node)
      if (withPaths.length >= 2) {
        // Several items in one list -> one row of thumbnails right after the list, keeping list items compact (and preserving
        // rehype-lead-label's side-by-side yellow labels).
        const merged = uniqueNew(withPaths.flatMap((entry) => entry.paths), new Set())
        if (merged.length > 0) out.push(makeStrip(merged))
      } else if (withPaths.length === 1) {
        // Single item with image path(s) -> place right after that item's content (before any nested sublist).
        const { item, paths } = withPaths[0]!
        const itemChildren = item.children ?? []
        const firstSublist = itemChildren.findIndex((child) => isElement(child) && LISTS.has(child.tagName ?? ''))
        const strip = makeStrip(paths, true)
        if (firstSublist >= 0) {
          item.children = [...itemChildren.slice(0, firstSublist), strip, ...itemChildren.slice(firstSublist)]
        } else {
          item.children = [...itemChildren, strip]
        }
      }
      continue
    }

    if (tag === 'table') {
      const paths = transformTable(node, folder)
      out.push(node)
      if (paths.length > 0) out.push(makeStrip(paths))
      continue
    }

    if (tag === 'blockquote' || tag === 'div' || tag === 'section') {
      transformBlockContainer(node, folder)
      out.push(node)
      continue
    }

    out.push(node)
  }

  parent.children = out
}

// Resolve local folder image paths in an Agent or task node reply:
// - Markdown images pointing inside the folder are marked for loading through the folder content endpoint (outside paths are stripped
//   to plain alt text with no thumbnail and no request);
// - Absolute paths inside the folder written in inline code or bare text become links to the Agent's Folder tab (when available) and
//   emit a thumbnail strip right after the paragraph or list item (or one row right after a list when several items hold image paths).
export function rehypeLocalImages(folder: ReplyFolderContext) {
  return (tree: HastNode) => {
    transformBlockContainer(tree, folder)
  }
}
