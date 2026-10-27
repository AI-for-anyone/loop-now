import { nodeMessagesUnsupported } from '@/api/task-node-capability'
import { useId, useState } from 'react'
import type { Schemas } from '@/api/client'
import { useTaskRunTimeline } from '@/api/task-inbox'
import { ToolList } from '@/features/chat/RunStatus'
import { TimelineSteps, UsageBlock } from '@/features/chat/TimelineBody'
import { buildSteps, runDurationMs } from '@/features/chat/timeline-steps'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { taskInboxText as copy } from '@/i18n/task-inbox'

export function TaskRunRecord({ taskId, nodeRunId, runId, agentId, runtimeId }: { taskId: string; nodeRunId: string; runId: string; agentId: string; runtimeId: string }) {
  const timeline = useTaskRunTimeline(taskId, nodeRunId, runId, agentId, runtimeId)
  const [open, setOpen] = useState(false), panelId = useId()
  const pages = timeline.data?.pages ?? [], latest = pages.at(-1)
  // Refetch revisits the loaded cursor range. Each page is one committed slice;
  // usage is the same full-turn total and is displayed once, never added up.
  const entries = pages.flatMap(page => page.entries)
  const tools: Schemas['ToolEvent'][] = entries.filter(entry => entry.type === 'tool').map(entry => ({ toolCallId: entry.toolCallId, toolName: entry.toolName, state: entry.state, at: entry.at }))
  const duration = latest ? runDurationMs(latest.run) : null
  return <div className="mt-2 grid min-w-0 gap-2" data-task-run-id={runId}>
    <ToolList tools={tools} />
    {timeline.hasNextPage && <p className="text-xs">{copy.timelinePartial}</p>}
    <button type="button" aria-expanded={open} aria-controls={open ? panelId : undefined} onClick={() => setOpen(value => !value)} className="inline-flex min-h-9 items-center gap-2 justify-self-start rounded-full bg-tint-blue px-3.5 text-[13px] font-black text-ink outline-none hover:bg-[#d6ddf4] focus-visible:shadow-focus">{duration !== null && latest ? t.timeline.toggle(t.timeline.duration(duration), t.overview.tokens(latest.usage.total)) : t.timeline.title}</button>
    {timeline.isPending && <p role="status" className="text-xs">{copy.recordsLoading}</p>}
    <ErrorAlert error={nodeMessagesUnsupported(timeline.error) ? null : timeline.error} />{nodeMessagesUnsupported(timeline.error) && <p role="status" className="text-xs">{copy.toolsUnsupported}</p>}
    {open && latest && <div id={panelId} className="min-w-0 overflow-hidden rounded-[16px] bg-white shadow-[3px_3px_0_rgb(24_38_94/.12)]">
      <div className="grid gap-1 bg-tint-blue p-3 text-xs [overflow-wrap:anywhere]"><strong>{latest.run.id}</strong><span>{latest.run.executionMode === 'task_node' ? copy.workMode : latest.run.executionMode === 'task_conversation' ? copy.chatMode : copy.unknownMode} · {t.tasks.messageStatuses[latest.run.status]}</span>{latest.run.errorMessage && <p>{latest.run.errorMessage}</p>}</div>
      <TimelineSteps steps={buildSteps(null, entries)} />
      {timeline.hasNextPage && <div className="p-3"><p className="text-xs">{copy.timelinePartial}</p><Button variant="secondary" size="sm" disabled={timeline.isFetching} onClick={() => void timeline.fetchNextPage({ cancelRefetch: false })}>{copy.timelineMore}</Button></div>}
      <UsageBlock usage={latest.usage} />
    </div>}
  </div>
}
