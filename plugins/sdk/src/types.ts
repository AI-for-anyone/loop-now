// 平台半边的接口（plugins/SPEC.md §2.2）。宿主为每个插件建一个 PluginAPI 交给它的 register；
// 所有注册都返回取消函数，插件停用或卸载时宿主统一释放，作者不需要手动清理。

import type { EventResult, EventType, PluginEvent } from './events.ts'

/** 取消一项注册；重复调用无害。 */
export type Disposer = () => void

/** 处理函数的上下文。 */
export interface EventContext {
  eventId: string
  type: EventType
  projectId: string | null
  agentId: string | null
  taskId: string | null
  runId: string | null
  /** 观察类事件的第几次投递（从 1 开始）；同步调用为 1。 */
  attempt: number
  /** 超时或插件停用时触发；长操作应把它传下去。 */
  signal: AbortSignal
}

export type EventHandler<E extends EventType> = (
  event: PluginEvent<E>,
  ctx: EventContext,
) => EventResult<E> | Promise<EventResult<E>>

export interface LifecycleContext {
  /** 插件停用或卸载时触发。 */
  signal: AbortSignal
}

export type LifecyclePhase = 'start' | 'stop'

export interface StateOptions {
  /** 项目级状态；省略或 null 为插件全局状态。 */
  projectId?: string | null
}

export interface StateEntry<T = unknown> {
  key: string
  value: T
}

/** 插件的持久状态，经平台保存在数据库里；需要清单声明 `state` 权限。key 为 1 到 512 字节，值为 JSON，最多 1 MiB。 */
export interface PluginState {
  get<T = unknown>(key: string, options?: StateOptions): Promise<T | null>
  set(key: string, value: unknown, options?: StateOptions): Promise<void>
  /** 不存在也成功。 */
  delete(key: string, options?: StateOptions): Promise<void>
  /** 按 key 排序，最多 1000 项。 */
  list<T = unknown>(prefix: string, options?: StateOptions): Promise<StateEntry<T>[]>
}

export type ConfigValues = Record<string, unknown>

export interface PluginConfig {
  /** 生效的配置（不含 x-secret 字段）：全局值之上叠加该项目的 x-scope: project 字段。 */
  get<T extends object = ConfigValues>(projectId?: string | null): T
  /** 启用了本插件的项目 id（按 id 排序），例如按项目配置查找某个外部会话属于哪个项目。启用、停用或改配置后随之更新。 */
  projects(): string[]
  /** 平台改了配置后调用；收到时 get 已返回新值。 */
  onChange(listener: () => void | Promise<void>): Disposer
}

export interface PluginSecrets {
  /** x-secret 字段的值，项目值优先；没有设置时为 undefined。宿主会在插件日志里遮住这些值。 */
  get(name: string, projectId?: string | null): string | undefined
}

export interface Account {
  id: string
  kind: string
  /** 人在项目里给账号起的名字。 */
  name: string
  /** 账号字段（按清单 accounts[].fields），含 x-secret 字段的明文值；不要写进日志。 */
  values: Record<string, unknown>
}

export interface PluginAccounts {
  /**
   * 项目里某种账号的列表（按创建顺序，不含已删除的）。需要 accounts.read 权限；kind 须在清单 accounts 里声明；
   * 插件须在该项目启用。每次都从平台读最新的，不缓存。
   */
  list(projectId: string, kind: string): Promise<Account[]>
}

export interface PlatformFetchInit extends Omit<RequestInit, 'body'> {
  /** 字符串、Buffer 等原样发送；普通对象按 JSON 发送并加 content-type。 */
  body?: RequestInit['body'] | Record<string, unknown> | unknown[]
}

export interface PluginPlatform {
  /** 平台接口地址（…/api/v1）。 */
  readonly apiUrl: string
  /** 带插件令牌调用 /api/v1；path 以 / 开头。只能调用清单 permissions 范围内的操作，超出返回 403。 */
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
  /** 按钮所在的页面：清单 where 对应的 id。 */
  where: ActionWhere
  signal: AbortSignal
}

export interface ActionResult {
  /** 控制台以提示显示。 */
  notice: string
  /** 可选的跳转地址。 */
  url?: string
}

export type ActionHandler = (input: Record<string, unknown>, ctx: ActionContext) => ActionResult | Promise<ActionResult>

export interface PluginActions {
  /** 清单 actions 里声明的按钮；input 已按清单 fields 校验。30 秒内没有返回时控制台得到失败。 */
  register(id: string, handler: ActionHandler): Disposer
}

export interface HttpContext {
  /** external：外部 TLS 口的无令牌路径 /hooks/<id>/*；console：控制台口的 /api/v1/plugins/<id>/http/*（需令牌）。 */
  via: 'external' | 'console'
  signal: AbortSignal
}

export type HttpHandler = (request: Request, ctx: HttpContext) => Response | Promise<Response>

export interface PluginHttp {
  /**
   * 清单 http 为 true 时，平台把 HTTP 入口收到的请求转到这里，原样返回状态码、头与正文。
   * request.url 的路径是去掉入口前缀之后的部分；头名小写，不含 Authorization 与 Cookie；正文不超过 1 MiB；30 秒超时。
   * 签名校验由插件自己做。
   */
  handle(handler: HttpHandler): Disposer
}

