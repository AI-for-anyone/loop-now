import { beforeEach, expect, it, vi } from 'vitest'
import mermaid from 'mermaid'
import { mermaidConfig, mermaidConfigFor, renderMermaid, resetMermaidForTest } from './mermaid-renderer'

vi.mock('mermaid', () => ({ default: { initialize: vi.fn(), render: vi.fn(async () => ({ svg: '<svg/>', bindFunctions: vi.fn() })) } }))

beforeEach(() => {
  vi.mocked(mermaid.initialize).mockClear()
  vi.mocked(mermaid.render).mockClear()
  resetMermaidForTest()
})

it('initializes only once per theme and locks the security/limit settings; each diagram gets its own ID and no callbacks are bound', async () => {
  expect(mermaid.initialize).not.toHaveBeenCalled()
  await renderMermaid('flowchart LR\n A-->B', { skin: 'mono', resolvedTheme: 'light' })
  await renderMermaid('sequenceDiagram\n A->>B: Hi', { skin: 'mono', resolvedTheme: 'light' })
  expect(mermaid.initialize).toHaveBeenCalledExactlyOnceWith(mermaidConfigFor('mono', 'light'))
  expect(mermaidConfig).toMatchObject({ startOnLoad: false, securityLevel: 'strict', htmlLabels: false, suppressErrorRendering: true, maxTextSize: 50_000, maxEdges: 500 })
  for (const key of ['securityLevel', 'htmlLabels', 'maxTextSize', 'maxEdges', 'suppressErrorRendering', 'secure']) expect(mermaidConfig.secure).toContain(key)
  const renders = vi.mocked(mermaid.render)
  expect(renders.mock.calls[0][0]).not.toBe(renders.mock.calls[1][0])
  for (const result of renders.mock.results) expect((await result.value).bindFunctions).not.toHaveBeenCalled()
})

it('re-initializes with dark or plate theme variables when appearance changes', async () => {
  await renderMermaid('flowchart LR\n A-->B', { skin: 'mono', resolvedTheme: 'light' })
  await renderMermaid('flowchart LR\n A-->B', { skin: 'mono', resolvedTheme: 'dark' })
  await renderMermaid('flowchart LR\n A-->B', { skin: 'plate', resolvedTheme: 'light' })

  const init = vi.mocked(mermaid.initialize)
  expect(init).toHaveBeenCalledTimes(3)
  expect(init.mock.calls[0][0]).toMatchObject({ theme: 'neutral', securityLevel: 'strict' })
  expect(init.mock.calls[1][0]).toMatchObject({ theme: 'dark', securityLevel: 'strict', themeVariables: { darkMode: true } })
  expect(init.mock.calls[2][0]).toMatchObject({ theme: 'default', securityLevel: 'strict' })
})

it('rejects an overlong source before loading the parser', async () => {
  const before = vi.mocked(mermaid.render).mock.calls.length
  await expect(renderMermaid('x'.repeat(50_001))).rejects.toThrow('maxTextSize')
  expect(mermaid.render).toHaveBeenCalledTimes(before)
})
