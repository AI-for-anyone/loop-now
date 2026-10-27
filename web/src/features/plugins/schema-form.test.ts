import { describe, expect, it } from 'vitest'
import { webhookSchema } from '@/test/plugins'
import { fieldsOf, initialDraft, isDirty, serverFieldErrors, toPayload, validate } from './schema-form'

const schema = {
  type: 'object',
  required: ['url', 'token'],
  properties: {
    url: { type: 'string', format: 'uri', title: '回调地址', 'x-scope': 'project' },
    token: { type: 'string', title: '令牌', 'x-secret': true },
    retries: { type: 'integer', title: '重试上限', minimum: 0, maximum: 10, default: 5 },
    ratio: { type: 'number', title: '比例' },
    verbose: { type: 'boolean', title: '详细日志', 'x-scope': 'project' },
    level: { enum: ['low', 'high', 3], title: '级别' },
    mode: { oneOf: [{ const: 'a', title: '模式 A' }, { const: 'b', title: '模式 B' }] },
    nested: { type: 'object', title: '高级' },
  },
}

describe('按 JSON Schema 生成的配置表单', () => {
  it('按 properties 顺序列出字段，认出各种类型、必填、密钥与项目级覆盖', () => {
    const fields = fieldsOf(schema)
    expect(fields.map((f) => [f.name, f.kind, f.required, f.secret, f.projectScope])).toEqual([
      ['url', 'uri', true, false, true],
      ['token', 'string', true, true, false],
      ['retries', 'integer', false, false, false],
      ['ratio', 'number', false, false, false],
      ['verbose', 'boolean', false, false, true],
      ['level', 'enum', false, false, false],
      ['mode', 'enum', false, false, false],
      ['nested', 'unsupported', false, false, false],
    ])
    expect(fields.find((f) => f.name === 'mode')!.options).toEqual([{ value: 'a', label: '模式 A' }, { value: 'b', label: '模式 B' }])
    expect(fieldsOf({})).toEqual([])
    expect(fieldsOf(null)).toEqual([])
  })

  it('项目覆盖只留 x-scope: project 的字段，而且都可以留空沿用全局值', () => {
    const fields = fieldsOf(schema, 'project')
    expect(fields.map((f) => [f.name, f.required])).toEqual([
      ['url', false],
      ['verbose', false],
    ])
  })

  it('还没保存过的全局配置用默认值；已保存的值按类型写进输入框，enum 记选项序号', () => {
    const fields = fieldsOf(schema)
    expect(initialDraft(fields, { values: {}, secretsSet: [], revision: 0 }).values.retries).toBe('5')
    const draft = initialDraft(fields, { values: { url: 'https://a.example/x', retries: 2, verbose: true, level: 3 }, secretsSet: ['token'], revision: 4 })
    expect(draft.values).toEqual({ url: 'https://a.example/x', retries: '2', ratio: '', verbose: 'true', level: '2', mode: '' })
    expect(draft.secrets).toEqual({ token: { mode: 'keep', value: '' } })
    // 项目覆盖不填默认值。
    expect(initialDraft(fieldsOf(schema, 'project'), { values: {}, secretsSet: [], revision: 0 }, 'project').values).toEqual({ url: '', verbose: '' })
  })

  it('校验必填、数字、整数、范围与地址；从未设置的必填密钥要填，已设置的可以不动', () => {
    const fields = fieldsOf(schema)
    const draft = initialDraft(fields, { values: {}, secretsSet: [], revision: 1 })
    expect(validate(fields, draft)).toEqual({ url: '必填', token: '必填' })
    const bad = { ...draft, values: { ...draft.values, url: 'hooks.example.com', retries: '2.5', ratio: 'abc' } }
    expect(validate(fields, bad)).toEqual({ url: '请填完整地址，例如 https://…', token: '必填', retries: '请填整数', ratio: '请填数字' })
    const range = { ...draft, values: { ...draft.values, url: 'https://ok.example', retries: '11' }, secrets: { token: { mode: 'keep' as const, value: '' } } }
    expect(validate(fields, range, ['token'])).toEqual({ retries: '不能大于 10' })
    // 必填密钥不能清除。
    expect(validate(fields, { ...range, values: { ...range.values, retries: '1' }, secrets: { token: { mode: 'clear', value: '' } } }, ['token'])).toEqual({ token: '必填' })
  })

  it('请求体：数字、布尔、enum 还原成原来的类型；没填的可选项不发送；编辑不了的字段原样带回', () => {
    const fields = fieldsOf(schema)
    const draft = initialDraft(fields, { values: { nested: { a: 1 } }, secretsSet: [], revision: 1 })
    draft.values = { ...draft.values, url: ' https://ok.example/hook ', retries: '3', ratio: '0.5', verbose: 'false', level: '2', mode: '1' }
    draft.secrets = { token: { mode: 'set', value: 's3cret' } }
    expect(toPayload(fields, draft, { nested: { a: 1 } })).toEqual({
      values: { url: 'https://ok.example/hook', retries: 3, ratio: 0.5, verbose: false, level: 3, mode: 'b', nested: { a: 1 } },
      secrets: { token: 's3cret' },
    })
  })

  it('密钥只带要改的：没动的不发送，清除发 null；点了“更换”没填不算改动', () => {
    const fields = fieldsOf(webhookSchema)
    const initial = initialDraft(fields, { values: { url: 'https://a.example' }, secretsSet: ['secret'], revision: 2 })
    expect(toPayload(fields, initial, {})).toEqual({ values: { url: 'https://a.example' } })
    const replacing = { ...initial, secrets: { secret: { mode: 'set' as const, value: '' } } }
    expect(isDirty(initial, replacing)).toBe(false)
    const cleared = { ...initial, secrets: { secret: { mode: 'clear' as const, value: '' } } }
    expect(isDirty(initial, cleared)).toBe(true)
    expect(toPayload(fields, cleared, {})).toEqual({ values: { url: 'https://a.example' }, secrets: { secret: null } })
  })

  it('服务端 422 的字段错误按字段名（可带 values. 或 secrets. 前缀）对上表单项', () => {
    const fields = fieldsOf(webhookSchema)
    expect(serverFieldErrors(fields, [{ field: 'values.url', message: '地址不可达' }, { field: 'secrets.secret', message: '太短' }, { field: 'other', message: 'x' }])).toEqual({
      url: '地址不可达',
      secret: '太短',
    })
  })
})
