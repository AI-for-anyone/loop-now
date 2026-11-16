import { isMissingEndpoint } from './backend-signals'
import { focusManager, useQuery, type QueryClient } from '@tanstack/react-query'
import type { Readable } from 'openapi-typescript-helpers'
import { config } from '@/config'
import { api, call, type Schemas } from './client'

// Only for detecting whether the account list is supported; must not be used for a 404 on a single-account resource.
export function projectCredentialsUnsupported(error: unknown) {
  return isMissingEndpoint(error)
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
// openapi-fetch's Readable drops properties that can only be null (NonNullable<null> = never).
// Restore the fields that must be null per branch of the generated mode union, so components keep using the complete DTO as defined;
// the generated types are not modified.
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
// All logins in progress in the project (Codex and plugin providers), each with its provider; matching compares both provider and
// targetAccountId.
export function useProjectLogins(projectId: string, enabled = true) {
  return useQuery({ queryKey: credentialKeys.operations(projectId), queryFn: ({ signal }) => call(api.GET('/projects/{projectId}/model-auth/operations', { params: { path: { projectId } }, signal })), enabled, gcTime: 0, retry: false, refetchOnMount: 'always', refetchOnWindowFocus: 'always', refetchIntervalInBackground: true, refetchInterval: query => focusManager.isFocused() || query.state.data?.operations.some(projectLoginActive) ? 1500 : false })
}
export const projectLoginActive = (op: Schemas['ProjectModelAuthOperation']) => op.status === 'pending' || op.status === 'awaiting_authorization'
export function startProjectLogin(projectId: string, body: Schemas['ProjectModelAuthStart']) {
  return call(api.POST('/projects/{projectId}/model-auth/operations', { params: { path: { projectId } }, body }))
}
export function readProjectLogin(projectId: string, operationId: string, signal?: AbortSignal) {
  return call(api.GET('/projects/{projectId}/model-auth/operations/{operationId}', { params: { path: { projectId, operationId } }, signal }))
}
// Answer a question asked partway through a login: the answer is treated as a secret and sent directly, without the mutation cache and
// without logging.
export function answerProjectLogin(projectId: string, operationId: string, body: Schemas['ProjectModelLoginInput']) {
  return call(api.POST('/projects/{projectId}/model-auth/operations/{operationId}/input', { params: { path: { projectId, operationId } }, body }))
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
