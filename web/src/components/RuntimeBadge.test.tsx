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

describe('runtime marks in the badge and the sidebar row', () => {
  it('each runtime installed on this computer shows the ink frame with its own shape; one without a shape shows its initial', async () => {
    const acme = { ...runtimeDefinitions().runtimes[1], id: 'acme', name: 'Acme CLI', providers: ['acme'] } as unknown as Schemas['RuntimeDefinition']
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json({ runtimes: [...withCodex().runtimes, acme] })))
    const svgOf = (element: Element) => element.querySelector('svg')
    renderWithQuery(<><RuntimeMark runtime="claude-code" /><RuntimeMark runtime="codex" /><RuntimeMark runtime={'acme' as Schemas['AgentRuntime']} /><RuntimeBadge runtime="codex" /></>)
    await screen.findAllByTitle('Runtime: Codex on this computer')
    const claude = svgOf(screen.getByTitle('Runtime: Claude Code on this computer'))!
    expect(claude).toHaveAttribute('data-logo', 'claude')
    expect(claude.querySelector(':scope > rect')).toHaveAttribute('fill', 'var(--ink)')
    for (const mark of screen.getAllByTitle('Runtime: Codex on this computer')) {
      const svg = svgOf(mark)!
      expect(svg).toHaveAttribute('data-logo', 'openai')
      expect(svg.querySelector(':scope > rect')).toHaveAttribute('fill', 'var(--ink)')
    }
    const added = svgOf(screen.getByTitle('Runtime: Acme CLI on this computer'))!
    expect(added.querySelector(':scope > rect')).toHaveAttribute('fill', 'var(--ink)')
    expect(added.querySelector('text')).toHaveTextContent('A')
  })

  it('the sidebar mark uses the 16 px ink frame directly, with no light square wrapper behind it', async () => {
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(withCodex())))
    renderWithQuery(<RuntimeMark runtime="codex" />)
    const mark = await screen.findByTitle('Runtime: Codex on this computer')
    expect(mark).not.toHaveClass('bg-paper-raised')
    expect(mark).not.toHaveClass('opacity-75')
    expect(mark.querySelector('svg')).toHaveClass('size-4')
    expect(mark.querySelector('svg > rect')).toHaveAttribute('fill', 'var(--ink)')
  })
})
