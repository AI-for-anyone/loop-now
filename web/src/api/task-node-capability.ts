import { useQuery } from '@tanstack/react-query'
import { isMissingEndpoint } from './backend-signals'
import { api, call } from './client'

export const nodeCapabilityKey = (taskId: string, nodeRunId: string) => ['taskNodeCapability', taskId, nodeRunId] as const
// Only a missing endpoint (an older server; see isMissingEndpoint) means unsupported.
// A missing task/node, permission denial or unknown error remains a real error.
export function nodeMessagesUnsupported(error: unknown) {
  return isMissingEndpoint(error)
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
