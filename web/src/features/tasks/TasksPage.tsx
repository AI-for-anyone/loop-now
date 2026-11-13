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
import { useTaskLive } from './useTaskLive'

type Task = Schemas['TaskSummary']
type Filter = 'all' | 'active' | 'blocked' | 'completed'

// 状态筛选：进行中包括准备中、执行中、等待回复；已取消的任务只在“全部”里。
const FILTERS: Filter[] = ['all', 'active', 'blocked', 'completed']
const ACTIVE: Schemas['TaskStatus'][] = ['preparing', 'running', 'waiting']
const inFilter = (filter: Filter, task: Task) =>
  filter === 'all' ? true : filter === 'active' ? ACTIVE.includes(task.status) : task.status === filter

export function TasksPage() {
  const scope = useScope()
  if (scope.kind !== 'project') return null
  return <ProjectTasks project={scope.project} />
}

// 项目任务（改版“一张印出来的纸”第 3 步）：页面标题下面是纸张上沿的状态筛选（索引标签，带数量），纸上是任务列表，
// 右侧票根里是进行中、阻塞、已完成三个大数字。列表接口不能按状态筛选，筛选只作用于已经读到的任务；
// 进行中与阻塞的数量取自未结束任务的计数接口（与左侧栏一致），已完成与全部按已经读到的任务算，还有更多页时加“+”。
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
          {/* 滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）随内容滚动，不会按未滚动时的位置把整页撑高。 */}
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
                {/* 表头（装饰，列名在每行里有读屏说明）：下面一道 2px 实线。 */}
                <div aria-hidden="true" className="flex items-center gap-[18px] border-b-2 border-ink px-1 pb-2 text-[12px] font-black text-ink-500 max-sm:hidden">
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

// 一个任务一行：标题（链接到详情）加副标题（等待或失败原因），状态，等宽的当前节点，责任 Agent（带头像），等宽的更新时间。
// 行与行之间 1.5px 虚线；窄时各列换行。
function TaskRow({ task, projectName, agentName, pending }: { task: Task; projectName: string; agentName: (id: string) => string; pending?: Parameters<typeof PendingBadge>[0]['counts'] }) {
  return (
    <li className="flex flex-wrap items-center gap-x-[18px] gap-y-1.5 border-b-[1.5px] border-dashed border-ink/25 px-1 py-3.5">
      <span className="flex min-w-0 flex-[1_1_280px] flex-col leading-[1.4]">
        <span className="flex min-w-0 items-start gap-2">
          <Link
            className="min-w-0 text-[15px] font-black break-words text-ink underline decoration-ink/30 underline-offset-4 hover:text-riso-blue hover:decoration-riso-blue"
            to={`${tasksPath(projectName)}/${task.id}`}
          >
            {task.title || task.id}
          </Link>
          <PendingBadge counts={pending} />
        </span>
        {task.reason && <span className={cn('text-[12px] break-words', task.status === 'blocked' ? 'text-red-text' : 'text-ink-500')}>{task.reason}</span>}
      </span>
      <span className="flex-[0_0_84px]">
        <span className="sr-only">{t.tasks.state}</span>
        <TaskState status={task.status} />
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

// 任务状态（改版通用规则第 6 条）：执行中荧光粉胶囊，阻塞是略微倾斜的红色描边印章，已完成浅绿胶囊，
// 等待回复浅黄胶囊，准备中与已取消是浅蓝胶囊。
function TaskState({ status }: { status: Schemas['TaskStatus'] }) {
  const label = t.tasks.statuses[status]
  if (status === 'blocked') {
    return (
      <span
        data-task-state={status}
        className="riso-tilt inline-flex min-h-[22px] items-center rounded-[4px] px-2.5 text-[12.5px] font-black tracking-[0.06em] whitespace-nowrap text-red-text [--tilt-bg:transparent] [--tilt-shadow:inset_0_0_0_2px_var(--red-text)] [--tilt:-3deg]"
      >
        {label}
      </span>
    )
  }
  const tone =
    status === 'running' ? 'bg-riso-pink text-ink' : status === 'completed' ? 'bg-tint-green text-green-text' : status === 'waiting' ? 'bg-tint-yellow text-ink' : 'bg-tint-blue text-ink-700'
  return (
    <span data-task-state={status} className={cn('inline-block rounded-full px-2.5 text-[12.5px] leading-[22px] font-black whitespace-nowrap', tone)}>
      {label}
    </span>
  )
}

export function LiveState({ state }: { state: string }) { return <p role="status" className="text-sm font-bold text-ink-700">{state === 'open' ? t.tasks.updating : t.tasks.reconnecting}</p> }
