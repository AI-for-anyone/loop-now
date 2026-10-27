import { Navigate, useParams, useSearchParams } from 'react-router'
import { counterpartOf, useConsoleThread } from '@/api/console-inbox'
import { useProjects } from '@/api/queries'
import { tasksPath, useTask } from '@/api/tasks'
import { t } from '@/i18n'

// 收件箱页已经去掉，旧的 /inbox/<线索> 地址转到消息现在所在的地方：任务实例的线索到任务节点，
// Agent 的线索到它的对话，外部身份的线索到它的页面；读不到线索或分辨不出时回到概览。
export function InboxRedirect() {
  const { threadId = null } = useParams()
  const [search] = useSearchParams()
  const thread = useConsoleThread(threadId)
  // 旧的任务问题链接带 ?task=；其余按线索里消息的任务归属判断。
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
