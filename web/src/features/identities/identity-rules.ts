import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

// 外部身份的名称规则，与契约中的 ExternalIdentityName 一致：小写字母或数字开头，只含小写字母、数字、点、下划线与连字符，
// 最长 64 个字符；console 与 platform 是保留名称（控制台操作者与平台通知的地址）。
const NAME_PATTERN = /^[a-z0-9][a-z0-9._-]{0,63}$/
const RESERVED = ['console', 'platform']

export function identityNameError(name: string): string | undefined {
  if (name === '') return t.common.required
  if (!NAME_PATTERN.test(name)) return t.identities.nameInvalid
  if (RESERVED.includes(name)) return t.identities.nameReserved(name)
  return undefined
}

export type TokenState = 'active' | 'revoked' | 'expired'

export function tokenState(token: Schemas['ExternalToken'], now = Date.now()): TokenState {
  if (token.revokedAt) return 'revoked'
  if (token.expiresAt && new Date(token.expiresAt).getTime() <= now) return 'expired'
  return 'active'
}

// 签发时的有效期选项；预设天数由控制台换算为到期时间，“指定日期”取当天结束的时刻，“永不过期”不提交 expiresAt。
export type ExpiryChoice = 'never' | 'd7' | 'd30' | 'd90' | 'date'

const DAYS: Partial<Record<ExpiryChoice, number>> = { d7: 7, d30: 30, d90: 90 }

export const defaultExpiry = (days: number): ExpiryChoice => (days === 7 ? 'd7' : days === 90 ? 'd90' : 'd30')

// 返回到期时间（ISO 字符串），永不过期时为 null；指定的日期为空或早于今天时为 undefined。
export function expiresAtOf(choice: ExpiryChoice, date: string, now = new Date()): string | null | undefined {
  if (choice === 'never') return null
  const days = DAYS[choice]
  if (days !== undefined) return new Date(now.getTime() + days * 24 * 60 * 60 * 1000).toISOString()
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (!match) return undefined
  const end = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]), 23, 59, 59, 999)
  return end.getTime() > now.getTime() ? end.toISOString() : undefined
}

// 日期输入框的最小值：今天（本地时间）。
export function todayInput(now = new Date()): string {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}
