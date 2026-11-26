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
      <div role="group" aria-label={t.tasks.filters.label} className="relative z-[1] flex shrink-0 items-end gap-1.5 pl-[26px] max-nav:overflow-x-auto max-nav:pt-1.5 max-nav:pl-3 max-nav:no-scrollbar">
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
          <div className="relative h-full min-w-0 flex-1 overflow-y-auto pt-[22px] pr-[clamp(14px,3vw,40px)] pb-14 pl-[clamp(12px,2vw,34px)]">
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
                <div aria-hidden="true" className="flex items-center gap-[18px] border-b-2 border-ink px-1 pb-2 text-[12px] font-black text-ink-500 max-sm:hidden">
                  <span className="flex-[0_0_34px]">{t.tasks.number}</span>
                  <span className="flex-[1_1_280px]">{t.tasks.task}</span>
                  <span className="flex-[0_0_84px]">{t.tasks.state}</span>
                  <span className="flex-[0_0_104px]">{t.tasks.currentNode}</span>
                  <span className="flex-[0_0_130px]">{t.tasks.responsible}</span>
                  <span className="flex-[0_0_96px] text-right">{t.tasks.updated}</span>
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

// One row per task: monospace number, title (linking to the details) plus subtitle (wait or failure reason), status, monospace current
// node, responsible Agent (with avatar), monospace updated time.
// 1.5px dashed lines between rows; the columns wrap when narrow.
function TaskRow({ task, projectName, agentName, pending }: { task: Task; projectName: string; agentName: (id: string) => string; pending?: Parameters<typeof PendingBadge>[0]['counts'] }) {
  return (
    <li className="flex flex-wrap items-center gap-x-[18px] gap-y-1.5 border-b-[1.5px] border-dashed border-ink/25 px-1 py-3.5">
      {/* Number and title form one group (width = number 34px + gap 18px + title from 280px) that doesn't wrap: on narrow screens the
          number stays to the left of the title. */}
      <span className="flex min-w-0 flex-[1_1_332px] flex-nowrap items-center gap-x-[18px] max-sm:items-start max-sm:gap-x-2.5">
        <span className="flex-[0_0_34px] font-mono text-[12px] font-bold text-ink-500 max-sm:leading-[21px]">
          <span className="sr-only">{t.tasks.number}</span>
          {t.tasks.numberLabel(task.number)}
        </span>
        <span className="flex min-w-0 flex-1 flex-col leading-[1.4]">
          <span className="flex min-w-0 items-start gap-2">
            <Link
              className="min-w-0 text-[15px] font-black break-words text-ink underline decoration-ink/30 underline-offset-4 hover:text-riso-blue hover:decoration-riso-blue"
              to={`${tasksPath(projectName)}/${task.id}`}
            >
              {task.title || task.id}
            </Link>
            <PendingBadge counts={pending} />
          </span>
          {task.branch && <Branch name={task.branch} />}
          {task.workspaceMode === 'copy' && <span className="w-fit rounded-[4px] bg-tint-blue px-1.5 text-[11.5px] leading-[20px] font-bold text-riso-blue">{t.tasks.workspace.copyLabel}</span>}
          {task.reason && <span className={cn('text-[12px] break-words', task.status === 'blocked' ? 'text-red-text' : 'text-ink-500')}>{task.reason}</span>}
        </span>
      </span>
      <span className="flex-[0_0_84px]">
        <span className="sr-only">{t.tasks.state}</span>
        <TaskBadge status={task.status} />
      </span>
      <span className="min-w-0 flex-[0_0_104px]">
        <span className="sr-only">{t.tasks.currentNode}</span>
        {task.currentStepId ? (
          <span className="rounded-[4px] bg-tint-blue px-2 font-mono text-[12px] leading-[22px] break-all">{task.currentStepId}</span>
        ) : (
          <span className="text-ink-500">{t.tasks.none}</span>
        )}
      </span>
      <span className="flex min-w-0 flex-[0_0_130px] items-center gap-2 text-[13.5px] font-bold">
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
      <time dateTime={task.updatedAt} className="flex-[0_0_96px] text-right font-mono text-[12px] text-ink-700 max-sm:text-left">
        {t.tasks.updatedAt(task.updatedAt)}
      </time>
    </li>
  )
}

// The branch of a task with a worktree, under its title: monospace, cut in the middle when the row is too narrow (the whole name is in
// the hover title and for screen readers).
function Branch({ name }: { name: string }) {
  const { head, tail } = branchParts(name)
  return (
    <span data-testid="task-branch" title={name} className="flex max-w-full min-w-0 font-mono text-[12px] leading-[1.6] text-riso-blue">
      <span className="sr-only">{t.tasks.branchLabel(name)}</span>
      <span aria-hidden="true" className="min-w-0 truncate">{head}</span>
      <span aria-hidden="true" className="shrink-0 whitespace-pre">{tail}</span>
    </span>
  )
}
