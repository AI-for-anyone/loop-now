type HastNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HastNode[] }

// Limits on a caption above an image in a row: up to 3 lines (hard or soft breaks), each line at most 110 display units and the whole
// caption at most 160 units (a CJK/full-width character counts as 2, everything else as 1). Keeps short multi-line descriptions (such as
// a label line plus a local file link) grouped with their picture without pulling long prose paragraphs into an image row.
const MAX_CAPTION_LINES = 3
const MAX_CAPTION_LINE_UNITS = 110
const MAX_CAPTION_TOTAL_UNITS = 160

const CAPTION_HEADINGS = new Set(['h3', 'h4', 'h5', 'h6'])
const DISALLOWED_IN_CAPTION = new Set(['img', 'p', 'div', 'ul', 'ol', 'pre', 'blockquote', 'table', 'hr', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6'])
const CONTAINERS = new Set(['blockquote', 'li'])

interface RowItem {
  caption?: HastNode[]
  image: HastNode
}

const isBlank = (node: HastNode) => node.type === 'text' && !node.value?.trim()
const isElement = (node: HastNode, tag?: string) => node.type === 'element' && (tag ? node.tagName === tag : Boolean(node.tagName))
const isImg = (node: HastNode) => isElement(node, 'img')
const isBr = (node: HastNode) => isElement(node, 'br')

const units = (text: string) => Array.from(text).reduce((sum, char) => sum + ((char.codePointAt(0) ?? 0) > 0x2e7f ? 2 : 1), 0)

function hasDisallowedCaptionNode(node: HastNode): boolean {
  if (node.type === 'element' && DISALLOWED_IN_CAPTION.has(node.tagName ?? '')) return true
  return (node.children ?? []).some(hasDisallowedCaptionNode)
}

// Split inline caption nodes into logical lines across <br> hard breaks and soft newlines. Markdown hard breaks emit a <br> followed by
// a "\n" text node, so one leading newline right after <br> is part of that same break, not a second line break.
function captionLines(nodes: HastNode[]): string[] {
  const lines = ['']
  let afterBr = false
  const walk = (node: HastNode) => {
    if (isBr(node)) {
      lines.push('')
      afterBr = true
      return
    }
    if (node.type === 'text') {
      let value = node.value ?? ''
      if (afterBr) {
        value = value.replace(/^\r?\n/, '')
        afterBr = false
      }
      const parts = value.split(/\r?\n/)
      lines[lines.length - 1] += parts[0]!
      for (let i = 1; i < parts.length; i++) lines.push(parts[i]!)
      return
    }
    afterBr = false
    for (const child of node.children ?? []) walk(child)
  }
  for (const node of nodes) walk(node)
  return lines.map((line) => line.trim())
}

function validCaptionNodes(nodes: HastNode[]): boolean {
  if (nodes.some(hasDisallowedCaptionNode)) return false
  const lines = captionLines(nodes)
  if (lines.length === 0 || lines.length > MAX_CAPTION_LINES) return false
  const nonEmpty = lines.filter(Boolean)
  if (nonEmpty.length === 0) return false
  let total = 0
  for (const line of lines) {
    const width = units(line)
    if (width > MAX_CAPTION_LINE_UNITS) return false
    total += width
  }
  return total <= MAX_CAPTION_TOTAL_UNITS
}

// Images in a paragraph that holds only images (plus whitespace and line breaks); null otherwise.
function onlyImages(node: HastNode): HastNode[] | null {
  if (!isElement(node, 'p')) return null
  const children = node.children ?? []
  const images: HastNode[] = []
  for (const child of children) {
    if (isImg(child)) images.push(child)
    else if (!isBlank(child) && !isBr(child)) return null
  }
  return images.length > 0 ? images : null
}

// A standalone short single-line paragraph or sub-heading that can serve as a caption above an image-only paragraph.
function captionOfBlock(node: HastNode): HastNode[] | null {
  if (!isElement(node)) return null
  const tag = node.tagName ?? ''
  if (tag !== 'p' && !CAPTION_HEADINGS.has(tag)) return null
  const children = node.children ?? []
  if (!validCaptionNodes(children)) return null
  return CAPTION_HEADINGS.has(tag)
    ? [{ type: 'element', tagName: 'strong', properties: {}, children }]
    : children
}

// A paragraph whose lines alternate "short caption" + line break + image (e.g. "2D-1: ...\n![2D-1](url)"), with nothing else after the
// last image. Returns null when text appears on the same line as an image or when any caption is too long or multi-line.
function captionedItemsInParagraph(node: HastNode): RowItem[] | null {
  if (!isElement(node, 'p')) return null
  const children = node.children ?? []
  const segments: { before: HastNode[]; image: HastNode }[] = []
  let current: HastNode[] = []
  for (const child of children) {
    if (isImg(child)) {
      segments.push({ before: current, image: child })
      current = []
    } else {
      current.push(child)
    }
  }
  if (segments.length === 0 || current.some((child) => !isBlank(child))) return null

  const items: RowItem[] = []
  for (let idx = 0; idx < segments.length; idx++) {
    const seg = segments[idx]!
    const nodes = seg.before.map((child) => (child.type === 'text' ? { ...child } : child))

    // Subsequent items inside one paragraph must begin on a new line after the previous image.
    if (idx > 0) {
      let broke = false
      while (nodes.length > 0 && nodes[0]!.type === 'text' && /^[ \t]*$/.test(nodes[0]!.value ?? '')) nodes.shift()
      if (nodes[0] && isBr(nodes[0])) {
        nodes.shift()
        broke = true
        if (nodes[0]?.type === 'text' && nodes[0].value) {
          nodes[0].value = nodes[0].value.replace(/^[ \t]*\r?\n/, '')
          if (nodes[0].value === '') nodes.shift()
        }
      } else if (nodes[0]?.type === 'text' && /^[ \t]*\r?\n/.test(nodes[0].value ?? '')) {
        nodes[0].value = (nodes[0].value ?? '').replace(/^[ \t]*\r?\n/, '')
        if (nodes[0].value === '') nodes.shift()
        broke = true
      }
      if (!broke) return null
    }

    // The caption must end with a line break right before its image, never prose on the same line.
    let endedWithBreak = false
    while (nodes.length > 0 && nodes.at(-1)!.type === 'text' && /^[ \t]*$/.test(nodes.at(-1)!.value ?? '')) nodes.pop()
    const last = nodes.at(-1)
    if (last?.type === 'text' && /\r?\n[ \t]*$/.test(last.value ?? '')) {
      last.value = (last.value ?? '').replace(/\r?\n[ \t]*$/, '')
      if (last.value === '') nodes.pop()
      if (nodes.length > 0 && isBr(nodes.at(-1)!)) nodes.pop()
      endedWithBreak = true
    } else if (last && isBr(last)) {
      nodes.pop()
      endedWithBreak = true
    }
    if (!endedWithBreak || !validCaptionNodes(nodes)) return null

    items.push({ caption: nodes, image: seg.image })
  }
  return items
}

function nextNonBlank(children: HastNode[], from: number): number {
  for (let i = from; i < children.length; i++) {
    if (!isBlank(children[i]!)) return i
  }
  return -1
}

function readCaptionedUnit(children: HastNode[], index: number): { items: RowItem[]; nextIndex: number } | null {
  const node = children[index]!
  const inline = captionedItemsInParagraph(node)
  if (inline) return { items: inline, nextIndex: index + 1 }

  const caption = captionOfBlock(node)
  if (!caption) return null
  const next = nextNonBlank(children, index + 1)
  if (next < 0) return null
  const imgs = onlyImages(children[next]!)
  if (!imgs || imgs.length !== 1) return null
  return { items: [{ caption, image: imgs[0]! }], nextIndex: next + 1 }
}

function makeRow(items: RowItem[]): HastNode {
  return {
    type: 'element',
    tagName: 'div',
    properties: { className: ['md-image-row-wrap'] },
    children: [
      {
        type: 'element',
        tagName: 'div',
        properties: { className: ['md-image-row'], dataMdImageRow: '', dataCount: String(items.length) },
        children: items.map((item) =>
          item.caption
            ? {
                type: 'element',
                tagName: 'figure',
                properties: { className: ['md-image-item'] },
                children: [
                  { type: 'element', tagName: 'figcaption', properties: { className: ['md-image-caption'] }, children: item.caption },
                  item.image,
                ],
              }
            : {
                type: 'element',
                tagName: 'div',
                properties: { className: ['md-image-item'] },
                children: [item.image],
              },
        ),
      },
    ],
  }
}

// When a paragraph that stays outside an image row ends with an image right after a soft or hard line break, mark that image to start
// on its own line below the text instead of sitting inline to the right of the last text line, and drop the now-redundant break before it.
function markTrailingBlockImage(node: HastNode) {
  if (!isElement(node, 'p') || !node.children?.length) return
  const children = node.children
  let lastIdx = children.length - 1
  while (lastIdx >= 0 && isBlank(children[lastIdx]!)) lastIdx--
  if (lastIdx <= 0 || !isImg(children[lastIdx]!)) return

  const before = children.slice(0, lastIdx).map((child) => (child.type === 'text' ? { ...child } : child))
  let endedWithBreak = false
  while (before.length > 0 && before.at(-1)!.type === 'text' && /^[ \t]*$/.test(before.at(-1)!.value ?? '')) before.pop()
  const prev = before.at(-1)
  if (prev?.type === 'text' && /\r?\n[ \t]*$/.test(prev.value ?? '')) {
    prev.value = (prev.value ?? '').replace(/\r?\n[ \t]*$/, '')
    if (prev.value === '') before.pop()
    if (before.length > 0 && isBr(before.at(-1)!)) before.pop()
    endedWithBreak = true
  } else if (prev && isBr(prev)) {
    before.pop()
    endedWithBreak = true
  }
  if (!endedWithBreak || before.every(isBlank)) return

  const image = children[lastIdx]!
  ;(image.properties ??= {}).dataMdBlockImage = ''
  node.children = [...before, image, ...children.slice(lastIdx + 1)]
}

function groupChildren(parent: HastNode) {
  const children = parent.children
  if (!children?.length) return

  // Recurse into nested block containers first.
  for (const child of children) {
    if (isElement(child) && CONTAINERS.has(child.tagName ?? '')) groupChildren(child)
  }

  const out: HastNode[] = []
  let i = 0
  while (i < children.length) {
    const node = children[i]!
    if (isBlank(node)) {
      out.push(node)
      i++
      continue
    }

    // First look for 2+ consecutive captioned images (either inline "caption\n![img]" or caption block + single-image paragraph).
    // Requiring at least 2 captioned units keeps a lone intro paragraph before plain images (or a single image between two text
    // paragraphs) untouched.
    const captioned: RowItem[] = []
    let cursor = i
    let unit = readCaptionedUnit(children, cursor)
    while (unit) {
      captioned.push(...unit.items)
      cursor = unit.nextIndex
      const next = nextNonBlank(children, cursor)
      unit = next >= 0 ? readCaptionedUnit(children, next) : null
    }
    if (captioned.length >= 2) {
      out.push(makeRow(captioned))
      i = cursor
      continue
    }

    // Otherwise look for consecutive images (multiple images in one paragraph, or consecutive image-only paragraphs).
    const firstImages = onlyImages(node)
    if (firstImages) {
      const plain: RowItem[] = firstImages.map((image) => ({ image }))
      let plainCursor = i + 1
      for (;;) {
        const next = nextNonBlank(children, plainCursor)
        if (next < 0) break
        const more = onlyImages(children[next]!)
        if (!more) break
        plain.push(...more.map((image) => ({ image })))
        plainCursor = next + 1
      }
      if (plain.length >= 2) {
        out.push(makeRow(plain))
        i = plainCursor
        continue
      }
    }

    markTrailingBlockImage(node)
    out.push(node)
    i++
  }

  parent.children = out
}

// Group consecutive Markdown images (and short caption-line + image pairs) into a wrapping row (styles in .md-image-row in index.css).
export function rehypeImageRows() {
  return (tree: HastNode) => groupChildren(tree)
}
