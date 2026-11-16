import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'

// The configSchema and the account fields in a plugin manifest are JSON Schema (an object whose properties are the fields).
// Only the parts the form needs are recognized here: string (format uri for URLs), number / integer, boolean, enum (or oneOf with
// const + title), required, and two extensions: x-secret (write-only, never echoed) and x-scope: project (can be overridden per project).
// title and description (including the title that names a oneOf option) may be a LocalizedText; the form shows them in the UI
// language (localizedText).
// Fields of other types are read-only on the page, and their original values are saved unchanged.

const text = t.plugins.form

export type JsonSchema = {
  type?: string | string[]
  title?: Schemas['LocalizedText']
  description?: Schemas['LocalizedText']
  format?: string
  enum?: unknown[]
  oneOf?: { const?: unknown; title?: Schemas['LocalizedText'] }[]
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

// The draft of each form field: a regular field is always the text in its input (booleans are 'true' / 'false', and the empty string
// means not filled in);
// a secret field records only what to do: keep the existing value, replace it with a new value, or clear it.
export type SecretDraft = { mode: 'keep' | 'set' | 'clear'; value: string }
export type Draft = { values: Record<string, string>; secrets: Record<string, SecretDraft> }

// What the form is for: global is the plugin's global config; project is one project's overrides of the x-scope: project fields (empty
// means use the global value).
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

// A title or description as text in the UI language: a string as is, a per-language object through localizedText; anything else
// (not allowed by the manifest rules) is ignored.
const textOf = (value: unknown): string | undefined =>
  typeof value === 'string' ? value : isObject(value) && Object.values(value).every((v) => typeof v === 'string') ? localizedText(value as Record<string, string>) || undefined : undefined

// List the fields in the order of properties. Project mode keeps only the x-scope: project fields.
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
            : (property.oneOf ?? []).filter((option) => 'const' in option).map((option) => ({ value: option.const, label: textOf(option.title) ?? String(option.const) }))
      return {
        name,
        kind,
        label: textOf(property.title) || name,
        description: textOf(property.description),
        // In project overrides, every field may be left empty to keep using the global value.
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

// Turn a saved value into the text for its input; enum stores the option's index, so numbers and strings alike are restored exactly.
function toText(field: Field, value: unknown): string {
  if (value === undefined || value === null) return ''
  if (field.kind === 'enum') {
    const index = optionIndex(field, value)
    return index < 0 ? '' : String(index)
  }
  if (field.kind === 'boolean') return value === true ? 'true' : value === false ? 'false' : ''
  return typeof value === 'string' || typeof value === 'number' ? String(value) : ''
}

// Initial draft: when the global config has never been saved (revision 0), use the defaults from the manifest; project overrides don't
// fill in defaults, and empty means keep using the global value.
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

// Validate field by field, returning the failing fields and their reasons; an empty object when there are no errors. A secret field
// must be filled in when it is required and has never been set.
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

// Turn the draft into the request body's values and secrets: optional fields left empty are not sent (in project overrides, that means
// keep using the global value);
// fields that can't be edited on the page are sent back unchanged, so saving doesn't wipe them. secrets carries only what changes: a new
// value or null (clear).
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

// Has changes: the text of a regular field changed, or a secret is to be replaced with a new value or cleared (clicking “更换” (Replace)
// without filling anything in doesn't count).
export function isDirty(initial: Draft, draft: Draft): boolean {
  if (JSON.stringify(initial.values) !== JSON.stringify(draft.values)) return true
  return Object.values(draft.secrets).some((secret) => secret.mode === 'clear' || (secret.mode === 'set' && secret.value !== ''))
}

// Match the server's 422 field errors to form fields by field name; the path may carry a prefix (values.url, secrets.token).
export function serverFieldErrors(fields: Field[], errors: { field: string; message: string }[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const error of errors) {
    const field = fields.find((f) => error.field === f.name || error.field.endsWith(`.${f.name}`) || error.field.endsWith(`/${f.name}`))
    if (field && !out[field.name]) out[field.name] = error.message
  }
  return out
}

// The global value shown as a hint in project overrides (“全局值：…” (Global value: ...)); for a secret, only whether it is set.
export function describeValue(field: Field, value: unknown): string | null {
  if (value === undefined || value === null || value === '') return null
  if (field.kind === 'enum') return field.options[optionIndex(field, value)]?.label ?? String(value)
  if (field.kind === 'boolean') return value === true ? text.yes : text.no
  return typeof value === 'string' || typeof value === 'number' ? String(value) : JSON.stringify(value)
}
