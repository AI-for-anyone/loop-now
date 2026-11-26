import { useState } from 'react'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { useAgents } from '@/api/queries'
import { tasksPath, useProjectTaskCounts, useProjectTasks } from '@/api/tasks'
import { useInboxPending } from '@/api/console-inbox'
import { AgentAvatar } from '@/components/AgentAvatar'
import { ErrorAlert } from '@/components/ErrorAlert'
import { LiveDot, PageFrame, PageHeader } from '@/components/PageHeader'
import { RulerTicks, Sheet, Stub, StubFigure } from '@/components/Sheet'
import { Button } from '@/components/ui/button'
import { PendingBadge } from '@/features/console-inbox/PendingBadge'
import { useScope } from '@/features/projects/scope-context'
import { t } from '@/i18n'
import { indexTabClass } from '@/lib/index-tab'
import { overviewPath } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { TaskBadge } from './TaskBadge'
import { branchParts } from './branch-name'
import { useTaskLive } from './useTaskLive'

type Task = Schemas['TaskSummary']
type Filter = 'all' | 'active' | 'blocked' | 'completed'

// Status filters: In progress covers preparing, running and waiting for reply; cancelled tasks appear only under “全部” (All).
const FILTERS: Filter[] = ['all', 'active', 'blocked', 'completed']
const ACTIVE: Schemas['TaskStatus'][] = ['preparing', 'running', 'waiting']
const inFilter = (filter: Filter, task: Task) =>
  filter === 'all' ? true : filter === 'active' ? ACTIVE.includes(task.status) : task.status === filter

export function TasksPage() {
  const scope = useScope()
  if (scope.kind !== 'project') return null
  return <ProjectTasks project={scope.project} />
}

// Project tasks (step 3 of the "printed sheet" redesign): below the page title, the status filters on the sheet's top edge (index tabs,
// with counts); on the sheet, the task list;
// in the stub on the right, three big numbers: in progress, blocked, completed. The list API can't filter by status, so the filters
// apply only to the tasks read so far;
// the in-progress and blocked counts come from the API that counts unfinished tasks (consistent with the sidebar), while completed and
// all are counted from the tasks read so far, with a trailing "+" while there are more pages.
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

  return (
    <PageFrame>
      <PageHeader crumbs={[{ label: project.name, to: overviewPath({ kind: 'project', name: project.name }) }]} title={t.tasks.title}>
        <span>{t.tasks.intro}</span>
        <LiveDot state={connection === 'open' ? 'open' : connection === 'connecting' ? 'connecting' : 'reconnecting'} label={t.tasks.updating} />
      </PageHeader>
      <div role="group" aria-label={t.tasks.filters.label} className="relative z-[1] flex shrink-0 items-end gap-1.5 pl-6 max-nav:overflow-x-auto max-nav:pt-1.5 max-nav:pl-3.5 max-nav:no-scrollbar">
        {FILTERS.map((item) => (
          <button key={item} type="button" aria-pressed={item === filter} onClick={() => setFilter(item)} className={cn(indexTabClass(item === filter), 'cursor-pointer')}>
            {t.tasks.filters[item]}
            <span className="font-display-latin text-[12px] font-extrabold">{amount[item]}</span>
          </button>
        ))}
      </div>
      <Sheet className="flex-1">
        <div className="flex min-h-0 flex-1">
          {/* The scroll area is the positioning container: absolutely positioned elements inside it (such as sr-only text) scroll with
              the content instead of stretching the whole page based on their unscrolled position. */}
          <div className="relative h-full min-w-0 flex-1 overflow-y-auto px-3.5 pt-3.5 pb-14 nav:px-6">
            <ErrorAlert error={tasks.error} />
            {tasks.isPending && <p className="text-[14px] text-ink-500">{t.tasks.loading}</p>}
            {tasks.isSuccess && rows.length === 0 && (
              <section className="flex flex-col items-start gap-4 py-6">
                <p className="text-[14.5px]">{t.tasks.empty}</p>
                <Button asChild variant="secondary">
                  <Link to={project.managerAgentId ? `/p/${encodeURIComponent(project.name)}/agents/${project.managerAgentId}/chat` : `/p/${encodeURIComponent(project.name)}/settings`}>
                    {project.managerAgentId ? t.tasks.manager : t.tasks.settings}
                  </Link>
                </Button>
              </section>
            )}
            {rows.length > 0 && (
              <>
                {/* Table header (decorative; each row carries screen-reader text for the column names): a 2px solid line below it. */}
                <div aria-hidden="true" className="flex items-center gap-3 border-b-2 border-ink px-1 pb-1.5 text-[12px] font-black text-ink-500 max-sm:hidden">
                  <span className="flex-[0_0_34px]">{t.tasks.number}</span>
                  <span className="flex-[1_1_134px]">{t.tasks.task}</span>
                  <span className="flex-[0_0_114px]">{t.tasks.state}</span>
                  <span className="flex-[0_0_120px]">{t.tasks.currentNode}</span>
                  <span className="flex-[0_0_160px]">{t.tasks.responsible}</span>
                  <span className="flex-[0_0_82px] text-right">{t.tasks.updated}</span>
                </div>
                <ul aria-label={t.tasks.title} className="flex flex-col">
                  {shown.map((task) => (
                    <TaskRow key={task.id} task={task} projectName={project.name} agentName={agentName} pending={pendingByTask.get(task.id)} />
                  ))}
                </ul>
              </>
            )}
            {tasks.hasNextPage && (
              <Button className="mt-5" variant="secondary" disabled={tasks.isFetchingNextPage} onClick={() => void tasks.fetchNextPage()}>
                {t.tasks.more}
              </Button>
            )}
          </div>
          <Stub as="aside" aria-label={t.tasks.progress.title} label={t.tasks.progress.title} className="w-[150px]">
            {tasks.isSuccess && (
              <div className="relative flex flex-1 flex-col gap-[22px] pt-1.5 pr-[30px] pl-[18px]">
                <RulerTicks />
                <StubFigure tone="pink" value={amount.active} label={t.tasks.progress.active} />
                <StubFigure tone="red" value={amount.blocked} label={t.tasks.progress.blocked} />
                <StubFigure tone="green" value={amount.completed} label={t.tasks.progress.completed} />
              </div>
            )}
          </Stub>
        </div>
      </Sheet>
    </PageFrame>
  )
}

