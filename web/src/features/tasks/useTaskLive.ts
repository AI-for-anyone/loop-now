import { taskInboxKeys, taskInboxProjection } from '@/api/task-inbox'
import { taskNodeQueueKeys, taskNodeQueueProjection } from '@/api/task-node-queue'
import { taskMessageProjection } from './message-projection'
import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { connectProjectTaskEvents, connectTaskEvents, type TaskEvent, type ProjectTaskEvent } from '@/api/task-events'
import { nodeMessagesUnsupported } from '@/api/task-node-capability'
import { taskKeys } from '@/api/tasks'
import type { ConnectionState } from '@/api/events'
import { sleepProjections } from '@/features/sleep/projection'

// Decimal persistent event IDs are compared numerically, independently of Redis stream IDs.
export function acceptTaskEvent(previous: string, next: string) {
  return !previous || !/^\d+$/.test(next) || !/^\d+$/.test(previous) || BigInt(next) > BigInt(previous)
}
export function useTaskLive(id: string, project: boolean) {
  const client = useQueryClient()
  const [state, setState] = useState<ConnectionState>('connecting')
  useEffect(() => {
    let lastId = ''
    let stopped = false
    const keys = project ? [taskKeys.project(id)] : [taskKeys.detail(id), taskKeys.messages(id), taskInboxKeys.records(id)]
    let dirty = false
    let refreshing = false
    let scheduled: ReturnType<typeof setTimeout> | undefined
    const calibrate = async () => {
      if (stopped || refreshing) return
      refreshing = true
      try {
        do {
          const alreadyFetching = keys.some(queryKey => client.isFetching({ queryKey }) > 0)
          dirty = false
          // Infinite-query refetch starts at the first cursor and revisits the
          // loaded range. Do not cancel/restart on each streaming event: a long
          // reply must not starve the authoritative HTTP read.
          await Promise.all(keys.map(queryKey => client.invalidateQueries({ queryKey }, { cancelRefetch: false })))
          // An event may have arrived while an initial read or load-more was
          // already running. Joining that old request is not a fresh calibration.
          if (alreadyFetching) dirty = true
        } while (dirty && !stopped)
      } finally { refreshing = false }
    }
    const refresh = () => {
      dirty = true
      if (stopped || refreshing || scheduled) return
      scheduled = setTimeout(() => { scheduled = undefined; void calibrate() }, 60)
    }
    const options = {
      onEvent: (event: TaskEvent | ProjectTaskEvent) => {
        if (event.type !== 'snapshot' && !acceptTaskEvent(lastId, event.id)) return
        lastId = event.id
        if (!project && event.type === 'message.created' && taskMessageProjection(client).message(id, event.id, event.data)) return
        if (!project && event.type === 'task.inbox.updated' && taskInboxProjection(client).event(id, event.id, event.data)) return
        if (!project && event.type === 'task.node.queue.updated' && taskNodeQueueProjection(client).event(client, id, event.data)) return
        // Queue events only carry changes; after a (re)connection snapshot, read each shown queue once.
        if (!project && event.type === 'snapshot') { taskMessageProjection(client).connection(id); taskInboxProjection(client).connection(id); void client.invalidateQueries({ queryKey: taskNodeQueueKeys.task(id) }) }
        if (!project && event.type === 'sleep.updated') {
          const store = sleepProjections(client)
          if (!store.update(event.data, { task: id })) return
          const current = store.currentTask(id)
          if (current) client.setQueryData(taskKeys.detail(id), current)
        }
        if (!project && event.type === 'snapshot' && 'task' in event.data) client.setQueryData(taskKeys.detail(id), sleepProjections(client).task(event.data.task))
        if (!project && !event.type.startsWith('message.')) void client.invalidateQueries({ queryKey: ['taskNodeCapability', id], predicate: query => !nodeMessagesUnsupported(query.state.error) }, { cancelRefetch: false })
        void refresh()
      },
      onState: (next: ConnectionState) => { setState(next); if (next === 'open') void refresh() },
      onClosed: async () => true,
    }
    const stop = project ? connectProjectTaskEvents(id, options) : connectTaskEvents(id, options)
    const focus = () => { refresh(); if (!project) void client.invalidateQueries({ queryKey: ['taskNodeCapability', id] }, { cancelRefetch: false }) }
    window.addEventListener('focus', focus)
    return () => { stopped = true; if (scheduled) clearTimeout(scheduled); stop(); window.removeEventListener('focus', focus) }
  }, [client, id, project])
  return state
}
