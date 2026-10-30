import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { Readable } from 'openapi-typescript-helpers'
import { api, ApiError, call, type Schemas } from './client'
import { backendSignals } from './backend-signals'

export type Verification = Schemas['CLIVerificationOperation']
export const verificationKeys = {
  account: (project: string, account: string, version: string) => ['cli-verification', project, account, version] as const,
  state: (project: string, account: string, version: string) => [...verificationKeys.account(project, account, version), 'state'] as const,
  operation: (project: string, account: string, version: string, id: string) => [...verificationKeys.account(project, account, version), id] as const,
}
export const verificationActive = (op: Verification) => op.status === 'pending' || op.status === 'running'
export function mergeVerification(previous: Verification | undefined, next: Verification): Verification {
  if (!previous || previous.id !== next.id) return next
  // Terminal facts cannot be undone by a GET which began before cancel/completion.
  return !verificationActive(previous) ? previous : next
}
export function verificationUnsupported(error: unknown) {
  return error instanceof ApiError && error.status === 404 && error.code === 'not_found' && error.serverMessage === backendSignals.missingEndpoint
}
// openapi-fetch Readable removes pure-null oneOf properties from its TypeScript view.
// The frozen wire DTO requires every property; the actual JSON retains these nulls.
function operation(data: Readable<Verification>): Verification {
  return data as Verification
}

const path = '/projects/{projectId}/credential-accounts/{accountId}/verification-operations' as const
export async function startVerification(projectId: string, accountId: string, body: Schemas['CLIVerificationStart']) {
  return operation(await call(api.POST(path, { params: { path: { projectId, accountId } }, body })))
}
export async function cancelVerification(projectId: string, accountId: string, operationId: string) {
  return operation(await call(api.POST('/projects/{projectId}/credential-accounts/{accountId}/verification-operations/{operationId}/cancel', { params: { path: { projectId, accountId, operationId } } })))
}
export function verificationInterval(op: Verification | null | undefined) {
  return op && verificationActive(op) && Date.now() < Date.parse(op.expiresAt) + 5000 ? 1500 : false
}
export function useVerificationState(projectId: string, accountId: string, version: string, enabled: boolean) {
  const client = useQueryClient(), key = verificationKeys.state(projectId, accountId, version)
  return useQuery<Schemas['CLIVerificationState']>({ queryKey: key, enabled, retry: false, refetchOnWindowFocus: true, refetchIntervalInBackground: true,
    refetchInterval: query => verificationInterval(query.state.data?.operation),
    queryFn: async ({ signal }): Promise<Schemas['CLIVerificationState']> => {
      const data = await call(api.GET(path, { params: { path: { projectId, accountId } }, signal }))
      let next = data.operation ? operation(data.operation) : null
      const previous = client.getQueryData<Schemas['CLIVerificationState']>(key)?.operation
      if (!next && data.currentVersionId === version && previous) next = previous
      if (next) {
        next = mergeVerification(client.getQueryData<Verification>(verificationKeys.operation(projectId, accountId, version, next.id)) ?? (previous?.id === next.id ? previous : undefined), next)
        if (previous && previous.id !== next.id && previous.startedAt > next.startedAt) next = previous
      }
      return { ...data, currentVersionId: data.currentVersionId ?? null, operation: next }
    },
  })
}
export function useVerificationOperation(projectId: string, accountId: string, version: string, id: string | null, enabled: boolean) {
  const client = useQueryClient(), key = verificationKeys.operation(projectId, accountId, version, id ?? '')
  return useQuery<Verification>({ queryKey: key, enabled: enabled && !!id, retry: false, refetchOnWindowFocus: true, refetchIntervalInBackground: true,
    refetchInterval: query => verificationInterval(query.state.data),
    queryFn: async ({ signal }) => {
      const next = operation(await call(api.GET('/projects/{projectId}/credential-accounts/{accountId}/verification-operations/{operationId}', { params: { path: { projectId, accountId, operationId: id! } }, signal })))
      return mergeVerification(client.getQueryData<Verification>(key), next)
    },
  })
}
