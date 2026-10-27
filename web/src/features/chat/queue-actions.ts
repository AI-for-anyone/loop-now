import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import { config } from '@/config'
import { t } from '@/i18n'

// 无法操作排队消息时的一行轻提示（由 QueueNotice 显示），过一会儿自动消失；再次显示同一句时重新计时。
export function useQueueNotice() {
  const [notice, setNotice] = useState<{ text: string; seq: number } | null>(null)
  const seq = useRef(0)
  useEffect(() => {
    if (!notice) return
    const timer = setTimeout(() => setNotice(null), config.queueNoticeMs)
    return () => clearTimeout(timer)
  }, [notice])
  const show = useCallback((text: string) => setNotice({ text, seq: ++seq.current }), [])
  const clear = useCallback(() => setNotice(null), [])
  return { text: notice?.text ?? null, show, clear }
}

// 同一条消息的请求进行中时不再发出；pending 中是进行中的键。
export function usePendingKeys() {
  const inFlight = useRef(new Set<string>())
  const [pending, setPending] = useState<ReadonlySet<string>>(() => new Set())
  const begin = useCallback((key: string) => {
    if (inFlight.current.has(key)) return false
    inFlight.current.add(key)
    setPending(new Set(inFlight.current))
    return true
  }, [])
  const end = useCallback((key: string) => {
    inFlight.current.delete(key)
    setPending(new Set(inFlight.current))
  }, [])
  return { pending, begin, end }
}

export type QueueActionResult<T> = { ok: true; value: T } | { ok: false; error: unknown; indeterminate: boolean }

// 请求没有到达服务端或服务端出错：结果不明，动作可能已经生效。
export const isIndeterminate = (error: unknown) => error instanceof ApiError && (error.status === 0 || error.status >= 500)

// 排队消息上的动作（打断、撤回、停止）：同一行一次只发一个请求。结果不明时保留 actionId，按钮改为重试，
// 重试沿用同一 actionId，服务端返回首次结果；有了确定结果后丢弃。行已在请求中时返回 null。
export function useQueueActions() {
  const { pending, begin, end } = usePendingKeys()
  const actionIds = useRef(new Map<string, string>())
  const [unknown, setUnknown] = useState<ReadonlySet<string>>(() => new Set())
  const mark = useCallback((key: string, value: boolean) => setUnknown(old => {
    if (old.has(key) === value) return old
    const next = new Set(old)
    if (value) next.add(key)
    else next.delete(key)
    return next
  }), [])
  const run = useCallback(async <T,>(row: string, operation: string, request: (actionId: string) => Promise<T>): Promise<QueueActionResult<T> | null> => {
    if (!begin(row)) return null
    const key = `${operation}:${row}`
    const actionId = actionIds.current.get(key) ?? crypto.randomUUID()
    actionIds.current.set(key, actionId)
    try {
      const value = await request(actionId)
      actionIds.current.delete(key)
      mark(key, false)
      return { ok: true, value }
    } catch (error) {
      const indeterminate = isIndeterminate(error)
      if (!indeterminate) actionIds.current.delete(key)
      mark(key, indeterminate)
      return { ok: false, error, indeterminate }
    } finally {
      end(row)
    }
  }, [begin, end, mark])
  const isUnknown = useCallback((row: string, operation: string) => unknown.has(`${operation}:${row}`), [unknown])
  return { pending, isUnknown, run }
}

// 普通对话里排队项已不在队列、或要停止的回复已经结束时的轻提示；其他错误返回 null，照常报错。
export function conflictNotice(error: unknown, operation: 'promote' | 'cancel' | 'stop'): string | null {
  if (!(error instanceof ApiError)) return null
  if (error.code === 'run_not_active') return t.chat.queueList.stopEnded
  if (error.code !== 'run_not_queued') return null
  const state = error.details && 'messageState' in error.details ? error.details.messageState : undefined
  if (state === 'read') return t.chat.queueList.read
  if (state === 'cancelled') return t.chat.queueList.cancelled
  return operation === 'cancel' ? t.chat.queueList.dispatchedCancel : t.chat.queueList.dispatched
}

// 停止当前回复：只停这一次，不发送新消息。结果不明时沿用 actionId 重试；这次回复已经结束等冲突只给一行提示。
export function useStopReply<R>(request: (runId: string, actionId: string) => Promise<R>, notices: (error: unknown) => string | null) {
  const actions = useQueueActions()
  const notice = useQueueNotice()
  const [error, setError] = useState<unknown>(null)
  const stop = async (runId: string, onStopped?: (receipt: R) => void) => {
    setError(null)
    notice.clear()
    const result = await actions.run(runId, 'stop', (actionId) => request(runId, actionId))
    if (!result) return
    if (result.ok) {
      onStopped?.(result.value)
      return
    }
    const text = notices(result.error)
    if (text) notice.show(text)
    else setError(result.error)
  }
  const control = (runId: string, onStopped?: (receipt: R) => void) => ({
    pending: actions.pending.has(runId),
    retry: actions.isUnknown(runId, 'stop'),
    onStop: () => void stop(runId, onStopped),
  })
  return { control, notice: notice.text, error }
}

// 任务节点里同样的冲突：task_conflict 的 reason 说明排队项已不在队列，或要停止的这一轮已经结束。
export function nodeConflictNotice(error: unknown, operation: 'promote' | 'cancel' | 'stop'): string | null {
  const reason = error instanceof ApiError && error.code === 'task_conflict' && error.details && 'reason' in error.details ? error.details.reason : null
  switch (reason) {
    case 'message_dispatched':
      return operation === 'cancel' ? t.chat.queueList.dispatchedCancel : t.chat.queueList.dispatched
    case 'message_not_executed':
      return t.chat.queueList.notExecuted
    case 'message_read':
      return t.chat.queueList.read
    case 'message_cancelled':
      return t.chat.queueList.cancelled
    case 'run_not_active':
      return t.chat.queueList.stopEnded
    default:
      return null
  }
}
