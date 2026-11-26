import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { runtimeDefinitions, withCodex } from '@/test/runtimes'
import { server } from '@/test/server'
import { RuntimeBadge, RuntimeMark } from './RuntimeBadge'

// A runtime the contract does not have yet: installed on this computer, like Claude Code.
const codex = { ...runtimeDefinitions().runtimes[1], id: 'codex', name: 'Codex', providers: ['codex'] } as unknown as Schemas['RuntimeDefinition']
const codexId = 'codex' as Schemas['AgentRuntime']

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

describe('runtime logos in the badge and the sidebar mark', () => {
  it('each runtime installed on this computer shows its own logo, so a Claude Code Agent and a Codex Agent tell apart; one without a logo shows its initial', async () => {
    const acme = { ...runtimeDefinitions().runtimes[1], id: 'acme', name: 'Acme CLI', providers: ['acme'] } as unknown as Schemas['RuntimeDefinition']
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json({ runtimes: [...withCodex().runtimes, acme] })))
    const logo = (element: Element) => element.querySelector('img')?.getAttribute('data-logo')
    renderWithQuery(<><RuntimeMark runtime="claude-code" /><RuntimeMark runtime="codex" /><RuntimeMark runtime={'acme' as Schemas['AgentRuntime']} /><RuntimeBadge runtime="codex" /></>)
    await screen.findAllByTitle('Runtime: Codex on this computer')
    expect(logo(screen.getByTitle('Runtime: Claude Code on this computer'))).toBe('claude')
    for (const mark of screen.getAllByTitle('Runtime: Codex on this computer')) expect(logo(mark)).toBe('openai')
    expect(screen.getByTitle('Runtime: Acme CLI on this computer').querySelector('text')).toHaveTextContent('A')
  })
  it('the sidebar mark sits on a light tile at full strength, so a black logo reads on the blue sidebar', async () => {
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(withCodex())))
    renderWithQuery(<RuntimeMark runtime="codex" />)
    const mark = await screen.findByTitle('Runtime: Codex on this computer')
    expect(mark).toHaveClass('bg-paper-raised')
    expect(mark).not.toHaveClass('opacity-75')
  })
})
