import { api, call } from './client'

// Stop one of an Agent's background tasks, or all of them (contract BackgroundTask). A call returns once the end is recorded, or after a
// few seconds with the task still running and stopRequestedAt set; the end then arrives through background_tasks.updated. Repeating an
// actionId returns the first result.
export const stopBackgroundTask = (agentId: string, backgroundTaskId: string, actionId: string) =>
  call(api.POST('/agents/{agentId}/background-tasks/{backgroundTaskId}/stop', { params: { path: { agentId, backgroundTaskId } }, body: { actionId } }))

export const stopAllBackgroundTasks = (agentId: string, actionId: string) =>
  call(api.POST('/agents/{agentId}/background-tasks/stop', { params: { path: { agentId } }, body: { actionId } }))
