// The platform-side API (plugins/SPEC.md §2.2). The host builds one PluginAPI per plugin and passes it to the plugin's register;
// every registration returns a disposer, and the host releases them all when the plugin is disabled or unloaded, so authors need
// no manual cleanup.

import type { EventResult, EventType, PluginEvent } from './events.ts'

/** Cancels one registration; calling it again is harmless. */
export type Disposer = () => void

/** Context passed to handlers. */
export interface EventContext {
  eventId: string
  type: EventType
  projectId: string | null
  agentId: string | null
  taskId: string | null
  runId: string | null
  /** Delivery attempt number of an observe event (starting at 1); 1 for synchronous calls. */
  attempt: number
  /** Fires on timeout or when the plugin is disabled; long operations should pass it on. */
  signal: AbortSignal
}

export type EventHandler<E extends EventType> = (
  event: PluginEvent<E>,
  ctx: EventContext,
) => EventResult<E> | Promise<EventResult<E>>

export interface LifecycleContext {
  /** Fires when the plugin is disabled or unloaded. */
  signal: AbortSignal
}

export type LifecyclePhase = 'start' | 'stop'

export interface StateOptions {
  /** Project-level state; omitted or null means the plugin's global state. */
  projectId?: string | null
}

export interface StateEntry<T = unknown> {
  key: string
  value: T
}

/**
 * The plugin's persistent state, saved in the database through the platform; the manifest must declare the `state` permission.
 * Keys are 1 to 512 bytes; values are JSON, at most 1 MiB.
 */
export interface PluginState {
  get<T = unknown>(key: string, options?: StateOptions): Promise<T | null>
  set(key: string, value: unknown, options?: StateOptions): Promise<void>
  /** Succeeds even if the key does not exist. */
  delete(key: string, options?: StateOptions): Promise<void>
  /** Sorted by key, at most 1000 entries. */
  list<T = unknown>(prefix: string, options?: StateOptions): Promise<StateEntry<T>[]>
}

export type ConfigValues = Record<string, unknown>

export interface PluginConfig {
  /** The effective config (without x-secret fields): the project's x-scope: project fields layered over the global values. */
  get<T extends object = ConfigValues>(projectId?: string | null): T
  /**
   * Ids of the projects that have enabled this plugin (sorted by id), e.g. to find which project an external conversation belongs to
   * from per-project config. Updated after enabling, disabling or a config change.
   */
  projects(): string[]
  /** Called after the platform changes the config; by then get already returns the new values. */
  onChange(listener: () => void | Promise<void>): Disposer
}

export interface PluginSecrets {
  /** Value of an x-secret field, the project value first; undefined when not set. The host masks these values in plugin logs. */
  get(name: string, projectId?: string | null): string | undefined
}

export interface Account {
  id: string
  kind: string
  /** The name a person gave the account in the project. */
  name: string
  /** Account fields (per the manifest's accounts[].fields), including plaintext values of x-secret fields; do not write them to logs. */
  values: Record<string, unknown>
}

export interface PluginAccounts {
  /**
   * Accounts of one kind in a project (in creation order, excluding deleted ones). Needs the accounts.read permission; kind must be declared
   * in the manifest's accounts; the plugin must be enabled in that project. Always reads the latest from the platform, no caching.
   */
  list(projectId: string, kind: string): Promise<Account[]>
}

export interface PlatformFetchInit extends Omit<RequestInit, 'body'> {
  /** Strings, Buffers and the like are sent as is; plain objects are sent as JSON with a content-type added. */
  body?: RequestInit['body'] | Record<string, unknown> | unknown[]
}

export interface PluginPlatform {
  /** The platform API URL (…/api/v1). */
  readonly apiUrl: string
  /** Calls /api/v1 with the plugin token; path starts with /. Only operations within the manifest's permissions work; others get 403. */
  fetch(path: string, init?: PlatformFetchInit): Promise<Response>
}

