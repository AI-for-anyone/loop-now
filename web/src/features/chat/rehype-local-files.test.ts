import { describe, expect, it } from 'vitest'
import { unified } from 'unified'
import remarkParse from 'remark-parse'
import remarkGfm from 'remark-gfm'
import remarkRehype from 'remark-rehype'
import rehypeHighlight from 'rehype-highlight'
import type { ReplyFolderContext } from './local-images'
import { rehypeLocalFiles } from './rehype-local-files'
import { remarkHttpLinks } from './remark-http-links'

type HastNode = { type: string; tagName?: string; value?: string; properties?: Record<string, unknown>; children?: HastNode[] }

const ROOT = '/var/work/agents/a1/workspace'

const folder: ReplyFolderContext = {
  roots: [ROOT],
  rootsReady: true,
  contentURL: (rel) => `/api/v1/agents/a1/folder/content?path=${encodeURIComponent(rel)}`,
  rawURL: (rel) => `/api/v1/agents/a1/folder/raw/${rel}`,
  folderLink: (rel) => `/p/demo/agents/a1/folder?path=${encodeURIComponent(rel)}`,
  maxBytes: 20 << 20,
}

function runRehype(markdown: string, ctx: ReplyFolderContext = folder): HastNode {
  const processor = unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkHttpLinks)
    .use(remarkRehype)
    .use(rehypeHighlight, { detect: false, ignore: ['mermaid'] })
    .use(rehypeLocalFiles, ctx)
  const parsed = processor.parse(markdown)
  return processor.runSync(parsed) as unknown as HastNode
}

function parseCards(node: HastNode): Array<{ relativePath: string; certain: boolean }> {
  const raw = node.properties?.dataMdFileCards
  return typeof raw === 'string' ? (JSON.parse(raw) as Array<{ relativePath: string; certain: boolean }>) : []
}

function elementChildren(node: HastNode): HastNode[] {
  return (node.children ?? []).filter((child) => child.type === 'element')
}

