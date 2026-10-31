import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useReducer, useRef } from 'react'
import { api, call, type Schemas } from '@/api/client'
import { connectAgentEvents, type AgentEvent } from '@/api/events'
import { keys, updateRunInMessages, upsertMessage } from '@/api/queries'
import { config } from '@/config'
import { isNotFound } from '@/lib/errors'
import { initialLiveState, liveReducer } from './live-state'
import { applyNormalSleep, sleepProjections } from '@/features/sleep/projection'

// 快照要求历史与服务端重新对齐，需要一次在快照之后才开始的获取。首次加载尚未完成时，
// invalidateQueries 会并入这次更早开始的请求，所以先取消进行中的请求再重新获取。
async function resyncHistory(client: QueryClient, agentId: string) {
  await client.cancelQueries({ queryKey: keys.messages(agentId) })
  await client.invalidateQueries({ queryKey: keys.messages(agentId) })
}

// 把事件同步到查询缓存：历史中的消息与执行、Agent 详情与列表中的忙闲和会话状态。
// 历史请求进行中时写入的消息与执行会被请求结果覆盖，对话页另以实时部分记下的内容为准（见 live-state）。
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
      break
  }
}

// 事件涉及的执行；文字增量不算，工具状态与执行状态变化才会让运行记录多出条目。
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

// 展开中的运行记录随回复的进展重新获取：同一执行两次获取之间至少间隔 timelineRefreshMinMs，
// 间隔内的变化合并到间隔结束时的那一次。只有正在显示的时间线会真正发出请求。
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

// 订阅所选 Agent 的事件流。事件按动画帧合并后再更新界面，避免每个文字增量都重新渲染 Markdown。
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

  // 发送结果与事件一样记入实时部分；结果到达前已切换到其他 Agent 时忽略。
  const recordSent = useCallback(
    (result: Schemas['SendMessageResult']) => {
      if (activeAgent.current === agentId) dispatch({ type: 'sent', result })
    },
    [agentId],
  )

  // 排队动作（打断、撤回、停止）的回执同样只在仍是这个 Agent 时记入；seq 为动作开始时的 live.queueSeq。
  const recordQueueAction = useCallback(
    (receipt: Schemas['AgentQueueActionReceipt'], seq: number) => {
      if (activeAgent.current === agentId && receipt.agentId === agentId) dispatch({ type: 'queueAction', receipt, seq })
    },
    [agentId],
  )

  return { live, recordSent, recordQueueAction }
}
