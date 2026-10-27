import { useEffect } from 'react'
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'
import { connectInboxEvents, type InboxEvent } from '@/api/events'
import type { MessagePages } from '@/api/inbox'
import { inboxConnection, inboxUnread } from '@/api/inbox-store'
import { keys } from '@/api/queries'
import { consoleKeys, patchConsoleMessage } from '@/api/console-inbox'
import { taskInboxKeys } from '@/api/task-inbox'

// 重新获取各个收件箱视图：失效后只有正在显示的查询立即重新请求，其余的在下次显示时请求。
export function refreshInboxViews(client: QueryClient) {
  for (const queryKey of [keys.inboxThreads, ['inboxThread'], ['agentInbox'], ['identityInbox'], ['consoleMessages'], consoleKeys.pending, [taskInboxKeys.records('')[0]]]) {
    void client.invalidateQueries({ queryKey })
  }
}

type MessagePatch = Pick<Schemas['InboxMessageSummary'], 'readAt'> | Pick<Schemas['InboxMessageSummary'], 'cancelledAt'>

// 按消息 ID 在已加载的各个视图中更新已读或取消时间：Agent 与外部身份的收件箱、线索详情，以及展开过的全文。
function patchMessage(client: QueryClient, messageId: string, patch: MessagePatch) {
  // 同一前缀下还有别的形状（外部身份与你往来的线索列表），只改分页的消息列表。
  const patchPages = (data: MessagePages | undefined) =>
    data?.pages
      ? { ...data, pages: data.pages.map((page) => ({ ...page, messages: page.messages.map((m) => (m.id === messageId ? { ...m, ...patch } : m)) })) }
      : data
  client.setQueriesData<MessagePages>({ queryKey: ['agentInbox'] }, patchPages)
  client.setQueriesData<MessagePages>({ queryKey: ['identityInbox'] }, patchPages)
  client.setQueriesData<Schemas['InboxThread']>({ queryKey: ['inboxThread'] }, (data) =>
    data ? { ...data, messages: data.messages.map((m) => (m.id === messageId ? { ...m, ...patch } : m)) } : data,
  )
  for (const prefix of ['agentInboxMessage', 'identityInboxMessage']) {
    client.setQueriesData<Schemas['InboxMessage']>({ queryKey: [prefix] }, (data) => (data?.id === messageId ? { ...data, ...patch } : data))
  }
}

// 事件覆盖全部 Inbox 消息，按发件方与收件方的类别和 ID 分发到对应的视图；地址可能为空或被新身份重用，不用它判断归属。
// 只有发件方或收件方是操作者的消息才进入操作者的线索；平台通知等发给 Agent 的消息只会让该 Agent 的收件箱重新获取。
// 你与 Agent、任务实例的往来（对话与节点对话里的“发给你”）以及待你处理的汇总也在这里更新：待你处理每个事件都重新获取。
export function applyInboxEvent(client: QueryClient, event: InboxEvent) {
  inboxUnread.set(event.data.unreadTotal)
  if (event.type !== 'snapshot') void client.invalidateQueries({ queryKey: consoleKeys.pending })
  switch (event.type) {
    case 'snapshot':
      refreshInboxViews(client)
      return
    case 'inbox.question.updated':
    case 'inbox.message.created': {
      const { from, to, threadId } = event.data.message
      if (from.kind === 'console' || to.kind === 'console') {
        void client.invalidateQueries({ queryKey: keys.inboxThreads })
        void client.invalidateQueries({ queryKey: keys.inboxThread(threadId) })
      }
      if (to.kind === 'agent' && to.id) void client.invalidateQueries({ queryKey: keys.agentInbox(to.id) })
      const { task } = event.data.message
      if (task) void client.invalidateQueries({ queryKey: taskInboxKeys.records(task.taskId, task.nodeRunId ?? undefined) })
      else {
        for (const [party, other] of [[from, to], [to, from]] as const) {
          if (party.kind === 'agent' && party.id && other.kind === 'console') void client.invalidateQueries({ queryKey: consoleKeys.agent(party.id) })
        }
      }
      for (const party of [from, to]) {
        if (party.kind === 'external' && party.id) void client.invalidateQueries({ queryKey: keys.identityInbox(party.id) })
      }
      return
    }
    case 'inbox.message.read':
    case 'inbox.message.cancelled': {
      const { id, to } = event.data
      const patch = event.type === 'inbox.message.read' ? { readAt: event.data.readAt } : { cancelledAt: event.data.cancelledAt }
      patchMessage(client, id, patch)
      patchConsoleMessage(client, id, patch)
      // “只看未读”的列表要去掉这条消息，所以收件方的收件箱另外重新获取；操作者的线索列表要更新未读数。
      // 取消的消息带选项的问题同时关闭，线索详情另外重新获取。
      if (event.type === 'inbox.message.cancelled') void client.invalidateQueries({ queryKey: keys.inboxThread(event.data.threadId) })
      if (to.kind === 'console' || event.type === 'inbox.message.cancelled') void client.invalidateQueries({ queryKey: keys.inboxThreads })
      if (to.kind === 'agent' && to.id) void client.invalidateQueries({ queryKey: keys.agentInbox(to.id) })
      if (to.kind === 'external' && to.id) void client.invalidateQueries({ queryKey: keys.identityInbox(to.id) })
      return
    }
  }
}

// 进入控制台后建立一条收件箱事件流，不轮询。永久断开后按退避间隔重建，新连接由服务端先推送快照。
// 服务端追加事件失败时可能漏掉个别事件，所以重连成功和窗口重新获得焦点时，都重新获取正在显示的收件箱视图。
export function useInboxLive() {
  const client = useQueryClient()
  useEffect(() => {
    let interrupted = false
    const stop = connectInboxEvents({
      onEvent: (event) => applyInboxEvent(client, event),
      onState: (state) => {
        inboxConnection.set(state)
        if (state === 'reconnecting') interrupted = true
        if (state === 'open' && interrupted) {
          interrupted = false
          refreshInboxViews(client)
        }
      },
      onClosed: async () => true,
    })
    const onFocus = () => refreshInboxViews(client)
    window.addEventListener('focus', onFocus)
    return () => {
      stop()
      window.removeEventListener('focus', onFocus)
      inboxConnection.set('connecting')
    }
  }, [client])
}
