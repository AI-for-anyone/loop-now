import { describe, expect, it } from 'vitest'
import { mergeExample, MODEL_EXAMPLES } from './model-examples'

const example = MODEL_EXAMPLES[0]

describe('Merging model examples', () => {
  it('keeps existing providers and appends the example', () => {
    const result = mergeExample('{"providers":{"existing":{"keep":true}},"other":1}', example)
    if (result.kind !== 'merged') throw new Error('should be mergeable')
    const parsed = JSON.parse(result.text)
    expect(parsed.providers.existing).toEqual({ keep: true })
    expect(parsed.providers['my-openai']).toEqual(example.providers['my-openai'])
    expect(parsed.other).toBe(1)
  })

  it('returns a conflict for a provider with the same name first, and after confirmation replaces only that provider', () => {
    const text = '{"providers":{"my-openai":{"keep":true},"existing":{"keep":true}}}'
    expect(mergeExample(text, example)).toEqual({ kind: 'conflict', providers: ['my-openai'] })
    const result = mergeExample(text, example, true)
    if (result.kind !== 'merged') throw new Error('should be mergeable after confirmation')
    const parsed = JSON.parse(result.text)
    expect(parsed.providers['my-openai']).toEqual(example.providers['my-openai'])
    expect(parsed.providers.existing).toEqual({ keep: true })
  })

  it('does not overwrite the draft on a syntax error or a malformed providers structure', () => {
    expect(mergeExample('{', example).kind).toBe('invalid')
    expect(mergeExample('{"providers":[]}', example).kind).toBe('invalid')
  })
})
