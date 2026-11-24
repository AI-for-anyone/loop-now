import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { buildRows, orderProviders } from './runner-rows'

const provider = (id: string, overrides: Partial<Schemas['ModelProvider']> = {}): Schemas['ModelProvider'] => ({ id, name: null, source: 'builtin', runtime: 'pi', pluginId: null, pluginDisplayName: null, unavailableReason: null, usable: null, apiKeyEnvVars: [], modelCount: 2, authMethods: ['api_key'], ...overrides })
const model = (id: string, name = id): Schemas['Model'] => ({ id, name, reasoning: true, contextWindow: 200000, maxTokens: 64000, input: ['text'] })

const anthropic = provider('anthropic'), openrouter = provider('openrouter'), plugin = provider('grok-cli', { source: 'plugin', name: 'Grok CLI' })
const models = new Map([
  ['anthropic', [model('claude-sonnet-4-5', 'Claude Sonnet 4.5'), model('claude-haiku-4-5', 'Claude Haiku 4.5')]],
  ['openrouter', [model('anthropic/claude-sonnet-4.5', 'Anthropic: Claude Sonnet 4.5'), model('openai/gpt-5', 'OpenAI: GPT-5')]],
])
const keys = (rows: ReturnType<typeof buildRows>['rows']) => rows.map((row) => row.key)

describe('runner picker rows', () => {
  it('lists the models of a runtime with one provider without a provider level, filtered by name or id', () => {
    const one = { providers: [anthropic], models, expanded: new Set<string>(), grouped: false, limit: 100 }
    expect(keys(buildRows({ ...one, query: '' }).rows)).toEqual(['m:anthropic/claude-sonnet-4-5', 'm:anthropic/claude-haiku-4-5'])
    expect(keys(buildRows({ ...one, query: 'HAIKU' }).rows)).toEqual(['m:anthropic/claude-haiku-4-5'])
    expect(keys(buildRows({ ...one, query: 'sonnet-4-5' }).rows)).toEqual(['m:anthropic/claude-sonnet-4-5'])
  })

  it('groups models under their providers; only expanded providers show their models', () => {
    const { rows } = buildRows({ providers: [anthropic, openrouter], models, expanded: new Set(['openrouter']), grouped: true, query: '', limit: 100 })
    expect(keys(rows)).toEqual(['p:anthropic', 'p:openrouter', 'm:openrouter/anthropic/claude-sonnet-4.5', 'm:openrouter/openai/gpt-5'])
    expect(rows[0]).toMatchObject({ kind: 'provider', expanded: false })
    expect(rows[1]).toMatchObject({ kind: 'provider', expanded: true })
  })

  it('a search looks through every provider, matching each word against the provider and the model, and leaves out providers without matches', () => {
    const search = (query: string) => keys(buildRows({ providers: [anthropic, openrouter, plugin], models, expanded: new Set(), grouped: true, query, limit: 100 }).rows)
    expect(search('sonnet')).toEqual(['p:anthropic', 'm:anthropic/claude-sonnet-4-5', 'p:openrouter', 'm:openrouter/anthropic/claude-sonnet-4.5'])
    expect(search('openrouter sonnet')).toEqual(['p:openrouter', 'm:openrouter/anthropic/claude-sonnet-4.5'])
    // A provider whose models are not read yet still shows when its own name matches.
    expect(search('grok')).toEqual(['p:grok-cli'])
    expect(search('nothing-like-this')).toEqual([])
  })

  it('while searching, a provider row says how many of its models were found', () => {
    const { rows } = buildRows({ providers: [anthropic, openrouter], models, expanded: new Set(), grouped: true, query: 'sonnet', limit: 100 })
    expect(rows.filter((row) => row.kind === 'provider').map((row) => row.kind === 'provider' && row.matched)).toEqual([1, 1])
    expect(buildRows({ providers: [anthropic], models, expanded: new Set(), grouped: true, query: '', limit: 100 }).rows[0]).toMatchObject({ matched: null })
  })

  it('shows at most the limit of models and says how many more match', () => {
    const result = buildRows({ providers: [anthropic, openrouter], models, expanded: new Set(), grouped: true, query: 'a', limit: 3 })
    expect(keys(result.rows)).toEqual(['p:anthropic', 'm:anthropic/claude-sonnet-4-5', 'm:anthropic/claude-haiku-4-5', 'p:openrouter', 'm:openrouter/anthropic/claude-sonnet-4.5'])
    expect(result.hidden).toBe(1)
  })

  it('puts the current provider first, those that cannot be chosen last, and keeps the catalog order otherwise', () => {
    const order = orderProviders([anthropic, plugin, openrouter, provider('xai')], 'openrouter', (p) => p.source === 'plugin')
    expect(order.map((p) => p.id)).toEqual(['openrouter', 'anthropic', 'xai', 'grok-cli'])
    expect(orderProviders([anthropic, openrouter], undefined, () => false).map((p) => p.id)).toEqual(['anthropic', 'openrouter'])
  })

  it('puts the providers the Agent can use now first, the current one leading its own group', () => {
    const ready = (id: string) => provider(id, { usable: true }), needs = (id: string) => provider(id, { usable: false })
    const order = orderProviders([needs('amazon-bedrock'), ready('anthropic'), needs('openai'), ready('openai-codex'), provider('grok-cli', { usable: false, source: 'plugin' })], 'openai', (p) => p.source === 'plugin')
    expect(order.map((p) => p.id)).toEqual(['anthropic', 'openai-codex', 'openai', 'amazon-bedrock', 'grok-cli'])
  })

  it('a provider that cannot be chosen goes under the second heading even when the catalog calls it usable', () => {
    const ready = provider('anthropic', { usable: true }), needs = provider('openrouter', { usable: false }), blockedPlugin = provider('grok-cli', { usable: true, source: 'plugin' })
    const rows = buildRows({ providers: [ready, needs, blockedPlugin], models, expanded: new Set(), grouped: true, query: '', limit: 100, usableNow: (p) => p.usable === true && p.source !== 'plugin' }).rows
    expect(rows.map((row) => row.key)).toEqual(['s:true', 'p:anthropic', 's:false', 'p:openrouter', 'p:grok-cli'])
  })

  it('heads the two groups when the catalog says which providers can be used now', () => {
    const ready = provider('anthropic', { usable: true }), needs = provider('openrouter', { usable: false })
    const rows = buildRows({ providers: [ready, needs], models, expanded: new Set(), grouped: true, query: '', limit: 100 }).rows
    expect(rows.map((row) => (row.kind === 'section' ? `section:${row.usable}` : row.key))).toEqual(['section:true', 'p:anthropic', 'section:false', 'p:openrouter'])
    // Without usable flags there are no headings.
    expect(keys(buildRows({ providers: [anthropic, openrouter], models, expanded: new Set(), grouped: true, query: '', limit: 100 }).rows)).toEqual(['p:anthropic', 'p:openrouter'])
    // A heading goes only above a group that has rows.
    expect(buildRows({ providers: [ready, needs], models, expanded: new Set(), grouped: true, query: 'gpt', limit: 100 }).rows.map((row) => row.key)).toEqual(['s:false', 'p:openrouter', 'm:openrouter/openai/gpt-5'])
  })
})
