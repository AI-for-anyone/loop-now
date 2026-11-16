import { useQueries, useQuery, type QueryClient } from '@tanstack/react-query'
import { api, call, type Schemas } from './client'
import { keys } from './queries'
import { taskInboxKeys } from './task-inbox'

// The operator's (@console) Inbox no longer has a page of its own: messages and questions an Agent sends you show in its conversation,
// those from task instances show in the node conversation, and your exchanges with an external identity show on its page; the summary
// of what needs you shows on the Agent list, the task list and the overview.

type Message = Schemas['InboxMessage']

export const consoleKeys = {
  agent: (agentId: string) => ['consoleMessages', agentId] as const,
  pending: ['inboxPending'] as const,
}

// Read at most this many pages at a time; beyond that, only what has been read is used (newest first). Each Agent usually has few
// exchanges with you, so once read they are merged into the conversation by time as a whole.
const maxPages = 20

// All messages between you and one Agent (not including its task instances), in both directions, in chronological order. Reading does
// not change the read state.
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

// Needs you: open questions and other unread messages sent to you, with summaries per Agent and per task. Any event on the inbox event
// stream makes it refetch.
export function useInboxPending() {
  return useQuery({ queryKey: consoleKeys.pending, queryFn: () => call(api.GET('/inbox/pending')) })
}

export type PendingCounts = { questions: number; unread: number }
export const pendingTotal = (counts: PendingCounts | undefined) => (counts ? counts.questions + counts.unread : 0)

const threadQuery = (threadId: string) => ({
  queryKey: keys.inboxThread(threadId),
  queryFn: () => call(api.GET('/inbox/threads/{threadId}', { params: { path: { threadId } } })),
})

// All messages of one thread.
export function useConsoleThread(threadId: string | null) {
  return useQuery({ ...threadQuery(threadId ?? ''), enabled: threadId !== null })
}

// Your exchanges with an external identity: find the threads between it and you in its inbox (both directions), then read the full text
// of those threads and merge them in chronological order.
// Its exchanges with Agents are only shown in the inbox records below. threads holds these threads, the one with the latest message first.
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

// A message's read or cancel time changed: update this message in place in your exchanges with Agents and in threads. A task node's Inbox
// records are merged by its own projection and pick up the new time when refetched.
export function patchConsoleMessage(client: QueryClient, messageId: string, patch: Partial<Pick<Message, 'readAt' | 'cancelledAt'>>) {
  const apply = (message: Message) => (message.id === messageId ? { ...message, ...patch } : message)
  client.setQueriesData<Message[]>({ queryKey: ['consoleMessages'] }, (data) => data?.map(apply))
  client.setQueriesData<Schemas['InboxThread']>({ queryKey: ['inboxThread'] }, (data) => (data ? { ...data, messages: data.messages.map(apply) } : data))
  void client.invalidateQueries({ queryKey: [taskInboxKeys.records('')[0]] })
}

// A message sent to you is marked read when it comes into view, the same meaning of "read" as opening a thread in the old inbox. The
// "needs you" summary is refetched afterwards.
export async function markMessageRead(client: QueryClient, messageId: string) {
  const message = await call(api.POST('/inbox/messages/{messageId}/read', { params: { path: { messageId } } }))
  patchConsoleMessage(client, message.id, { readAt: message.readAt })
  void client.invalidateQueries({ queryKey: consoleKeys.pending })
  return message
}

// The other party in a thread (the side that is not you).
export function counterpartOf(messages: Pick<Message, 'from' | 'to'>[]) {
  const first = messages[0]
  return first ? (first.from.kind === 'console' ? first.to : first.from) : undefined
}
