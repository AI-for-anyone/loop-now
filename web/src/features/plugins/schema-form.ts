import { t } from '@/i18n'

// 插件清单里的 configSchema 与账号 fields 是 JSON Schema（一个 object，properties 是各个字段）。
// 这里只认表单用得到的部分：string（format uri 为地址）、number / integer、boolean、enum（或 oneOf 的 const + title）、
// required，以及两个扩展：x-secret（只写不回显）与 x-scope: project（项目里可覆盖）。其余类型的字段在页面上只读，原值照旧保存。

const text = t.plugins.form

export type JsonSchema = {
  type?: string | string[]
  title?: string
  description?: string
  format?: string
  enum?: unknown[]
  oneOf?: { const?: unknown; title?: string }[]
  default?: unknown
  minimum?: number
  maximum?: number
  maxLength?: number
  properties?: Record<string, JsonSchema>
  required?: string[]
  'x-secret'?: boolean
  'x-scope'?: string
}

export type FieldKind = 'string' | 'uri' | 'number' | 'integer' | 'boolean' | 'enum' | 'unsupported'

export type Field = {
  name: string
  kind: FieldKind
  label: string
  description?: string
  required: boolean
  secret: boolean
  projectScope: boolean
  options: { value: unknown; label: string }[]
  defaultValue?: unknown
  minimum?: number
  maximum?: number
  maxLength?: number
}

// 表单里每一项的草稿：普通字段一律是输入框里的文字（布尔为 'true' / 'false'，空串表示没填）；
// secret 字段只记要做的事：保留原值、换成新值，或清除。
export type SecretDraft = { mode: 'keep' | 'set' | 'clear'; value: string }
export type Draft = { values: Record<string, string>; secrets: Record<string, SecretDraft> }

// 表单的用途：global 是插件的全局配置，project 是某个项目对 x-scope: project 字段的覆盖（留空即用全局值）。
export type FormMode = 'global' | 'project'

const typeOf = (schema: JsonSchema) => (Array.isArray(schema.type) ? schema.type.find((t) => t !== 'null') : schema.type)

function kindOf(schema: JsonSchema): FieldKind {
  if (schema.enum?.length || schema.oneOf?.some((option) => 'const' in option)) return 'enum'
  switch (typeOf(schema)) {
    case 'string':
      return schema.format === 'uri' || schema.format === 'url' ? 'uri' : 'string'
    case 'number':
      return 'number'
    case 'integer':
      return 'integer'
    case 'boolean':
      return 'boolean'
    default:
      return 'unsupported'
  }
}

const isObject = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value)

// 按 properties 的顺序列出字段。project 模式只留 x-scope: project 的字段。
export function fieldsOf(schema: unknown, mode: FormMode = 'global'): Field[] {
  if (!isObject(schema) || !isObject(schema.properties)) return []
  const root = schema as JsonSchema
  const required = new Set(root.required ?? [])
  return Object.entries(root.properties ?? {})
    .filter(([, property]) => isObject(property))
    .map(([name, property]): Field => {
      const kind = kindOf(property)
      const options =
        kind !== 'enum'
          ? []
          : property.enum?.length
            ? property.enum.map((value) => ({ value, label: String(value) }))
            : (property.oneOf ?? []).filter((option) => 'const' in option).map((option) => ({ value: option.const, label: option.title ?? String(option.const) }))
      return {
        name,
        kind,
        label: property.title || name,
        description: property.description,
        // 项目覆盖时每一项都可以留空沿用全局值。
        required: mode === 'global' && required.has(name),
        secret: property['x-secret'] === true,
        projectScope: property['x-scope'] === 'project',
        options,
        defaultValue: property.default,
        minimum: property.minimum,
        maximum: property.maximum,
        maxLength: property.maxLength,
      }
    })
    .filter((field) => mode === 'global' || field.projectScope)
}

const optionIndex = (field: Field, value: unknown) => field.options.findIndex((option) => Object.is(option.value, value) || JSON.stringify(option.value) === JSON.stringify(value))

// 已保存的值写成输入框里的文字；enum 记选项的序号，数字、字符串都能原样还原。
function toText(field: Field, value: unknown): string {
  if (value === undefined || value === null) return ''
  if (field.kind === 'enum') {
    const index = optionIndex(field, value)
    return index < 0 ? '' : String(index)
  }
  if (field.kind === 'boolean') return value === true ? 'true' : value === false ? 'false' : ''
  return typeof value === 'string' || typeof value === 'number' ? String(value) : ''
}

