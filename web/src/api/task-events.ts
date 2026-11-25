import type { Schemas } from './client'
import { connectEventStream, type StreamOptions } from './events'
import { config } from '@/config'
export interface TaskEventDataMap {
  snapshot: Schemas['TaskSnapshotEvent']
  'task.updated': Schemas['TaskUpdatedEvent']
  'node.updated': Schemas['TaskNodeUpdatedEvent']
  'message.created': Schemas['TaskMessage']
  'task.inbox.updated': Schemas['TaskNodeInboxRecord']
  'artifact.published': Schemas['TaskArtifactEvent']
  'sleep.updated': Schemas['SseSleepUpdated']
  'task.node.queue.updated': Schemas['TaskNodeQueue']
  'task.node.background_tasks.updated': Schemas['TaskNodeBackgroundTasks']
  'task.node.permission_requests.updated': Schemas['TaskNodePermissionRequests']
}
export type TaskEvent = { [K in keyof TaskEventDataMap]: { type: K; id: string; data: TaskEventDataMap[K] } }[keyof TaskEventDataMap]
export const TASK_EVENT_NAMES = ['snapshot', 'task.updated', 'node.updated', 'message.created', 'task.inbox.updated', 'artifact.published', 'sleep.updated', 'task.node.queue.updated', 'task.node.background_tasks.updated', 'task.node.permission_requests.updated'] as const satisfies readonly TaskEvent['type'][]
export interface ProjectTaskEventDataMap { snapshot: Schemas['TaskProjectSnapshotEvent']; 'task.updated': Schemas['TaskUpdatedEvent'] }
export type ProjectTaskEvent = { [K in keyof ProjectTaskEventDataMap]: { type: K; id: string; data: ProjectTaskEventDataMap[K] } }[keyof ProjectTaskEventDataMap]
export const PROJECT_TASK_EVENT_NAMES = ['snapshot', 'task.updated'] as const
export const connectTaskEvents = (id: string, options: StreamOptions<TaskEvent>) => connectEventStream({ url: `${config.apiBase}/tasks/${encodeURIComponent(id)}/events`, stream: `task:${id}` }, TASK_EVENT_NAMES, options)
export const connectProjectTaskEvents = (id: string, options: StreamOptions<ProjectTaskEvent>) => connectEventStream({ url: `${config.apiBase}/projects/${encodeURIComponent(id)}/tasks/events`, stream: `project-tasks:${id}` }, PROJECT_TASK_EVENT_NAMES, options)
