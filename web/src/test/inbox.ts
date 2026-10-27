import { act } from '@testing-library/react'
import { http, HttpResponse, type HttpHandler } from 'msw'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody } from './api'
import { FakeEventSource } from './fake-event-source'

type Message = Schemas['InboxMessage']
type Participant = Schemas['InboxParticipant']

let seq = 0
const id = () => `00000000-0000-7000-a000-${String(++seq).padStart(12, '0')}`
// 依次递增的时间，越晚创建的消息越新。
let clock = Date.parse('2026-10-12T08:00:00Z')
const nextTime = () => new Date((clock += 60_000)).toISOString()

export const consoleParty: Participant = { kind: 'console', id: null, address: '@console', name: '控制台', deleted: false, pluginId: null, channelId: null }

export const agentParty = (agent: Pick<Schemas['Agent'], 'id' | 'name' | 'address'>, deleted = false): Participant => ({
  kind: 'agent',
  id: agent.id,
  address: agent.address,
  name: deleted ? '已删除 Agent' : agent.name,
  deleted,
  pluginId: null,
  channelId: null,
})

export const externalParty = (identity: Pick<Schemas['ExternalIdentity'], 'id' | 'name'>, deleted = false): Participant => ({
  kind: 'external',
  id: identity.id,
  address: `@${identity.name}`,
  name: deleted ? '已删除身份' : identity.name,
  deleted,
  pluginId: null,
  channelId: null,
})

export function makeInboxMessage(overrides: Partial<Message> & Pick<Message, 'from' | 'to'>): Message {
  const messageId = overrides.id ?? id()
  const text = overrides.text ?? '你好'
  return {
    id: messageId,
    threadId: overrides.threadId ?? messageId,
    inReplyTo: null,
    preview: text.slice(0, 200),
    whenBusy: overrides.to.kind === 'agent' ? 'queue' : null,
    createdAt: nextTime(),
    readAt: null,
    cancelledAt: null,
    task: null,
    text,
    ...overrides,
  }
}

export function summaryOf(message: Message): Schemas['InboxMessageSummary'] {
  const { text, ...summary } = message
  void text
  return summary
}

export function makeIdentity(overrides: Partial<Schemas['ExternalIdentity']> = {}): Schemas['ExternalIdentity'] {
  const name = overrides.name ?? 'ci-bot'
  return { id: id(), name, address: `@${name}`, createdAt: '2026-09-30T02:12:44Z', deletedAt: null, managedBy: null, ...overrides }
}

export function makeToken(overrides: Partial<Schemas['ExternalToken']> = {}): Schemas['ExternalToken'] {
  const tokenId = overrides.id ?? id()
  return {
    id: tokenId,
    // 真实的标识取 ID 去掉连字符后的前 8 位；样例 ID 前缀相同，这里取后 8 位以便区分。
    prefix: `lne_${tokenId.replace(/-/g, '').slice(-8)}`,
    createdAt: '2026-10-01T02:00:00Z',
    expiresAt: null,
    revokedAt: null,
    lastUsedAt: null,
    ...overrides,
  }
}

const otherSide = (thread: Message[]): Participant => {
  const first = thread[0]
  return first.from.kind === 'console' ? first.to : first.from
}

interface FakeInboxOptions {
  messages?: Message[]
  // 收件人地址或 ID 到参与方的对应，用于新建消息。
  recipients?: Record<string, Participant>
  // 新建或回复时返回的错误；给出后请求不写入。
  sendError?: { status: number; body: Schemas['Error'] }
}

