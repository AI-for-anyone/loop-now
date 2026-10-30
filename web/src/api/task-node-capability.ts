import { useQuery } from '@tanstack/react-query'
import { backendSignals } from './backend-signals'
import { api, ApiError, call } from './client'

export const nodeCapabilityKey = (taskId: string, nodeRunId: string) => ['taskNodeCapability', taskId, nodeRunId] as const
// Only the old router's exact missing-interface response means unsupported.
// A missing task/node, permission denial or unknown error remains a real error.
export function nodeMessagesUnsupported(error: unknown) {
  return error instanceof ApiError && error.status === 404 && error.code === 'not_found' && error.serverMessage === backendSignals.missingEndpoint
}
export function useNodeMessageCapability(taskId: string, nodeRunId: string, enabled: boolean) {
  return useQuery({
    queryKey: nodeCapabilityKey(taskId, nodeRunId),
    queryFn: ({ signal }) => call(api.GET('/tasks/{taskId}/nodes/{nodeRunId}/message-capability', { signal, params: { path: { taskId, nodeRunId } } })),
    enabled,
    retry: false,
    refetchOnWindowFocus: true,
  })
}
