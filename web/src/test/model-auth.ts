import type { Schemas } from '@/api/client'

export const codexProvider: Schemas['ModelProvider'] = { id: 'openai-codex', apiKeyEnvVars: [], modelCount: 1, authMethods: ['oauth'] }
export function makeModelAccount(overrides: Partial<Schemas['CredentialMeta']> = {}): Schemas['CredentialMeta'] {
  return { status: 'set', updatedAt: '2026-10-14T08:00:00Z', fieldNames: [], envNames: [], filePaths: [], warnings: [], provider: 'openai-codex', authMethod: 'oauth', versionId: '00000000-0000-7000-8000-000000000301', accountLabel: 'fixture-old@example.test', ...overrides }
}
export function makeModelLogin(agentId: string, overrides: Partial<Schemas['ModelAuthOperation']> = {}): Schemas['ModelAuthOperation'] {
  return { id: '00000000-0000-7000-8000-000000000302', agentId, provider: 'openai-codex', configVersion: 1, status: 'awaiting_authorization', startedAt: '2026-10-14T08:00:00Z', expiresAt: '2099-10-14T09:00:00Z', completedAt: null, authorizationUrl: 'https://auth.openai.com/oauth/authorize?client_id=fixture-only', errorCode: null, errorMessage: null, ...overrides }
}
