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

describe('收件箱事件的分发', () => {
  it('原题状态更新失效原thread，不用后续普通消息替换原题ID', () => {
    const { client, invalidated } = setup()
    const question = makeInboxMessage({ from: agentParty(agent), to: consoleParty, question: { options: [{ id: '1', label: '继续' }], expiresAt: '2099-01-01T00:00:00Z', state: 'closed', answer: null } })
    applyInboxEvent(client, { ...created(question), type: 'inbox.question.updated' })
    expect(invalidated()).toContain(JSON.stringify(keys.inboxThread(question.threadId)))
    expect(invalidated()).toContain(JSON.stringify(keys.inboxThreads))
  })
  it('未读总数取最新事件中的值', () => {
    const { client } = setup()
    applyInboxEvent(client, { type: 'snapshot', id: '1-0', data: { unreadTotal: 4 } })
    expect(inboxUnread.get()).toBe(4)
    applyInboxEvent(client, created(makeInboxMessage({ from: agentParty(agent), to: consoleParty }), 5))
    expect(inboxUnread.get()).toBe(5)
  })

  it('同一事件重复到达（续传时重放）不改变结果：未读数仍取事件中的值，只是再次让视图重新获取', () => {
    const { client, invalidated } = setup()
    const event = created(makeInboxMessage({ from: agentParty(agent), to: consoleParty }), 2)
    applyInboxEvent(client, event)
    applyInboxEvent(client, event)
    expect(inboxUnread.get()).toBe(2)
    expect(new Set(invalidated()).size).toBe(4)
  })

  it('排队消息被人取消：已加载的视图记下取消时间，线索、线索列表与收件方的收件箱重新获取', () => {
    const { client, invalidated } = setup()
    const message = makeInboxMessage({ from: consoleParty, to: agentParty(agent), text: '先别做了' })
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

  it('快照到达时重新获取全部收件箱视图', () => {
    const { client, invalidated } = setup()
    applyInboxEvent(client, { type: 'snapshot', id: '1-0', data: { unreadTotal: 0 } })
    expect(invalidated()).toEqual([JSON.stringify(keys.inboxThreads), '["inboxThread"]', '["agentInbox"]', '["identityInbox"]', '["consoleMessages"]', JSON.stringify(consoleKeys.pending), '["taskInbox"]'])
  })

  it('发给操作者的消息更新待你处理、操作者的线索、那条线索与这个 Agent 的对话里“发给你”的消息', () => {
    const { client, invalidated } = setup()
    const message = makeInboxMessage({ from: agentParty(agent), to: consoleParty })
    applyInboxEvent(client, created(message))
    expect(invalidated()).toEqual([JSON.stringify(consoleKeys.pending), JSON.stringify(keys.inboxThreads), JSON.stringify(keys.inboxThread(message.threadId)), JSON.stringify(consoleKeys.agent(agent.id))])
  })

  it('任务实例发给你的消息让那个节点的 Inbox 记录重新获取，不进入 Agent 的普通对话', () => {
    const { client, invalidated } = setup()
    const message = makeInboxMessage({ from: agentParty(agent), to: consoleParty, task: { taskId: 'task-1', nodeRunId: 'node-1', runtimeId: 'runtime-1' } })
    applyInboxEvent(client, created(message))
    expect(invalidated()).toContain(JSON.stringify(taskInboxKeys.records('task-1', 'node-1')))
    expect(invalidated()).not.toContain(JSON.stringify(consoleKeys.agent(agent.id)))
  })

  it('与操作者无关的消息不进入操作者的线索：只让收件 Agent 与外部身份的收件箱重新获取', () => {
    const { client, invalidated } = setup()
    applyInboxEvent(client, created(makeInboxMessage({ from: externalParty(ciBot), to: agentParty(agent) })))
    expect(invalidated()).toEqual([JSON.stringify(consoleKeys.pending), JSON.stringify(keys.agentInbox(agent.id)), JSON.stringify(keys.identityInbox(ciBot.id))])
  })

  it('同一前缀下不是分页消息的缓存（外部身份与你往来的线索列表）在已读事件中保持原样', () => {
    const { client } = setup()
    const message = makeInboxMessage({ from: externalParty(ciBot), to: consoleParty })
    client.setQueryData([...keys.identityInbox(ciBot.id), 'console'], [message.threadId])
    expect(() => applyInboxEvent(client, { type: 'inbox.message.read', id: '3-0', data: { id: message.id, threadId: message.threadId, to: consoleParty, readAt: '2026-10-12T09:00:00Z', unreadTotal: 0 } })).not.toThrow()
    expect(client.getQueryData([...keys.identityInbox(ciBot.id), 'console'])).toEqual([message.threadId])
  })

  it('已读事件按消息 ID 就地更新已加载的收件箱，并让收件方的列表重新获取', () => {
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
