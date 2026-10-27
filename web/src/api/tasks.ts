import { useSyncExternalStore } from 'react'
import { taskMessageProjection, taskMessageRows } from '@/features/tasks/message-projection'
import { type InfiniteData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call, type Schemas } from './client'
import { sleepProjections } from '@/features/sleep/projection'

export const taskKeys = {
  project: (id: string) => ['projectTasks', id] as const,
  detail: (id: string) => ['task', id] as const,
  messages: (taskId: string, nodeRunId?: string) => nodeRunId ? ['taskMessages', taskId, nodeRunId] as const : ['taskMessages', taskId] as const,
}
export const tasksPath = (name: string) => `/p/${encodeURIComponent(name)}/tasks`
export function useProjectTasks(projectId: string) {
  return useInfiniteQuery({
    queryKey: taskKeys.project(projectId),
    queryFn: ({ pageParam, signal }) => call(api.GET('/projects/{projectId}/tasks', { signal, params: { path: { projectId }, query: { limit: 20, ...(pageParam ? { cursor: pageParam } : {}) } } })),
    initialPageParam: null as string | null,
    getNextPageParam: (page: Schemas['TaskPage']) => page.nextCursor ?? undefined,
  })
}
export function useTask(taskId: string) {
  const client = useQueryClient()
  return useQuery({ queryKey: taskKeys.detail(taskId), queryFn: async ({ signal }) => {
    const store = sleepProjections(client)
    const read = () => call(api.GET('/tasks/{taskId}', { signal, params: { path: { taskId } } }))
    let detail = await read()
    // An event may arrive before the first detail. Read committed facts again
    // instead of inventing workflow state from an instance sleep projection.
    if (detail.task.version < store.taskVersion(taskId) && !store.currentTask(taskId)) detail = await read()
    return store.task(detail)
  } })
}
type MessageHistory = InfiniteData<Schemas['TaskMessagePage'], string | null> & { retainedMessages: Schemas['TaskMessage'][] }
// A partial HTTP page is not a deletion list. Retain previously observed IDs,
// updating their full entity only from the next serialized HTTP result.
export function mergeTaskMessageHistory(previous: MessageHistory | undefined, next: InfiniteData<Schemas['TaskMessagePage'], string | null>): MessageHistory {
  const messages = new Map((previous?.retainedMessages ?? previous?.pages.flatMap(p => p.messages) ?? []).map(message => [message.id, message]))
  for (const page of next.pages) for (const message of page.messages) messages.set(message.id, message)
  return { ...next, retainedMessages: [...messages.values()].sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id)) }
}
export function useTaskMessages(taskId: string, nodeRunId: string) {
  const client = useQueryClient(), projection = taskMessageProjection(client)
  useSyncExternalStore(projection.subscribe, projection.snapshot)
  const query = useInfiniteQuery({
    queryKey: taskKeys.messages(taskId, nodeRunId),
    queryFn: async ({ pageParam, signal }) => {
      const read = projection.beginRead(taskId, nodeRunId)
      const page = await call(api.GET('/tasks/{taskId}/nodes/{nodeRunId}/messages', { signal, params: { path: { taskId, nodeRunId }, query: { limit: 50, ...(pageParam ? { cursor: pageParam } : {}) } } }))
      return { ...page, messages: projection.reconcile(read, page.messages) }
    },
    initialPageParam: null as string | null,
    getNextPageParam: (page: Schemas['TaskMessagePage']) => page.nextCursor ?? undefined,
    structuralSharing: (old, next) => mergeTaskMessageHistory(old as MessageHistory | undefined, next as InfiniteData<Schemas['TaskMessagePage'], string | null>),
    select: data => data as MessageHistory,
  })
  const rows = taskMessageRows(query.data?.retainedMessages ?? [], projection.messages(taskId, nodeRunId), nodeRunId)
  return { ...query, data: query.data || rows.length ? { ...(query.data ?? { pages: [], pageParams: [] }), retainedMessages: rows } : undefined }
}
export function useTaskAction(taskId: string, projectId: string) {
  const client = useQueryClient()
  return useMutation({
    retry: false,
    mutationFn: (action: { kind: 'retry'; body: Schemas['TaskRetryInput'] } | { kind: 'cancel'; body: Schemas['TaskCancelInput'] }) =>
      action.kind === 'retry'
        ? call(api.POST('/tasks/{taskId}/retry', { params: { path: { taskId } }, body: action.body }))
        : call(api.POST('/tasks/{taskId}/cancel', { params: { path: { taskId } }, body: action.body })),
    onSuccess: (detail) => {
      client.setQueryData(taskKeys.detail(taskId), sleepProjections(client).task(detail))
      void client.invalidateQueries({ queryKey: taskKeys.messages(taskId) })
    },
    onSettled: () => {
      void client.invalidateQueries({ queryKey: taskKeys.project(projectId) })
      void client.invalidateQueries({ queryKey: taskKeys.detail(taskId) })
      void client.invalidateQueries({ queryKey: ['inboxThread'] })
    },
  })
}
// Only platform-owned artifact paths are turned into links; credentials and host paths have no URL fallback.
export function artifactUrl(artifact: Schemas['TaskArtifact']) {
  return `/tasks/${encodeURIComponent(artifact.taskId)}/artifacts/${encodeURIComponent(artifact.id)}`
}
