import { backendSignals } from './backend-signals'
import { focusManager, useQuery, type QueryClient } from '@tanstack/react-query'
import type { Readable } from 'openapi-typescript-helpers'
import { config } from '@/config'
import { ApiError, api, call, type Schemas } from './client'

// 仅用于账号列表能力探测，不应用于单账号资源的404。
export function projectCredentialsUnsupported(error: unknown) {
  return error instanceof ApiError && error.status === 404 && error.code === 'not_found' && error.serverMessage === backendSignals.missingEndpoint
}

export const credentialKeys = {
  project: (projectId: string) => ['project-credentials', projectId] as const,
  selections: (agentId: string) => ['credential-selections', agentId] as const,
  operations: (projectId: string) => ['project-model-auth', projectId] as const,
}
export function useProjectAccounts(projectId: string) {
  return useQuery({ queryKey: credentialKeys.project(projectId), queryFn: ({ signal }) => call(api.GET('/projects/{projectId}/credential-accounts', { params: { path: { projectId } }, signal })), retry: false, refetchOnWindowFocus: true, refetchInterval: config.agentListPollMs })
}
export function readProjectAccount(projectId: string, accountId: string) {
  return call(api.GET('/projects/{projectId}/credential-accounts/{accountId}', { params: { path: { projectId, accountId } } }))
}
export function createProjectAccount(projectId: string, body: Schemas['ProjectCredentialCreate']) {
  return call(api.POST('/projects/{projectId}/credential-accounts', { params: { path: { projectId } }, body }))
}
export function renameProjectAccount(projectId: string, accountId: string, name: string, expectedRevision: number) {
  return call(api.PATCH('/projects/{projectId}/credential-accounts/{accountId}', { params: { path: { projectId, accountId } }, body: { name, expectedRevision } }))
}
export function replaceProjectAccount(projectId: string, accountId: string, credential: Schemas['ProjectCredentialInput'], expectedRevision: number) {
  return call(api.PUT('/projects/{projectId}/credential-accounts/{accountId}/credential', { params: { path: { projectId, accountId } }, body: { credential, expectedRevision } }))
}
export function deleteProjectAccount(projectId: string, accountId: string, expectedRevision: number) {
  return call(api.DELETE('/projects/{projectId}/credential-accounts/{accountId}', { params: { path: { projectId, accountId }, query: { expectedRevision } } }))
}
// openapi-fetch的Readable会剔除纯null属性（NonNullable<null>=never）。
// 按生成的正式mode分支恢复必为null的字段，保持组件直接使用完整正式DTO；不修改生成类型。
function selectionResponse(data: Readable<Schemas['CredentialSelections']>): Schemas['CredentialSelections'] {
  return { ...data, selections: data.selections.map(item => item.mode === 'explicit' ? item : item.mode === 'legacy_cleared' ? { ...item, selectedAccountId: null, resolvedAccountId: null, resolvedVersionId: null } : { ...item, selectedAccountId: null }) }
}
export async function readSelections(agentId: string, signal?: AbortSignal) {
  return selectionResponse(await call(api.GET('/agents/{agentId}/credential-selections', { params: { path: { agentId } }, signal })))
}
export function useCredentialSelections(agentId: string, enabled = true) {
  return useQuery({ queryKey: credentialKeys.selections(agentId), queryFn: ({ signal }) => readSelections(agentId, signal), enabled, refetchInterval: config.agentListPollMs })
}
export async function putSelection(agentId: string, body: Schemas['CredentialSelectionPut']) {
  return selectionResponse(await call(api.PUT('/agents/{agentId}/credential-selections', { params: { path: { agentId } }, body })))
}
export function useCredentialImpact(projectId: string, purpose: Schemas['ProjectCredentialPurpose'], slot: string, accountId?: string, enabled = true) {
  return useQuery({ queryKey: [...credentialKeys.project(projectId), 'impact', purpose, slot, accountId ?? null], queryFn: ({ signal }) => call(api.GET('/projects/{projectId}/credential-account-impact', { params: { path: { projectId }, query: purpose === 'model' ? { purpose, provider: slot, accountId } : { purpose, cli: slot, accountId } }, signal })), enabled: enabled && !!slot, staleTime: 0 })
}
export function useProjectLogins(projectId: string, enabled = true) {
  return useQuery({ queryKey: credentialKeys.operations(projectId), queryFn: ({ signal }) => call(api.GET('/projects/{projectId}/model-auth/operations', { params: { path: { projectId }, query: { provider: 'openai-codex' } }, signal })), enabled, gcTime: 0, retry: false, refetchOnMount: 'always', refetchOnWindowFocus: 'always', refetchIntervalInBackground: true, refetchInterval: query => focusManager.isFocused() || query.state.data?.operations.some(projectLoginActive) ? 1500 : false })
}
export const projectLoginActive = (op: Schemas['ProjectModelAuthOperation']) => op.status === 'pending' || op.status === 'awaiting_authorization'
export function startProjectLogin(projectId: string, body: Schemas['ProjectModelAuthStart']) {
  return call(api.POST('/projects/{projectId}/model-auth/operations', { params: { path: { projectId } }, body }))
}
export function readProjectLogin(projectId: string, operationId: string, signal?: AbortSignal) {
  return call(api.GET('/projects/{projectId}/model-auth/operations/{operationId}', { params: { path: { projectId, operationId } }, signal }))
}
export function cancelProjectLogin(projectId: string, operationId: string) {
  return call(api.POST('/projects/{projectId}/model-auth/operations/{operationId}/cancel', { params: { path: { projectId, operationId } } }))
}
export async function invalidateProjectCredentials(client: QueryClient, projectId: string) {
  await Promise.all([
    client.invalidateQueries({ queryKey: credentialKeys.project(projectId) }),
    client.invalidateQueries({ queryKey: ['credential-selections'] }),
    client.invalidateQueries({ queryKey: ['db-connections'] }),
    client.invalidateQueries({ queryKey: ['agent'] }),
    client.invalidateQueries({ queryKey: ['agents'] }),
  ])
}
