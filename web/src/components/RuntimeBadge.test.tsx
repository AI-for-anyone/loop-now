import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { runtimeDefinitions } from '@/test/runtimes'
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
