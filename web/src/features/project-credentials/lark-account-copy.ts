import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { accountStatus } from './account-copy'

export function larkAccountOptionLabel(account: Schemas['ProjectCredentialAccount']) {
  return `${account.name} · ${account.authMethod === 'oauth' ? t.credentials.lark.user : t.credentials.lark.bot} · ${accountStatus[account.status]}${account.larkIdentity ? ` · ${account.larkIdentity.displayName}` : ''}`
}