// 假的收件箱后端：按消息推出线索、未读数与已读状态，记录新建与回复的请求体。
export function fakeInbox(options: FakeInboxOptions = {}) {
  const messages = [...(options.messages ?? [])]
  const sent: { to?: string; text?: string; whenBusy?: string; replyTo?: string; optionId?: string }[] = []
  const readCalls: string[] = []
  const readMessages: string[] = []
  let sendError = options.sendError

  const unreadTotal = () => messages.filter((m) => m.to.kind === 'console' && !m.readAt).length
  const threadsOf = () => {
    const groups = new Map<string, Message[]>()
    for (const m of messages) groups.set(m.threadId, [...(groups.get(m.threadId) ?? []), m])
    return [...groups.entries()]
      .map(([threadId, list]): Schemas['InboxThreadSummary'] => {
        const last = list.at(-1)!
        return {
          threadId,
          participants: [consoleParty, otherSide(list)],
          lastMessage: summaryOf(last),
          unreadCount: list.filter((m) => m.to.kind === 'console' && !m.readAt).length,
          updatedAt: last.createdAt,
        }
      })
      .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
  }

  const handlers: HttpHandler[] = [
    http.get(apiUrl('/inbox/threads'), () => HttpResponse.json({ threads: threadsOf(), unreadTotal: unreadTotal(), nextCursor: null })),
    http.get(apiUrl('/inbox/threads/:threadId'), ({ params }) => {
      const list = messages.filter((m) => m.threadId === params.threadId)
      if (list.length === 0) return HttpResponse.json(errorBody('not_found'), { status: 404 })
      return HttpResponse.json({ threadId: String(params.threadId), messages: list })
    }),
    http.post(apiUrl('/inbox/threads/:threadId/read'), ({ params }) => {
      readCalls.push(String(params.threadId))
      const now = new Date().toISOString()
      for (const m of messages) if (m.threadId === params.threadId && m.to.kind === 'console' && !m.readAt) m.readAt = now
      return HttpResponse.json({ unreadTotal: unreadTotal() })
    }),
    // 单条标为已读：返回全文，已读时间保持第一次的值。
    http.post(apiUrl('/inbox/messages/:messageId/read'), ({ params }) => {
      const message = messages.find((m) => m.id === params.messageId)
      if (!message) return HttpResponse.json(errorBody('not_found'), { status: 404 })
      readMessages.push(message.id)
      message.readAt ??= new Date().toISOString()
      return HttpResponse.json(message)
    }),
    // 你与一个 Agent（不含任务实例）之间的消息，新的在前。
    http.get(apiUrl('/agents/:agentId/console-messages'), ({ params }) => {
      const withAgent = (p: Participant) => p.kind === 'agent' && p.id === params.agentId
      const list = messages.filter((m) => m.task === null && ((m.from.kind === 'console' && withAgent(m.to)) || (m.to.kind === 'console' && withAgent(m.from))))
      return HttpResponse.json({ messages: [...list].reverse(), nextCursor: null })
    }),
    http.get(apiUrl('/inbox/pending'), () => HttpResponse.json(pendingOf(messages))),
    http.post(apiUrl('/inbox/messages'), async ({ request }) => {
      const body = (await request.json()) as Schemas['InboxSendInput']
      sent.push(body)
      if (sendError) return HttpResponse.json(sendError.body, { status: sendError.status })
      const to = options.recipients?.[body.to]
      if (!to) return HttpResponse.json(errorBody('not_found'), { status: 404 })
      const message = makeInboxMessage({ from: consoleParty, to, text: body.text, whenBusy: to.kind === 'agent' ? (body.whenBusy ?? 'queue') : null })
      messages.push(message)
      return HttpResponse.json(message, { status: 201 })
    }),
    http.post(apiUrl('/inbox/messages/:messageId/reply'), async ({ params, request }) => {
      const body = (await request.json()) as Schemas['InboxReplyInput']
      sent.push({ ...body, replyTo: String(params.messageId) })
      if (sendError) return HttpResponse.json(sendError.body, { status: sendError.status })
      const target = messages.find((m) => m.id === params.messageId)
      if (!target) return HttpResponse.json(errorBody('not_found'), { status: 404 })
      const to = otherSide(messages.filter((m) => m.threadId === target.threadId))
      const message = makeInboxMessage({
        from: consoleParty,
        to,
        threadId: target.threadId,
        inReplyTo: target.id,
        text: body.text ?? (body.optionId ? `选择：${target.question?.options.find(option => option.id === body.optionId)?.label ?? body.optionId}` : ''),
        whenBusy: to.kind === 'agent' ? (body.whenBusy ?? 'queue') : null,
      })
      if (target.question?.state === 'open') {
        target.question = { ...target.question, state: 'answered', answer: { messageId: message.id, optionId: body.optionId ?? null, optionLabel: target.question.options.find(option => option.id === body.optionId)?.label ?? null, textPreview: message.text, by: consoleParty, at: message.createdAt } }
        message.answerTo = { messageId: target.id, optionId: body.optionId ?? null, optionLabel: target.question.answer?.optionLabel ?? null }
      }
      messages.push(message)
      return HttpResponse.json(message, { status: 201 })
    }),
  ]

  return {
    handlers,
    messages,
    sent,
    readCalls,
    readMessages,
    unreadTotal,
    clearSendError: () => {
      sendError = undefined
    },
    // 模拟另一方发来消息（不经过控制台）。
    receive(message: Message) {
      messages.push(message)
      return message
    },
  }
}

// 待你处理：发给你的 open 问题与其他未读消息（不含取消的），按发件 Agent 或任务汇总。
export function pendingOf(messages: Message[], projectOf: (message: Message) => string | null = () => null): Schemas['InboxPending'] {
  const toYou = messages.filter((m) => m.to.kind === 'console' && !m.cancelledAt)
  const item = (message: Message) => ({ message: summaryOf(message), projectId: projectOf(message) })
  const questions = toYou.filter((m) => m.question?.state === 'open').reverse()
  const unread = toYou.filter((m) => m.question?.state !== 'open' && !m.readAt).reverse()
  const byAgent = new Map<string, Schemas['InboxPendingAgentCount']>(), byTask = new Map<string, Schemas['InboxPendingTaskCount']>()
  for (const [list, field] of [[questions, 'questions'], [unread, 'unread']] as const) {
    for (const m of list) {
      if (m.task) {
        const row = byTask.get(m.task.taskId) ?? { taskId: m.task.taskId, projectId: projectOf(m), questions: 0, unread: 0 }
        row[field]++; byTask.set(m.task.taskId, row)
      } else if (m.from.kind === 'agent' && m.from.id) {
        const row = byAgent.get(m.from.id) ?? { agentId: m.from.id, projectId: projectOf(m), questions: 0, unread: 0 }
        row[field]++; byAgent.set(m.from.id, row)
      }
    }
  }
  return { questions: questions.map(item), unread: unread.map(item), byAgent: [...byAgent.values()], byTask: [...byTask.values()] }
}

// 收件箱事件流：由测试推送事件、打开或中断连接。
export const inboxStream = () => FakeEventSource.instances.filter((s) => s.url.endsWith('/inbox/events')).at(-1)!

let eventSeq = 9000
export async function emitInbox(type: string, data: unknown) {
  await act(async () => {
    inboxStream().emit(type, data, `${++eventSeq}-0`)
    await new Promise((resolve) => setTimeout(resolve, 10))
  })
}

export async function openInboxStream(unreadTotal = 0) {
  await act(async () => inboxStream().open())
  await emitInbox('snapshot', { unreadTotal })
}
