import { credentialText } from '@/i18n/credential-text'
import type { Schemas } from '@/api/client'

export const accountStatus: Record<Schemas['ProjectCredentialStatus'], string> = {
  saved: credentialText.accountCopyText1, logged_in: credentialText.accountCopyText2, unavailable: credentialText.accountCopyText3, requires_login: credentialText.accountCopyText4, deleted: credentialText.accountCopyText5,
}
export function accountSummary(account: Schemas['ProjectCredentialAccount']) {
  return credentialText.accountCopyText6(account.name, accountStatus[account.status], account.revision, account.updatedAt)
}
export function selectionDescription(selection: Schemas['CredentialSelection']) {
  if (selection.mode === 'legacy_cleared') return credentialText.accountCopyText7
  const status: Record<Schemas['CredentialSelectionStatus'], string> = {
    resolved: selection.mode === 'explicit' ? credentialText.accountCopyText8 : credentialText.accountCopyText9,
    not_configured: credentialText.accountCopyText10,
    selection_required: credentialText.accountCopyText11,
    account_deleted: credentialText.accountCopyText12,
    scope_mismatch: credentialText.accountCopyText13,
    unavailable: credentialText.accountCopyText14,
    requires_login: credentialText.accountCopyText15,
  }
  return status[selection.status]
}
