import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'
import { imageDraftsOf } from '@/api/message-images'
import { taskInboxKeys } from '@/api/task-inbox'
import { nodeMessagesUnsupported } from '@/api/task-node-capability'
import { taskNodeQueueAction, taskNodeQueueKeys, useTaskNodeQueue } from '@/api/task-node-queue'
import { taskKeys } from '@/api/tasks'
import { PromoteButton, QueueMessages, QueuePanel, RowButtons, type QueueRow } from '@/features/chat/QueueList'
import { nodeConflictNotice, useQueueActions, useQueueNotice } from '@/features/chat/queue-actions'
import type { ImageDraft } from '@/features/images/image-drafts'
import { partyDisplay } from '@/features/inbox/participants'
import { t } from '@/i18n'

type Operation = 'promote' | 'cancel'
// The node's queued messages, one row per message in the server's claim order.
// Every row can be promoted (unless already an interrupt) and cancelled; the console's own rows can also be edited.
export function NodeQueueList({ taskId, nodeRunId, agentId, runtimeId, disabled, onEdit }: { taskId: string; nodeRunId: string; agentId: string; runtimeId: string; disabled: boolean; onEdit: (text: string, images: ImageDraft[]) => void }) {
  const client = useQueryClient()
  const queue = useTaskNodeQueue(taskId, nodeRunId, true)
  const actions = useQueueActions(), notice = useQueueNotice()
  const [error, setError] = useState<unknown>(null)
  // Collapsed to one line by default; kept while this node panel is mounted, reset on another node or reload.
  const [expanded, setExpanded] = useState(false)
  const refresh = () => { void client.invalidateQueries({ queryKey: taskNodeQueueKeys.node(taskId, nodeRunId) }); void client.invalidateQueries({ queryKey: taskInboxKeys.records(taskId, nodeRunId) }); void client.invalidateQueries({ queryKey: taskKeys.messages(taskId, nodeRunId) }) }
  const act = async (item: Schemas['QueueItem'], operation: Operation, edit = false) => {
    const messageId = item.messageId
    if (!messageId) return
    setError(null); notice.clear()
    // Images are read back before cancelling, so a failed read leaves the message queued.
    let drafts: ImageDraft[] = []
    const result = await actions.run(messageId, edit ? 'edit' : operation, async actionId => {
      if (edit && item.images.length > 0) drafts = await imageDraftsOf(item.images)
      return taskNodeQueueAction(taskId, nodeRunId, messageId, operation, { actionId, expectedAgentId: agentId, expectedRuntimeId: runtimeId })
    })
    if (!result) return
    if (result.ok) {
      // The receipt's queue is not part of the node contract; the list follows task.node.queue.updated and the refresh below.
      if (edit) onEdit((result.value.item ?? item).text, drafts)
    } else {
      // The retry button must stay reachable even if the list was collapsed meanwhile.
      if (result.indeterminate) setExpanded(true)
      const text = nodeConflictNotice(result.error, operation)
      if (text) notice.show(text)
      else setError(result.error)
    }
    refresh()
  }
  if (nodeMessagesUnsupported(queue.error)) return null
  // A queue read for another instance of this node is not this draft's queue.
  const items = queue.data?.runtimeId === runtimeId ? queue.data.items : []
  const rows = items.flatMap((item): QueueRow[] => {
    const id = item.messageId
    if (!id) return []
    const own = item.from.kind === 'console', interrupt = item.whenBusy === 'interrupt', busy = actions.pending.has(id)
    return [{
      key: id, interrupt, sender: { name: own ? t.chat.you : partyDisplay(item.from).title, own }, text: item.text, images: item.images,
      actions: <>
        {!interrupt && <PromoteButton pending={busy} disabled={disabled} label={actions.isUnknown(id, 'promote') ? t.chat.queueList.retryPromote : undefined} onClick={() => void act(item, 'promote')} />}
        <RowButtons disabled={busy || disabled} onEdit={own ? () => void act(item, 'cancel', true) : undefined} onCancel={() => void act(item, 'cancel')} cancelLabel={actions.isUnknown(id, 'cancel') ? t.chat.queueList.retryCancel : undefined} />
      </>,
    }]
  })
  const problem = error ?? queue.error
  if (!notice.text && !problem && items.length === 0) return null
  return <div className="grid" data-node-queue>
    <QueueMessages notice={notice.text} error={problem} />
    <QueuePanel count={items.length} rows={rows} collapse={{ expanded, onToggle: () => setExpanded(value => !value) }} />
  </div>
}
