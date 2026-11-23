import { expect, it, vi } from 'vitest'
import mermaid from 'mermaid'
import { mermaidConfig, renderMermaid } from './mermaid-renderer'
vi.mock('mermaid', () => ({ default: { initialize: vi.fn(), render: vi.fn(async () => ({ svg: '<svg/>', bindFunctions: vi.fn() })) } }))
it('initializes only once and locks the security/limit settings; each diagram gets its own ID and no callbacks are bound', async () => {
  expect(mermaid.initialize).not.toHaveBeenCalled()
  await renderMermaid('flowchart LR\n A-->B'); await renderMermaid('sequenceDiagram\n A->>B: Hi')
  expect(mermaid.initialize).toHaveBeenCalledExactlyOnceWith(mermaidConfig)
  expect(mermaidConfig).toMatchObject({ startOnLoad: false, securityLevel: 'strict', htmlLabels: false, suppressErrorRendering: true, maxTextSize: 50_000, maxEdges: 500 })
  for (const key of ['securityLevel', 'htmlLabels', 'maxTextSize', 'maxEdges', 'suppressErrorRendering', 'secure']) expect(mermaidConfig.secure).toContain(key)
  const renders = vi.mocked(mermaid.render)
  expect(renders.mock.calls[0][0]).not.toBe(renders.mock.calls[1][0])
  for (const result of renders.mock.results) expect((await result.value).bindFunctions).not.toHaveBeenCalled()
})
it('rejects an overlong source before loading the parser', async () => {
  const before = vi.mocked(mermaid.render).mock.calls.length
  await expect(renderMermaid('x'.repeat(50_001))).rejects.toThrow('maxTextSize')
  expect(mermaid.render).toHaveBeenCalledTimes(before)
})
