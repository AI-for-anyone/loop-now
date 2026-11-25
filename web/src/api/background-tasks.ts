import { useQuery } from '@tanstack/react-query'
import { api, call } from './client'

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

export function useTaskNodeBackgroundTasks(taskId: string, nodeRunId: string, enabled: boolean) {
  return useQuery({
    queryKey: taskNodeBackgroundKeys.node(taskId, nodeRunId),
    enabled,
    retry: false,
    queryFn: ({ signal }) => call(api.GET('/tasks/{taskId}/nodes/{nodeRunId}/background-tasks', { signal, params: { path: { taskId, nodeRunId } } })),
  })
}

// Stopping one of them, or all, leaves the node's turn going; the expected Agent and instance guard against a node that moved on.
type NodeTarget = { taskId: string; nodeRunId: string; agentId: string; runtimeId: string }
export const stopTaskNodeBackgroundTask = (node: NodeTarget, backgroundTaskId: string, actionId: string) =>
  call(api.POST('/tasks/{taskId}/nodes/{nodeRunId}/background-tasks/{backgroundTaskId}/stop', { params: { path: { taskId: node.taskId, nodeRunId: node.nodeRunId, backgroundTaskId } }, body: { actionId, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId } }))

export const stopAllTaskNodeBackgroundTasks = (node: NodeTarget, actionId: string) =>
  call(api.POST('/tasks/{taskId}/nodes/{nodeRunId}/background-tasks/stop', { params: { path: { taskId: node.taskId, nodeRunId: node.nodeRunId } }, body: { actionId, expectedAgentId: node.agentId, expectedRuntimeId: node.runtimeId } }))
