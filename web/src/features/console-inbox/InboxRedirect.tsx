import { Navigate, useParams, useSearchParams } from 'react-router'
import { counterpartOf, useConsoleThread } from '@/api/console-inbox'
import { useProjects } from '@/api/queries'
import { tasksPath, useTask } from '@/api/tasks'
import { t } from '@/i18n'

// The inbox page has been removed; old /inbox/<thread> URLs redirect to where the messages live now: a task instance's thread
// to the task node, an Agent's thread to its chat, an external identity's thread to its page; when the thread can't be
// loaded or identified, go back to the overview.
export function InboxRedirect() {
  const { threadId = null } = useParams()
  const [search] = useSearchParams()
  const thread = useConsoleThread(threadId)
  // Old task question links carry ?task=; otherwise decide by the task the thread's messages belong to.
  const ref = thread.data?.messages.find((message) => message.task)?.task
  const taskId = search.get('task') ?? ref?.taskId ?? null
  if (!threadId) return <Navigate replace to="/" />
  if (taskId) return <TaskRedirect taskId={taskId} nodeRunId={ref?.taskId === taskId ? ref.nodeRunId : null} />
  if (thread.isPending) return <p className="p-6 text-[14px] text-ink-500">{t.common.loading}</p>
  const party = counterpartOf(thread.data?.messages ?? [])
  if (party?.kind === 'agent' && party.id) return <Navigate replace to={`/agents/${party.id}/chat`} />
  if (party?.kind === 'external' && party.id) return <Navigate replace to={`/identities/${party.id}`} />
  return <Navigate replace to="/" />
}

function TaskRedirect({ taskId, nodeRunId }: { taskId: string; nodeRunId: string | null }) {
  const task = useTask(taskId)
  const projects = useProjects()
  if (task.isError || projects.isError) return <Navigate replace to="/" />
  if (!task.data || !projects.data) return <p className="p-6 text-[14px] text-ink-500">{t.common.loading}</p>
  const project = projects.data.projects.find((p) => p.id === task.data.task.projectId)
  if (!project) return <Navigate replace to="/" />
  const node = task.data.nodes.find((n) => n.id === (nodeRunId ?? task.data.task.currentNodeRunId))
  const query = node ? `?${new URLSearchParams({ step: node.stepId, record: node.id })}` : ''
  return <Navigate replace to={`${tasksPath(project.name)}/${taskId}${query}`} />
}
