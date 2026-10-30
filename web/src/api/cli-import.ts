import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, call, type Schemas } from './client'
import { backendSignals } from './backend-signals'

export type ImportOperation = Schemas['CLIImportOperation']
export const importKey = (projectId: string, operationId: string) => ['cli-import', projectId, operationId] as const
export const importActive = (op: ImportOperation) => op.status === 'pending' || op.status === 'running'
export function mergeImport(previous: ImportOperation | undefined, next: ImportOperation): ImportOperation {
  return previous?.id === next.id && !importActive(previous) ? previous : next
}
export const importUnsupported = (e: unknown) => e instanceof ApiError && e.status === 404 && e.code === 'not_found' && e.serverMessage === backendSignals.missingEndpoint
export function scanCLIImports(projectId: string) {
  return call(api.POST('/projects/{projectId}/cli-credential-import/scans', { params: { path: { projectId } }, body: {} }))
}
export async function startCLIImport(projectId: string, body: Schemas['CLIImportStart']) {
  return (await call(api.POST('/projects/{projectId}/cli-credential-import/imports', { params: { path: { projectId } }, body }))).operation
}
export async function cancelCLIImport(projectId: string, operationId: string) {
  return (await call(api.POST('/projects/{projectId}/cli-credential-import/imports/{operationId}/cancel', { params: { path: { projectId, operationId } }, body: {} }))).operation
}
export function useCLIImport(projectId: string, operationId: string | null, enabled: boolean) {
  const client = useQueryClient(), key = importKey(projectId, operationId ?? '')
  return useQuery<ImportOperation>({ queryKey: key, enabled: enabled && !!operationId, retry: false, refetchOnWindowFocus: true, refetchIntervalInBackground: true,
    refetchInterval: query => query.state.data && importActive(query.state.data) && Date.now() < Date.parse(query.state.data.deadlineAt) + 5000 ? 1500 : false,
    queryFn: async ({ signal }) => {
      const next = (await call(api.GET('/projects/{projectId}/cli-credential-import/imports/{operationId}', { params: { path: { projectId, operationId: operationId! } }, signal }))).operation
      return mergeImport(client.getQueryData<ImportOperation>(key), next)
    },
  })
}
