import type { Schemas } from '@/api/client'
import { larkText } from '@/i18n/lark'
import { accountStatus } from './account-copy'

export function larkAccountOptionLabel(account: Schemas['ProjectCredentialAccount']) {
  return `${account.name} · ${account.authMethod === 'oauth' ? larkText.user : larkText.bot} · ${accountStatus[account.status]}${account.larkIdentity ? ` · ${account.larkIdentity.displayName}` : ''}`
}