// 初始草稿：全局配置还没保存过时（revision 0）用清单里的默认值；项目覆盖不填默认值，留空即沿用全局值。
export function initialDraft(fields: Field[], config: { values: Record<string, unknown>; secretsSet: string[]; revision: number }, mode: FormMode = 'global'): Draft {
  const values: Record<string, string> = {}
  const secrets: Record<string, SecretDraft> = {}
  for (const field of fields) {
    if (field.secret) {
      secrets[field.name] = { mode: config.secretsSet.includes(field.name) ? 'keep' : 'set', value: '' }
      continue
    }
    if (field.kind === 'unsupported') continue
    const saved = config.values[field.name]
    const fallback = mode === 'global' && config.revision === 0 ? field.defaultValue : undefined
    values[field.name] = toText(field, saved === undefined ? fallback : saved)
  }
  return { values, secrets }
}

const isAbsoluteUrl = (value: string) => {
  try {
    return Boolean(new URL(value).protocol)
  } catch {
    return false
  }
}

// 逐项校验，返回出错的字段与原因；没有错误时为空对象。secret 字段：必填且从未设置时要填。
export function validate(fields: Field[], draft: Draft, secretsSet: string[] = []): Record<string, string> {
  const errors: Record<string, string> = {}
  for (const field of fields) {
    if (field.secret) {
      const secret = draft.secrets[field.name] ?? { mode: 'keep', value: '' }
      const missing = secret.mode === 'clear' || (secret.mode === 'set' && secret.value === '' && !secretsSet.includes(field.name))
      if (field.required && missing) errors[field.name] = text.required
      continue
    }
    if (field.kind === 'unsupported') continue
    const value = (draft.values[field.name] ?? '').trim()
    if (value === '') {
      if (field.required) errors[field.name] = text.required
      continue
    }
    if (field.kind === 'number' || field.kind === 'integer') {
      const number = Number(value)
      if (!Number.isFinite(number) || (field.kind === 'integer' && !Number.isInteger(number))) errors[field.name] = field.kind === 'integer' ? text.integer : text.number
      else if (field.minimum !== undefined && number < field.minimum) errors[field.name] = text.minimum(field.minimum)
      else if (field.maximum !== undefined && number > field.maximum) errors[field.name] = text.maximum(field.maximum)
    } else if (field.kind === 'uri' && !isAbsoluteUrl(value)) errors[field.name] = text.uri
    else if (field.maxLength !== undefined && [...value].length > field.maxLength) errors[field.name] = text.maxLength(field.maxLength)
  }
  return errors
}

// 草稿转成请求体的 values 与 secrets：没填的可选项不发送（项目覆盖里即沿用全局值）；
// 页面上编辑不了的字段原样带回，保存不会把它们清掉。secrets 只带要改的：新值或 null（清除）。
export function toPayload(fields: Field[], draft: Draft, saved: Record<string, unknown>) {
  const values: Record<string, unknown> = {}
  const secrets: Record<string, string | null> = {}
  for (const field of fields) {
    if (field.secret) {
      const secret = draft.secrets[field.name]
      if (secret?.mode === 'clear') secrets[field.name] = null
      else if (secret?.mode === 'set' && secret.value !== '') secrets[field.name] = secret.value
      continue
    }
    if (field.kind === 'unsupported') {
      if (saved[field.name] !== undefined) values[field.name] = saved[field.name]
      continue
    }
    const raw = draft.values[field.name] ?? ''
    const value = field.kind === 'string' ? raw : raw.trim()
    if (value === '') continue
    if (field.kind === 'number' || field.kind === 'integer') values[field.name] = Number(value)
    else if (field.kind === 'boolean') values[field.name] = value === 'true'
    else if (field.kind === 'enum') values[field.name] = field.options[Number(value)]?.value
    else values[field.name] = value
  }
  return { values, ...(Object.keys(secrets).length ? { secrets } : {}) }
}

// 有改动：普通字段的文字变了，或 secret 要换新值、要清除（点了“更换”但没填不算）。
export function isDirty(initial: Draft, draft: Draft): boolean {
  if (JSON.stringify(initial.values) !== JSON.stringify(draft.values)) return true
  return Object.values(draft.secrets).some((secret) => secret.mode === 'clear' || (secret.mode === 'set' && secret.value !== ''))
}

// 服务端 422 的字段错误按字段名对上表单里的一项；路径可能带前缀（values.url、secrets.token）。
export function serverFieldErrors(fields: Field[], errors: { field: string; message: string }[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const error of errors) {
    const field = fields.find((f) => error.field === f.name || error.field.endsWith(`.${f.name}`) || error.field.endsWith(`/${f.name}`))
    if (field && !out[field.name]) out[field.name] = error.message
  }
  return out
}

// 全局值在项目覆盖里作为提示（“全局值：…”）；secret 只说是否已设置。
export function describeValue(field: Field, value: unknown): string | null {
  if (value === undefined || value === null || value === '') return null
  if (field.kind === 'enum') return field.options[optionIndex(field, value)]?.label ?? String(value)
  if (field.kind === 'boolean') return value === true ? text.yes : text.no
  return typeof value === 'string' || typeof value === 'number' ? String(value) : JSON.stringify(value)
}
