import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

// Naming rules for external identities, matching ExternalIdentityName in the contract: starts with a lowercase letter or digit,
// contains only lowercase letters, digits, dots, underscores and hyphens, at most 64 characters; console and platform are
// reserved names (the addresses of the console operator and of platform notifications).
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

// Expiry options when issuing; the console converts the preset day counts into an expiry time, “指定日期” (Pick a date) uses the
// end of that day, and “永不过期” (Never expires) doesn't send expiresAt.
export type ExpiryChoice = 'never' | 'd7' | 'd30' | 'd90' | 'date'

const DAYS: Partial<Record<ExpiryChoice, number>> = { d7: 7, d30: 30, d90: 90 }

export const defaultExpiry = (days: number): ExpiryChoice => (days === 7 ? 'd7' : days === 90 ? 'd90' : 'd30')

// Returns the expiry time (ISO string), null for never expires, and undefined when the picked date is empty or before today.
export function expiresAtOf(choice: ExpiryChoice, date: string, now = new Date()): string | null | undefined {
  if (choice === 'never') return null
  const days = DAYS[choice]
  if (days !== undefined) return new Date(now.getTime() + days * 24 * 60 * 60 * 1000).toISOString()
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  if (!match) return undefined
  const end = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]), 23, 59, 59, 999)
  return end.getTime() > now.getTime() ? end.toISOString() : undefined
}

// The minimum value of the date input: today (local time).
export function todayInput(now = new Date()): string {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}
