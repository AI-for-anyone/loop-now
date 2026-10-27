import type { Schemas } from '@/api/client'
import { defaultProject } from './api'

export const githubOperationId = '31000000-0000-7000-8000-000000000001'
type Operation = Extract<Schemas['CLIAuthOperation'], { cli: 'gh' }>
type Target = { targetAccountId: null; targetRevision: null } | { targetAccountId: string; targetRevision: number }
export function githubOperation(status: Operation['status'] = 'awaiting_authorization', id = githubOperationId, target: Target = { targetAccountId: null, targetRevision: null }): Operation {
  const base = { id, projectId: defaultProject.id, cli: 'gh' as const, authType: 'github_user' as const, ...target, name: null, accountId: null, startedAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 300000).toISOString(), completedAt: null, authorizationUrl: null, userCode: null, errorCode: null, errorMessage: null }
  if (status === 'pending') return { ...base, status }
  if (status === 'awaiting_authorization') return { ...base, status, authorizationUrl: 'https://github.com/login/device', userCode: 'ABCD-1234' }
  if (status === 'succeeded') return { ...base, status, accountId: target.targetAccountId ?? '10000000-0000-7000-8000-000000000001', completedAt: new Date().toISOString() }
  return { ...base, status, completedAt: new Date().toISOString() }
}
export function githubAccount(scopes: string[] | null = ['repo', 'read:org']): Schemas['ProjectCredentialAccount'] {
  return { id: '10000000-0000-7000-8000-000000000001', projectId: defaultProject.id, purpose: 'cli', provider: null, cli: 'gh', name: '保留的备注', accountLabel: null, revision: 2, currentVersionId: '20000000-0000-7000-8000-000000000002', authMethod: 'oauth', status: 'logged_in', template: null, createdAt: '2026-10-18T00:00:00Z', updatedAt: '2026-10-18T01:00:00Z', deletedAt: null, githubIdentity: { host: 'github.com', userId: '9007199254740993', login: 'verified-new-user', grantedScopes: scopes, verifiedAt: '2026-10-18T01:00:00Z' } }
}
