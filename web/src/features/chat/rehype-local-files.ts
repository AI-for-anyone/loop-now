import { classifyFencedPath } from './file-cards'
import {
  classifyInlineCodePath,
  classifyMarkdownLinkHref,
  splitBareLocalPaths,
} from './local-files'
import {
  classifyMarkdownImageSource,
  isReplyImagePath,
  resolveAbsoluteLocalImage,
  type ReplyFolderContext,
} from './local-images'

type HastNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HastNode[] }

export interface FileCardCandidate {
  relativePath: string
  certain: boolean
}

const HEADINGS = new Set(['h1', 'h2', 'h3', 'h4', 'h5', 'h6'])
const LISTS = new Set(['ul', 'ol'])

const isElement = (node: HastNode, tag?: string) => node.type === 'element' && (tag ? node.tagName === tag : Boolean(node.tagName))

function textOf(node: HastNode): string {
  if (node.type === 'text') return node.value ?? ''
  return (node.children ?? []).map(textOf).join('')
}

function preLanguage(node: HastNode): string | null {
  const code = node.children?.find((child) => isElement(child, 'code'))
  const classes = code?.properties?.className
  const language = Array.isArray(classes) ? classes.map(String).find((name) => name.startsWith('language-')) : undefined
  return language ? language.slice('language-'.length) : null
}

