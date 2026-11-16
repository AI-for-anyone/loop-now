import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import type { MessagePages } from '@/api/inbox'
import { inboxUnread } from '@/api/inbox-store'
import { keys } from '@/api/queries'
import { consoleKeys } from '@/api/console-inbox'
import { taskInboxKeys } from '@/api/task-inbox'
import { makeAgent } from '@/test/api'
import { agentParty, consoleParty, externalParty, makeIdentity, makeInboxMessage, summaryOf } from '@/test/inbox'
import { createTestQueryClient } from '@/test/render'
import { applyInboxEvent } from './useInboxLive'

const agent = makeAgent({ name: '开发助手' })
const ciBot = makeIdentity({ name: 'ci-bot' })

function setup() {
  const client = createTestQueryClient()
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  const invalidated = () => invalidate.mock.calls.map(([filters]) => JSON.stringify(filters?.queryKey))
  return { client, invalidated }
}

const created = (message: Schemas['InboxMessage'], unreadTotal = 0) =>
  ({ type: 'inbox.message.created', id: '1-0', data: { message: summaryOf(message), unreadTotal } }) as const

describe('Dispatching inbox events', () => {
  it('a question state update invalidates the original thread and does not replace the question ID with a later plain message', () => {
    const { client, invalidated } = setup()
    const question = makeInboxMessage({ from: agentParty(agent), to: consoleParty, question: { options: [{ id: '1', label: 'Go on' }], expiresAt: '2099-01-01T00:00:00Z', state: 'closed', answer: null } })
    applyInboxEvent(client, { ...created(question), type: 'inbox.question.updated' })
    expect(invalidated()).toContain(JSON.stringify(keys.inboxThread(question.threadId)))
    expect(invalidated()).toContain(JSON.stringify(keys.inboxThreads))
  })
  it('the unread total is taken from the latest event', () => {
    const { client } = setup()
    applyInboxEvent(client, { type: 'snapshot', id: '1-0', data: { unreadTotal: 4 } })
    expect(inboxUnread.get()).toBe(4)
    applyInboxEvent(client, created(makeInboxMessage({ from: agentParty(agent), to: consoleParty }), 5))
    expect(inboxUnread.get()).toBe(5)
  })

  it('the same event arriving twice (replayed on resume) changes nothing: the unread count still comes from the event, the views are only refetched again', () => {
    const { client, invalidated } = setup()
    const event = created(makeInboxMessage({ from: agentParty(agent), to: consoleParty }), 2)
    applyInboxEvent(client, event)
    applyInboxEvent(client, event)
    expect(inboxUnread.get()).toBe(2)
    expect(new Set(invalidated()).size).toBe(4)
  })

  it('a queued message cancelled by a person: loaded views record the cancel time; the thread, the thread list and the recipient\'s inbox are refetched', () => {
    const { client, invalidated } = setup()
    const message = makeInboxMessage({ from: consoleParty, to: agentParty(agent), text: 'Hold off for now' })
    client.setQueryData<MessagePages>(keys.agentInbox(agent.id, false), { pages: [{ messages: [summaryOf(message)], nextCursor: null }], pageParams: [null] })
    client.setQueryData<Schemas['InboxThread']>(keys.inboxThread(message.threadId), { threadId: message.threadId, participants: [consoleParty, agentParty(agent)], messages: [message] } as Schemas['InboxThread'])
    client.setQueryData(consoleKeys.agent(agent.id), [message])
    const cancelledAt = '2026-10-25T08:00:00Z'
    applyInboxEvent(client, { type: 'inbox.message.cancelled', id: '2-0', data: { id: message.id, threadId: message.threadId, from: message.from, to: message.to, cancelledAt, unreadTotal: 3 } })
    expect(inboxUnread.get()).toBe(3)
    expect(client.getQueryData<MessagePages>(keys.agentInbox(agent.id, false))?.pages[0].messages[0].cancelledAt).toBe(cancelledAt)
    expect(client.getQueryData<Schemas['InboxThread']>(keys.inboxThread(message.threadId))?.messages[0].cancelledAt).toBe(cancelledAt)
    expect(client.getQueryData<Schemas['InboxMessage'][]>(consoleKeys.agent(agent.id))?.[0].cancelledAt).toBe(cancelledAt)
    expect(invalidated()).toEqual([JSON.stringify(consoleKeys.pending), '["taskInbox"]', JSON.stringify(keys.inboxThread(message.threadId)), JSON.stringify(keys.inboxThreads), JSON.stringify(keys.agentInbox(agent.id))])
  })

  it('a snapshot refetches every inbox view', () => {
    const { client, invalidated } = setup()
    applyInboxEvent(client, { type: 'snapshot', id: '1-0', data: { unreadTotal: 0 } })
    expect(invalidated()).toEqual([JSON.stringify(keys.inboxThreads), '["inboxThread"]', '["agentInbox"]', '["identityInbox"]', '["consoleMessages"]', JSON.stringify(consoleKeys.pending), '["taskInbox"]'])
  })

  it('a message to the operator updates the waiting-for-you list, the operator\'s threads, that thread and the “to you” messages in this Agent\'s chat', () => {
    const { client, invalidated } = setup()
    const message = makeInboxMessage({ from: agentParty(agent), to: consoleParty })
    applyInboxEvent(client, created(message))
    expect(invalidated()).toEqual([JSON.stringify(consoleKeys.pending), JSON.stringify(keys.inboxThreads), JSON.stringify(keys.inboxThread(message.threadId)), JSON.stringify(consoleKeys.agent(agent.id))])
  })

  it('a message a task instance sends you refetches that node\'s Inbox records and does not enter the Agent\'s plain chat', () => {
    const { client, invalidated } = setup()
    const message = makeInboxMessage({ from: agentParty(agent), to: consoleParty, task: { taskId: 'task-1', nodeRunId: 'node-1', runtimeId: 'runtime-1' } })
    applyInboxEvent(client, created(message))
    expect(invalidated()).toContain(JSON.stringify(taskInboxKeys.records('task-1', 'node-1')))
    expect(invalidated()).not.toContain(JSON.stringify(consoleKeys.agent(agent.id)))
  })

  it('a message not involving the operator stays out of the operator\'s threads: only the inboxes of the recipient Agent and the external identity are refetched', () => {
    const { client, invalidated } = setup()
    applyInboxEvent(client, created(makeInboxMessage({ from: externalParty(ciBot), to: agentParty(agent) })))
    expect(invalidated()).toEqual([JSON.stringify(consoleKeys.pending), JSON.stringify(keys.agentInbox(agent.id)), JSON.stringify(keys.identityInbox(ciBot.id))])
  })

  it('a cache under the same prefix that is not paged messages (the thread list between an external identity and you) stays as is on a read event', () => {
    const { client } = setup()
    const message = makeInboxMessage({ from: externalParty(ciBot), to: consoleParty })
    client.setQueryData([...keys.identityInbox(ciBot.id), 'console'], [message.threadId])
    expect(() => applyInboxEvent(client, { type: 'inbox.message.read', id: '3-0', data: { id: message.id, threadId: message.threadId, to: consoleParty, readAt: '2026-10-12T09:00:00Z', unreadTotal: 0 } })).not.toThrow()
    expect(client.getQueryData([...keys.identityInbox(ciBot.id), 'console'])).toEqual([message.threadId])
  })

  it('a read event updates loaded inboxes in place by message ID and refetches the recipient\'s list', () => {
    const { client, invalidated } = setup()
    const message = makeInboxMessage({ from: externalParty(ciBot), to: agentParty(agent) })
    const pages: MessagePages = { pages: [{ messages: [summaryOf(message)], nextCursor: null }], pageParams: [null] }
    client.setQueryData(keys.agentInbox(agent.id, false), pages)
    client.setQueryData(keys.identityInbox(ciBot.id, 'all', false), pages)
    const readAt = '2026-10-12T09:00:00Z'
    applyInboxEvent(client, { type: 'inbox.message.read', id: '2-0', data: { id: message.id, threadId: message.threadId, to: agentParty(agent), readAt, unreadTotal: 0 } })
    expect(client.getQueryData<MessagePages>(keys.agentInbox(agent.id, false))!.pages[0].messages[0].readAt).toBe(readAt)
    expect(client.getQueryData<MessagePages>(keys.identityInbox(ciBot.id, 'all', false))!.pages[0].messages[0].readAt).toBe(readAt)
    expect(invalidated()).toEqual([JSON.stringify(consoleKeys.pending), '["taskInbox"]', JSON.stringify(keys.agentInbox(agent.id))])
  })
})
