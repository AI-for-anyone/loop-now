import { useId, type ReactNode } from 'react'
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
import { t } from '@/i18n'
import { consoleInboxText as text } from '@/i18n/console-inbox'
import type { ScopeQuery } from '@/lib/scope'

type Item = Schemas['InboxPendingItem'] & { kind: 'question' | 'unread' }

// 待你处理：发给你的 open 问题在前，其他未读消息在后，各自从新到旧；每条链接到消息所在的对话或任务节点。
// 按概览的范围筛选：项目只看这个项目的，平台只看平台 Agent 的。没有待处理的内容时整块不显示。
export function PendingSection({ scope = {} }: { scope?: ScopeQuery }) {
  const titleId = useId()
  const pending = useInboxPending()
  if (!pending.data) return null
  const inScope = (item: Schemas['InboxPendingItem']) =>
    scope.projectId ? item.projectId === scope.projectId : scope.platform ? item.projectId === null && item.message.from.kind === 'agent' : true
  const items: Item[] = [
    ...pending.data.questions.filter(inScope).map((item) => ({ ...item, kind: 'question' as const })),
    ...pending.data.unread.filter(inScope).map((item) => ({ ...item, kind: 'unread' as const })),
  ]
  if (items.length === 0) return null
  return (
    <section aria-labelledby={titleId} className="flex flex-col gap-[18px]">
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-2.5">
        <h2 id={titleId} className="riso-misreg-sm font-display-cn text-[40px] leading-[1.1] font-normal">{text.pendingTitle}</h2>
        <span className="text-[13px] font-bold text-ink-500">{text.pendingCount(items.length)}</span>
      </div>
      <ul className="flex flex-col gap-2.5">
        {items.map((item) => <PendingRow key={item.message.id} item={item} />)}
      </ul>
    </section>
  )
}

const rowClass =
  'grid min-h-16 grid-cols-[minmax(64px,96px)_minmax(0,1fr)] items-center gap-x-4 gap-y-1.5 rounded-[16px] bg-white px-[18px] py-3 text-ink shadow-print-rest sm:grid-cols-[96px_minmax(0,1fr)_max-content]'

function PendingRow({ item }: { item: Item }) {
  const { message } = item
  const body = (target: string | null, field?: string) => (
    <>
      <span className="flex flex-col">
        <span className="font-mono text-[13px] font-bold">{t.overview.anomalies.time(message.createdAt)}</span>
        <span className="text-[12px] text-ink-500">{dayLabel(message.createdAt)}</span>
      </span>
      <span className="flex min-w-0 flex-col gap-[3px]">
        <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
          <Badge variant="stamp" tone={item.kind === 'question' ? 'busy' : 'info'} className="[--stamp-tilt:0deg]">{text.pendingKind[item.kind]}</Badge>
          {message.from.kind === 'plugin' ? <PluginSenderChip participant={message.from} /> : <span className="text-[14.5px] font-black [overflow-wrap:anywhere]">{message.from.name}</span>}
          {message.from.kind === 'agent' && <ScopeTag scope={item.projectId ? 'project' : 'platform'} projectId={item.projectId} />}
          {field && <span className="text-[12px] [overflow-wrap:anywhere] text-ink-500">{field}</span>}
        </span>
        <span className="line-clamp-2 text-[13.5px] [overflow-wrap:anywhere] text-ink-700">{message.preview}</span>
      </span>
      {target && (
        <span className="col-start-2 inline-flex items-center gap-1.5 text-[13.5px] font-black whitespace-nowrap sm:col-start-auto">
          {item.kind === 'question' ? text.answer : text.view}
          <ArrowRightIcon aria-hidden="true" className="size-3.5" strokeWidth={2.6} />
        </span>
      )}
    </>
  )
  if (message.task) return <TaskRow item={item} task={message.task} render={body} />
  const target = message.from.kind === 'agent' && message.from.id ? `/agents/${message.from.id}/chat` : message.from.kind === 'external' && message.from.id ? `/identities/${message.from.id}` : null
  return <li>{target ? <Link to={target} className={`${rowClass} hover:bg-tint-pink-2`}>{body(target)}</Link> : <div className={rowClass}>{body(null)}</div>}</li>
}

// 任务实例发来的：链接到任务里这条消息所属的节点，标出任务名称。
function TaskRow({ item, task, render }: { item: Item; task: Schemas['InboxTaskRef']; render: (target: string | null, field?: string) => ReactNode }) {
  const detail = useTask(task.taskId)
  const projects = useProjects()
  const project = projects.data?.projects.find((p) => p.id === (item.projectId ?? detail.data?.task.projectId))
  const target = project ? `${tasksPath(project.name)}/${task.taskId}${task.nodeRunId ? `?${new URLSearchParams({ record: task.nodeRunId })}` : ''}` : null
  const field = text.inTask(detail.data?.task.title ?? text.taskFallback)
  return <li>{target ? <Link to={target} className={`${rowClass} hover:bg-tint-pink-2`}>{render(target, field)}</Link> : <div className={rowClass}>{render(null, field)}</div>}</li>
}