function makeMarkerLink(properties: Record<string, unknown>, children: HastNode[]): HastNode {
  return {
    type: 'element',
    tagName: 'a',
    properties: { href: '', ...properties },
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

function makeFileCards(cards: FileCardCandidate[], inline = false): HastNode {
  return {
    type: 'element',
    tagName: 'div',
    properties: {
      dataMdFileCards: JSON.stringify(cards),
      ...(inline ? { dataMdFileCardsInline: '' } : {}),
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

interface ReplyCardState {
  // Paths referenced with certainty (explicit link, absolute/file:// inside folder, or ./ path) anywhere in this reply.
  certainPaths: ReadonlySet<string>
  // Paths that have already claimed their single card slot in this reply.
  emittedCards: Set<string>
}

function takeNewFileCards(paths: string[], state: ReplyCardState): FileCardCandidate[] {
  const out: FileCardCandidate[] = []
  for (const relativePath of paths) {
    if (state.emittedCards.has(relativePath)) continue
    state.emittedCards.add(relativePath)
    out.push({ relativePath, certain: state.certainPaths.has(relativePath) })
  }
  return out
}

// Pre-pass: gather all non-image local paths that appear with certainty anywhere in the reply so reply-wide dedupe keeps
// certainty even when an earlier mention of the same file used a bare relative filename.
function collectCertainFilePaths(tree: HastNode, folder: ReplyFolderContext): Set<string> {
  const rootsReady = folder.rootsReady ?? true
  const certain = new Set<string>()
  const walk = (node: HastNode, inLink: boolean) => {
    if (node.type === 'text') {
      if (inLink || !node.value) return
      for (const part of splitBareLocalPaths(node.value, folder.roots, rootsReady)) {
        if (part.kind === 'local' && part.certain && !part.trailingSlash && !isReplyImagePath(part.relativePath)) {
          certain.add(part.relativePath)
        }
      }
      return
    }
    if (!isElement(node)) {
      for (const child of node.children ?? []) walk(child, inLink)
      return
    }
    const tag = node.tagName ?? ''
    if (tag === 'pre') {
      const classified = classifyFencedPath(textOf(node), folder.roots, rootsReady, preLanguage(node))
      if (classified.kind === 'file' && classified.certain) certain.add(classified.relativePath)
      return
    }
    if (tag === 'code') {
      if (!inLink) {
        const classified = classifyInlineCodePath(textOf(node), folder.roots, rootsReady)
        if (classified.kind === 'local' && classified.certain && !classified.trailingSlash && !isReplyImagePath(classified.relativePath)) {
          certain.add(classified.relativePath)
        }
      }
      return
    }
    if (tag === 'a') {
      const href = typeof node.properties?.href === 'string' ? node.properties.href : ''
      const classified = classifyMarkdownLinkHref(href, folder.roots, rootsReady)
      if (classified.kind === 'local' && !classified.trailingSlash && !isReplyImagePath(classified.relativePath)) {
        certain.add(classified.relativePath)
      }
      for (const child of node.children ?? []) walk(child, true)
      return
    }
    for (const child of node.children ?? []) walk(child, inLink)
  }
  walk(tree, false)
  return certain
}

interface InlineCollection {
  // Local image paths named in inline code or bare text (candidates for trailing thumbnails).
  textPaths: string[]
  // Local image paths already rendered as Markdown <img> elements inside this block.
  markdownImagePaths: Set<string>
  // Local non-image file paths named via Markdown links, inline code, or bare text (candidates for file cards).
  filePaths: string[]
}

function newInlineCollection(): InlineCollection {
  return { textPaths: [], markdownImagePaths: new Set(), filePaths: [] }
}

function transformInlineChildren(
  children: HastNode[],
  folder: ReplyFolderContext,
  collected: InlineCollection,
  inLink: boolean,
): HastNode[] {
  const rootsReady = folder.rootsReady ?? true
  const out: HastNode[] = []
  for (const child of children) {
    if (child.type === 'text') {
      if (inLink || !child.value) {
        out.push(child)
        continue
      }
      const parts = splitBareLocalPaths(child.value, folder.roots, rootsReady)
      if (parts.length === 1 && parts[0]!.kind === 'text') {
        out.push(child)
        continue
      }
      for (const part of parts) {
        if (part.kind === 'text') {
          out.push({ type: 'text', value: part.value })
        } else if (part.kind === 'local') {
          const imageRel = resolveAbsoluteLocalImage(part.raw, folder.roots)
          if (imageRel) collected.textPaths.push(imageRel)
          if (!part.trailingSlash && !isReplyImagePath(part.relativePath)) {
            collected.filePaths.push(part.relativePath)
          }
          out.push(
            makeMarkerLink(
              {
                dataMdLocalFile: part.relativePath,
                dataMdLocalOrigin: part.certain ? 'bare-certain' : 'bare-maybe',
                ...(part.trailingSlash ? { dataMdLocalTrailingSlash: '' } : {}),
                ...(part.suffix ? { dataMdLocalSuffix: part.suffix } : {}),
              },
              [{ type: 'text', value: part.raw }],
            ),
          )
        } else if (part.kind === 'local-dir') {
          out.push(
            makeMarkerLink(
              { dataMdLocalDir: part.relativePath, dataMdLocalOrigin: 'bare-certain' },
              [{ type: 'text', value: part.raw }],
            ),
          )
        } else if (part.kind === 'outside') {
          out.push(makeMarkerLink({ dataMdLocalOutside: 'text' }, [{ type: 'text', value: part.raw }]))
        } else {
          out.push(makeMarkerLink({ dataMdLocalPending: '' }, [{ type: 'text', value: part.raw }]))
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
        const rawText = textOf(child)
        const classified = classifyInlineCodePath(rawText, folder.roots, rootsReady)
        if (classified.kind === 'local') {
          const imageRel = resolveAbsoluteLocalImage(rawText, folder.roots)
          if (imageRel) collected.textPaths.push(imageRel)
          if (!classified.trailingSlash && !isReplyImagePath(classified.relativePath)) {
            collected.filePaths.push(classified.relativePath)
          }
          out.push(
            makeMarkerLink(
              {
                dataMdLocalFile: classified.relativePath,
                dataMdLocalOrigin: classified.certain ? 'code-certain' : 'code-maybe',
                ...(classified.trailingSlash ? { dataMdLocalTrailingSlash: '' } : {}),
              },
              [child],
            ),
          )
          continue
        }
        if (classified.kind === 'local-dir') {
          out.push(makeMarkerLink({ dataMdLocalDir: classified.relativePath, dataMdLocalOrigin: 'code-certain' }, [child]))
          continue
        }
        if (classified.kind === 'outside') {
          out.push(makeMarkerLink({ dataMdLocalOutside: 'text' }, [child]))
          continue
        }
        if (classified.kind === 'pending-root') {
          out.push(makeMarkerLink({ dataMdLocalPending: '' }, [child]))
          continue
        }
      }
      out.push(child)
      continue
    }

    if (tag === 'a') {
      const href = typeof child.properties?.href === 'string' ? child.properties.href : ''
      const classified = classifyMarkdownLinkHref(href, folder.roots, rootsReady)
      if (classified.kind === 'local') {
        if (!classified.trailingSlash && !isReplyImagePath(classified.relativePath)) {
          collected.filePaths.push(classified.relativePath)
        }
        child.properties = {
          ...(child.properties ?? {}),
          href: '',
          dataMdLocalFile: classified.relativePath,
          dataMdLocalOrigin: 'link',
          ...(classified.trailingSlash ? { dataMdLocalTrailingSlash: '' } : {}),
          ...(classified.suffix ? { dataMdLocalSuffix: classified.suffix } : {}),
        }
      } else if (classified.kind === 'local-dir') {
        child.properties = {
          ...(child.properties ?? {}),
          href: '',
          dataMdLocalDir: classified.relativePath,
          dataMdLocalOrigin: 'link',
        }
      } else if (classified.kind === 'outside') {
        child.properties = { ...(child.properties ?? {}), href: '', dataMdLocalOutside: 'link' }
      } else if (classified.kind === 'pending-root') {
        child.properties = { ...(child.properties ?? {}), href: '', dataMdLocalPending: '' }
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

function markFencedPath(node: HastNode, folder: ReplyFolderContext, state: ReplyCardState) {
  const rootsReady = folder.rootsReady ?? true
  const classified = classifyFencedPath(textOf(node), folder.roots, rootsReady, preLanguage(node))
  if (classified.kind !== 'file') return
  const duplicate = state.emittedCards.has(classified.relativePath)
  const certain = classified.certain || state.certainPaths.has(classified.relativePath)
  if (!duplicate) state.emittedCards.add(classified.relativePath)
  node.properties = {
    ...(node.properties ?? {}),
    dataMdFencedFile: classified.relativePath,
    dataMdFencedCertain: certain ? '1' : '0',
    ...(duplicate ? { dataMdFencedDuplicate: '1' } : {}),
  }
}

function insertBeforeSublist(item: HastNode, nodes: HastNode[]) {
  if (nodes.length === 0) return
  const itemChildren = item.children ?? []
  const firstSublist = itemChildren.findIndex((child) => isElement(child) && LISTS.has(child.tagName ?? ''))
  if (firstSublist >= 0) {
    item.children = [...itemChildren.slice(0, firstSublist), ...nodes, ...itemChildren.slice(firstSublist)]
  } else {
    item.children = [...itemChildren, ...nodes]
  }
}

function transformListItem(
  item: HastNode,
  folder: ReplyFolderContext,
  state: ReplyCardState,
): { imagePaths: string[]; fileCards: FileCardCandidate[] } {
  const collected = newInlineCollection()
  const fileCards: FileCardCandidate[] = []
  const flushDirectCards = () => {
    if (collected.filePaths.length === 0) return
    fileCards.push(...takeNewFileCards(collected.filePaths, state))
    collected.filePaths = []
  }

  const children = item.children ?? []
  const out: HastNode[] = []
  for (const child of children) {
    if (isElement(child, 'p')) {
      if (child.children) child.children = transformInlineChildren(child.children, folder, collected, false)
      out.push(child)
    } else if (isElement(child) && (LISTS.has(child.tagName ?? '') || child.tagName === 'blockquote' || child.tagName === 'table')) {
      flushDirectCards()
      transformBlockContainer(child, folder, state)
      out.push(child)
    } else if (isElement(child, 'pre')) {
      flushDirectCards()
      markFencedPath(child, folder, state)
      out.push(child)
    } else {
      out.push(...transformInlineChildren([child], folder, collected, false))
    }
  }
  flushDirectCards()
  item.children = out
  return {
    imagePaths: uniqueNew(collected.textPaths, collected.markdownImagePaths),
    fileCards,
  }
}

function transformTable(table: HastNode, folder: ReplyFolderContext): { imagePaths: string[]; filePaths: string[] } {
  const collected = newInlineCollection()
  const walk = (node: HastNode) => {
    if (isElement(node, 'td') || isElement(node, 'th')) {
      if (node.children) node.children = transformInlineChildren(node.children, folder, collected, false)
      return
    }
    for (const child of node.children ?? []) walk(child)
  }
  walk(table)
  return {
    imagePaths: uniqueNew(collected.textPaths, collected.markdownImagePaths),
    filePaths: collected.filePaths,
  }
}

function transformBlockContainer(parent: HastNode, folder: ReplyFolderContext, state: ReplyCardState) {
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
      markFencedPath(node, folder, state)
      out.push(node)
      continue
    }

    if (tag === 'p' || HEADINGS.has(tag)) {
      const collected = newInlineCollection()
      if (node.children) node.children = transformInlineChildren(node.children, folder, collected, false)
      out.push(node)
      const imagePaths = uniqueNew(collected.textPaths, collected.markdownImagePaths)
      if (imagePaths.length > 0) out.push(makeStrip(imagePaths))
      const fileCards = takeNewFileCards(collected.filePaths, state)
      if (fileCards.length > 0) out.push(makeFileCards(fileCards))
      continue
    }

    if (LISTS.has(tag)) {
      const listChildren = node.children ?? []
      const perItem: { item: HastNode; imagePaths: string[]; fileCards: FileCardCandidate[] }[] = []
      for (const child of listChildren) {
        if (isElement(child, 'li')) {
          perItem.push({ item: child, ...transformListItem(child, folder, state) })
        }
      }
      const withImages = perItem.filter((entry) => entry.imagePaths.length > 0)
      const withCards = perItem.filter((entry) => entry.fileCards.length > 0)
      out.push(node)

      if (withImages.length >= 2) {
        // Several items in one list -> one row of thumbnails right after the list, keeping list items compact (and preserving
        // rehype-lead-label's side-by-side yellow labels).
        const merged = uniqueNew(withImages.flatMap((entry) => entry.imagePaths), new Set())
        if (merged.length > 0) out.push(makeStrip(merged))
      } else if (withImages.length === 1) {
        insertBeforeSublist(withImages[0]!.item, [makeStrip(withImages[0]!.imagePaths, true)])
      }

      if (withCards.length >= 2) {
        out.push(makeFileCards(withCards.flatMap((entry) => entry.fileCards)))
      } else if (withCards.length === 1) {
        insertBeforeSublist(withCards[0]!.item, [makeFileCards(withCards[0]!.fileCards, true)])
      }
      continue
    }

    if (tag === 'table') {
      const { imagePaths, filePaths } = transformTable(node, folder)
      out.push(node)
      if (imagePaths.length > 0) out.push(makeStrip(imagePaths))
      const fileCards = takeNewFileCards(filePaths, state)
      if (fileCards.length > 0) out.push(makeFileCards(fileCards))
      continue
    }

    if (tag === 'blockquote' || tag === 'div' || tag === 'section') {
      transformBlockContainer(node, folder, state)
      out.push(node)
      continue
    }

    out.push(node)
  }

  parent.children = out
}

// Resolve local folder files, directories, and images in an Agent or task node reply:
// - Markdown images pointing inside the folder load through the folder content endpoint (outside paths become plain alt text);
// - Links, inline code, and bare paths inside the folder are marked for opening in the sandboxed raw view in a new tab (or the
//   Folder tab for directories), while paths outside the folder become non-clickable text with a tooltip;
// - Absolute image paths in inline code or bare text emit trailing thumbnail strips right after the paragraph or list;
// - Non-image local files emit deduplicated file-card strips after their block (and path-only fenced code blocks are marked for
//   replacement by a card or suppression when already shown earlier in the reply).
export function rehypeLocalFiles(folder: ReplyFolderContext) {
  return (tree: HastNode) => {
    const state: ReplyCardState = {
      certainPaths: collectCertainFilePaths(tree, folder),
      emittedCards: new Set(),
    }
    transformBlockContainer(tree, folder, state)
  }
}
