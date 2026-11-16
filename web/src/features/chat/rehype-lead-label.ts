type HastNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HastNode[] }

// When a list item contains any of these block-level elements (paragraphs, sublists, code blocks, etc.), the item can't be laid out
// as a single "label + text" line.
const BLOCKS = new Set(['p', 'ul', 'ol', 'pre', 'blockquote', 'table', 'div', 'hr', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6'])
// The maximum width of a label (a full-width character such as Chinese counts as 2, everything else as 1): anything longer is a bold
// sentence, not a word.
const MAX_UNITS = 24

const textOf = (node: HastNode): string => (node.type === 'text' ? (node.value ?? '') : (node.children ?? []).map(textOf).join(''))
const units = (text: string) => Array.from(text).reduce((sum, char) => sum + ((char.codePointAt(0) ?? 0) > 0x2e7f ? 2 : 1), 0)
const blank = (node: HastNode) => node.type === 'text' && !node.value?.trim()

function addClass(node: HastNode, name: string) {
  const properties = (node.properties ??= {})
  const current = properties.className
  properties.className = [...(Array.isArray(current) ? current : current ? [current] : []), name]
}

// Whether this content starts (ignoring whitespace) with a bold word; if so return its position, otherwise -1.
function leadIndex(children: HastNode[]): number {
  const index = children.findIndex((child) => !blank(child))
  const first = children[index]
  if (first?.type !== 'element' || first.tagName !== 'strong') return -1
  const words = textOf(first).trim()
  return words && units(words) <= MAX_UNITS ? index : -1
}

interface Lead {
  // The content holding the bold word (the list item itself, or the first paragraph of a list item with paragraphs) and the word's
  // position in it.
  children: HastNode[]
  index: number
  // This item has only inline content and can be laid out as a single "label + text" line.
  inline: boolean
}

function leadOf(item: HastNode): Lead | null {
  const children = item.children ?? []
  const first = children.find((child) => !blank(child))
  // A list item with paragraphs: the bold word is at the start of the first paragraph.
  if (first?.type === 'element' && first.tagName === 'p') {
    const index = leadIndex(first.children ?? [])
    return index < 0 ? null : { children: first.children!, index, inline: false }
  }
  const index = leadIndex(children)
  return index < 0 ? null : { children, index, inline: !children.some((child) => child.type === 'element' && BLOCKS.has(child.tagName ?? '')) }
}

// A colon used as a separator after the label is no longer shown (the label itself already separates the "word" from the
// "explanation"): a full-width colon (U+FF1A), or an ASCII colon followed by whitespace or the end of the line.
// Colons in ports (localhost:3000), times (12:30), drive letters (C:\) and scopes (std::vector) are content and are kept.
function dropColon({ children, index }: Lead) {
  const next = children[index + 1]
  if (next?.type === 'text' && next.value) next.value = next.value.replace(/^(?:：|:(?=\s|$))\s*/u, '')
}

function markList(list: HastNode) {
  const items = (list.children ?? []).filter((child) => child.type === 'element' && child.tagName === 'li')
  const leads = items.map(leadOf)
  for (const lead of leads) {
    if (!lead) continue
    addClass(lead.children[lead.index], 'md-lead')
    dropColon(lead)
  }
  // The side-by-side layout is decided for the whole list: only when every item of an unordered list is "bold word + inline text" is
  // it changed, with the label on the left, the rest of the text wrapped into one block on the right, and no bullets.
  // If even one item isn't (a plain item, or one with a sublist or paragraphs), the whole list keeps its bullets and indentation so all
  // items are laid out the same way. Ordered lists must keep their numbers and are left unchanged.
  if (list.tagName !== 'ul' || items.length === 0 || !leads.every((lead) => lead?.inline)) return
  addClass(list, 'md-lead-list')
  items.forEach((item, at) => {
    const { children, index } = leads[at]!
    addClass(item, 'md-lead-item')
    const rest = children.slice(index + 1)
    item.children = rest.some((child) => !blank(child))
      ? [children[index], { type: 'element', tagName: 'span', properties: { className: ['md-lead-text'] }, children: rest }]
      : [children[index]]
  })
}

// Turn the bold word at the start of list items in replies into a small yellow label (styles in .md-lead in index.css).
export function rehypeLeadLabel() {
  const visit = (node: HastNode) => {
    // Handle the sublists inside first, then this list.
    node.children?.forEach(visit)
    if (node.type === 'element' && (node.tagName === 'ul' || node.tagName === 'ol')) markList(node)
  }
  return (tree: HastNode) => visit(tree)
}
