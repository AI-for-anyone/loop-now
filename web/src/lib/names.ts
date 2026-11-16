import { t } from '@/i18n'

// Matches the EntityName rules in the contract (see api/openapi.yaml):
// whitespace is the same set as Go's unicode.IsSpace; length is counted in Unicode code points.
const SPACE = '\\u0009-\\u000D\\u0020\\u0085\\u00A0\\u1680\\u2000-\\u200A\\u2028\\u2029\\u202F\\u205F\\u3000'
const EDGE_SPACE = new RegExp(`^[${SPACE}]|[${SPACE}]$`)
// The naming rule is precisely about finding control characters.
// eslint-disable-next-line no-control-regex
const CONTROL = /[\u0000-\u001F\u007F-\u009F]/
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
export const MAX_NAME_LENGTH = 100

// The naming rules for organization, project and Agent names. Used for a local pre-check; the server's validation has the final word.
export function nameRuleError(name: string): string | undefined {
  if ([...name].length > MAX_NAME_LENGTH) return t.createAgent.nameRules.tooLong
  if (name.includes('/') || name.includes('#')) return t.createAgent.nameRules.separator
  if (name.startsWith('@')) return t.createAgent.nameRules.atStart
  if (CONTROL.test(name)) return t.createAgent.nameRules.control
  if (EDGE_SPACE.test(name)) return t.createAgent.nameRules.whitespace
  if (UUID.test(name)) return t.createAgent.nameRules.uuid
  return undefined
}