export interface PluginLog {
  debug(...args: unknown[]): void
  info(...args: unknown[]): void
  warn(...args: unknown[]): void
  error(...args: unknown[]): void
}

export interface ActionWhere {
  projectId?: string
  agentId?: string
  taskId?: string
  identityId?: string
}

export interface ActionContext {
  actionId: string
  /** The page the button is on: the ids that match the manifest's where. */
  where: ActionWhere
  signal: AbortSignal
}

export interface ActionResult {
  /** Shown by the console as a notice. */
  notice: string
  /** Optional URL to navigate to. */
  url?: string
}

export type ActionHandler = (input: Record<string, unknown>, ctx: ActionContext) => ActionResult | Promise<ActionResult>

export interface PluginActions {
  /**
   * A button declared in the manifest's actions; input has already been validated against the manifest's fields.
   * If it does not return within 30 seconds, the console gets a failure.
   */
  register(id: string, handler: ActionHandler): Disposer
}

export interface HttpContext {
  /**
   * external: the token-free path /hooks/<id>/* on the external TLS port;
   * console: /api/v1/plugins/<id>/http/* on the console port (needs a token).
   */
  via: 'external' | 'console'
  signal: AbortSignal
}

export type HttpHandler = (request: Request, ctx: HttpContext) => Response | Promise<Response>

export interface PluginHttp {
  /**
   * When the manifest's http is true, the platform forwards requests received at the HTTP entry to this handler and returns its status code,
   * headers and body as is.
   * The path of request.url is what remains after the entry prefix is removed; header names are lowercase, without Authorization and Cookie;
   * the body is at most 1 MiB; 30-second timeout.
   * The plugin verifies signatures itself.
   */
  handle(handler: HttpHandler): Disposer
}

export interface ScheduleContext {
  scheduleId: string
  cron: string
  /** The time this run was due to fire (RFC 3339). */
  at: string
  signal: AbortSignal
}

export interface PluginSchedule {
  /**
   * A scheduled job declared in the manifest's schedule: the platform fires it by cron (server local time); one entry never runs
   * concurrently, missed runs are not made up, and the timeout is 10 minutes.
   */
  on(id: string, handler: (ctx: ScheduleContext) => void | Promise<void>): Disposer
}

export interface ChannelHandlers {
  /**
   * An external identity managed by this channel received a message or question (identity.inbound, sent only to the managing plugin;
   * this channel must declare manageIdentities in the manifest).
   * Acknowledged only once handled; throwing redelivers it like an observe event. Push the message to the person, and send their reply
   * back to the platform with api.identities.reply / answer.
   */
  identityInbound?: (event: PluginEvent<'identity.inbound'>, ctx: EventContext) => void | Promise<void>
  /**
   * The Agent replied to a message that came through this channel (channel.outbound, sent only to the plugin that owns the message thread).
   * The event carries only an excerpt; fetch the body separately via the API under the inbox.read scope. Like observe events it is
   * delivered at least once; deduplicate by event.id.
   */
  outbound?: (event: PluginEvent<'channel.outbound'>, ctx: EventContext) => void | Promise<void>
}

/** Full inbox message (InboxMessage in api/openapi.yaml); only common fields are listed here. */
export interface InboxMessage {
  id: string
  threadId: string
  inReplyTo: string | null
  /** Which question this reply answered; null when it answered none (e.g. a text reply after the question was closed). */
  answerTo?: { messageId: string; optionId: string | null; optionLabel: string | null } | null
  text: string
  preview: string
  createdAt: string
  readAt: string | null
  from: Record<string, unknown>
  to: Record<string, unknown>
  [field: string]: unknown
}

export interface ChannelSendInput {
  /** Recipient address, written like the to of POST /inbox/messages (e.g. <project name>/<Agent name>). */
  to: string
  text: string
  /** Sender name shown in the conversation; defaults to the plugin's display name (e.g. the Lark user's name). */
  senderName?: string
  whenBusy?: 'queue' | 'interrupt'
}

