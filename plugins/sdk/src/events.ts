// 平台事件目录（plugins/SPEC.md §4；字段合同以 api/openapi.yaml 的 PluginEvent 为准）。
// 事件的种类决定处理函数怎样被调用、返回什么：拦截可阻止，修改按字段合并，认领第一个处理者赢，观察只通知。

export type HookKind = 'intercept' | 'modify' | 'claim' | 'observe'

/** 每个事件的种类。与 internal/pluginmanifest 的 Events 一致。 */
export const EVENTS = {
  'run.before_start': 'intercept',
  'task.before_start': 'intercept',
  'node.before_start': 'intercept',
  'node.before_submit': 'intercept',
  'inbox.before_send': 'intercept',
  'message.before_enqueue': 'modify',
  'inbox.before_deliver': 'modify',
  'sleep.before_schedule': 'modify',
  'console.command': 'claim',
  'run.queued': 'observe',
  'run.started': 'observe',
  'run.ended': 'observe',
  'task.started': 'observe',
  'task.waiting': 'observe',
  'task.blocked': 'observe',
  'task.completed': 'observe',
  'task.cancelled': 'observe',
  'node.started': 'observe',
  'node.submitted': 'observe',
  'node.blocked': 'observe',
  'inbox.received': 'observe',
  'inbox.read': 'observe',
  'inbox.cancelled': 'observe',
  'question.asked': 'observe',
  'question.answered': 'observe',
  'sleep.requested': 'observe',
  'sleep.woken': 'observe',
  'agent.created': 'observe',
  'agent.updated': 'observe',
  'agent.deleted': 'observe',
  'pi.started': 'observe',
  'pi.exited': 'observe',
  'model.retry': 'observe',
  'context.compacted': 'observe',
  'folder.changed': 'observe',
  'plugin.enabled': 'observe',
  'plugin.disabled': 'observe',
  'plugin.config_changed': 'observe',
  'channel.outbound': 'observe',
  'identity.inbound': 'observe',
} as const satisfies Record<string, HookKind>

export type EventType = keyof typeof EVENTS
export type EventKind<E extends EventType> = (typeof EVENTS)[E]
export type EventsOfKind<K extends HookKind> = { [E in EventType]: EventKind<E> extends K ? E : never }[EventType]

/** 清单 permissions 的可选值（plugins/SPEC.md §5）。 */
export const PERMISSIONS = [
  'agents.read',
  'agents.write',
  'tasks.read',
  'tasks.write',
  'runs.read',
  'inbox.read',
  'inbox.send',
  'folders.read',
  'folders.write',
  'identities.read',
  'identities.manage',
  'state',
  'accounts.read',
] as const

export type Permission = (typeof PERMISSIONS)[number]

/** 参与方：agent 带 id 与地址，external 带 id 与 @名称，plugin 带 plugin:<id> 地址，console 与 platform 只有 kind 与控制台地址。 */
export interface Party {
  kind: 'agent' | 'console' | 'external' | 'platform' | 'plugin'
  id: string | null
  address: string | null
}

export type RunOrigin = 'console' | 'inbox_reminder' | 'sleep_wake' | 'task_node' | 'console_node'

export interface RunTokens {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
  total: number
}

interface TaskFields {
  title: string
  /** 阻塞、取消的原因；没有时为 null。 */
  reason: string | null
}

interface NodeFields {
  nodeRunId: string
  stepId: string
  reason: string | null
}

interface InboxFields {
  messageId: string
  threadId: string
  from: Party
  to: Party
  isQuestion: boolean
  /** 正文前 80 个字符；正文用 inbox.read 范围的接口另取。 */
  excerpt: string
}

interface QuestionFields {
  messageId: string
  threadId: string
  from: Party
  to: Party
}

/** 事件里的提问：代管的外部身份收到的（identity.inbound，plugins/SPEC.md §12），或 Agent 在渠道线索里提的（channel.outbound）。 */
export interface EventQuestion {
  /** 提问消息的 id，回答时用它（api.identities.answer / api.channels.answer 的 questionId）。 */
  id: string
  options: { id: string; label: string }[]
  /** 是否允许不选选项、直接用文字回答。 */
  allowText: boolean
  /** RFC 3339；过期后不能再回答。 */
  deadline: string | null
}

/** 旧名，同 EventQuestion。 */
export type IdentityQuestion = EventQuestion

interface AgentFields {
  name: string
  address: string
}

