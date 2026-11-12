type HastNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HastNode[] }

// 列表项里出现这些块级内容（分段、子列表、代码块等）时，这一项不改成“标签 + 文字”的横排。
const BLOCKS = new Set(['p', 'ul', 'ol', 'pre', 'blockquote', 'table', 'div', 'hr', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6'])
// 标签最长这么宽（中文等宽字符算 2，其余算 1）：再长就是一句加粗的话，不是一个词。
const MAX_UNITS = 24

const textOf = (node: HastNode): string => (node.type === 'text' ? (node.value ?? '') : (node.children ?? []).map(textOf).join(''))
const units = (text: string) => Array.from(text).reduce((sum, char) => sum + ((char.codePointAt(0) ?? 0) > 0x2e7f ? 2 : 1), 0)
const blank = (node: HastNode) => node.type === 'text' && !node.value?.trim()

function addClass(node: HastNode, name: string) {
  const properties = (node.properties ??= {})
  const current = properties.className
  properties.className = [...(Array.isArray(current) ? current : current ? [current] : []), name]
}

// 这段内容开头（忽略空白）是不是一个加粗词；是的话返回它的位置，不是返回 -1。
function leadIndex(children: HastNode[]): number {
  const index = children.findIndex((child) => !blank(child))
  const first = children[index]
  if (first?.type !== 'element' || first.tagName !== 'strong') return -1
  const words = textOf(first).trim()
  return words && units(words) <= MAX_UNITS ? index : -1
}

// 标签后面紧跟的冒号不再显示：标签本身已经把“词”和“说明”分开了。
function dropColon(children: HastNode[], index: number) {
  const next = children[index + 1]
  if (next?.type === 'text' && next.value) next.value = next.value.replace(/^[:：]\s*/u, '')
}

function mark(item: HastNode, list: string | undefined) {
  const children = item.children ?? []
  const first = children.find((child) => !blank(child))
  // 分段的列表项：加粗词在第一段的开头。
  if (first?.type === 'element' && first.tagName === 'p') {
    const index = leadIndex(first.children ?? [])
    if (index < 0) return
    addClass(first.children![index], 'md-lead')
    dropColon(first.children!, index)
    return
  }
  const index = leadIndex(children)
  if (index < 0) return
  addClass(children[index], 'md-lead')
  dropColon(children, index)
  // 无序列表里只有行内内容的一项改成横排：标签在左，其余文字包成一块排在右边。有序列表要保留序号，不改。
  if (list !== 'ul' || children.some((child) => child.type === 'element' && BLOCKS.has(child.tagName ?? ''))) return
  addClass(item, 'md-lead-item')
  const rest = children.slice(index + 1)
  item.children = rest.some((child) => !blank(child))
    ? [children[index], { type: 'element', tagName: 'span', properties: { className: ['md-lead-text'] }, children: rest }]
    : [children[index]]
}

// 回复里列表项开头的加粗词做成黄色小标签（样式见 index.css 的 .md-lead）。
export function rehypeLeadLabel() {
  const visit = (node: HastNode, parent?: HastNode) => {
    // 先处理里面的子列表，再处理这一项。
    node.children?.forEach((child) => visit(child, node))
    if (node.type === 'element' && node.tagName === 'li') mark(node, parent?.tagName)
  }
  return (tree: HastNode) => visit(tree)
}
