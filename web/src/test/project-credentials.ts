import { http, HttpResponse } from 'msw'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject } from './api'

// Existing fixtures have no verified OAuth identity. Keep both generated null
// branches so CLI/name overrides cannot accidentally imply another identity.
export type AccountWithoutGitHubIdentity = Extract<Schemas['ProjectCredentialAccount'], { githubIdentity?: null; larkIdentity?: null }>
export function projectAccount(overrides: Partial<AccountWithoutGitHubIdentity> = {}): AccountWithoutGitHubIdentity {
  return { id: '10000000-0000-7000-8000-000000000001', projectId: defaultProject.id, purpose: 'model', provider: 'anthropic', cli: null, name: '项目账号A', accountLabel: null, revision: 1, currentVersionId: '20000000-0000-7000-8000-000000000001', authMethod: 'api_key', status: 'saved', template: null, createdAt: '2026-10-15T01:00:00Z', updatedAt: '2026-10-15T01:00:00Z', deletedAt: null, ...overrides }
}
export function projectOperation(overrides: Partial<Schemas['ProjectModelAuthOperation']> = {}): Schemas['ProjectModelAuthOperation'] {
  return { id: '30000000-0000-7000-8000-000000000001', projectId: defaultProject.id, provider: 'openai-codex', targetAccountId: null, targetRevision: null, name: '新Codex账号', status: 'awaiting_authorization', accountId: null, startedAt: '2026-10-15T01:00:00Z', expiresAt: '2026-10-15T02:00:00Z', completedAt: null, authorizationUrl: 'https://auth.openai.com/oauth/authorize?client_id=fixture-only&state=fixture&code_challenge=fixture&id_token_add_organizations=true', errorCode: null, errorMessage: null, ...overrides }
}
export function emptySelections(agent: Schemas['Agent']): Schemas['CredentialSelections'] {
  return { agentId: agent.id, projectId: agent.projectId!, configVersion: agent.configVersion, credentialsRevision: 1, selections: [
    { purpose: 'model', provider: agent.model.provider, cli: null, mode: 'automatic', selectedAccountId: null, resolvedAccountId: null, resolvedVersionId: null, status: 'not_configured', candidates: [] },
    ...agent.clis.filter(cli => cli.hasTemplate).map(cli => ({ purpose: 'cli' as const, provider: null, cli: cli.name, mode: 'automatic' as const, selectedAccountId: null, resolvedAccountId: null, resolvedVersionId: null, status: 'not_configured' as const, candidates: [] })),
  ] }
}
// 仅测试环境默认空列表，具体场景用server.use覆盖；不进入产品或实际App驱动。
export const emptyProjectCredentialHandlers = [
  // Unrelated credential fixtures predate the optional verification interface.
  // Tests exercising verification override this exact old-router response.
  http.get(apiUrl('/projects/:projectId/credential-accounts/:accountId/verification-operations'), () => HttpResponse.json({ error: { code: 'not_found', message: '接口不存在' } }, { status: 404 })),
  http.get(apiUrl('/projects/:projectId/credential-accounts'), ({ params }) => HttpResponse.json({ projectId: params.projectId, credentialsRevision: 1, accounts: [] })),
  http.get(apiUrl('/projects/:projectId/model-auth/operations'), () => HttpResponse.json({ operations: [] })),
]
