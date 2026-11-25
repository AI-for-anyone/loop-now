import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { api, call, type Schemas } from './client'

// Stop one of an Agent's background tasks, or all of them (contract BackgroundTask). A call returns once the end is recorded, or after a
// few seconds with the task still running and stopRequestedAt set; the end then arrives through background_tasks.updated. Repeating an
// actionId returns the first result.
export const stopBackgroundTask = (agentId: string, backgroundTaskId: string, actionId: string) =>
  call(api.POST('/agents/{agentId}/background-tasks/{backgroundTaskId}/stop', { params: { path: { agentId, backgroundTaskId } }, body: { actionId } }))

export const stopAllBackgroundTasks = (agentId: string, actionId: string) =>
  call(api.POST('/agents/{agentId}/background-tasks/stop', { params: { path: { agentId } }, body: { actionId } }))

// A task node's running background tasks (the node's turn waits for them), read once and then replaced whole by
// task.node.background_tasks.updated on the task's event stream (see useTaskLive).
export const taskNodeBackgroundKeys = {
  task: (taskId: string) => ['taskNodeBackground', taskId] as const,
  node: (taskId: string, nodeRunId: string) => ['taskNodeBackground', taskId, nodeRunId] as const,
}

// Events seen per node. A read that was already under way when one came returns what the event left instead: the event is newer.
const nodeEvents = new Map<string, number>()
const eventsSeen = (taskId: string, nodeRunId: string) => nodeEvents.get(`${taskId}/${nodeRunId}`) ?? 0

export function applyTaskNodeBackgroundEvent(client: QueryClient, taskId: string, list: Schemas['TaskNodeBackgroundTasks']) {
  nodeEvents.set(`${taskId}/${list.nodeRunId}`, eventsSeen(taskId, list.nodeRunId) + 1)
  client.setQueryData(taskNodeBackgroundKeys.node(taskId, list.nodeRunId), list)
}

export function useTaskNodeBackgroundTasks(taskId: string, nodeRunId: string, enabled: boolean) {
  const client = useQueryClient()
  const queryKey = taskNodeBackgroundKeys.node(taskId, nodeRunId)
  return useQuery({
    queryKey,
    enabled,
    retry: false,
    queryFn: async ({ signal }) => {
      const seen = eventsSeen(taskId, nodeRunId)
      const list = await call(api.GET('/tasks/{taskId}/nodes/{nodeRunId}/background-tasks', { signal, params: { path: { taskId, nodeRunId } } }))
      return eventsSeen(taskId, nodeRunId) === seen ? list : (client.getQueryData<Schemas['TaskNodeBackgroundTasks']>(queryKey) ?? list)
    },
  })
}

// Stopping one of them, or all, leaves the node's turn going; the expected Agent and instance guard against a node that moved on.
type NodeTarget = { taskId: string; nodeRunId: string; agentId: string; runtimeId: string }
export const stopTaskNodeBackgroundTask = (node: NodeTarget, backgroundTaskId: string, actionId: string) =>
  call(api.POST('/tasks/{taskId}/nodes/{nodeRunId}/background-tasks/{backgroundTaskId}/stop', { params: { path: { taskId: node.taskId, nodeRunId: node.nodeRunId, backgroundTaskId } }, body: { actionId, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId } }))

export const stopAllTaskNodeBackgroundTasks = (node: NodeTarget, actionId: string) =>
  call(api.POST('/tasks/{taskId}/nodes/{nodeRunId}/background-tasks/stop', { params: { path: { taskId: node.taskId, nodeRunId: node.nodeRunId } }, body: { actionId, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId } }))
