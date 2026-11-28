import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useReducer, useRef } from 'react'
import { api, ApiError, call, type Schemas } from '@/api/client'
import { connectAgentEvents, type AgentEvent } from '@/api/events'
import { keys, updateRunInMessages, upsertMessage } from '@/api/queries'
import { config } from '@/config'
import { isNotFound } from '@/lib/errors'
import { initialLiveState, liveReducer } from './live-state'
import { applyNormalSleep, sleepProjections } from '@/features/sleep/projection'

// A snapshot requires the history to be realigned with the server, which needs a fetch that starts after the snapshot. While the
// first load hasn't finished,
// invalidateQueries would merge into that earlier request, so cancel the request in flight first and then refetch.
async function resyncHistory(client: QueryClient, agentId: string) {
  await client.cancelQueries({ queryKey: keys.messages(agentId) })
  await client.invalidateQueries({ queryKey: keys.messages(agentId) })
}

// Sync events into the query cache: messages and runs in the history, and busy/idle and session state in the Agent details and list.
// Messages and runs written while a history request is in flight get overwritten by the request's result, so the chat page also
// relies on what the live part recorded (see live-state).
function syncCache(client: QueryClient, agentId: string, event: AgentEvent) {
  switch (event.type) {
    case 'snapshot':
      client.setQueryData(['agentStatus', agentId], sleepProjections(client).status(event.data.status))
      void resyncHistory(client, agentId)
      void client.invalidateQueries({ queryKey: keys.agentStatus(agentId) })
      void client.invalidateQueries({ queryKey: keys.agents })
      break
    case 'sleep.updated':
      if (applyNormalSleep(client, agentId, event.data)) {
        void client.invalidateQueries({ queryKey: keys.agentStatus(agentId) })
        void client.invalidateQueries({ queryKey: keys.agents })
        // A committed terminal sleep may have scheduled a new timer message or
        // annotated real queued work. Read its original origin/reference from
        // history instead of inventing a user message from sleep.updated.
        if (event.data.endedSleepId) void resyncHistory(client, agentId)
      }
      break
    case 'message.created':
      upsertMessage(client, agentId, event.data.message)
      void client.invalidateQueries({ queryKey: keys.agents })
      break
    case 'run.updated':
      updateRunInMessages(client, agentId, event.data.run)
      void client.invalidateQueries({ queryKey: keys.agents })
      break
    case 'run.ended':
      updateRunInMessages(client, agentId, event.data.run)
      if (event.data.message) upsertMessage(client, agentId, event.data.message)
      void client.invalidateQueries({ queryKey: keys.agents })
      break
    case 'session.updated':
      void client.invalidateQueries({ queryKey: keys.agent(agentId) })
      void client.invalidateQueries({ queryKey: keys.agents })
      // Whether platform commands can be used right now depends only on the runtime and the session state, and every change to
      // either pushes this event (contract AgentCommand.available).
      void client.invalidateQueries({ queryKey: keys.agentCommands(agentId) })
      break
    // A request answered or cancelled leaves the list: an open run record reads again to say how.
    case 'permission_requests.updated':
      void client.invalidateQueries({ queryKey: ['timeline', agentId] })
      break
    // The running list replaces the status's list; the Agent details and the list carry the count.
    case 'background_tasks.updated':
      client.setQueryData<Schemas['AgentStatus']>(keys.agentStatus(agentId), (status) => status && { ...status, backgroundTasks: event.data.tasks, restartOnNextMessage: status.restartOnNextMessage && event.data.tasks.length > 0 })
      // Whether the next message restarts the CLI can turn on when a task starts after a settings change: read the status again.
      void client.invalidateQueries({ queryKey: keys.agentStatus(agentId) })
      void client.invalidateQueries({ queryKey: keys.agent(agentId) })
      void client.invalidateQueries({ queryKey: keys.agents })
      break
  }
}

