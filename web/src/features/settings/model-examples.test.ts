import { describe, expect, it } from 'vitest'
import { mergeExample, MODEL_EXAMPLES } from './model-examples'

const example = MODEL_EXAMPLES[0]

describe('模型示例合并', () => {
  it('保留现有 Provider 并追加示例', () => {
    const result = mergeExample('{"providers":{"existing":{"keep":true}},"other":1}', example)
    if (result.kind !== 'merged') throw new Error('应可合并')
    const parsed = JSON.parse(result.text)
    expect(parsed.providers.existing).toEqual({ keep: true })
    expect(parsed.providers['my-openai']).toEqual(example.providers['my-openai'])
    expect(parsed.other).toBe(1)
  })

  it('同名时先返回冲突，确认后只替换同名 Provider', () => {
    const text = '{"providers":{"my-openai":{"keep":true},"existing":{"keep":true}}}'
    expect(mergeExample(text, example)).toEqual({ kind: 'conflict', providers: ['my-openai'] })
    const result = mergeExample(text, example, true)
    if (result.kind !== 'merged') throw new Error('确认后应可合并')
    const parsed = JSON.parse(result.text)
    expect(parsed.providers['my-openai']).toEqual(example.providers['my-openai'])
    expect(parsed.providers.existing).toEqual({ keep: true })
  })

  it('语法错误或 providers 结构错误时不覆盖草稿', () => {
    expect(mergeExample('{', example).kind).toBe('invalid')
    expect(mergeExample('{"providers":[]}', example).kind).toBe('invalid')
  })
})
