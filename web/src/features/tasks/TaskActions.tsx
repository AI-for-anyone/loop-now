import { useRef, useState } from 'react'
import type { Schemas } from '@/api/client'
import { useTaskAction } from '@/api/tasks'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { t } from '@/i18n'

export function TaskActions({ task, disabled = false, showRetry = true, showCancel = true }: { task: Schemas['TaskSummary']; disabled?: boolean; showRetry?: boolean; showCancel?: boolean }) {
  const [retrying, setRetrying] = useState(false)
  const [cancelling, setCancelling] = useState(false)
  const [note, setNote] = useState('')
  const target = useRef<{ actionId: string; nodeRunId: string } | null>(null)
  const cancelId = useRef('')
  const action = useTaskAction(task.id, task.projectId)
  const ended = task.status === 'completed' || task.status === 'cancelled'
  return <div className="flex flex-wrap gap-3">
    {showRetry && task.status === 'blocked' && task.currentNodeRunId && <Button disabled={disabled || action.isPending} onClick={() => { target.current = { actionId: crypto.randomUUID(), nodeRunId: task.currentNodeRunId! }; setRetrying(true) }}>{t.tasks.retry}</Button>}
    {showCancel && !ended && <Button variant="destructive" disabled={disabled || action.isPending} onClick={() => { cancelId.current = crypto.randomUUID(); setCancelling(true) }}>{t.tasks.cancel}</Button>}
    <ConfirmDialog open={retrying} onOpenChange={setRetrying} title={t.tasks.retry} body={t.tasks.retryBody} confirmLabel={t.tasks.retry} onConfirm={async () => {
      if (!target.current || disabled) return Promise.reject(new Error(t.tasks.reconnecting))
      await action.mutateAsync({ kind: 'retry', body: { actionId: target.current.actionId, expectedNodeRunId: target.current.nodeRunId, note } }); setNote('')
    }}><label htmlFor="task-retry-note" className="text-sm font-bold">{t.tasks.retryNote}</label><Textarea id="task-retry-note" value={note} onChange={(e) => { setNote(e.target.value); if (target.current) target.current.actionId = crypto.randomUUID() }} /></ConfirmDialog>
    <ConfirmDialog open={cancelling} onOpenChange={setCancelling} title={t.tasks.cancelTitle} body={t.tasks.cancelBody} confirmLabel={t.tasks.cancel} destructive onConfirm={() => disabled ? Promise.reject(new Error(t.tasks.reconnecting)) : action.mutateAsync({ kind: 'cancel', body: { actionId: cancelId.current } })} />
  </div>
}
