import { nodeMessagesUnsupported } from '@/api/task-node-capability'
import { useId, useState } from 'react'
import type { Schemas } from '@/api/client'
import { useTaskRunTimeline } from '@/api/task-inbox'
import { ToolList } from '@/features/chat/RunStatus'
import { BackgroundToolChips } from '@/features/chat/BackgroundToolChip'
import { tasksFromTimeline } from '@/features/chat/background-text'
import { stopTaskNodeBackgroundTask, useTaskNodeBackgroundTasks } from '@/api/background-tasks'
import { unfinishedTool } from '@/features/chat/tool-state'
import { TimelineSteps, UsageBlock } from '@/features/chat/TimelineBody'
import { buildSteps, runDurationMs } from '@/features/chat/timeline-steps'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useRuntimeLookup } from '@/lib/runtime'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { turnErrorText } from './turn-error'

const copy = t.taskInbox

export function TaskRunRecord({ taskId, nodeRunId, runId, agentId, runtimeId }: { taskId: string; nodeRunId: string; runId: string; agentId: string; runtimeId: string }) {
  const timeline = useTaskRunTimeline(taskId, nodeRunId, runId, agentId, runtimeId)
  const [open, setOpen] = useState(false), panelId = useId()
  const runtimes = useRuntimeLookup()
  const pages = timeline.data?.pages ?? [], latest = pages.at(-1)
  // Refetch revisits the loaded cursor range. Each page is one committed slice;
  // usage is the same full-turn total and is displayed once, never added up. When the run says its runtime reported none
  // (run.usageReported), its zeros mean "not known": the record says "no usage data" instead of "0 tokens".
  const entries = pages.flatMap(page => page.entries)
  const tools: Schemas['ToolEvent'][] = entries.filter(entry => entry.type === 'tool').map(entry => ({ toolCallId: entry.toolCallId, toolName: entry.toolName, state: entry.state, at: entry.at }))
  // Tools that went on in the background are chips with their details behind a click (as in an Agent's chat), told by the record; the node's live list is read from the
  // cache only (the node panel keeps it), for whether one was asked to stop.
  const running = useTaskNodeBackgroundTasks(taskId, nodeRunId, false).data?.tasks
  const background = tasksFromTimeline(entries, runId, running, latest ? unfinishedTool(latest.run) !== 'running' : false)
  const backgroundTools = new Set(background.flatMap(task => task.toolCallId ? [task.toolCallId] : []))
  const duration = latest ? runDurationMs(latest.run) : null
  return <div className="mt-2 grid min-w-0 gap-2" data-task-run-id={runId}>
    <ToolList
      tools={tools}
      unfinished={latest ? unfinishedTool(latest.run) : 'running'}
      exclude={backgroundTools}
      after={
        background.length ? (
          <BackgroundToolChips
            tasks={background}
            toolNames={new Map(tools.map((tool) => [tool.toolCallId, tool.toolName]))}
            onStop={(bgTaskId, actionId) => stopTaskNodeBackgroundTask({ taskId, nodeRunId, agentId, runtimeId }, bgTaskId, actionId)}
          />
        ) : undefined
      }
    />
    {timeline.hasNextPage && <p className="text-xs">{copy.timelinePartial}</p>}
    <button type="button" aria-expanded={open} aria-controls={open ? panelId : undefined} onClick={() => setOpen(value => !value)} className="inline-flex min-h-9 items-center gap-2 justify-self-start rounded-full border border-hairline bg-surface-subtle px-3.5 text-[13px] font-semibold text-ink outline-none hover:bg-surface-hover focus-visible:shadow-focus">{duration !== null && latest ? (latest.run.usageReported ? t.timeline.toggle(t.timeline.duration(duration), t.overview.tokens(latest.usage.total)) : t.timeline.toggleNoUsage(t.timeline.duration(duration))) : t.timeline.title}</button>
    {timeline.isPending && <p role="status" className="text-xs">{copy.recordsLoading}</p>}
    <ErrorAlert error={nodeMessagesUnsupported(timeline.error) ? null : timeline.error} />{nodeMessagesUnsupported(timeline.error) && <p role="status" className="text-xs">{copy.toolsUnsupported}</p>}
    {open && latest && <div id={panelId} className="min-w-0 overflow-hidden rounded-card border border-hairline bg-surface-control shadow-surface">
      <div className="grid gap-1 border-b border-hairline bg-surface-subtle p-3 text-xs [overflow-wrap:anywhere]"><strong>{latest.run.id}</strong><span>{latest.run.executionMode === 'task_node' ? copy.workMode : latest.run.executionMode === 'task_conversation' ? copy.chatMode : copy.unknownMode} · {t.tasks.messageStatuses[latest.run.status]}</span>{(latest.run.errorCode || latest.run.errorMessage) && <p>{turnErrorText(latest.run.errorCode, latest.run.errorMessage)}</p>}</div>
      <TimelineSteps steps={buildSteps(null, entries, runtimes.name)} />
      {timeline.hasNextPage && <div className="p-3"><p className="text-xs">{copy.timelinePartial}</p><Button variant="secondary" size="sm" disabled={timeline.isFetching} onClick={() => void timeline.fetchNextPage({ cancelRefetch: false })}>{copy.timelineMore}</Button></div>}
      <UsageBlock usage={latest.usage} reported={latest.run.usageReported} />
    </div>}
  </div>
}
