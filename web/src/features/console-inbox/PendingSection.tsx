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
import type { ScopeQuery } from '@/lib/scope'

const text = t.consoleInbox

type Item = Schemas['InboxPendingItem'] & { kind: 'question' | 'unread' }

// 待你处理：发给你的 open 问题在前，其他未读消息在后，各自从新到旧；每条链接到消息所在的对话或任务节点。
// 按概览的范围筛选：项目只看这个项目的，平台只看平台 Agent 的。没有待处理的内容时整块不显示。
// 改版：排在概览纸张的最上面，不编号；浅粉底、2px 藏青描边、荧光粉实色投影的卡片，条目之间虚线。
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
    <section aria-labelledby={titleId} data-pending className="mr-1 mb-1 flex flex-col gap-2 rounded-card border-2 border-ink bg-tint-pink-2 px-[18px] pt-3.5 pb-2 shadow-print-pink">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1.5">
        <h2 id={titleId} className="font-display-cn text-[24px] leading-[1.2] font-normal">{text.pendingTitle}</h2>
        <span className="text-[12px] font-bold text-ink-500">{text.pendingCount(items.length)}</span>
      </div>
      <ul className="flex flex-col">
        {items.map((item) => <PendingRow key={item.message.id} item={item} />)}
      </ul>
    </section>
  )
}

const rowClass =
  'grid min-h-14 grid-cols-[minmax(64px,96px)_minmax(0,1fr)] items-center gap-x-4 gap-y-1.5 border-t-[1.5px] border-dashed border-ink/25 px-1 py-2.5 text-ink hover:bg-white/60 sm:grid-cols-[96px_minmax(0,1fr)_max-content]'

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
