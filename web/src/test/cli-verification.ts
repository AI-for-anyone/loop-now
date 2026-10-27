import type { Schemas } from '@/api/client'
import { projectAccount, type AccountWithoutGitHubIdentity } from './project-credentials'
export const verificationAccount = (overrides: Partial<AccountWithoutGitHubIdentity> = {}) => projectAccount({ purpose: 'cli', provider: null, cli: 'mysql', authMethod: null, name: '数据库账号', ...overrides })
export function verificationOperation(overrides: Partial<Schemas['CLIVerificationOperation']> = {}): Schemas['CLIVerificationOperation'] {
  const account = verificationAccount()
  return { id: '30000000-0000-7000-8000-000000000077', projectId: account.projectId, accountId: account.id, versionId: account.currentVersionId!, cli: 'mysql', authMethod: 'static', templateSource: 'builtin', credentialFormat: '1', templateFingerprint: 'a'.repeat(64), toolVersion: null, adapterRevision: '1', probeKind: 'database_select1', status: 'pending', phase: 'preparing_tool', startedAt: '2026-10-16T10:00:00Z', expiresAt: '2030-10-16T10:00:00Z', completedAt: null, result: null, isCurrentVersion: true, ...overrides } as Schemas['CLIVerificationOperation']
}
export function verificationPassed(overrides: Partial<Schemas['CLIVerificationOperation']> = {}) {
  return { ...verificationOperation(overrides), status: 'succeeded', phase: null, completedAt: '2026-10-16T10:00:10Z', toolVersion: '8.4.9', result: { outcome: 'passed', verifiedAt: '2026-10-16T10:00:10Z', durationMs: 1000, errorCode: null, httpStatus: null } } as Schemas['CLIVerificationOperation']
}
