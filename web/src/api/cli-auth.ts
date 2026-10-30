import { focusManager, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import type { Readable } from 'openapi-typescript-helpers'
import { api, ApiError, call, type Schemas } from './client'
import { t } from '@/i18n'

export type CLIAuthCLI = Schemas['CLIAuthOperation']['cli']
export type GitHubOperation = Extract<Schemas['CLIAuthOperation'], { cli: 'gh' }>

export const cliAuthKeys = {
  project: (projectId: string, cli: CLIAuthCLI = 'gcloud') => ['project-cli-auth', projectId, cli] as const,
  operation: (projectId: string, id: string | null, cli: CLIAuthCLI = 'gcloud') => ['project-cli-auth', projectId, cli, 'operation', id] as const,
  terminal: (projectId: string, id: string, cli: CLIAuthCLI) => ['project-cli-auth-terminal', projectId, cli, id] as const,
}
export function cliAuthActive(operation: Pick<Schemas['CLIAuthOperation'], 'status'>) {
  return operation.status === 'pending' || operation.status === 'awaiting_authorization'
}
// Readable removes pure-null properties from its TypeScript view, not the JSON.
// Restore only the nulls fixed by each generated branch, without casting the
// whole response or adding GitHub-only keys to the existing Google response.
function operationResponse(operation: Readable<Schemas['CLIAuthOperation']>): Schemas['CLIAuthOperation'] {
  if (operation.cli === 'gcloud') return operation.status === 'pending' || operation.status === 'awaiting_authorization' ? operation : { ...operation, authorizationUrl: null }
  const targetAccountId = 'targetAccountId' in operation ? operation.targetAccountId ?? null : null
  const targetRevision = 'targetRevision' in operation ? operation.targetRevision ?? null : null
  const target = typeof targetAccountId === 'string' && typeof targetRevision === 'number'
    ? { targetAccountId, targetRevision }
    : targetAccountId === null && targetRevision === null
      ? { targetAccountId: null, targetRevision: null }
      : null
  if (!target) throw new Error('Invalid CLI auth target pair')
  switch (operation.status) {
    case 'pending':
      return { ...operation, ...target, name: null, accountId: null, completedAt: null, authorizationUrl: null, errorCode: null, errorMessage: null, userCode: null }
    case 'awaiting_authorization':
      return { ...operation, ...target, name: null, accountId: null, completedAt: null, errorCode: null, errorMessage: null }
    case 'succeeded':
      return { ...operation, ...target, name: null, authorizationUrl: null, errorCode: null, errorMessage: null, userCode: null }
    default:
      return { ...operation, ...target, name: null, accountId: null, authorizationUrl: null, userCode: null }
  }
}
function stateResponse(state: Readable<Schemas['CLIAuthState']>): Schemas['CLIAuthState'] {
  return { ...state, operation: operationResponse(state.operation) }
}
function absorbOperation(client: QueryClient, next: Schemas['CLIAuthOperation']) {
  const previous = client.getQueryData<Schemas['CLIAuthOperation']>(cliAuthKeys.terminal(next.projectId, next.id, next.cli))
    ?? client.getQueryData<Schemas['CLIAuthState']>(cliAuthKeys.operation(next.projectId, next.id, next.cli))?.operation
  const operation = previous && !cliAuthActive(previous) ? previous : next
  if (!cliAuthActive(operation)) {
    // The unobserved terminal entry contains no short-lived fields. It uses the
    // QueryClient's normal bounded in-memory GC, never browser persistence.
    client.setQueryData(cliAuthKeys.terminal(operation.projectId, operation.id, operation.cli), operation)
    client.setQueryData<Schemas['CLIAuthOperations']>(cliAuthKeys.project(operation.projectId, operation.cli), old => old ? { operations: old.operations.filter(item => item.id !== operation.id) } : old)
  }
  return operation
}
export function acceptCLIAuthState(client: QueryClient, projectId: string, cli: CLIAuthCLI, id: string, next: Schemas['CLIAuthState']) {
  if (next.operation.projectId !== projectId || next.operation.cli !== cli || next.operation.id !== id) throw new ApiError({ status: 502, code: 'unknown', serverMessage: t.credentials.github.mismatch })
  const key = cliAuthKeys.operation(projectId, id, cli), previous = client.getQueryData<Schemas['CLIAuthState']>(key)
  const operation = absorbOperation(client, next.operation)
  const state = { ...next, operation, account: operation !== next.operation ? previous?.account ?? null : next.account }
  client.setQueryData(key, state)
  return state
}
export function useCLIAuthOperation(projectId: string, cli: CLIAuthCLI, id: string | null, enabled: boolean, validate?: (state: Schemas['CLIAuthState']) => void) {
  const client = useQueryClient()
  return useQuery({
    queryKey: cliAuthKeys.operation(projectId, id, cli), enabled: enabled && !!id, retry: false, gcTime: 0, refetchIntervalInBackground: true,
    queryFn: async ({ signal }) => {
      if (!id) throw new Error('Missing CLI auth operation ID')
      const state = await readCLIAuth(projectId, id, signal)
      validate?.(state)
      return acceptCLIAuthState(client, projectId, cli, id, state)
    },
    refetchInterval: query => cli === 'lark-cli' && query.state.error ? false : query.state.data && cliAuthActive(query.state.data.operation) && Date.now() < Date.parse(query.state.data.operation.expiresAt) + 5000 ? 1500 : false,
  })
}
export function useProjectCLIAuth(projectId: string, enabled: boolean, cli: CLIAuthCLI = 'gcloud') {
  const client = useQueryClient()
  return useQuery({
    queryKey: cliAuthKeys.project(projectId, cli),
    queryFn: async ({ signal }) => {
      const data = await call(api.GET('/projects/{projectId}/cli-auth/operations', { params: { path: { projectId }, ...(cli !== 'gcloud' ? { query: { cli } } : {}) }, signal, cache: 'no-store' }))
      const operations = data.operations.map(operationResponse).map(operation => {
        if (operation.projectId !== projectId || operation.cli !== cli) throw new ApiError({ status: 502, code: 'unknown', serverMessage: t.credentials.github.mismatch })
        return absorbOperation(client, operation)
      }).filter(cliAuthActive)
      return { operations }
    },
    enabled, retry: false, gcTime: 0, refetchIntervalInBackground: true,
    refetchInterval: query => cli === 'gcloud'
      ? focusManager.isFocused() || query.state.data?.operations.some(cliAuthActive) ? 1500 : false
      : query.state.data?.operations.some(op => cliAuthActive(op) && Date.now() < Date.parse(op.expiresAt) + 5000) ? 1500 : false,
  })
}
export async function readCLIAuth(projectId: string, operationId: string, signal?: AbortSignal) {
  return stateResponse(await call(api.GET('/projects/{projectId}/cli-auth/operations/{operationId}', { params: { path: { projectId, operationId } }, signal })))
}
export async function startCLIAuth(projectId: string, body: Schemas['CLIAuthStart']) {
  return stateResponse(await call(api.POST('/projects/{projectId}/cli-auth/operations', { params: { path: { projectId } }, body })))
}
export async function cancelCLIAuth(projectId: string, operationId: string) {
  return stateResponse(await call(api.POST('/projects/{projectId}/cli-auth/operations/{operationId}/cancel', { params: { path: { projectId, operationId } } })))
}
