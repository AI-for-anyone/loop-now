import { waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody } from '@/test/api'
import { renderHookWithQuery } from '@/test/render'
import { runtimeDefinitions } from '@/test/runtimes'
import { server } from '@/test/server'
import { runtimeLookup, useRuntimeLookup } from './runtime'

const definitions = runtimeDefinitions().runtimes
// A runtime the contract does not have yet, to show nothing here depends on the two known ones.
const codex = { ...definitions[1], id: 'codex', name: 'Codex', providers: ['openai-codex-cli'] } as unknown as Schemas['RuntimeDefinition']

describe('runtime lookup', () => {
  it('finds a definition by id; nothing for an id the server does not define, or before the definitions are read', () => {
    const lookup = runtimeLookup(definitions)
    expect(lookup.get('claude-code')).toBe(definitions[1])
    expect(lookup.get(null)).toBeUndefined()
    expect(runtimeLookup([definitions[0]]).get('claude-code')).toBeUndefined()
    expect(runtimeLookup(undefined).get('pi')).toBeUndefined()
  })

  it('a model provider runs on the runtime that lists it, any other provider on the default runtime', () => {
    const lookup = runtimeLookup([...definitions, codex])
    expect(lookup.forProvider('claude-code')?.name).toBe('Claude Code')
    expect(lookup.forProvider('openai-codex-cli')?.name).toBe('Codex')
    expect(lookup.forProvider('anthropic')?.name).toBe('Pi')
    expect(runtimeLookup(undefined).forProvider('anthropic')).toBeUndefined()
  })

  it('names come from the definitions; the id stands in until they are read', () => {
    expect(runtimeLookup(definitions).name('claude-code')).toBe('Claude Code')
    expect(runtimeLookup(undefined).name('claude-code')).toBe('claude-code')
  })

  it('reads GET /runtime-definitions, and reports why when it cannot', async () => {
    const { result } = renderHookWithQuery(() => useRuntimeLookup())
    await waitFor(() => expect(result.current.get('pi')?.capabilities.bundled).toBe(true))
    expect(result.current.error).toBeNull()

    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 })))
    const failed = renderHookWithQuery(() => useRuntimeLookup()).result
    await waitFor(() => expect(failed.current.error).not.toBeNull())
    expect(failed.current.get('pi')).toBeUndefined()
    expect(failed.current.name('pi')).toBe('pi')
  })
})
