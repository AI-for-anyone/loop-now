import { t } from '@/i18n'
import type { Schemas } from '@/api/client'

export const accountStatus: Record<Schemas['ProjectCredentialStatus'], string> = {
  saved: t.credentials.accountCopy.statusSaved, logged_in: t.credentials.accountCopy.statusLoggedIn, unavailable: t.credentials.accountCopy.statusUnavailable, requires_login: t.credentials.accountCopy.statusRequiresLogin, deleted: t.credentials.accountCopy.statusDeleted,
}
export function accountSummary(account: Schemas['ProjectCredentialAccount']) {
  return t.credentials.accountCopy.summary(account.name, accountStatus[account.status], account.revision, account.updatedAt)
}
export function selectionDescription(selection: Schemas['CredentialSelection']) {
  if (selection.mode === 'legacy_cleared') return t.credentials.accountCopy.legacyCleared
  const status: Record<Schemas['CredentialSelectionStatus'], string> = {
    resolved: selection.mode === 'explicit' ? t.credentials.accountCopy.resolvedExplicit : t.credentials.accountCopy.resolvedAutomatic,
    not_configured: t.credentials.accountCopy.notConfigured,
    selection_required: t.credentials.accountCopy.selectionRequired,
    account_deleted: t.credentials.accountCopy.accountDeleted,
    scope_mismatch: t.credentials.accountCopy.scopeMismatch,
    unavailable: t.credentials.accountCopy.unavailable,
    requires_login: t.credentials.accountCopy.requiresLogin,
  }
  return status[selection.status]
}
