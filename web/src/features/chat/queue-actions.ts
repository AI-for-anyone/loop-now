import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import { config } from '@/config'
import { t } from '@/i18n'

// A light one-line notice when a queued message can't be acted on (shown by QueueNotice); it disappears by itself after a while, and
// showing the same text again restarts the timer.
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

// While a request for a message is in flight, no further request is sent for it; pending holds the keys in flight.
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

// The request didn't reach the server or the server errored: the outcome is unknown and the action may already have taken effect.
export const isIndeterminate = (error: unknown) => error instanceof ApiError && (error.status === 0 || error.status >= 500)

// Actions on queued messages (interrupt, withdraw, stop): only one request at a time per row. When the outcome is unknown, the
// actionId is kept and the button turns into a retry;
// the retry reuses the same actionId and the server returns the first result; the actionId is dropped once a definite result is in.
// Returns null when the row already has a request in flight.
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

// The light notice in an ordinary conversation when a queued item is no longer in the queue, or the reply to stop has already ended;
// returns null for other errors, which are reported as usual.
export function conflictNotice(error: unknown, operation: 'promote' | 'cancel' | 'stop'): string | null {
  if (!(error instanceof ApiError)) return null
  if (error.code === 'run_not_active') return t.chat.queueList.stopEnded
  if (error.code !== 'run_not_queued') return null
  const state = error.details && 'messageState' in error.details ? error.details.messageState : undefined
  if (state === 'read') return t.chat.queueList.read
  if (state === 'cancelled') return t.chat.queueList.cancelled
  return operation === 'cancel' ? t.chat.queueList.dispatchedCancel : t.chat.queueList.dispatched
}

// Stop the current reply: stops only this one, without sending a new message. When the outcome is unknown, retry with the same
// actionId; conflicts such as this reply having already ended only get a one-line notice.
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

// The same conflicts in a task node: the reason of task_conflict says the queued item is no longer in the queue, or the turn to stop
// has already ended.
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
