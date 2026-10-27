import { TaskBadge } from './TaskBadge'
import { Link } from 'react-router'
import { useProjectTasks, tasksPath } from '@/api/tasks'
import { useScope } from '@/features/projects/scope-context'
import { useAgents } from '@/api/queries'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { t } from '@/i18n'
import { useTaskLive } from './useTaskLive'
import { useInboxPending } from '@/api/console-inbox'
import { PendingBadge } from '@/features/console-inbox/PendingBadge'
import { pageTitleClass } from '@/lib/page-title'
import { cn } from '@/lib/utils'

export function TasksPage() {
  const scope = useScope()
  if (scope.kind !== 'project') return null
  return <ProjectTasks project={scope.project} />
}
function ProjectTasks({ project }: { project: import('@/api/client').Schemas['Project'] }) {
  const tasks = useProjectTasks(project.id)
  const agents = useAgents(project.id)
  const connection = useTaskLive(project.id, true)
  const rows = [...new Map(tasks.data?.pages.flatMap((p) => p.tasks).map((task) => [task.id, task])).values()]
  // Pending for you: questions and new messages the task's instances sent to @console.
  const pending = useInboxPending()
  const pendingByTask = new Map((pending.data?.byTask ?? []).map((row) => [row.taskId, row]))
  return <div className="h-full overflow-y-auto p-4 sm:p-[30px]">
    <div className="mb-6 flex flex-wrap items-end justify-between gap-3"><div><p className="font-bold">{project.name}</p><h1 className={cn(pageTitleClass, 'my-2')}>{t.tasks.title}</h1><p>{t.tasks.intro}</p></div><LiveState state={connection} /></div>
    <ErrorAlert error={tasks.error} />
    {tasks.isPending && <p>{t.tasks.loading}</p>}
    {tasks.isSuccess && rows.length === 0 && <section className="grid gap-4 rounded-panel bg-white p-6 shadow-print-rest"><p>{t.tasks.empty}</p><Button asChild variant="secondary"><Link to={project.managerAgentId ? `/p/${encodeURIComponent(project.name)}/agents/${project.managerAgentId}/chat` : `/p/${encodeURIComponent(project.name)}/settings`}>{project.managerAgentId ? t.tasks.manager : t.tasks.settings}</Link></Button></section>}
    <ul aria-label={t.tasks.title} className="grid gap-3">{rows.map((task) => <li key={task.id} className="grid gap-3 rounded-card bg-white p-5 shadow-print-rest sm:grid-cols-[minmax(0,2fr)_1fr_1fr_1fr]">
      <span className="flex min-w-0 items-start gap-2"><Link className="min-w-0 font-bold break-words text-riso-blue underline" to={`${tasksPath(project.name)}/${task.id}`}>{task.title || task.id}</Link><PendingBadge counts={pendingByTask.get(task.id)} /></span>
      <div><span className="mr-2 text-sm text-ink-500">{t.tasks.state}</span><TaskBadge status={task.status} /></div>
      <p className="min-w-0 break-words"><span className="mr-2 text-sm text-ink-500">{t.tasks.currentNode}</span>{task.currentStepId ?? t.tasks.none}</p>
      <p className="min-w-0 break-words"><span className="mr-2 text-sm text-ink-500">{t.tasks.responsible}</span>{agents.data?.agents.find((a) => a.id === task.currentAgentId)?.name ?? task.currentAgentId ?? t.tasks.none}</p>
      {task.reason && <p className="text-sm text-red-text sm:col-span-4">{task.reason}</p>}
    </li>)}</ul>
    {tasks.hasNextPage && <Button className="mt-5" variant="secondary" disabled={tasks.isFetchingNextPage} onClick={() => void tasks.fetchNextPage()}>{t.tasks.more}</Button>}
  </div>
}
export function LiveState({ state }: { state: string }) { return <p role="status" className="text-sm font-bold text-ink-700">{state === 'open' ? t.tasks.updating : t.tasks.reconnecting}</p> }