export interface ScheduleContext {
  scheduleId: string
  cron: string
  /** 这一次应触发的时间（RFC 3339）。 */
  at: string
  signal: AbortSignal
}

export interface PluginSchedule {
  /** 清单 schedule 里声明的定时任务：平台按 cron（服务端本地时间）触发，同一条目不并发，错过的不补，10 分钟超时。 */
  on(id: string, handler: (ctx: ScheduleContext) => void | Promise<void>): Disposer
}

export interface ChannelHandlers {
  /**
   * 这个渠道代管的外部身份收到了消息或提问（identity.inbound，只发给代管插件；清单里这个渠道须声明 manageIdentities）。
   * 处理完才确认；抛错按观察类事件重投。把消息推给人，人的回复用 api.identities.reply / answer 送回平台。
   */
  identityInbound?: (event: PluginEvent<'identity.inbound'>, ctx: EventContext) => void | Promise<void>
  /**
   * Agent 回复了经这个渠道发来的消息（channel.outbound，只发给消息线索所属的插件）。
   * 事件只带摘要；正文用 inbox.read 范围的接口另取。与观察类事件一样至少送达一次，按 event.id 去重。
   */
  outbound?: (event: PluginEvent<'channel.outbound'>, ctx: EventContext) => void | Promise<void>
}

/** 收件箱消息全文（api/openapi.yaml 的 InboxMessage）；这里只列常用字段。 */
export interface InboxMessage {
  id: string
  threadId: string
  inReplyTo: string | null
  /** 这条回复回答了哪个提问；没有回答提问（例如提问已关闭时用文字回复）时为 null。 */
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
  /** 收件地址，写法同 POST /inbox/messages 的 to（如 项目名/Agent名）。 */
  to: string
  text: string
  /** 对话里显示的发件名称，默认为插件的显示名称（例如飞书用户的名字）。 */
  senderName?: string
  whenBusy?: 'queue' | 'interrupt'
}

/** 回答一个提问：选一个选项（可另附文字），或在提问允许时只用文字回答。 */
export type QuestionAnswer = { optionId: string; text?: string } | { text: string; optionId?: never }

export interface PluginChannels {
  /** 清单 channels 里声明的渠道；channel.outbound 事件按 channelId 交给对应渠道的 outbound。 */
  register(id: string, handlers: ChannelHandlers): Disposer
  /**
   * 以插件发件方（经这个渠道）给 Agent 发一条新消息，开启新线索；Agent 的回复按线索以 channel.outbound 回到这个渠道。
   * 需要 inbox.send 权限；渠道须在清单里声明。
   */
  send(channelId: string, input: ChannelSendInput): Promise<InboxMessage>
  /** 在已有线索里回复一条消息（例如 channel.outbound 的 messageId）。需要 inbox.send 权限。插件不能提问，所以没有选项。 */
  reply(messageId: string, input: { text: string }): Promise<InboxMessage>
  /** 读一条发给插件的消息全文并标为已读（channel.outbound 只带摘要）。需要 inbox.read 权限。 */
  read(messageId: string): Promise<InboxMessage>
  /**
   * 回答 Agent 在这个渠道的线索里提的问题（channel.outbound 的 question.id）；效果与外部身份作答相同：问题被回答、等待的任务继续。
   * 需要 inbox.send 权限；渠道须在清单里声明。提问已回答、过期或关闭时平台返回 409（错误的 code 为 already_answered、question_expired
   * 或 question_closed），选项不存在时 422，线索不属于本插件时 403。
   */
  answer(channelId: string, questionId: string, input: QuestionAnswer): Promise<InboxMessage>
}

/** 同 QuestionAnswer。 */
export type IdentityAnswer = QuestionAnswer

/** 以代管的外部身份的名义读消息、回复与回答（plugins/SPEC.md §12）。需要 identities.manage 权限与声明 manageIdentities 的渠道；只限本插件代管的身份。 */
export interface PluginIdentities {
  /** 读代管身份收件箱里的一条消息全文。 */
  read(identityId: string, messageId: string): Promise<InboxMessage>
  /** 以该身份回复一条消息；对发起方的效果与该身份亲自回复相同。 */
  reply(identityId: string, messageId: string, input: { text: string }): Promise<InboxMessage>
  /** 以该身份回答一个提问：选一个选项，或在提问允许时用文字回答。 */
  answer(identityId: string, questionId: string, input: QuestionAnswer): Promise<InboxMessage>
}

export interface PluginAPI {
  /** 清单 loopNow.id。 */
  readonly id: string
  readonly version: string
  /** 安装目录（LOOP_NOW_PLUGIN_ROOT）。 */
  readonly root: string
  /** 数据目录（LOOP_NOW_PLUGIN_DATA），跨版本保留。 */
  readonly dataDir: string

  /** 订阅事件；事件须在清单 loopNow.events 内，否则加载失败。同一事件可以注册多个处理函数，按注册顺序调用。 */
  on<E extends EventType>(type: E, handler: EventHandler<E>): Disposer
  lifecycle: {
    /** start 在 register 之后、插件就绪之前调用，适合起定时器和连接；stop 在停用或卸载时调用。 */
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

/** 平台半边入口的默认导出。 */
export type PluginRegister = (api: PluginAPI) => void | Promise<void>
