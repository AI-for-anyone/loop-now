import type { Schemas } from '@/api/client'
import { projectAccount } from './project-credentials'

export const larkOperationId = '30000000-0000-7000-8000-000000000091'
export function larkApplicationAccount() {
  return projectAccount({ purpose: 'cli', provider: null, cli: 'lark-cli', authMethod: null, name: 'QA 应用', larkApplication: { brand: 'feishu', appId: 'fixture-app' } })
}
export function larkUserAccount(): Schemas['ProjectCredentialAccount'] {
  return { ...projectAccount(), id: '10000000-0000-7000-8000-000000000092', purpose: 'cli', provider: null, cli: 'lark-cli', authMethod: 'oauth', name: '人的备注', status: 'logged_in', currentVersionId: '20000000-0000-7000-8000-000000000092', larkApplication: { brand: 'feishu', appId: 'fixture-app' }, larkIdentity: { brand: 'feishu', appId: 'fixture-app', openId: 'fixture-open-id', displayName: '核实的飞书用户', grantedScopes: null, verifiedAt: '2026-10-20T00:00:00Z' } }
}
type LarkOperation = Extract<Schemas['CLIAuthOperation'], { cli: 'lark-cli' }>
export function larkOperation(status: LarkOperation['status'] = 'awaiting_authorization', id = larkOperationId): LarkOperation {
  const source = larkApplicationAccount()
  const base = { id, projectId: source.projectId, cli: 'lark-cli' as const, authType: 'lark_user' as const, targetAccountId: null, targetRevision: null, name: null, startedAt: '2026-10-20T00:00:00Z', expiresAt: '2030-01-01T00:00:00Z', sourceAccountId: source.id, sourceVersionId: source.currentVersionId, sourceRevision: source.revision }
  switch (status) {
    case 'pending': return { ...base, status, accountId: null, completedAt: null, authorizationUrl: null, userCode: null, errorCode: null, errorMessage: null }
    case 'awaiting_authorization': return { ...base, status, accountId: null, completedAt: null, authorizationUrl: 'https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=synthetic-flow', userCode: null, errorCode: null, errorMessage: null }
    case 'succeeded': return { ...base, status, accountId: larkUserAccount().id, completedAt: '2026-10-20T00:01:00Z', authorizationUrl: null, userCode: null, errorCode: null, errorMessage: null }
    default: return { ...base, status, accountId: null, completedAt: '2026-10-20T00:01:00Z', authorizationUrl: null, userCode: null, errorCode: 'oauth_failed', errorMessage: null }
  }
}
