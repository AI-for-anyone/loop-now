import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { api, call, type Schemas } from './client'

// Permission modes and the requests an Agent sends to be approved (contract PermissionMode, PermissionRequest).
export const permissionKeys = {
  settings: ['permissionSettings'] as const,
}

// The mode new Agents get (Settings); Full access until someone chooses otherwise.
export function usePermissionSettings() {
  return useQuery({ queryKey: permissionKeys.settings, queryFn: ({ signal }) => call(api.GET('/permission-settings', { signal })) })
}

export function usePutPermissionSettings() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: Schemas['PermissionSettings']) => call(api.PUT('/permission-settings', { body })),
    onSuccess: (settings) => client.setQueryData(permissionKeys.settings, settings),
  })
}

// Approve or reject a request (a reason rejects it and tells the Agent why). Answered once: a second answer, from here or the Inbox,
// gets 409 permission_request_answered with the request as it was answered.
export const answerPermissionRequest = (permissionRequestId: string, body: Schemas['PermissionAnswer']) =>
  call(api.POST('/permission-requests/{permissionRequestId}/answer', { params: { path: { permissionRequestId } }, body }))

// A task node's requests waiting for an answer (a Supervised step waits for them): read once, then replaced whole by
// task.node.permission_requests.updated on the task's event stream (see useTaskLive). A read already under way when an event came returns
// what the event left, which is newer.
export const taskNodePermissionKeys = {
  task: (taskId: string) => ['taskNodePermissionRequests', taskId] as const,
  node: (taskId: string, nodeRunId: string) => ['taskNodePermissionRequests', taskId, nodeRunId] as const,
}

const nodeEvents = new Map<string, number>()
const eventsSeen = (taskId: string, nodeRunId: string) => nodeEvents.get(`${taskId}/${nodeRunId}`) ?? 0

export function applyTaskNodePermissionEvent(client: QueryClient, taskId: string, list: Schemas['TaskNodePermissionRequests']) {
  nodeEvents.set(`${taskId}/${list.nodeRunId}`, eventsSeen(taskId, list.nodeRunId) + 1)
  client.setQueryData(taskNodePermissionKeys.node(taskId, list.nodeRunId), list)
}

export function useTaskNodePermissionRequests(taskId: string, nodeRunId: string, enabled: boolean) {
  const client = useQueryClient()
  const queryKey = taskNodePermissionKeys.node(taskId, nodeRunId)
  return useQuery({
    queryKey,
    enabled,
    retry: false,
    queryFn: async ({ signal }) => {
      const seen = eventsSeen(taskId, nodeRunId)
      const list = await call(api.GET('/tasks/{taskId}/nodes/{nodeRunId}/permission-requests', { signal, params: { path: { taskId, nodeRunId } } }))
      return eventsSeen(taskId, nodeRunId) === seen ? list : (client.getQueryData<Schemas['TaskNodePermissionRequests']>(queryKey) ?? list)
    },
  })
}
