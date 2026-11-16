import { describe, expect, it } from 'vitest'
import { webhookSchema } from '@/test/plugins'
import { fieldsOf, initialDraft, isDirty, serverFieldErrors, toPayload, validate } from './schema-form'

const schema = {
  type: 'object',
  required: ['url', 'token'],
  properties: {
    url: { type: 'string', format: 'uri', title: 'Callback URL', 'x-scope': 'project' },
    token: { type: 'string', title: 'Token', 'x-secret': true },
    retries: { type: 'integer', title: 'Retry limit', minimum: 0, maximum: 10, default: 5 },
    ratio: { type: 'number', title: 'Ratio' },
    verbose: { type: 'boolean', title: 'Verbose logs', 'x-scope': 'project' },
    level: { enum: ['low', 'high', 3], title: 'Level' },
    mode: { oneOf: [{ const: 'a', title: 'Mode A' }, { const: 'b', title: 'Mode B' }] },
    nested: { type: 'object', title: 'Advanced' },
  },
}

describe('Config form generated from JSON Schema', () => {
  it('lists fields in properties order and recognizes types, required fields, secrets and project overrides', () => {
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
    expect(fields.find((f) => f.name === 'mode')!.options).toEqual([{ value: 'a', label: 'Mode A' }, { value: 'b', label: 'Mode B' }])
    expect(fieldsOf({})).toEqual([])
    expect(fieldsOf(null)).toEqual([])
  })

  it('project overrides keep only x-scope: project fields, and all of them can be left empty to use the global value', () => {
    const fields = fieldsOf(schema, 'project')
    expect(fields.map((f) => [f.name, f.required])).toEqual([
      ['url', false],
      ['verbose', false],
    ])
  })

  it('a global config never saved uses the defaults; saved values go into the inputs by type, and an enum stores the option index', () => {
    const fields = fieldsOf(schema)
    expect(initialDraft(fields, { values: {}, secretsSet: [], revision: 0 }).values.retries).toBe('5')
    const draft = initialDraft(fields, { values: { url: 'https://a.example/x', retries: 2, verbose: true, level: 3 }, secretsSet: ['token'], revision: 4 })
    expect(draft.values).toEqual({ url: 'https://a.example/x', retries: '2', ratio: '', verbose: 'true', level: '2', mode: '' })
    expect(draft.secrets).toEqual({ token: { mode: 'keep', value: '' } })
    // Project overrides are not prefilled with default values.
    expect(initialDraft(fieldsOf(schema, 'project'), { values: {}, secretsSet: [], revision: 0 }, 'project').values).toEqual({ url: '', verbose: '' })
  })

  it('validates required fields, numbers, integers, ranges and URLs; a required secret never set must be filled in, one already set can be left alone', () => {
    const fields = fieldsOf(schema)
    const draft = initialDraft(fields, { values: {}, secretsSet: [], revision: 1 })
    expect(validate(fields, draft)).toEqual({ url: 'Required', token: 'Required' })
    const bad = { ...draft, values: { ...draft.values, url: 'hooks.example.com', retries: '2.5', ratio: 'abc' } }
    expect(validate(fields, bad)).toEqual({ url: 'Enter a full URL, e.g. https://…', token: 'Required', retries: 'Enter a whole number', ratio: 'Enter a number' })
    const range = { ...draft, values: { ...draft.values, url: 'https://ok.example', retries: '11' }, secrets: { token: { mode: 'keep' as const, value: '' } } }
    expect(validate(fields, range, ['token'])).toEqual({ retries: 'Must be at most 10' })
    // A required secret cannot be cleared.
    expect(validate(fields, { ...range, values: { ...range.values, retries: '1' }, secrets: { token: { mode: 'clear', value: '' } } }, ['token'])).toEqual({ token: 'Required' })
  })

  it('request body: numbers, booleans and enums get their original types back; empty optional fields are not sent; fields that cannot be edited are sent back unchanged', () => {
    const fields = fieldsOf(schema)
    const draft = initialDraft(fields, { values: { nested: { a: 1 } }, secretsSet: [], revision: 1 })
    draft.values = { ...draft.values, url: ' https://ok.example/hook ', retries: '3', ratio: '0.5', verbose: 'false', level: '2', mode: '1' }
    draft.secrets = { token: { mode: 'set', value: 's3cret' } }
    expect(toPayload(fields, draft, { nested: { a: 1 } })).toEqual({
      values: { url: 'https://ok.example/hook', retries: 3, ratio: 0.5, verbose: false, level: 3, mode: 'b', nested: { a: 1 } },
      secrets: { token: 's3cret' },
    })
  })

  it('secrets carry only changes: untouched ones are not sent and cleared ones send null; clicking "Replace" without typing is not a change', () => {
    const fields = fieldsOf(webhookSchema)
    const initial = initialDraft(fields, { values: { url: 'https://a.example' }, secretsSet: ['secret'], revision: 2 })
    expect(toPayload(fields, initial, {})).toEqual({ values: { url: 'https://a.example' } })
    const replacing = { ...initial, secrets: { secret: { mode: 'set' as const, value: '' } } }
    expect(isDirty(initial, replacing)).toBe(false)
    const cleared = { ...initial, secrets: { secret: { mode: 'clear' as const, value: '' } } }
    expect(isDirty(initial, cleared)).toBe(true)
    expect(toPayload(fields, cleared, {})).toEqual({ values: { url: 'https://a.example' }, secrets: { secret: null } })
  })

  it('server 422 field errors are matched to form fields by name (with an optional values. or secrets. prefix)', () => {
    const fields = fieldsOf(webhookSchema)
    expect(serverFieldErrors(fields, [{ field: 'values.url', message: 'The address is unreachable' }, { field: 'secrets.secret', message: 'Too short' }, { field: 'other', message: 'x' }])).toEqual({
      url: 'The address is unreachable',
      secret: 'Too short',
    })
  })
})
