import { useId, useState, type ReactNode } from 'react'
import { PluginSenderChip } from '@/features/plugins/ChatLines'
import { Link } from 'react-router'
import { ArrowRightIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { useInboxPending } from '@/api/console-inbox'
import { useProjects } from '@/api/queries'
import { tasksPath, useTask } from '@/api/tasks'
import { Badge } from '@/components/ui/badge'
import { ScopeTag } from '@/features/projects/OverviewScope'
import { dayLabel } from '@/features/overview/day-label'
import { partyName } from '@/features/inbox/participants'
import { t } from '@/i18n'
import type { ScopeQuery } from '@/lib/scope'

const text = t.consoleInbox
const DEFAULT_VISIBLE = 3

type Item = Schemas['InboxPendingItem'] & { kind: 'question' | 'unread' }

// Needs you: compact attention strip placed above the overview KPIs. Shows open questions first, then unread messages, capped at
// three rows by default with an inline expand toggle so pending items never push the dashboard below the fold.
export function PendingSection({ scope = {} }: { scope?: ScopeQuery }) {
  const titleId = useId()
  const [expanded, setExpanded] = useState(false)
  const pending = useInboxPending()
  if (!pending.data) return null
  const inScope = (item: Schemas['InboxPendingItem']) =>
    scope.projectId ? item.projectId === scope.projectId : scope.platform ? item.projectId === null && item.message.from.kind === 'agent' : true
  const items: Item[] = [
    ...pending.data.questions.filter(inScope).map((item) => ({ ...item, kind: 'question' as const })),
    ...pending.data.unread.filter(inScope).map((item) => ({ ...item, kind: 'unread' as const })),
  ]
  if (items.length === 0) return null
  const visible = expanded ? items : items.slice(0, DEFAULT_VISIBLE)
  const hiddenCount = items.length - visible.length

  return (
    <section
      aria-labelledby={titleId}
      data-pending
      className="flex flex-col gap-1.5 rounded-card border border-hairline-strong bg-surface-subtle px-3.5 py-2.5"
    >
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
        <h2
          id={titleId}
          className="text-[13.5px] leading-snug font-semibold text-ink"
        >
          {text.pendingTitle}
        </h2>
        <span className="font-mono text-[11.5px] font-medium text-ink-500 tabular-nums">
          {text.pendingCount(items.length)}
        </span>
        {items.length > DEFAULT_VISIBLE && (
          <button
            type="button"
            onClick={() => setExpanded((value) => !value)}
            className="ml-auto cursor-pointer rounded-stamp px-2 py-0.5 text-[12px] font-medium text-ink-700 outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus"
          >
            {expanded ? t.overview.pendingLess : t.overview.pendingMore(hiddenCount)}
          </button>
        )}
      </div>
      <ul className="flex flex-col">
        {visible.map((item) => (
          <PendingRow key={item.message.id} item={item} />
        ))}
      </ul>
    </section>
  )
}

const rowClass =
  'grid min-h-9 grid-cols-[minmax(52px,76px)_minmax(0,1fr)] items-center gap-x-3 gap-y-0.5 border-t border-hairline px-1 py-1.5 text-ink hover:bg-surface-hover sm:grid-cols-[76px_minmax(0,1fr)_max-content]'

function PendingRow({ item }: { item: Item }) {
  const { message } = item
  const body = (target: string | null, field?: string) => (
    <>
      <span className="flex items-baseline gap-1.5 sm:flex-col sm:gap-0">
        <span className="font-mono text-[12px] leading-tight font-semibold tabular-nums">
          {t.overview.anomalies.time(message.createdAt)}
        </span>
        <span className="text-[11px] leading-tight text-ink-500">{dayLabel(message.createdAt)}</span>
      </span>
      <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
        <Badge variant="stamp" tone={item.kind === 'question' ? 'busy' : 'info'} className="[--stamp-tilt:0deg]">
          {text.pendingKind[item.kind]}
        </Badge>
        {message.from.kind === 'plugin' ? (
          <PluginSenderChip participant={message.from} />
        ) : (
          <span className="shrink-0 text-[13px] font-semibold [overflow-wrap:anywhere]">
            {partyName(message.from)}
          </span>
        )}
        {message.from.kind === 'agent' && <ScopeTag scope={item.projectId ? 'project' : 'platform'} projectId={item.projectId} />}
        {field && <span className="shrink-0 text-[11.5px] [overflow-wrap:anywhere] text-ink-500">{field}</span>}
        <span className="min-w-0 flex-1 truncate text-[12.5px] text-ink-700">{message.preview}</span>
      </span>
      {target && (
        <span className="col-start-2 inline-flex items-center gap-1 text-[12.5px] font-semibold whitespace-nowrap sm:col-start-auto">
          {item.kind === 'question' ? text.answer : text.view}
          <ArrowRightIcon aria-hidden="true" className="size-3" strokeWidth={2.4} />
        </span>
      )}
    </>
  )
  if (message.task) return <TaskRow item={item} task={message.task} render={body} />
  const target =
    message.from.kind === 'agent' && message.from.id
      ? `/agents/${message.from.id}/chat`
      : message.from.kind === 'external' && message.from.id
        ? `/identities/${message.from.id}`
        : null
  return (
    <li>
      {target ? (
        <Link to={target} className={rowClass}>
          {body(target)}
        </Link>
      ) : (
        <div className={rowClass}>{body(null)}</div>
      )}
    </li>
  )
}

// From a task instance: link to the node in the task that this message belongs to, and show the task name.
function TaskRow({ item, task, render }: { item: Item; task: Schemas['InboxTaskRef']; render: (target: string | null, field?: string) => ReactNode }) {
  const detail = useTask(task.taskId)
  const projects = useProjects()
  const project = projects.data?.projects.find((p) => p.id === (item.projectId ?? detail.data?.task.projectId))
  const target = project ? `${tasksPath(project.name)}/${task.taskId}${task.nodeRunId ? `?${new URLSearchParams({ record: task.nodeRunId })}` : ''}` : null
  const field = text.inTask(detail.data?.task.title ?? text.taskFallback)
  return (
    <li>
      {target ? (
        <Link to={target} className={rowClass}>
          {render(target, field)}
        </Link>
      ) : (
        <div className={rowClass}>{render(null, field)}</div>
      )}
    </li>
  )
}
