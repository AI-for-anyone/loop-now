import { useEffect } from 'react'
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'
import { connectInboxEvents, type InboxEvent } from '@/api/events'
import type { MessagePages } from '@/api/inbox'
import { inboxConnection, inboxUnread } from '@/api/inbox-store'
import { keys } from '@/api/queries'
import { consoleKeys, patchConsoleMessage } from '@/api/console-inbox'
import { forwardToDesktop } from '@/features/desktop/desktop'
import { taskInboxKeys } from '@/api/task-inbox'

// Refetch all inbox views: after invalidation only the queries currently shown refetch right away; the rest refetch the
// next time they are shown.
export function refreshInboxViews(client: QueryClient) {
  for (const queryKey of [keys.inboxThreads, ['inboxThread'], ['agentInbox'], ['identityInbox'], ['consoleMessages'], consoleKeys.pending, [taskInboxKeys.records('')[0]]]) {
    void client.invalidateQueries({ queryKey })
  }
}

type MessagePatch = Pick<Schemas['InboxMessageSummary'], 'readAt'> | Pick<Schemas['InboxMessageSummary'], 'cancelledAt'>

// Update the read or cancel time by message ID in every loaded view: Agent and external identity inboxes, thread details, and
// full texts that have been opened.
function patchMessage(client: QueryClient, messageId: string, patch: MessagePatch) {
  // The same prefix also holds data of another shape (the list of threads between an external identity and you); only patch
  // the paged message lists.
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

// Events cover all Inbox messages and are routed to the matching views by the sender's and recipient's kind and ID; the
// address may be empty or reused by a new identity, so it isn't used to decide ownership.
// Only messages whose sender or recipient is the operator go into the operator's threads; messages to an Agent such as
// platform notifications only make that Agent's inbox refetch.
// Your exchanges with Agents and task instances (“发给你” (For you) in chat and in node chats) and the "Needs you" summary are
// also updated here: "Needs you" refetches on every event.
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
      // The “只看未读” (Unread only) list has to drop this message, so the recipient's inbox is refetched as well; the operator's
      // thread list has to update its unread count.
      // Cancelling a message also closes its question with options, so the thread details are refetched as well.
      if (event.type === 'inbox.message.cancelled') void client.invalidateQueries({ queryKey: keys.inboxThread(event.data.threadId) })
      if (to.kind === 'console' || event.type === 'inbox.message.cancelled') void client.invalidateQueries({ queryKey: keys.inboxThreads })
      if (to.kind === 'agent' && to.id) void client.invalidateQueries({ queryKey: keys.agentInbox(to.id) })
      if (to.kind === 'external' && to.id) void client.invalidateQueries({ queryKey: keys.identityInbox(to.id) })
      return
    }
  }
}

// After entering the console, open one inbox event stream; no polling. In a desktop shell window, events are also passed to
// the shell to show the Dock unread count and system notifications. After a permanent disconnect it is re-established at
// backoff intervals; on a new connection the server pushes a snapshot first.
// When the server fails to append an event, individual events may be missed, so after a successful reconnect and whenever
// the window regains focus, the inbox views currently shown are refetched.
export function useInboxLive() {
  const client = useQueryClient()
  useEffect(() => {
    let interrupted = false
    const stop = connectInboxEvents({
      onEvent: (event) => {
        applyInboxEvent(client, event)
        void forwardToDesktop(client, event)
      },
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