// One compact line per task (~40px): monospace number, single-line title (with full title on hover) plus inline branch or folder-copy
// label, status chip, current step title (with step id on hover), responsible Agent (with avatar), and monospace updated time.
// 1.5px dashed lines between rows; at phone width the row wraps into two short lines; a wait/failure reason sits on a tight truncated
// line beneath only when present.
function TaskRow({ task, projectName, agentName, pending }: { task: Task; projectName: string; agentName: (id: string) => string; pending?: Parameters<typeof PendingBadge>[0]['counts'] }) {
  const title = task.title || task.id
  const stepLabel = task.currentStepTitle || task.currentStepId
  const hasWorkspaceTag = Boolean(task.branch) || task.workspaceMode === 'copy'
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b-[1.5px] border-dashed border-ink/25 px-1 py-[7px] max-sm:gap-x-2">
      {/* Number and title form one group (width = number 34px + gap 12px + title from 134px) that doesn't wrap: on narrow screens they stay
          together on the first short line. */}
      <span className="flex min-w-0 flex-[1_1_180px] flex-nowrap items-center gap-x-3 max-sm:basis-full max-sm:gap-x-2">
        <span className="flex-[0_0_34px] font-mono text-[12px] font-bold text-ink-500">
          <span className="sr-only">{t.tasks.number}</span>
          {t.tasks.numberLabel(task.number)}
        </span>
        {/* Title (+ unread badge) always wins space first via minmax(0, max-content); branch or Folder copy sits in the remaining 1fr
            track and gives way (shrinking to a short stub, then hiding) before the title ever truncates. */}
        <span className={cn('min-w-0 flex-1 items-center', hasWorkspaceTag ? 'grid grid-cols-[minmax(0,max-content)_minmax(0,1fr)]' : 'flex gap-2')}>
          <span className="flex min-w-0 items-center gap-2">
            <Link
              title={title}
              className="min-w-0 truncate text-[14px] leading-[22px] font-black text-ink underline decoration-ink/30 underline-offset-3 hover:text-riso-blue hover:decoration-riso-blue"
              to={`${tasksPath(projectName)}/${task.id}`}
            >
              {title}
            </Link>
            <PendingBadge counts={pending} />
          </span>
          {task.branch && <Branch name={task.branch} />}
          {task.workspaceMode === 'copy' && (
            <span className="@container flex min-w-0 items-center overflow-hidden">
              <span className="ml-2 rounded-[4px] bg-tint-blue px-1.5 text-[11px] leading-[18px] font-bold whitespace-nowrap text-riso-blue @max-[84px]:hidden">
                {t.tasks.workspace.copyLabel}
              </span>
            </span>
          )}
        </span>
      </span>
      {/* On desktop this wrapper disappears (display: contents) so the four metadata columns align directly with the table header; on phone
          width it groups them into one tight second line without letting the timestamp dangle on a third line. */}
      <span className="contents max-sm:flex max-sm:min-w-0 max-sm:basis-full max-sm:items-center max-sm:gap-x-1.5">
        <span className="flex-[0_0_114px] max-sm:flex-none">
          <span className="sr-only">{t.tasks.state}</span>
          <TaskBadge status={task.status} className="min-h-[20px] px-2 text-[11.5px] leading-[20px]" />
        </span>
        <span className="min-w-0 flex-[0_0_120px] max-sm:max-w-[92px] max-sm:flex-[0_1_auto]">
          <span className="sr-only">{t.tasks.currentNode}</span>
          {stepLabel ? (
            <span
              title={task.currentStepId ?? undefined}
              className={cn(
                'inline-block max-w-full truncate rounded-[4px] bg-tint-blue px-1.5 align-middle text-[12px] leading-[20px] font-bold text-ink',
                !task.currentStepTitle && 'font-mono font-normal',
              )}
            >
              {stepLabel}
            </span>
          ) : (
            <span className="text-ink-500">{t.tasks.none}</span>
          )}
        </span>
        <span className="flex min-w-0 flex-[0_0_160px] items-center gap-1.5 text-[13px] leading-[20px] font-bold max-sm:flex-[1_1_0px]">
          <span className="sr-only">{t.tasks.responsible}</span>
          {task.currentAgentId ? (
            <>
              <AgentAvatar size="sm" id={task.currentAgentId} />
              <span className="min-w-0 truncate" title={agentName(task.currentAgentId)}>
                {agentName(task.currentAgentId)}
              </span>
            </>
          ) : (
            <span className="text-ink-500">{t.tasks.none}</span>
          )}
        </span>
        <time dateTime={task.updatedAt} className="flex-[0_0_82px] text-right font-mono text-[11.5px] leading-[20px] whitespace-nowrap text-ink-700 max-sm:ml-auto max-sm:flex-none">
          {t.tasks.updatedAt(task.updatedAt)}
        </time>
      </span>
      {task.reason && (
        <span
          title={task.reason}
          className={cn('min-w-0 basis-full truncate pl-[46px] text-[12px] leading-[1.35] max-sm:pl-0', task.status === 'blocked' ? 'text-red-text' : 'text-ink-500')}
        >
          {task.reason}
        </span>
      )}
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
