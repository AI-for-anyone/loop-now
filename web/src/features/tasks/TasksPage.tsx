import { useState } from 'react'
import { GitForkIcon } from 'lucide-react'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { useInboxPending } from '@/api/console-inbox'
import { useAgents } from '@/api/queries'
import { tasksPath, useProjectTaskCounts, useProjectTasks } from '@/api/tasks'
import { AgentAvatar } from '@/components/AgentAvatar'
import { ErrorAlert } from '@/components/ErrorAlert'
import { LiveDot, PageFrame, PageHeader } from '@/components/PageHeader'
import { PageBody, Sheet } from '@/components/Sheet'
import { Button } from '@/components/ui/button'
import { PendingBadge } from '@/features/console-inbox/PendingBadge'
import { useScope } from '@/features/projects/scope-context'
import { t } from '@/i18n'
import { overviewPath } from '@/lib/scope'
import { segmentedClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { TaskBadge } from './TaskBadge'
import { branchParts } from './branch-name'
import { useTaskLive } from './useTaskLive'
import { currentNodesOf } from './workflow-steps'

type Task = Schemas['TaskSummary']
type Filter = 'all' | 'active' | 'blocked' | 'completed'

// Status filters: In progress covers preparing, running and waiting for reply; cancelled tasks appear only under All.
const FILTERS: Filter[] = ['all', 'active', 'blocked', 'completed']
const ACTIVE: Schemas['TaskStatus'][] = ['preparing', 'running', 'waiting']
const inFilter = (filter: Filter, task: Task) =>
  filter === 'all' ? true : filter === 'active' ? ACTIVE.includes(task.status) : task.status === filter

// Shared desktop table grid tracks so header and rows stay aligned across all desktop widths.
const TASK_GRID = 'sm:grid sm:grid-cols-[48px_minmax(0,1fr)_96px_116px_136px_84px] sm:items-center sm:gap-x-4'

export function TasksPage() {
  const scope = useScope()
  if (scope.kind !== 'project') return null
  return <ProjectTasks project={scope.project} />
}

// Project tasks: compact single-row PageHeader with inline KPI summary counts and live status, a 40px segmented status filter toolbar,
// and a full-width CSS Grid table inside PageBody (with ScrollRuler). The list API can't filter by status, so filters apply to loaded
// tasks; active and blocked counts come from the unfinished-tasks counts API while completed and all reflect loaded tasks with "+" when
// more pages exist.
function ProjectTasks({ project }: { project: Schemas['Project'] }) {
  const tasks = useProjectTasks(project.id)
  const counts = useProjectTaskCounts(project.id)
  const agents = useAgents(project.id)
  const connection = useTaskLive(project.id, true)
  const [filter, setFilter] = useState<Filter>('all')
  const rows = [...new Map(tasks.data?.pages.flatMap((p) => p.tasks).map((task) => [task.id, task])).values()]
  // Pending for you: questions and new messages the task's instances sent to @console.
  const pending = useInboxPending()
  const pendingByTask = new Map((pending.data?.byTask ?? []).map((row) => [row.taskId, row]))
  const more = tasks.hasNextPage ? '+' : ''
  const loaded = (which: Filter) => `${rows.filter((task) => inFilter(which, task)).length}${more}`
  const counted = (value: number | undefined, which: Filter) => (value === undefined ? loaded(which) : String(value))
  const amount: Record<Filter, string> = {
    all: loaded('all'),
    active: counted(counts.data && counts.data.preparing + counts.data.running + counts.data.waiting, 'active'),
    blocked: counted(counts.data?.blocked, 'blocked'),
    completed: loaded('completed'),
  }
  const shown = rows.filter((task) => inFilter(filter, task))
  const agentName = (id: string) => agents.data?.agents.find((agent) => agent.id === id)?.name ?? id
  const blockedCount = Number.parseInt(amount.blocked, 10)

  return (
    <PageFrame>
      <PageHeader crumbs={[{ label: project.name, to: overviewPath({ kind: 'project', name: project.name }) }]} title={t.tasks.title}>
        {tasks.isSuccess && (
          <div data-tasks-summary aria-label={t.tasks.progress.title} className="inline-flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[12px] text-ink-500">
            <span className="inline-flex items-baseline gap-1">
              <span className="font-mono font-semibold text-ink tabular-nums">{amount.active}</span>
              <span>{t.tasks.progress.active}</span>
            </span>
            <span aria-hidden="true" className="text-ink-500/50">·</span>
            <span className="inline-flex items-baseline gap-1">
              <span className={cn('font-mono font-semibold tabular-nums', blockedCount > 0 ? 'text-red-text' : 'text-ink-700')}>
                {amount.blocked}
              </span>
              <span>{t.tasks.progress.blocked}</span>
            </span>
            <span aria-hidden="true" className="text-ink-500/50">·</span>
            <span className="inline-flex items-baseline gap-1">
              <span className="font-mono font-semibold text-green-text tabular-nums">{amount.completed}</span>
              <span>{t.tasks.progress.completed}</span>
            </span>
          </div>
        )}
        <LiveDot state={connection === 'open' ? 'open' : connection === 'connecting' ? 'connecting' : 'reconnecting'} label={t.tasks.updating} />
      </PageHeader>
      <div className="flex min-h-10 shrink-0 items-center gap-3 overflow-x-auto border-b border-hairline bg-surface-canvas px-4 py-1.5 no-scrollbar sm:px-6">
        <div
          role="group"
          aria-label={t.tasks.filters.label}
          className={cn(
            segmentedClass,
            'inline-flex h-8 shrink-0 items-center gap-0.5 rounded-input border-hairline bg-surface-subtle p-0.5',
          )}
        >
          {FILTERS.map((item) => {
            const active = item === filter
            return (
              <button
                key={item}
                type="button"
                aria-pressed={active}
                onClick={() => setFilter(item)}
                className={cn(
                  'inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-[calc(var(--skin-radius-input)-2px)] px-2.5 text-[12.5px] whitespace-nowrap transition-colors outline-none focus-visible:shadow-focus',
                  active
                    ? 'bg-surface-control font-semibold text-ink shadow-surface'
                    : 'font-medium text-ink-500 hover:text-ink',
                )}
              >
                {t.tasks.filters[item]}
                <span className={cn('font-mono text-[11px] tabular-nums', active ? 'text-ink-700' : 'text-ink-500')}>
                  {amount[item]}
                </span>
              </button>
            )
          })}
        </div>
      </div>
      <Sheet className="flex-1">
        <PageBody width="wide" innerClassName="pb-12">
          <ErrorAlert error={tasks.error} />
          {tasks.isPending && <p className="text-[13.5px] text-ink-500">{t.tasks.loading}</p>}
          {tasks.isSuccess && rows.length === 0 && (
            <section className="flex flex-col items-start gap-3 py-8">
              <p className="text-[13px] text-ink-500">{t.tasks.intro}</p>
              <p className="text-[14.5px] font-medium text-ink">{t.tasks.empty}</p>
              <Button asChild variant="secondary" size="sm">
                <Link to={project.managerAgentId ? `/p/${encodeURIComponent(project.name)}/agents/${project.managerAgentId}/chat` : `/p/${encodeURIComponent(project.name)}/settings`}>
                  {project.managerAgentId ? t.tasks.manager : t.tasks.settings}
                </Link>
              </Button>
            </section>
          )}
          {rows.length > 0 && shown.length === 0 && (
            <section role="status" className="flex flex-col items-start gap-3 rounded-card border border-hairline bg-surface-subtle/50 px-5 py-6">
              <p className="text-[13.5px] text-ink-700">{t.tasks.filteredEmpty}</p>
              <div className="flex flex-wrap items-center gap-2">
                <Button type="button" variant="secondary" size="xs" onClick={() => setFilter('all')}>
                  {t.tasks.showAll}
                </Button>
                {tasks.hasNextPage && (
                  <Button type="button" variant="ghost" size="xs" disabled={tasks.isFetchingNextPage} onClick={() => void tasks.fetchNextPage()}>
                    {t.tasks.more}
                  </Button>
                )}
              </div>
            </section>
          )}
          {shown.length > 0 && (
            <>
              {/* Decorative column header; each row carries screen-reader labels for its fields. */}
              <div
                aria-hidden="true"
                className={cn(
                  'hidden border-b border-hairline px-2 pb-2 text-[11.5px] font-medium text-ink-500',
                  TASK_GRID,
                )}
              >
                <span>{t.tasks.number}</span>
                <span>{t.tasks.task}</span>
                <span>{t.tasks.state}</span>
                <span>{t.tasks.currentNode}</span>
                <span>{t.tasks.responsible}</span>
                <span className="text-right">{t.tasks.updated}</span>
              </div>
              <ul aria-label={t.tasks.title} className="flex flex-col">
                {shown.map((task) => (
                  <TaskRow key={task.id} task={task} projectName={project.name} agentName={agentName} pending={pendingByTask.get(task.id)} />
                ))}
              </ul>
            </>
          )}
          {shown.length > 0 && tasks.hasNextPage && (
            <Button className="mt-4" variant="secondary" size="sm" disabled={tasks.isFetchingNextPage} onClick={() => void tasks.fetchNextPage()}>
              {t.tasks.more}
            </Button>
          )}
        </PageBody>
      </Sheet>
    </PageFrame>
  )
}

// One row per task: shared 6-track CSS Grid on sm+ and compact 2-row stack on narrow screens.
// Title wins width first; inline branch or Folder copy badge occupies remaining space and yields before the title truncates.
function TaskRow({
  task,
  projectName,
  agentName,
  pending,
}: {
  task: Task
  projectName: string
  agentName: (id: string) => string
  pending?: Parameters<typeof PendingBadge>[0]['counts']
}) {
  const title = task.title || task.id
  const current = currentNodesOf(task)
  const parallel = current.length > 1
  const only = current.length === 1 ? current[0] : null
  const stepLabel = only ? (only.stepTitle || only.stepId) : null
  const stepTitleMissing = only ? !only.stepTitle : false
  const parallelSteps = current.map((c) => c.stepTitle || c.stepId).join(' · ')
  const parallelStepsTooltip = current.map((c) => (c.stepTitle && c.stepTitle !== c.stepId ? `${c.stepTitle} (${c.stepId})` : c.stepId)).join(' · ')
  const uniqueAgents = [...new Set(current.map((c) => c.agentId).filter(Boolean))]
  const hasWorkspaceTag = Boolean(task.branch) || task.workspaceMode === 'copy'
  return (
    <li
      className={cn(
        'flex flex-col gap-1.5 border-b border-hairline px-2 py-2.5 transition-colors hover:bg-surface-hover/50',
        TASK_GRID,
      )}
    >
      {/* Mobile row 1: number + title/reason stay paired; dissolves into grid tracks 1 & 2 on sm+. */}
      <span className="flex min-w-0 items-baseline gap-2.5 sm:contents">
        <span className="w-10 shrink-0 font-mono text-[12px] font-medium text-ink-500 tabular-nums sm:w-auto">
          <span className="sr-only">{t.tasks.number}</span>
          {t.tasks.numberLabel(task.number)}
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-0.5 leading-[1.4]">
          <span className={cn('min-w-0 items-center', hasWorkspaceTag ? 'grid grid-cols-[minmax(0,max-content)_minmax(0,1fr)]' : 'flex gap-2')}>
            <span className="flex min-w-0 items-center gap-2">
              <Link
                className="min-w-0 truncate text-[13.5px] font-semibold text-ink underline decoration-hairline-strong underline-offset-4 hover:decoration-ink"
                to={`${tasksPath(projectName)}/${task.id}`}
                title={title}
              >
                {title}
              </Link>
              <PendingBadge counts={pending} />
            </span>
            {task.branch && <Branch name={task.branch} />}
            {task.workspaceMode === 'copy' && (
              <span className="@container flex min-w-0 items-center overflow-hidden">
                <span className="ml-2 rounded-stamp border border-hairline bg-tint-blue px-1.5 text-[11px] leading-[18px] font-medium whitespace-nowrap text-riso-blue @max-[84px]:hidden">
                  {t.tasks.workspace.copyLabel}
                </span>
              </span>
            )}
          </span>
          {task.reason && (
            <span title={task.reason} className={cn('truncate text-[12px]', task.status === 'blocked' ? 'text-red-text' : 'text-ink-500')}>
              {task.reason}
            </span>
          )}
        </span>
      </span>
      {/* Mobile row 2: state, current node, responsible agent and updated time; dissolves into grid tracks 3-6 on sm+. */}
      <span className="flex flex-wrap items-center gap-x-3 gap-y-1 pl-[50px] sm:contents">
        <span className="min-w-0">
          <span className="sr-only">{t.tasks.state}</span>
          <TaskBadge status={task.status} />
        </span>
        <span className="min-w-0">
          <span className="sr-only">{t.tasks.currentNode}</span>
          {parallel ? (
            <span
              data-parallel-steps
              title={parallelStepsTooltip}
              className="inline-flex max-w-full items-center gap-1 rounded-stamp border border-hairline bg-surface-subtle px-1.5 text-[11.5px] leading-[20px] font-medium text-ink-700"
            >
              <GitForkIcon aria-hidden="true" className="size-3 shrink-0 text-ink-500" strokeWidth={2.2} />
              <span className="min-w-0 truncate">{parallelSteps}</span>
            </span>
          ) : stepLabel ? (
            <span
              title={only?.stepId || undefined}
              className={cn(
                'inline-block max-w-full truncate rounded-stamp border border-hairline bg-surface-subtle px-1.5 text-[11.5px] leading-[20px] font-medium text-ink-700',
                stepTitleMissing && 'font-mono font-normal',
              )}
            >
              {stepLabel}
            </span>
          ) : (
            <span className="text-[12px] text-ink-500">{t.tasks.none}</span>
          )}
        </span>
        <span className="flex min-w-0 items-center gap-1.5 text-[12.5px] font-medium text-ink-700">
          <span className="sr-only">{t.tasks.responsible}</span>
          {uniqueAgents.length > 1 ? (
            <>
              <span className="flex shrink-0 items-center -space-x-1.5">
                {uniqueAgents.slice(0, 3).map((id) => (
                  <AgentAvatar key={id} size="sm" id={id} />
                ))}
              </span>
              <span className="min-w-0 truncate" title={uniqueAgents.map(agentName).join(' · ')}>
                {uniqueAgents.map(agentName).join(' · ')}
              </span>
            </>
          ) : uniqueAgents.length === 1 ? (
            <>
              <AgentAvatar size="sm" id={uniqueAgents[0]} />
              <span className="min-w-0 truncate" title={agentName(uniqueAgents[0])}>
                {agentName(uniqueAgents[0])}
              </span>
            </>
          ) : (
            <span className="text-ink-500">{t.tasks.none}</span>
          )}
        </span>
        <time dateTime={task.updatedAt} className="ml-auto font-mono text-[12px] text-ink-500 tabular-nums sm:ml-0 sm:text-right">
          {t.tasks.updatedAt(task.updatedAt)}
        </time>
      </span>
    </li>
  )
}

// The branch of a task with a worktree, inline in the remaining space after the title: small, muted monospace, middle-truncated down to a
// short stub and hidden visually when too narrow for a readable stub so the title always wins the row width first (the full name stays in
// the hover title and for screen readers).
function Branch({ name }: { name: string }) {
  const { head, tail } = branchParts(name)
  return (
    <span data-testid="task-branch" title={name} className="@container min-w-0 overflow-hidden font-mono text-[11.5px] leading-[18px] text-ink-500">
      <span className="sr-only">{t.tasks.branchLabel(name)}</span>
      {head ? (
        <span aria-hidden="true" className="flex min-w-0 pl-2 @max-[108px]:hidden">
          <span className="min-w-0 truncate">{head}</span>
          <span className="shrink-0 whitespace-pre">{tail}</span>
        </span>
      ) : (
        <span aria-hidden="true" className="block min-w-0 truncate pl-2 @max-[56px]:hidden">
          {tail}
        </span>
      )}
    </span>
  )
}