/** 每个事件自己的字段（固定字段之外）。 */
export interface EventFields {
  'run.queued': { origin: RunOrigin }
  'run.started': { origin: RunOrigin }
  'run.ended': {
    origin: RunOrigin
    status: 'completed' | 'failed' | 'interrupted'
    endReason: string | null
    errorCode: string | null
    tokens: RunTokens | null
  }
  'task.started': TaskFields
  'task.waiting': TaskFields
  'task.blocked': TaskFields
  'task.completed': TaskFields
  'task.cancelled': TaskFields
  'node.started': NodeFields
  'node.submitted': NodeFields
  'node.blocked': NodeFields
  'inbox.received': InboxFields
  'inbox.read': InboxFields
  'inbox.cancelled': InboxFields
  'question.asked': QuestionFields
  'question.answered': QuestionFields & { optionId: string | null }
  'sleep.requested': { sleepId: string; wakeAt: string | null }
  'sleep.woken': { sleepId: string; wakeAt: string | null; endReason: string | null }
  'agent.created': AgentFields
  'agent.updated': AgentFields
  'agent.deleted': AgentFields
  /** reason：启动原因或停止原因，意外退出为 unexpected_exit。 */
  'pi.started': { reason: string }
  'pi.exited': { reason: string }
  'model.retry': { attempt: number; delayMs: number }
  'context.compacted': { reason: string | null }
  'folder.changed': { level: 'agent' | 'project' | 'global'; path: string; by: Party }
  'plugin.enabled': { pluginId: string }
  'plugin.disabled': { pluginId: string }
  'plugin.config_changed': { pluginId: string }
  /**
   * Agent 在插件发来的线索里回复：只发给线索所属的插件，按 channelId 交给该渠道的 outbound。正文用 api.channels.read 另取。
   * Agent 提问时 isQuestion 为 true、question 给出选项，用 api.channels.answer 作答。
   */
  'channel.outbound': {
    messageId: string
    threadId: string
    channelId: string | null
    from: Party
    isQuestion: boolean
    question: EventQuestion | null
    /** 正文前 80 个字符。 */
    excerpt: string
  }
  /**
   * 插件代管的外部身份收到了消息或提问（plugins/SPEC.md §12）：只发给代管插件，按 channelId 交给该渠道的 identityInbound。
   * 正文用 api.identities.read 另取；回复与回答用 api.identities.reply / answer。
   */
  'identity.inbound': {
    identityId: string
    channelId: string
    messageId: string
    threadId: string
    from: { kind: string; agentId: string | null; name: string }
    nodeRunId: string | null
    isQuestion: boolean
    question: EventQuestion | null
    /** 正文前 80 个字符。 */
    excerpt: string
  }
  'run.before_start': { origin: RunOrigin; messageId: string | null }
  'task.before_start': { title: string; stepCount: number; sourceInboxMessageId: string | null }
  'node.before_start': { nodeRunId: string; stepId: string }
  'node.before_submit': { nodeRunId: string; edgeId: string | null }
  'inbox.before_send': { from: Party; to?: Party | null; replyTo?: string | null; isQuestion: boolean }
  'message.before_enqueue': { text: string; from: Party; imageCount: number; nodeRunId?: string }
  'inbox.before_deliver': { text: string; from: Party; to?: Party | null; replyTo?: string | null }
  'sleep.before_schedule': { durationSeconds: number; reason: string | null }
  'console.command': { command: string; text: string; from: Party }
}

/** 每个事件都有的固定字段；同名时固定字段优先。 */
export interface EventBase<E extends EventType = EventType> {
  /** ULID；观察类事件至少送达一次，按它去重。 */
  id: string
  type: E
  /** RFC 3339。 */
  at: string
  /** 项目内的事件为该项目；平台 Agent 与全局事件为 null。 */
  projectId: string | null
  agentId: string | null
  taskId: string | null
  runId: string | null
}

export type PluginEvent<E extends EventType = EventType> = E extends EventType ? EventBase<E> & EventFields[E] : never

/** 拦截：返回 `{ block: true, reason }` 阻止，其余放行。 */
export type InterceptResult = { block: true; reason: string } | { block?: false } | void | undefined

/** 修改：返回 `{ patch }`，按字段合并到事件，下一个处理者看到合并后的事件。 */
export type ModifyResult<E extends EventType> = { patch: Partial<EventFields[E]> } | void | undefined

/** 认领：返回 `{ handled: true, ... }` 认领，整个对象交给调用点（console.command 可带 reply）。 */
export type ClaimResult = { handled: true; reply?: string | null; [key: string]: unknown } | { handled?: false } | void | undefined

export type EventResult<E extends EventType> = EventKind<E> extends 'intercept'
  ? InterceptResult
  : EventKind<E> extends 'modify'
    ? ModifyResult<E>
    : EventKind<E> extends 'claim'
      ? ClaimResult
      : void

export function isEventType(type: string): type is EventType {
  return Object.hasOwn(EVENTS, type)
}
