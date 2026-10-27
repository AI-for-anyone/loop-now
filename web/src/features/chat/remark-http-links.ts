import type { Parent, Root } from 'mdast'
import type { Processor } from 'unified'
import type {} from 'remark-parse'
import type { Construct } from 'micromark-util-types'

// GFM's protocol tokenizer can consume closing emphasis and Chinese prose.
// Immediately after a Markdown delimiter, let CommonMark parse emphasis first;
// GFM's existing tree transform then creates bare links without positions.
// Elsewhere keep the original tokenizer (including URL query punctuation).
export function remarkHttpLinks(this: Processor) {
  const data = this.data()
  const wrap = (construct: Construct): Construct => construct.name !== 'protocolAutolink' ? construct : {
    ...construct,
    tokenize(effects, ok, nok) {
      if (this.previous === 42 || this.previous === 95 || this.previous === 126) return nok
      return construct.tokenize.call(this, effects, ok, nok)
    },
  }
  for (const extension of data.micromarkExtensions ?? []) {
    for (const [code, constructs] of Object.entries(extension.text ?? {})) {
      if (constructs) extension.text![Number(code)] = Array.isArray(constructs) ? constructs.map(wrap) : wrap(constructs)
    }
  }

  return (tree: Root) => {
    const visit = (parent: Parent) => {
      for (let i = 0; i < parent.children.length; i++) {
        const node = parent.children[i]
        if (node.type === 'link') {
          if (!/^https?:\/\//i.test(node.url)) continue
          const text = node.children[0]
          if (node.children.length !== 1 || text?.type !== 'text' || text.value !== node.url) continue
          // Native bare links start at their label. [label](url) and <url>
          // start before it: never trim those explicit destinations.
          if (node.position && node.position.start.offset !== text.position?.start.offset) continue
          const end = node.url.search(/[，。；：！？、（）【】《》“”‘’]/u)
          if (end < 0) continue
          const suffix = node.url.slice(end)
          node.url = node.url.slice(0, end)
          text.value = node.url
          parent.children.splice(++i, 0, { type: 'text', value: suffix })
        } else if ('children' in node && node.type !== 'linkReference') {
          visit(node)
        }
      }
    }
    visit(tree)
  }
}