// The run an event concerns; text deltas don't count, since only tool state and run state changes add entries to the run record.
function eventRunId(event: AgentEvent): string | null {
  switch (event.type) {
    case 'run.updated':
    case 'run.ended':
      return event.data.run.id
    case 'run.tool':
      return event.data.runId
    default:
      return null
  }
}

// An expanded run record is refetched as the reply progresses: two fetches for the same run are at least timelineRefreshMinMs apart,
// and changes within that interval are merged into the fetch at the end of the interval. Only timelines being displayed actually send
// requests.
function createTimelineRefresher(client: QueryClient, agentId: string) {
  const timers = new Map<string, ReturnType<typeof setTimeout>>()
  const last = new Map<string, number>()
  const refresh = (runId: string) => {
    timers.delete(runId)
    last.set(runId, Date.now())
    void client.invalidateQueries({ queryKey: keys.timeline(agentId, runId) })
  }
  return {
    schedule(runId: string) {
      if (timers.has(runId)) return
      const wait = (last.get(runId) ?? -Infinity) + config.timelineRefreshMinMs - Date.now()
      if (wait <= 0) refresh(runId)
      else timers.set(runId, setTimeout(() => refresh(runId), wait))
    },
    stop() {
      timers.forEach((timer) => clearTimeout(timer))
      timers.clear()
    },
  }
}

const nextFrame = (callback: () => void) =>
  typeof requestAnimationFrame === 'function' ? requestAnimationFrame(callback) : setTimeout(callback, 16)

// Subscribe to the selected Agent's event stream. Events are batched per animation frame before updating the UI, so Markdown isn't
// re-rendered for every text delta.
export function useAgentLive(agentId: string) {
  const client = useQueryClient()
  const [live, dispatch] = useReducer(liveReducer, initialLiveState)
  const pending = useRef<AgentEvent[]>([])
  const activeAgent = useRef(agentId)

  useEffect(() => {
    activeAgent.current = agentId
    dispatch({ type: 'reset', agentId })
    pending.current = []
    let scheduled = false
    let active = true
    const timelines = createTimelineRefresher(client, agentId)
    const flush = () => {
      scheduled = false
      if (!active || pending.current.length === 0) return
      const events = pending.current
      pending.current = []
      dispatch({ type: 'batch', events })
    }
    const close = connectAgentEvents({
      agentId,
      onEvent: (event) => {
        if (!active || (event.type === 'sleep.updated' && (event.data.scopeKind !== 'normal' || event.data.agentId !== agentId))) return
        pending.current.push(event)
        syncCache(client, agentId, event)
        const runId = eventRunId(event)
        if (runId) timelines.schedule(runId)
        if (!scheduled) {
          scheduled = true
          nextFrame(flush)
        }
      },
      onState: (connection) => dispatch({ type: 'connection', state: connection }),
      onClosed: async () => {
        try {
          await call(api.GET('/agents/{agentId}', { params: { path: { agentId } } }))
          return true
        } catch (error) {
          if (error instanceof ApiError && error.status === 401) return false
          if (!isNotFound(error)) return true
          await client.invalidateQueries({ queryKey: keys.agent(agentId) })
          return false
        }
      },
    })
    return () => {
      active = false
      timelines.stop()
      close()
    }
  }, [agentId, client])

  // Send results are recorded in the live part just like events; ignored if another Agent was selected before the result arrived.
  const recordSent = useCallback(
    (result: Schemas['SendMessageResult']) => {
      if (activeAgent.current === agentId) dispatch({ type: 'sent', result })
    },
    [agentId],
  )

  // Receipts of queue actions (interrupt, withdraw, stop) are likewise recorded only while it's still this Agent; seq is live.queueSeq
  // when the action started.
  const recordQueueAction = useCallback(
    (receipt: Schemas['AgentQueueActionReceipt'], seq: number) => {
      if (activeAgent.current === agentId && receipt.agentId === agentId) dispatch({ type: 'queueAction', receipt, seq })
    },
    [agentId],
  )

  return { live, recordSent, recordQueueAction }
}
