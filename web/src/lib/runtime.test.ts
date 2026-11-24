import { waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody } from '@/test/api'
import { renderHookWithQuery } from '@/test/render'
import { codexDefinition, runtimeDefinitions } from '@/test/runtimes'
import { server } from '@/test/server'
import { canRun, railOrder, runtimeGlyph, runtimeLookup, runtimeProblemText, runtimeReadiness, useRuntimeLookup, type RuntimeReadiness } from './runtime'

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

  it('lists every definition in the server order', () => {
    expect(runtimeLookup([...definitions, codex]).all.map((definition) => definition.name)).toEqual(['Pi', 'Claude Code', 'Codex'])
    expect(runtimeLookup(undefined).all).toEqual([])
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

describe('runtime marks', () => {
  it('a runtime with a glyph of its own has its strokes; any other has none and is marked with its initial', () => {
    expect(runtimeGlyph('pi')?.length).toBeGreaterThan(0)
    expect(runtimeGlyph('claude-code')?.length).toBeGreaterThan(0)
    expect(runtimeGlyph('codex')?.length).toBeGreaterThan(0)
    expect(runtimeGlyph('grok')?.length).toBeGreaterThan(0)
    expect(runtimeGlyph('antigravity')?.length).toBeGreaterThan(0)
    expect(runtimeGlyph('acme' as Schemas['AgentRuntime'])).toBeNull()
  })
})

describe('runtime readiness', () => {
  const [pi, claude] = definitions
  const status = (overrides: Partial<Schemas['RuntimeStatus']> = {}): Schemas['RuntimeStatus'] => ({ id: 'claude-code', available: true, version: '2.1.0', binaryPath: '/bin/claude', unavailableReason: null, login: { state: 'logged_in', authMethod: null, subscriptionType: null }, checkedAt: '2026-11-23T08:00:00Z', ...overrides })

  it('a bundled runtime is always ready, without reading the status', () => {
    expect(runtimeReadiness(pi, { status: undefined, checking: true, failed: false })).toEqual({ state: 'ready' })
  })

  it('a runtime installed on this computer is ready, missing, logged out or broken as GET /runtimes says', () => {
    const read = (s: Schemas['RuntimeStatus']) => runtimeReadiness(claude, { status: s, checking: false, failed: false })
    expect(read(status())).toEqual({ state: 'ready' })
    expect(read(status({ login: { state: 'unknown', authMethod: null, subscriptionType: null } }))).toEqual({ state: 'ready' })
    expect(read(status({ available: false, unavailableReason: 'not_found', login: null }))).toEqual({ state: 'problem', problem: 'not_found' })
    expect(read(status({ available: false, unavailableReason: 'version_failed', login: null }))).toEqual({ state: 'problem', problem: 'version_failed' })
    expect(read(status({ login: { state: 'logged_out', authMethod: null, subscriptionType: null } }))).toEqual({ state: 'problem', problem: 'logged_out' })
  })

  it('a CLI older than the lowest version the runtime supports is a problem of its own, said with both versions', () => {
    const tooOld = status({ id: 'codex', available: false, unavailableReason: 'version_unsupported', version: '0.150.0' })
    expect(runtimeReadiness(codexDefinition, { status: tooOld, checking: false, failed: false })).toEqual({ state: 'problem', problem: 'version_unsupported' })
    expect(runtimeProblemText(codexDefinition, 'version_unsupported', '0.150.0')).toBe('Codex on this computer is version 0.150.0; it needs 0.167.0 or later. Update it, then check again.')
    // Without the versions it still says what is wrong.
    expect(runtimeProblemText({ ...codexDefinition, minVersion: null }, 'version_unsupported', null)).toBe('Codex on this computer is too old for Loop Now. Update it, then check again.')
  })

  it('while the status is being read it is checking; when it cannot be read it is unknown', () => {
    expect(runtimeReadiness(claude, { status: undefined, checking: true, failed: false })).toEqual({ state: 'checking' })
    expect(runtimeReadiness(claude, { status: undefined, checking: false, failed: true })).toEqual({ state: 'unknown' })
  })

  it('only a known problem keeps a runtime from being chosen', () => {
    expect(canRun({ state: 'ready' })).toBe(true)
    expect(canRun({ state: 'checking' })).toBe(true)
    expect(canRun({ state: 'unknown' })).toBe(true)
    expect(canRun({ state: 'problem', problem: 'logged_out' })).toBe(false)
  })

  it('orders runtimes installed on this computer first, ready ones on top, and bundled ones last', () => {
    const second = { ...claude, id: 'codex', name: 'Codex' } as unknown as Schemas['RuntimeDefinition']
    const third = { ...claude, id: 'grok', name: 'Grok' } as unknown as Schemas['RuntimeDefinition']
    const readiness = new Map<string, RuntimeReadiness>([['claude-code', { state: 'problem', problem: 'not_found' }], ['codex', { state: 'ready' }], ['grok', { state: 'checking' }]])
    const order = railOrder([pi, claude, second, third], (definition) => readiness.get(definition.id) ?? { state: 'ready' })
    expect(order.map((definition) => definition.name)).toEqual(['Codex', 'Grok', 'Claude Code', 'Pi'])
  })
})
