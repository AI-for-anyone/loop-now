import { TaskRunRecord } from './TaskRunRecord'
import { PermissionRequestCard } from '@/features/permissions/PermissionRequestCard'
import { useRuntimeLookup } from '@/lib/runtime'
import { turnErrorText } from './turn-error'
import type { Schemas } from '@/api/client'
import { UserChatBubble, AgentChatReply, PlatformChatReminder, SleepWakeLine } from '@/features/chat/ChatBubble'
import type { ReplyFolderContext } from '@/features/chat/local-images'
import { Markdown } from '@/features/chat/Markdown'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

const copy = t.taskInbox

export function TaskMessageBubble({ message, agent, taskId, nodeRunId, runtimeId, requests = [], folder }: { message: Schemas['TaskMessage']; agent: { id: string; name: string }; taskId?: string; nodeRunId?: string; runtimeId?: string; requests?: Schemas['PermissionRequest'][]; folder?: ReplyFolderContext }) {
  const runtimes = useRuntimeLookup()
  // A turn stopped by a person is labelled “已停止” (Stopped), not shown as an error.
  const stopped = message.status === 'interrupted' && message.errorCode === 'user_stopped'
  if (message.role === 'user') {
    if (message.origin === 'sleep_wake') return <SleepWakeLine text={message.text} at={message.createdAt} sleepId={message.wakeSleepId} />
    if (message.origin !== 'console_node') return <PlatformChatReminder text={message.text} at={message.createdAt} label={message.origin === 'task_node' ? t.tasks.user : undefined} wakeSleepId={message.wakeSleepId} />
    return <UserChatBubble text={message.text} time={t.chat.time(message.createdAt)} wakeSleepId={message.wakeSleepId} />
  }
  return <AgentChatReply agent={agent} metadata={<><Badge tone="info">{copy.workOutput}</Badge><time dateTime={message.createdAt}>{t.chat.time(message.createdAt)}</time>{message.status !== 'completed' && <Badge tone={message.status === 'failed' ? 'danger' : message.status === 'interrupted' ? 'interrupted' : 'busy'} role="status">{stopped ? t.chat.status.user_stopped : t.tasks.messageStatuses[message.status]}</Badge>}</>}>
    <Markdown text={message.text} cursor={message.status === 'running'} folder={folder} />
    {!stopped && (message.errorCode || message.errorMessage) && <p className="rounded-input bg-tint-red p-3 text-sm break-words text-red-text">{turnErrorText(message.errorCode ?? null, message.errorMessage ?? null, message.runtime ? runtimes.name(message.runtime) : null)}</p>}
    {requests.map(request => <PermissionRequestCard key={request.id} request={request} agentName={agent.name} />)}
    {taskId && nodeRunId && runtimeId && <TaskRunRecord taskId={taskId} nodeRunId={nodeRunId} runId={message.runId} agentId={agent.id} runtimeId={runtimeId} />}
  </AgentChatReply>
}
