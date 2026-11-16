// Platform event catalog (plugins/SPEC.md §4; the field contract is PluginEvent in api/openapi.yaml).
// An event's kind decides how its handlers are called and what they return: intercept can block, modify merges by field,
// claim lets the first handler win, observe only notifies.

export type HookKind = 'intercept' | 'modify' | 'claim' | 'observe'

/** The kind of each event. Matches Events in internal/pluginmanifest. */
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

/** Allowed values of the manifest's permissions (plugins/SPEC.md §5). */
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

/**
 * A party: agent carries an id and an address, external an id and an @name, plugin a plugin:<id> address;
 * console and platform have only kind and the console address.
 */
export interface Party {
  kind: 'agent' | 'console' | 'external' | 'platform' | 'plugin'
  id: string | null
  address: string | null
}

export type RunOrigin = 'console' | 'inbox_reminder' | 'sleep_wake' | 'console_compact' | 'task_node' | 'console_node'

export interface RunTokens {
  input: number
  output: number
  cacheRead: number
  cacheWrite: number
  total: number
}

interface TaskFields {
  title: string
  /** Reason for blocking or cancelling; null when there is none. */
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
  /** First 80 characters of the body; fetch the body separately via the API under the inbox.read scope. */
  excerpt: string
}

interface QuestionFields {
  messageId: string
  threadId: string
  from: Party
  to: Party
}

/**
 * A question in an event: one a managed external identity received (identity.inbound, plugins/SPEC.md §12),
 * or one an Agent asked in a channel thread (channel.outbound).
 */
export interface EventQuestion {
  /** Id of the question message, used to answer it (questionId of api.identities.answer / api.channels.answer). */
  id: string
  options: { id: string; label: string }[]
  /** Whether answering with text only, without choosing an option, is allowed. */
  allowText: boolean
  /** RFC 3339; it can no longer be answered after this. */
  deadline: string | null
}

/** Old name, same as EventQuestion. */
export type IdentityQuestion = EventQuestion

interface AgentFields {
  name: string
  address: string
}

/** Each event's own fields (beyond the fixed fields). */
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
  /** reason: why it started or stopped; unexpected_exit for an unexpected exit. */
  'pi.started': { reason: string }
  'pi.exited': { reason: string }
  'model.retry': { attempt: number; delayMs: number }
  'context.compacted': { reason: string | null }
  'folder.changed': { level: 'agent' | 'project' | 'global'; path: string; by: Party }
  'plugin.enabled': { pluginId: string }
  'plugin.disabled': { pluginId: string }
  'plugin.config_changed': { pluginId: string }
  /**
   * The Agent replied in a thread a plugin started: sent only to the plugin that owns the thread, and passed to that channel's outbound
   * by channelId. Fetch the body separately with api.channels.read.
   * When the Agent asks a question, isQuestion is true and question gives the options; answer with api.channels.answer.
   */
  'channel.outbound': {
    messageId: string
    threadId: string
    channelId: string | null
    from: Party
    isQuestion: boolean
    question: EventQuestion | null
    /** First 80 characters of the body. */
    excerpt: string
  }
  /**
   * An external identity managed by a plugin received a message or question (plugins/SPEC.md §12): sent only to the managing plugin,
   * and passed to that channel's identityInbound by channelId.
   * Fetch the body separately with api.identities.read; reply and answer with api.identities.reply / answer.
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
    /** First 80 characters of the body. */
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

/** Fixed fields every event has; on a name clash the fixed field wins. */
export interface EventBase<E extends EventType = EventType> {
  /** ULID; observe events are delivered at least once, so deduplicate by it. */
  id: string
  type: E
  /** RFC 3339. */
  at: string
  /** The project for events within a project; null for platform Agents and global events. */
  projectId: string | null
  agentId: string | null
  taskId: string | null
  runId: string | null
}

export type PluginEvent<E extends EventType = EventType> = E extends EventType ? EventBase<E> & EventFields[E] : never

/** Intercept: return `{ block: true, reason }` to block; anything else lets it through. */
export type InterceptResult = { block: true; reason: string } | { block?: false } | void | undefined

/** Modify: return `{ patch }`, merged into the event by field; the next handler sees the merged event. */
export type ModifyResult<E extends EventType> = { patch: Partial<EventFields[E]> } | void | undefined

/** Claim: return `{ handled: true, ... }` to claim; the whole object goes to the call site (console.command may include reply). */
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
