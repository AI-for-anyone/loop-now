import { TaskRunRecord } from './TaskRunRecord'
import type { Schemas } from '@/api/client'
import { UserChatBubble, AgentChatReply, PlatformChatReminder, SleepWakeLine } from '@/features/chat/ChatBubble'
import { Markdown } from '@/features/chat/Markdown'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

const copy = t.taskInbox

export function TaskMessageBubble({ message, agent, taskId, nodeRunId, runtimeId }: { message: Schemas['TaskMessage']; agent: { id: string; name: string }; taskId?: string; nodeRunId?: string; runtimeId?: string }) {
  // A turn stopped by a person is labelled 已停止, not shown as an error.
  const stopped = message.status === 'interrupted' && message.errorCode === 'user_stopped'
  if (message.role === 'user') {
    if (message.origin === 'sleep_wake') return <SleepWakeLine text={message.text} at={message.createdAt} sleepId={message.wakeSleepId} />
    if (message.origin !== 'console_node') return <PlatformChatReminder text={message.text} at={message.createdAt} label={message.origin === 'task_node' ? t.tasks.user : undefined} wakeSleepId={message.wakeSleepId} />
    return <UserChatBubble text={message.text} time={t.chat.time(message.createdAt)} wakeSleepId={message.wakeSleepId} />
  }
  return <AgentChatReply agent={agent} metadata={<><Badge tone="info">{copy.workOutput}</Badge><time dateTime={message.createdAt}>{t.chat.time(message.createdAt)}</time>{message.status !== 'completed' && <Badge tone={message.status === 'failed' ? 'danger' : message.status === 'interrupted' ? 'interrupted' : 'busy'} role="status">{stopped ? t.chat.status.user_stopped : t.tasks.messageStatuses[message.status]}</Badge>}</>}>
    <Markdown text={message.text} cursor={message.status === 'running'} />
    {!stopped && (message.errorCode || message.errorMessage) && <p className="rounded-input bg-tint-red p-3 text-sm break-words text-red-text">{message.errorCode && message.errorCode in t.chat.runError ? t.chat.runError[message.errorCode as keyof typeof t.chat.runError] : t.tasks.messageError}{message.errorMessage ? ` · ${message.errorMessage}` : ''}</p>}
    {taskId && nodeRunId && runtimeId && <TaskRunRecord taskId={taskId} nodeRunId={nodeRunId} runId={message.runId} agentId={agent.id} runtimeId={runtimeId} />}
  </AgentChatReply>
}
