import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { runtimeBrandIcon, runtimeGlyph } from '@/lib/runtime'
import { runtimeDefinitions, withCodex } from '@/test/runtimes'
import { server } from '@/test/server'
import { resetThemeForTest, setSkin } from '@/theme/store'
import { RuntimeBadge, RuntimeMark } from './RuntimeBadge'

// A runtime the contract does not have yet: installed on this computer, like Claude Code.
const codex = { ...runtimeDefinitions().runtimes[1], id: 'codex', name: 'Codex', providers: ['codex'] } as unknown as Schemas['RuntimeDefinition']
const codexId = 'codex' as Schemas['AgentRuntime']

afterEach(() => {
  window.localStorage.clear()
  resetThemeForTest()
})

describe('runtime badge and sidebar mark', () => {
  it('mark a runtime installed on this computer with its name, not a bundled one, and nothing before the definitions are read', async () => {
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json({ runtimes: [...runtimeDefinitions().runtimes, codex] })))
    const { container } = renderWithQuery(<><RuntimeBadge runtime={codexId} /><RuntimeMark runtime={codexId} /><RuntimeBadge runtime="pi" /><RuntimeMark runtime="pi" /></>)
    expect(container).toBeEmptyDOMElement()
    expect(await screen.findByText('Codex')).toHaveAttribute('title', 'Runtime: Codex on this computer')
    expect(screen.getAllByTitle('Runtime: Codex on this computer')).toHaveLength(2)
    expect(container).toHaveTextContent(/^CodexRuntime: Codex on this computer$/)
  })
})

describe('runtime glyphs in the badge and the sidebar mark', () => {
  it('each runtime installed on this computer has its own brand icon in mono skin and its own stroke glyph in Plate skin', async () => {
    const acme = { ...runtimeDefinitions().runtimes[1], id: 'acme', name: 'Acme CLI', providers: ['acme'] } as unknown as Schemas['RuntimeDefinition']
    const claudeDef = withCodex().runtimes.find((r) => r.id === 'claude-code')!
    const codexDef = withCodex().runtimes.find((r) => r.id === 'codex')!
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json({ runtimes: [...withCodex().runtimes, acme] })))

    const { unmount } = renderWithQuery(<><RuntimeMark runtime="claude-code" /><RuntimeMark runtime="codex" /><RuntimeMark runtime={'acme' as Schemas['AgentRuntime']} /><RuntimeBadge runtime="codex" /></>)
    await screen.findAllByTitle('Runtime: Codex on this computer')
    expect(screen.getByTitle('Runtime: Claude Code on this computer').querySelector('[data-brand-icon]')).toHaveAttribute('data-brand-icon', runtimeBrandIcon(claudeDef))
    for (const mark of screen.getAllByTitle('Runtime: Codex on this computer')) {
      expect(mark.querySelector('[data-brand-icon]')).toHaveAttribute('data-brand-icon', runtimeBrandIcon(codexDef))
    }
    expect(screen.getByTitle('Runtime: Acme CLI on this computer').querySelector('[data-brand-icon]')).toHaveAttribute('data-brand-icon', runtimeBrandIcon(acme))
    unmount()

    setSkin('plate')
    const strokes = (title: string) => [...screen.getByTitle(title).querySelectorAll('path')].map((path) => path.getAttribute('d'))
    renderWithQuery(<><RuntimeMark runtime="claude-code" /><RuntimeMark runtime="codex" /><RuntimeMark runtime={'acme' as Schemas['AgentRuntime']} /><RuntimeBadge runtime="codex" /></>)
    await screen.findAllByTitle('Runtime: Codex on this computer')
    expect(strokes('Runtime: Claude Code on this computer')).toEqual(runtimeGlyph('claude-code'))
    const codexMarks = screen.getAllByTitle('Runtime: Codex on this computer')
    for (const mark of codexMarks) expect([...mark.querySelectorAll('path')].map((path) => path.getAttribute('d'))).toEqual(runtimeGlyph('codex'))
    expect(screen.getByTitle('Runtime: Acme CLI on this computer').querySelector('text')).toHaveTextContent('A')
  })
})