describe('rehypeLocalFiles file card placement, dedupe, and fenced-path replacement', () => {
  it('places file cards right after paragraphs that name files via link, inline code, or bare path', () => {
    const tree = runRehype([
      `See [Pelican](${ROOT}/pelican.html), \`README.md\`, and ./sales.csv.`,
      '',
      'Follow-up text.',
    ].join('\n'))
    const blocks = elementChildren(tree)
    expect(blocks.map((b) => b.tagName)).toEqual(['p', 'div', 'p'])
    expect(parseCards(blocks[1]!)).toEqual([
      { relativePath: 'pelican.html', certain: true },
      { relativePath: 'README.md', certain: false },
      { relativePath: 'sales.csv', certain: true },
    ])
  })

  it('deduplicates repeated file references across the reply and promotes certainty when any mention is certain', () => {
    const tree = runRehype([
      'First mention: `pelican-bike.html`.',
      '',
      `Second mention: [Open](${ROOT}/pelican-bike.html) and \`report.pdf\`.`,
      '',
      'Third mention: `report.pdf`.',
    ].join('\n'))
    const blocks = elementChildren(tree)
    expect(blocks.map((b) => b.tagName)).toEqual(['p', 'div', 'p', 'div', 'p'])
    // First mention wins placement and inherits certain:true from the later explicit link.
    expect(parseCards(blocks[1]!)).toEqual([{ relativePath: 'pelican-bike.html', certain: true }])
    expect(parseCards(blocks[3]!)).toEqual([{ relativePath: 'report.pdf', certain: false }])
  })

  it('marks path-only fenced code blocks for card replacement and marks duplicate fenced blocks for suppression', () => {
    const standalone = runRehype([
      'Done:',
      '',
      '```text',
      `${ROOT}/pelican-night-ride.html`,
      '```',
    ].join('\n'))
    const standaloneBlocks = elementChildren(standalone)
    expect(standaloneBlocks.map((b) => b.tagName)).toEqual(['p', 'pre'])
    expect(standaloneBlocks[1]!.properties).toMatchObject({
      dataMdFencedFile: 'pelican-night-ride.html',
      dataMdFencedCertain: '1',
    })
    expect(standaloneBlocks[1]!.properties?.dataMdFencedDuplicate).toBeUndefined()

    // When a link already claimed the file earlier in the reply, the fenced path block is marked duplicate.
    const withEarlierLink = runRehype([
      `[Open](${ROOT}/pelican-night-ride.html)`,
      '',
      '```text',
      `${ROOT}/pelican-night-ride.html`,
      '```',
    ].join('\n'))
    const linkedBlocks = elementChildren(withEarlierLink)
    expect(linkedBlocks.map((b) => b.tagName)).toEqual(['p', 'div', 'pre'])
    expect(parseCards(linkedBlocks[1]!)).toEqual([{ relativePath: 'pelican-night-ride.html', certain: true }])
    expect(linkedBlocks[2]!.properties).toMatchObject({
      dataMdFencedFile: 'pelican-night-ride.html',
      dataMdFencedCertain: '1',
      dataMdFencedDuplicate: '1',
    })
  })

  it('matches image placement rules for lists (multi-item after list, single-item inside item) and tables', () => {
    const multiList = runRehype([
      `- Report: [PDF](${ROOT}/report.pdf)`,
      `- Table: \`${ROOT}/sales.csv\``,
    ].join('\n'))
    const multiBlocks = elementChildren(multiList)
    expect(multiBlocks.map((b) => b.tagName)).toEqual(['ul', 'div'])
    expect(parseCards(multiBlocks[1]!)).toEqual([
      { relativePath: 'report.pdf', certain: true },
      { relativePath: 'sales.csv', certain: true },
    ])

    const singleList = runRehype([
      `- First: [PDF](${ROOT}/report.pdf)`,
      '- Second without files',
    ].join('\n'))
    const singleBlocks = elementChildren(singleList)
    expect(singleBlocks.map((b) => b.tagName)).toEqual(['ul'])
    const items = elementChildren(singleBlocks[0]!)
    const firstChildren = elementChildren(items[0]!)
    expect(firstChildren.at(-1)?.tagName).toBe('div')
    expect(firstChildren.at(-1)?.properties?.dataMdFileCardsInline).toBe('')
    expect(parseCards(firstChildren.at(-1)!)).toEqual([{ relativePath: 'report.pdf', certain: true }])
    expect(elementChildren(items[1]!)).toHaveLength(0)

    const tableTree = runRehype([
      '| File | Note |',
      '| --- | --- |',
      `| [Zip](${ROOT}/build.zip) | Release |`,
    ].join('\n'))
    const tableBlocks = elementChildren(tableTree)
    expect(tableBlocks.map((b) => b.tagName)).toEqual(['table', 'div'])
    expect(parseCards(tableBlocks[1]!)).toEqual([{ relativePath: 'build.zip', certain: true }])
  })

  it('excludes reply images, directories, outside paths, and HTTP links from file cards', () => {
    const tree = runRehype([
      `![Shot](assets/shot.png) and [Banner](${ROOT}/assets/banner.svg) and \`${ROOT}/assets/photo.jpg\``,
      '',
      `Dirs: [Assets](assets/) and \`${ROOT}/\` and [Root](.)`,
      '',
      'Outside and HTTP: [Secret](/etc/passwd), `../escape.html`, [Web](https://example.com/a.pdf), https://example.com/b.html',
      '',
      '```text',
      '/etc/passwd',
      '```',
    ].join('\n'))
    const allNodes: HastNode[] = []
    const walk = (n: HastNode) => {
      allNodes.push(n)
      for (const child of n.children ?? []) walk(child)
    }
    walk(tree)
    expect(allNodes.filter((n) => n.properties?.dataMdFileCards !== undefined)).toHaveLength(0)
    expect(allNodes.filter((n) => n.properties?.dataMdFencedFile !== undefined)).toHaveLength(0)
  })
})