/** Answers a question: pick an option (optionally with text), or answer with text only when the question allows it. */
export type QuestionAnswer = { optionId: string; text?: string } | { text: string; optionId?: never }

export interface PluginChannels {
  /** A channel declared in the manifest's channels; channel.outbound events go to the matching channel's outbound by channelId. */
  register(id: string, handlers: ChannelHandlers): Disposer
  /**
   * Sends a new message to an Agent as the plugin sender (through this channel), starting a new thread; the Agent's replies come back
   * to this channel as channel.outbound, by thread.
   * Needs the inbox.send permission; the channel must be declared in the manifest.
   */
  send(channelId: string, input: ChannelSendInput): Promise<InboxMessage>
  /**
   * Replies to a message in an existing thread (e.g. the messageId of channel.outbound). Needs the inbox.send permission.
   * Plugins cannot ask questions, so there are no options.
   */
  reply(messageId: string, input: { text: string }): Promise<InboxMessage>
  /**
   * Reads the full text of a message sent to the plugin and marks it read (channel.outbound carries only an excerpt).
   * Needs the inbox.read permission.
   */
  read(messageId: string): Promise<InboxMessage>
  /**
   * Answers a question the Agent asked in this channel's thread (question.id of channel.outbound); same effect as an external identity
   * answering: the question is answered and the waiting task continues.
   * Needs the inbox.send permission; the channel must be declared in the manifest. When the question is already answered, expired or
   * closed, the platform returns 409 (error code already_answered, question_expired or question_closed), 422 when the option does not exist,
   * and 403 when the thread does not belong to this plugin.
   */
  answer(channelId: string, questionId: string, input: QuestionAnswer): Promise<InboxMessage>
}

/** Same as QuestionAnswer. */
export type IdentityAnswer = QuestionAnswer

/**
 * Read messages, reply and answer in the name of a managed external identity (plugins/SPEC.md §12). Needs the identities.manage
 * permission and a channel that declares manageIdentities; limited to the identities this plugin manages.
 */
export interface PluginIdentities {
  /** Reads the full text of one message in a managed identity's inbox. */
  read(identityId: string, messageId: string): Promise<InboxMessage>
  /** Replies to a message as that identity; for the sender it has the same effect as the identity replying in person. */
  reply(identityId: string, messageId: string, input: { text: string }): Promise<InboxMessage>
  /** Answers a question as that identity: pick an option, or answer with text when the question allows it. */
  answer(identityId: string, questionId: string, input: QuestionAnswer): Promise<InboxMessage>
}

export interface PluginAPI {
  /** The manifest's loopNow.id. */
  readonly id: string
  readonly version: string
  /** Install directory (LOOP_NOW_PLUGIN_ROOT). */
  readonly root: string
  /** Data directory (LOOP_NOW_PLUGIN_DATA), kept across versions. */
  readonly dataDir: string

  /**
   * Subscribes to an event; the event must be in the manifest's loopNow.events, or loading fails. One event can have several handlers,
   * called in registration order.
   */
  on<E extends EventType>(type: E, handler: EventHandler<E>): Disposer
  lifecycle: {
    /**
     * start is called after register and before the plugin is ready, the place to start timers and connections;
     * stop is called when the plugin is disabled or unloaded.
     */
    on(phase: LifecyclePhase, handler: (ctx: LifecycleContext) => void | Promise<void>): Disposer
  }
  state: PluginState
  config: PluginConfig
  secrets: PluginSecrets
  accounts: PluginAccounts
  platform: PluginPlatform
  log: PluginLog
  actions: PluginActions
  http: PluginHttp
  schedule: PluginSchedule
  channels: PluginChannels
  identities: PluginIdentities
}

/** The default export of the platform-side entry. */
export type PluginRegister = (api: PluginAPI) => void | Promise<void>
