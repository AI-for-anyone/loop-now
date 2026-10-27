import { t } from '@/i18n'

// 与契约中 EntityName 的规则一致（见 api/openapi.yaml）：
// 空白的范围与 Go 的 unicode.IsSpace 相同；长度按 Unicode 码点计。
const SPACE = '\\u0009-\\u000D\\u0020\\u0085\\u00A0\\u1680\\u2000-\\u200A\\u2028\\u2029\\u202F\\u205F\\u3000'
const EDGE_SPACE = new RegExp(`^[${SPACE}]|[${SPACE}]$`)
// 命名规则本身就是要找出控制字符。
// eslint-disable-next-line no-control-regex
const CONTROL = /[\u0000-\u001F\u007F-\u009F]/
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
export const MAX_NAME_LENGTH = 100

// 组织名、项目名与 Agent 名的命名规则。本地预检用，最终以服务端的校验结果为准。
export function nameRuleError(name: string): string | undefined {
  if ([...name].length > MAX_NAME_LENGTH) return t.createAgent.nameRules.tooLong
  if (name.includes('/') || name.includes('#')) return t.createAgent.nameRules.separator
  if (name.startsWith('@')) return t.createAgent.nameRules.atStart
  if (CONTROL.test(name)) return t.createAgent.nameRules.control
  if (EDGE_SPACE.test(name)) return t.createAgent.nameRules.whitespace
  if (UUID.test(name)) return t.createAgent.nameRules.uuid
  return undefined
}
