type HastNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HastNode[] }

// 列表项里出现这些块级内容（分段、子列表、代码块等）时，这一项不能排成“标签 + 文字”的一行。
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

interface Lead {
  // 加粗词所在的那一段内容（列表项本身，或分段列表项的第一段）与它在其中的位置。
  children: HastNode[]
  index: number
  // 这一项只有行内内容，可以排成“标签 + 文字”的一行。
  inline: boolean
}

function leadOf(item: HastNode): Lead | null {
  const children = item.children ?? []
  const first = children.find((child) => !blank(child))
  // 分段的列表项：加粗词在第一段的开头。
  if (first?.type === 'element' && first.tagName === 'p') {
    const index = leadIndex(first.children ?? [])
    return index < 0 ? null : { children: first.children!, index, inline: false }
  }
  const index = leadIndex(children)
  return index < 0 ? null : { children, index, inline: !children.some((child) => child.type === 'element' && BLOCKS.has(child.tagName ?? '')) }
}

// 标签后面当分隔用的冒号不再显示（标签本身已经把“词”和“说明”分开了）：全角冒号，或后面是空白、行尾的半角冒号。
// 端口（localhost:3000）、时间（12:30）、盘符（C:\）、作用域（std::vector）里的冒号是内容，保留。
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
  // 横排按整张列表决定：无序列表里每一项都是“加粗词 + 行内文字”时才改，标签在左、其余文字包成一块排在右边、不画圆点。
  // 只要有一项不是（普通的一项、带子列表或分段的一项），整张列表保留圆点和缩进，各项排法一致。有序列表要保留序号，不改。
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

// 回复里列表项开头的加粗词做成黄色小标签（样式见 index.css 的 .md-lead）。
export function rehypeLeadLabel() {
  const visit = (node: HastNode) => {
    // 先处理里面的子列表，再处理这一张。
    node.children?.forEach(visit)
    if (node.type === 'element' && (node.tagName === 'ul' || node.tagName === 'ol')) markList(node)
  }
  return (tree: HastNode) => visit(tree)
}
