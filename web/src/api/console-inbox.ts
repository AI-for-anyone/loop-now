import { useQueries, useQuery, type QueryClient } from '@tanstack/react-query'
import { api, call, type Schemas } from './client'
import { keys } from './queries'
import { taskInboxKeys } from './task-inbox'

// 操作者（@console）的 Inbox 不再有单独的页面：Agent 发给你的消息与问题显示在它的对话里，任务实例的显示在节点对话里，
// 外部身份与你的往来显示在它的页面上；待你处理的汇总显示在 Agent 列表、任务列表与概览上。

type Message = Schemas['InboxMessage']

export const consoleKeys = {
  agent: (agentId: string) => ['consoleMessages', agentId] as const,
  pending: ['inboxPending'] as const,
}

// 一次最多读这么多页；超出时只用已读到的部分（最新的在前）。每个 Agent 与你的往来通常不多，读完后整体按时间合并进对话。
const maxPages = 20

// 你与一个 Agent（不含它的任务实例）之间的全部消息，两个方向都有，按时间正序。读取不改变已读状态。
export function useAgentConsoleMessages(agentId: string) {
  return useQuery({
    queryKey: consoleKeys.agent(agentId),
    queryFn: async () => {
      const messages: Message[] = []
      let cursor: string | null = null
      for (let page = 0; page < maxPages; page++) {
        const result: Schemas['InboxConversationPage'] = await call(
          api.GET('/agents/{agentId}/console-messages', { params: { path: { agentId }, query: { limit: 100, ...(cursor ? { cursor } : {}) } } }),
        )
        messages.push(...result.messages)
        cursor = result.nextCursor
        if (!cursor) break
      }
      return messages.sort(byTime)
    },
  })
}

const byTime = (a: Pick<Message, 'createdAt' | 'id'>, b: Pick<Message, 'createdAt' | 'id'>) => a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id)

// 待你处理：发给你的 open 问题与其他未读消息，带按 Agent、按任务的汇总。收件箱事件流的任何事件都让它重新获取。
export function useInboxPending() {
  return useQuery({ queryKey: consoleKeys.pending, queryFn: () => call(api.GET('/inbox/pending')) })
}

export type PendingCounts = { questions: number; unread: number }
export const pendingTotal = (counts: PendingCounts | undefined) => (counts ? counts.questions + counts.unread : 0)

const threadQuery = (threadId: string) => ({
  queryKey: keys.inboxThread(threadId),
  queryFn: () => call(api.GET('/inbox/threads/{threadId}', { params: { path: { threadId } } })),
})

// 一条线索的全部消息。
export function useConsoleThread(threadId: string | null) {
  return useQuery({ ...threadQuery(threadId ?? ''), enabled: threadId !== null })
}

// 外部身份与你的往来：从它的收件箱（两个方向）里找出与你之间的线索，再读出这些线索的全文，按时间正序合并。
// 它与 Agent 之间的往来只在下方的收件箱记录里查看。threads 为这些线索，最近有消息的在前。
export function useIdentityConversation(identityId: string) {
  const inbox = useQuery({
    queryKey: [...keys.identityInbox(identityId), 'console'],
    queryFn: async () => {
      const found: Schemas['InboxMessageSummary'][] = []
      let cursor: string | null = null
      for (let page = 0; page < maxPages; page++) {
        const result: Schemas['InboxMessagePage'] = await call(
          api.GET('/external-identities/{identityId}/inbox', { params: { path: { identityId }, query: { limit: 100, direction: 'all', ...(cursor ? { cursor } : {}) } } }),
        )
        found.push(...result.messages.filter((message) => message.from.kind === 'console' || message.to.kind === 'console'))
        cursor = result.nextCursor
        if (!cursor) break
      }
      return [...new Set(found.map((message) => message.threadId))]
    },
  })
  const threads = inbox.data ?? []
  const details = useQueries({ queries: threads.map(threadQuery) })
  const messages = details.flatMap((detail) => detail.data?.messages ?? []).sort(byTime)
  const error = inbox.error ?? details.find((detail) => detail.error)?.error ?? null
  return { threads, messages, isPending: inbox.isPending || details.some((detail) => detail.isPending), error }
}

// 消息的已读或取消时间变了：就地改你与 Agent 的往来和线索里的这条消息。任务节点的 Inbox 记录由它自己的投影合并，
// 重新获取后带上新的时间。
export function patchConsoleMessage(client: QueryClient, messageId: string, patch: Partial<Pick<Message, 'readAt' | 'cancelledAt'>>) {
  const apply = (message: Message) => (message.id === messageId ? { ...message, ...patch } : message)
  client.setQueriesData<Message[]>({ queryKey: ['consoleMessages'] }, (data) => data?.map(apply))
  client.setQueriesData<Schemas['InboxThread']>({ queryKey: ['inboxThread'] }, (data) => (data ? { ...data, messages: data.messages.map(apply) } : data))
  void client.invalidateQueries({ queryKey: [taskInboxKeys.records('')[0]] })
}

// 发给你的消息进入视野时标为已读，与原收件箱打开线索时的已读含义相同。待你处理随后重新获取。
export async function markMessageRead(client: QueryClient, messageId: string) {
  const message = await call(api.POST('/inbox/messages/{messageId}/read', { params: { path: { messageId } } }))
  patchConsoleMessage(client, message.id, { readAt: message.readAt })
  void client.invalidateQueries({ queryKey: consoleKeys.pending })
  return message
}

// 线索里另一方（不是你的那一方）。
export function counterpartOf(messages: Pick<Message, 'from' | 'to'>[]) {
  const first = messages[0]
  return first ? (first.from.kind === 'console' ? first.to : first.from) : undefined
}
