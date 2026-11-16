// The runtime of one loaded plugin: its PluginAPI, all its registrations (each can be released), the serial queue of observe events, and
// synchronous calls. The manifest is authoritative (plugins/SPEC.md §2): subscribing to an event, or registering a button/schedule/channel,
// that the manifest does not declare throws; if that happens during loading, the load fails.

import { format } from 'node:util'

import type {
  Account,
  ActionHandler,
  ActionWhere,
  ChannelHandlers,
  ChannelSendInput,
  Disposer,
  EventContext,
  EventType,
  HttpHandler,
  InboxMessage,
  LifecycleContext,
  LifecyclePhase,
  PlatformFetchInit,
  PluginAPI,
  PluginEvent,
  QuestionAnswer,
  ScheduleContext,
  StateEntry,
  StateOptions,
} from '../../sdk/src/index.ts'
import { type Level, runAs } from './output.ts'
import type { Reply } from './protocol.ts'

export interface ConfigRows {
  values: Record<string, unknown>
  secrets: Record<string, string>
}

export interface ConfigPayload extends ConfigRows {
  projects: Record<string, ConfigRows>
}

export interface LoadManifest {
  events: string[]
  permissions: string[]
  failClosed: boolean
  /** Account kinds declared in the manifest's accounts. */
  accountKinds: string[]
  actions: { id: string }[]
  /** manageIdentities: this channel can manage external identities (plugins/SPEC.md §12). */
  channels: { id: string; manageIdentities: boolean }[]
  schedules: { id: string }[]
  http: boolean
}

export interface LoadPayload {
  id: string
  version: string
  root: string
  entry: string
  dataDir: string
  manifest: LoadManifest
  config: ConfigPayload
  token: string
  apiUrl: string
}

export interface RuntimeDeps {
  request(type: string, plugin: string, payload: unknown): Promise<unknown>
  log(plugin: string, level: Level, message: string): void
}

export type CallKind = 'intercept' | 'modify' | 'claim'

type AnyHandler = (event: unknown, ctx: EventContext) => unknown
type LifecycleHandler = (ctx: LifecycleContext) => unknown
type ScheduleHandler = (ctx: ScheduleContext) => unknown

const FIXED_FIELDS = ['id', 'type', 'at', 'projectId', 'agentId', 'taskId', 'runId']
const KEY_MAX = 512
const VALUE_MAX = 1 << 20
const LOG_MAX = 64 << 10
// A host reply line is at most 16 MiB; the HTTP body goes in as base64, so leave headroom.
const HTTP_BODY_MAX = 8 << 20
const HOP_BY_HOP = ['connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te', 'trailer', 'transfer-encoding', 'upgrade']

export class TimeoutError extends Error {
  override name = 'TimeoutError'
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function strings(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((s): s is string => typeof s === 'string') : []
}

function ids(v: unknown): { id: string }[] {
  return Array.isArray(v) ? v.filter((x): x is { id: string } => isObject(x) && typeof x.id === 'string') : []
}

function channelsOf(v: unknown): { id: string; manageIdentities: boolean }[] {
  return ids(v).map((c) => ({ id: c.id, manageIdentities: (c as Record<string, unknown>).manageIdentities === true }))
}

function rows(v: unknown): ConfigRows {
  const r = isObject(v) ? v : {}
  const secrets: Record<string, string> = {}
  if (isObject(r.secrets)) for (const [k, s] of Object.entries(r.secrets)) if (typeof s === 'string') secrets[k] = s
  return { values: isObject(r.values) ? { ...r.values } : {}, secrets }
}

export function parseConfig(v: unknown): ConfigPayload {
  const base = rows(v)
  const projects: Record<string, ConfigRows> = {}
  if (isObject(v) && isObject(v.projects)) for (const [pid, p] of Object.entries(v.projects)) projects[pid] = rows(p)
  return { ...base, projects }
}

/** Checks the load payload; throws when it breaks the rules, and the error text goes straight back to Go. */
export function parseLoad(plugin: string | undefined, payload: unknown): LoadPayload {
  if (!isObject(payload)) throw new Error('load payload must be an object')
  const id = payload.id
  if (typeof id !== 'string' || id === '' || id !== plugin) throw new Error('load payload id must match the message plugin')
  for (const key of ['entry', 'root'] as const) {
    if (typeof payload[key] !== 'string' || payload[key] === '') throw new Error(`load payload is missing ${key}`)
  }
  const m = isObject(payload.manifest) ? payload.manifest : {}
  return {
    id,
    version: typeof payload.version === 'string' ? payload.version : '',
    root: payload.root as string,
    entry: payload.entry as string,
    dataDir: typeof payload.dataDir === 'string' ? payload.dataDir : '',
    manifest: {
      events: strings(m.events),
      permissions: strings(m.permissions),
      failClosed: m.failClosed === true,
      accountKinds: Array.isArray(m.accounts) ? m.accounts.flatMap((a) => (isObject(a) && typeof a.kind === 'string' ? [a.kind] : [])) : [],
      actions: ids(m.actions),
      channels: channelsOf(m.channels),
      schedules: ids(m.schedules),
      http: m.http === true,
    },
    config: parseConfig(payload.config),
    token: typeof payload.token === 'string' ? payload.token : '',
    apiUrl: typeof payload.apiUrl === 'string' ? payload.apiUrl : '',
  }
}

export function describeError(err: unknown): string {
  if (err instanceof Error) return err.message || err.name
  return typeof err === 'string' ? err : format('%o', err)
}


/** Error text for logs: the message plus the first frame in the plugin's own code (or the first frame if none), without host-internal frames. */
export function briefError(err: unknown, root: string): string {
  const message = describeError(err)
  const frames = err instanceof Error && err.stack ? err.stack.split('\n').filter((l) => /^\s+at /.test(l)) : []
  const frame = frames.find((l) => l.includes(root)) ?? frames[0]
  return frame ? `${message}\n${frame.trim()}` : message
}

function isBodyInit(v: unknown): boolean {
  return (
    typeof v === 'string' ||
    v instanceof ArrayBuffer ||
    ArrayBuffer.isView(v) ||
    v instanceof Blob ||
    v instanceof URLSearchParams ||
    v instanceof FormData ||
    v instanceof ReadableStream
  )
}

export class PluginRuntime {
  readonly id: string
  readonly payload: LoadPayload
  config: ConfigPayload
  status: 'loading' | 'active' | 'unloading' | 'unloaded' = 'loading'
  /** Registrations during loading that break the manifest; the load fails when this is not empty. */
  readonly violations: string[] = []
  readonly api: PluginAPI

  private readonly handlers = new Map<string, AnyHandler[]>()
  private readonly lifecycle: Record<LifecyclePhase, LifecycleHandler[]> = { start: [], stop: [] }
  private readonly actions = new Map<string, ActionHandler>()
  private readonly schedules = new Map<string, ScheduleHandler>()
  private readonly channels = new Map<string, ChannelHandlers>()
  private httpHandler: HttpHandler | null = null
  private readonly configListeners = new Set<() => unknown>()
  private readonly disposers: (() => void)[] = []
  private readonly controllers = new Set<AbortController>()
  private readonly lifecycleController = new AbortController()
  private queue: Promise<unknown> = Promise.resolve()
  private readonly deps: RuntimeDeps

  constructor(payload: LoadPayload, deps: RuntimeDeps) {
    this.deps = deps
    this.id = payload.id
    this.payload = payload
    this.config = payload.config
    this.api = this.buildApi()
  }

  /** Values to mask in this plugin's logs: all secrets and the plugin token. */
  secretValues(): string[] {
    const out = new Set<string>()
    const add = (r: ConfigRows) => Object.values(r.secrets).forEach((s) => s && out.add(s))
    add(this.config)
    Object.values(this.config.projects).forEach(add)
    if (this.payload.token) out.add(this.payload.token)
    return [...out]
  }

  hasHandlers(type: string): boolean {
    return (this.handlers.get(type)?.length ?? 0) > 0
  }

  private brief(err: unknown): string {
    return briefError(err, this.payload.root)
  }

  private log(level: Level, message: string): void {
    this.deps.log(this.id, level, message.length > LOG_MAX ? `${message.slice(0, LOG_MAX)}… (truncated)` : message)
  }

  private track(remove: () => void): Disposer {
    let done = false
    const dispose = () => {
      if (done) return
      done = true
      const i = this.disposers.indexOf(dispose)
      if (i >= 0) this.disposers.splice(i, 1)
      remove()
    }
    this.disposers.push(dispose)
    return dispose
  }

  private assertOpen(what: string): void {
    if (this.status === 'unloading' || this.status === 'unloaded') throw new Error(`Plugin is disabled; cannot ${what}`)
  }

  private violate(message: string): never {
    if (this.status === 'loading') this.violations.push(message)
    else this.log('error', message)
    throw new Error(message)
  }

  private register<T>(map: Map<string, T>, id: string, value: T, declared: { id: string }[], what: string, field: string): Disposer {
    this.assertOpen(`register ${what}s`)
    if (typeof id !== 'string' || !declared.some((d) => d.id === id)) {
      this.violate(`Registered ${what} ${String(id)}, which is not declared in the manifest's loopNow.${field}`)
    }
    if (map.has(id)) throw new Error(`Cannot register ${what} ${id}: already registered`)
    map.set(id, value)
    return this.track(() => {
      if (map.get(id) === value) map.delete(id)
    })
  }

  private buildApi(): PluginAPI {
    const p = this.payload
    const m = p.manifest
    const requireState = () => {
      if (!m.permissions.includes('state')) throw new Error('Manifest does not declare the state permission')
      if (this.status === 'unloaded') throw new Error('Plugin is disabled; cannot read or write state')
    }
    const checkKey = (key: unknown, what = 'key') => {
      if (typeof key !== 'string') throw new TypeError(`${what} must be a string`)
      const n = Buffer.byteLength(key)
      if (what === 'key' ? n < 1 || n > KEY_MAX : n > KEY_MAX) throw new RangeError(`${what} must be ${what === 'key' ? 1 : 0} to ${KEY_MAX} bytes`)
    }
    const projectOf = (o?: StateOptions) => o?.projectId ?? null
    const state = (type: string, payload: unknown) => this.deps.request(type, this.id, payload)

    const logger = (level: Level) => (...args: unknown[]) => this.log(level, format(...args))
    const platformFetch = async (path: string, init: PlatformFetchInit = {}) => {
      if (typeof path !== 'string' || !path.startsWith('/')) throw new TypeError('path must start with /')
      if (!p.apiUrl) throw new Error('Platform did not provide an API URL')
      const headers = new Headers(init.headers)
      headers.set('authorization', `Bearer ${p.token}`)
      let body = init.body
      if (body !== undefined && body !== null && !isBodyInit(body)) {
        body = JSON.stringify(body)
        if (!headers.has('content-type')) headers.set('content-type', 'application/json')
      }
      return fetch(p.apiUrl.replace(/\/+$/, '') + path, { ...init, headers, body: body as RequestInit['body'] })
    }
    // Helper for channels and managed identities: calls the inbox API and, on a non-2xx response, throws the platform's error message
    // (with status and code).
    const platformJson = async (method: 'GET' | 'POST', path: string, body?: unknown): Promise<InboxMessage> => {
      const res = await platformFetch(path, method === 'GET' ? { method } : { method, body: body as Record<string, unknown> })
      const text = await res.text()
      let data: unknown = null
      try {
        data = text ? JSON.parse(text) : null
      } catch {
        // A non-JSON error body goes into the message as is.
      }
      if (!res.ok) {
        const e = isObject(data) && isObject(data.error) ? data.error : null
        const err = new Error(`Platform returned ${res.status}: ${e && typeof e.message === 'string' ? e.message : text.slice(0, 200)}`)
        Object.assign(err, { status: res.status, code: e?.code ?? null, details: e?.details ?? null })
        throw err
      }
      return data as InboxMessage
    }
    const inboxCall = async (permission: string, path: string, body: unknown): Promise<InboxMessage> => {
      if (!m.permissions.includes(permission)) throw new Error(`Manifest does not declare the ${permission} permission`)
      return platformJson('POST', path, body)
    }
    // Body for answering a question: an option (optionally with text), or text only.
    const answerBody = (input: QuestionAnswer): Record<string, unknown> => {
      const text = isObject(input) && typeof input.text === 'string' && input.text !== '' ? input.text : undefined
      if (isObject(input) && input.text !== undefined && text === undefined) throw new TypeError('text must be a non-empty string')
      if (isObject(input) && typeof input.optionId === 'string' && input.optionId !== '') return text === undefined ? { optionId: input.optionId } : { optionId: input.optionId, text }
      if (isObject(input) && input.optionId === undefined && text !== undefined) return { text }
      throw new TypeError('optionId or text is required')
    }
    const requireChannel = (channelId: unknown) => {
      if (!m.channels.some((c) => c.id === channelId)) throw new Error(`Channel ${String(channelId)} is not declared in the manifest's loopNow.channels`)
    }
    const requireManage = (identityId: unknown, id: unknown, what: string) => {
      if (!m.permissions.includes('identities.manage')) throw new Error('Manifest does not declare the identities.manage permission')
      if (!m.channels.some((c) => c.manageIdentities)) throw new Error("No channel in the manifest's loopNow.channels declares manageIdentities")
      if (typeof identityId !== 'string' || identityId === '') throw new TypeError('identityId is required')
      if (typeof id !== 'string' || id === '') throw new TypeError(`${what} is required`)
    }

    return Object.freeze({
      id: p.id,
      version: p.version,
      root: p.root,
      dataDir: p.dataDir,
      on: <E extends EventType>(type: E, handler: (event: PluginEvent<E>, ctx: EventContext) => unknown) => {
        this.assertOpen('subscribe to events')
        if (typeof handler !== 'function') throw new TypeError('handler must be a function')
        if (typeof type !== 'string' || !m.events.includes(type)) this.violate(`Subscribed to ${String(type)}, which is not declared in the manifest's loopNow.events`)
        const list = this.handlers.get(type) ?? []
        const h = handler as AnyHandler
        list.push(h)
        this.handlers.set(type, list)
        return this.track(() => {
          const i = list.indexOf(h)
          if (i >= 0) list.splice(i, 1)
        })
      },
      lifecycle: Object.freeze({
        on: (phase: LifecyclePhase, handler: LifecycleHandler) => {
          this.assertOpen('register lifecycle handlers')
          if (phase !== 'start' && phase !== 'stop') throw new Error(`Lifecycle phase must be start or stop, not ${String(phase)}`)
          if (typeof handler !== 'function') throw new TypeError('handler must be a function')
          const list = this.lifecycle[phase]
          list.push(handler)
          return this.track(() => {
            const i = list.indexOf(handler)
            if (i >= 0) list.splice(i, 1)
          })
        },
      }),
      state: Object.freeze({
        get: async <T>(key: string, o?: StateOptions) => {
          requireState()
          checkKey(key)
          return ((await state('state_get', { projectId: projectOf(o), key })) ?? null) as T | null
        },
        set: async (key: string, value: unknown, o?: StateOptions) => {
          requireState()
          checkKey(key)
          const json = JSON.stringify(value)
          if (json === undefined) throw new TypeError('value must be serializable to JSON')
          if (Buffer.byteLength(json) > VALUE_MAX) throw new RangeError(`value must not exceed ${VALUE_MAX} bytes`)
          await state('state_set', { projectId: projectOf(o), key, value: JSON.parse(json) })
        },
        delete: async (key: string, o?: StateOptions) => {
          requireState()
          checkKey(key)
          await state('state_delete', { projectId: projectOf(o), key })
        },
        list: async <T>(prefix: string, o?: StateOptions) => {
          requireState()
          checkKey(prefix, 'prefix')
          const v = await state('state_list', { projectId: projectOf(o), prefix })
          return (Array.isArray(v) ? v : []) as StateEntry<T>[]
        },
      }),
      config: Object.freeze({
        get: <T extends object>(projectId?: string | null) => {
          const merged = { ...this.config.values }
          if (projectId) Object.assign(merged, this.config.projects[projectId]?.values)
          return structuredClone(merged) as T
        },
        projects: () => Object.keys(this.config.projects).sort(),
        onChange: (listener: () => unknown) => {
          this.assertOpen('register config listeners')
          if (typeof listener !== 'function') throw new TypeError('listener must be a function')
          this.configListeners.add(listener)
          return this.track(() => this.configListeners.delete(listener))
        },
      }),
      secrets: Object.freeze({
        get: (name: string, projectId?: string | null) => {
          const fromProject = projectId ? this.config.projects[projectId]?.secrets[name] : undefined
          return fromProject || this.config.secrets[name] || undefined
        },
      }),
      accounts: Object.freeze({
        list: async (projectId: string, kind: string) => {
          if (!m.permissions.includes('accounts.read')) throw new Error('Manifest does not declare the accounts.read permission')
          if (typeof kind !== 'string' || !m.accountKinds.includes(kind)) throw new Error(`Manifest does not declare account kind ${String(kind)}`)
          if (typeof projectId !== 'string' || projectId === '') throw new Error('projectId is invalid')
          const v = await this.deps.request('accounts_list', this.id, { projectId, kind })
          return (Array.isArray(v) ? v : []) as Account[]
        },
      }),
      platform: Object.freeze({ apiUrl: p.apiUrl, fetch: platformFetch }),
      log: Object.freeze({ debug: logger('debug'), info: logger('info'), warn: logger('warn'), error: logger('error') }),
      actions: Object.freeze({
        register: (id: string, handler: ActionHandler) => {
          if (typeof handler !== 'function') throw new TypeError('handler must be a function')
          return this.register(this.actions, id, handler, m.actions, 'action', 'actions')
        },
      }),
      schedule: Object.freeze({
        on: (id: string, handler: ScheduleHandler) => {
          if (typeof handler !== 'function') throw new TypeError('handler must be a function')
          return this.register(this.schedules, id, handler, m.schedules, 'scheduled job', 'schedule')
        },
      }),
      channels: Object.freeze({
        register: (id: string, handlers: ChannelHandlers) => {
          if (!isObject(handlers)) throw new TypeError('handlers must be an object')
          for (const k of ['outbound', 'identityInbound'] as const) {
            if (handlers[k] !== undefined && typeof handlers[k] !== 'function') throw new TypeError(`${k} must be a function`)
          }
          if (handlers.identityInbound !== undefined) {
            this.assertOpen('register channels')
            if (m.channels.some((c) => c.id === id) && !m.channels.some((c) => c.id === id && c.manageIdentities)) {
              this.violate(`Cannot register identityInbound: channel ${id} does not declare manageIdentities in the manifest`)
            }
            if (!m.permissions.includes('identities.manage')) this.violate('Registering identityInbound requires the identities.manage permission in the manifest')
          }
          return this.register(this.channels, id, handlers, m.channels, 'channel', 'channels')
        },
        send: async (channelId: string, input: ChannelSendInput) => {
          requireChannel(channelId)
          if (!isObject(input) || typeof input.to !== 'string' || typeof input.text !== 'string') throw new TypeError('to and text are required')
          const body: Record<string, unknown> = { to: input.to, text: input.text, channelId }
          if (input.senderName !== undefined) body.senderName = input.senderName
          if (input.whenBusy !== undefined) body.whenBusy = input.whenBusy
          return inboxCall('inbox.send', '/inbox/messages', body)
        },
        reply: async (messageId: string, input: { text: string }) => {
          if (typeof messageId !== 'string' || messageId === '') throw new TypeError('messageId is required')
          if (!isObject(input) || typeof input.text !== 'string') throw new TypeError('text is required')
          return inboxCall('inbox.send', `/inbox/messages/${encodeURIComponent(messageId)}/reply`, { text: input.text })
        },
        read: async (messageId: string) => {
          if (typeof messageId !== 'string' || messageId === '') throw new TypeError('messageId is required')
          return inboxCall('inbox.read', `/inbox/messages/${encodeURIComponent(messageId)}/read`, {})
        },
        answer: async (channelId: string, questionId: string, input: QuestionAnswer) => {
          requireChannel(channelId)
          if (typeof questionId !== 'string' || questionId === '') throw new TypeError('questionId is required')
          const body = answerBody(input)
          return inboxCall('inbox.send', `/inbox/messages/${encodeURIComponent(questionId)}/reply`, body)
        },
      }),
      identities: Object.freeze({
        read: async (identityId: string, messageId: string) => {
          requireManage(identityId, messageId, 'messageId')
          return platformJson('GET', `/external-identities/${encodeURIComponent(identityId)}/inbox/${encodeURIComponent(messageId)}`)
        },
        reply: async (identityId: string, messageId: string, input: { text: string }) => {
          requireManage(identityId, messageId, 'messageId')
          if (!isObject(input) || typeof input.text !== 'string' || input.text === '') throw new TypeError('text is required')
          return platformJson('POST', `/inbox/messages/${encodeURIComponent(messageId)}/reply`, { text: input.text, asIdentityId: identityId })
        },
        answer: async (identityId: string, questionId: string, input: QuestionAnswer) => {
          requireManage(identityId, questionId, 'questionId')
          const body = { ...answerBody(input), asIdentityId: identityId }
          return platformJson('POST', `/inbox/messages/${encodeURIComponent(questionId)}/reply`, body)
        },
      }),
      http: Object.freeze({
        handle: (handler: HttpHandler) => {
          if (typeof handler !== 'function') throw new TypeError('handler must be a function')
          this.assertOpen('register an HTTP handler')
          if (!m.http) this.violate("Cannot register an HTTP handler: the manifest's loopNow.http is not true")
          if (this.httpHandler !== null) throw new Error('Cannot register an HTTP handler: one is already registered')
          this.httpHandler = handler
          return this.track(() => {
            if (this.httpHandler === handler) this.httpHandler = null
          })
        },
      }),
    } satisfies PluginAPI) as PluginAPI
  }

  /** Runs fn in the plugin context; on timeoutMs or plugin disable, fires the signal and stops waiting; fn's later result is discarded. */
  private async withDeadline<T>(timeoutMs: number, what: string, fn: (signal: AbortSignal) => Promise<T>): Promise<T> {
    const controller = new AbortController()
    this.controllers.add(controller)
    const timer =
      timeoutMs > 0 ? setTimeout(() => controller.abort(new TimeoutError(`${what} timed out after ${timeoutMs} ms`)), timeoutMs) : null
    try {
      const work = runAs(this.id, () => fn(controller.signal))
      work.catch(() => undefined)
      const aborted = new Promise<never>((_, reject) => {
        if (controller.signal.aborted) reject(controller.signal.reason)
        controller.signal.addEventListener('abort', () => reject(controller.signal.reason), { once: true })
      })
      aborted.catch(() => undefined)
      return await Promise.race([work, aborted])
    } finally {
      if (timer) clearTimeout(timer)
      this.controllers.delete(controller)
    }
  }

  private context(event: Record<string, unknown>, attempt: number, signal: AbortSignal): EventContext {
    const str = (k: string) => (typeof event[k] === 'string' ? (event[k] as string) : null)
    return Object.freeze({
      eventId: String(event.id),
      type: event.type as EventType,
      projectId: str('projectId'),
      agentId: str('agentId'),
      taskId: str('taskId'),
      runId: str('runId'),
      attempt,
      signal,
    })
  }

  /** Observe events: serial per plugin; the next one starts only after this one is handled (or times out). */
  event(event: Record<string, unknown>, attempt: number, timeoutMs: number): Promise<Reply> {
    const run = this.queue.then(() => this.runEvent(event, attempt, timeoutMs))
    this.queue = run.catch(() => undefined)
    return run
  }

  private async runEvent(event: Record<string, unknown>, attempt: number, timeoutMs: number): Promise<Reply> {
    if (this.status !== 'active') return { ok: false, error: '插件未加载' }
    const type = String(event.type)
    const list = [...(this.handlers.get(type) ?? [])]
    // Events delivered per channel: channel.outbound goes to the channel's outbound, identity.inbound to identityInbound.
    // With no handler at all, reply with a failure so the platform redelivers and shows it on the plugin page, instead of silently dropping it.
    const routed = type === 'channel.outbound' ? 'outbound' : type === 'identity.inbound' ? 'identityInbound' : null
    if (routed) {
      const handler = typeof event.channelId === 'string' ? this.channels.get(event.channelId)?.[routed] : undefined
      if (handler) list.push(handler as AnyHandler)
      else if (list.length === 0) return { ok: false, error: `No channel handles ${type} (channelId ${String(event.channelId)})` }
    }
    if (list.length === 0) return { ok: true }
    try {
      await this.withDeadline(timeoutMs, `Handling ${type}`, async (signal) => {
        const ctx = this.context(event, attempt, signal)
        const failures: string[] = []
        for (const handler of list) {
          if (signal.aborted) break
          try {
            await handler(structuredClone(event), ctx)
          } catch (err) {
            if (signal.aborted) throw signal.reason
            this.log('warn', `Handler for ${type} failed: ${this.brief(err)}`)
            failures.push(describeError(err))
          }
        }
        if (failures.length > 0) throw new Error(failures.join('; '))
      })
      return { ok: true }
    } catch (err) {
      return { ok: false, error: describeError(err) }
    }
  }

  /** Synchronous calls (intercept, modify, claim): runs this plugin's handlers in registration order, combined into one result. */
  async call(kind: CallKind, event: Record<string, unknown>, timeoutMs: number): Promise<Reply> {
    if (this.status !== 'active') return { ok: false, error: '插件未加载' }
    const type = String(event.type)
    const list = [...(this.handlers.get(type) ?? [])]
    try {
      const value = await this.withDeadline(timeoutMs, `Handling ${type}`, async (signal) => {
        let current = structuredClone(event)
        const ctx = this.context(event, 1, signal)
        switch (kind) {
          case 'intercept':
            for (const handler of list) {
              const r = await handler(current, ctx)
              if (isObject(r) && r.block === true) {
                return { block: true, reason: typeof r.reason === 'string' && r.reason !== '' ? r.reason : '没有说明原因' }
              }
            }
            return {}
          case 'modify': {
            const patch: Record<string, unknown> = {}
            for (const handler of list) {
              const r = await handler(current, ctx)
              if (!isObject(r) || !isObject(r.patch)) continue
              for (const [k, v] of Object.entries(r.patch)) {
                if (FIXED_FIELDS.includes(k)) {
                  this.log('warn', `Ignored a ${type} patch that changes the fixed field ${k}`)
                  continue
                }
                patch[k] = v
              }
              current = { ...current, ...patch }
            }
            return Object.keys(patch).length > 0 ? { patch } : {}
          }
          case 'claim':
            for (const handler of list) {
              const r = await handler(current, ctx)
              if (isObject(r) && r.handled === true) return r
            }
            return { handled: false }
          default:
            throw new Error(`Unknown call kind ${String(kind)}`)
        }
      })
      return { ok: true, value }
    } catch (err) {
      if (!(err instanceof TimeoutError)) this.log('warn', `Handler for ${type} failed: ${this.brief(err)}`)
      return { ok: false, error: describeError(err) }
    }
  }

  /** Console action buttons (action): returns {notice, url?}. */
  async action(payload: Record<string, unknown>, timeoutMs: number): Promise<Reply> {
    if (this.status !== 'active') return { ok: false, error: '插件未加载' }
    const actionId = String(payload.actionId)
    const handler = this.actions.get(actionId)
    if (!handler) return { ok: false, error: `Plugin has not registered action ${actionId}` }
    const where = (isObject(payload.where) ? payload.where : {}) as ActionWhere
    const input = isObject(payload.input) ? payload.input : {}
    try {
      const r = await this.withDeadline(timeoutMs, `Action ${actionId}`, async (signal) => handler(structuredClone(input), Object.freeze({ actionId, where: { ...where }, signal })))
      if (!isObject(r) || typeof r.notice !== 'string') throw new Error('Action must return { notice, url? }')
      if (r.url !== undefined && typeof r.url !== 'string') throw new Error('url returned by the action must be a string')
      return { ok: true, value: r.url === undefined ? { notice: r.notice } : { notice: r.notice, url: r.url } }
    } catch (err) {
      if (!(err instanceof TimeoutError)) this.log('warn', `Action ${actionId} failed: ${this.brief(err)}`)
      return { ok: false, error: describeError(err) }
    }
  }

  /** HTTP entry (http): passes the request to the api.http.handle handler and returns its status, headers and body (base64) as is. */
  async http(payload: Record<string, unknown>, timeoutMs: number): Promise<Reply> {
    if (this.status !== 'active') return { ok: false, error: '插件未加载' }
    const handler = this.httpHandler
    if (!handler) return { ok: false, error: 'Plugin has not registered an HTTP handler' }
    const method = typeof payload.method === 'string' ? payload.method.toUpperCase() : 'GET'
    const path = typeof payload.path === 'string' && payload.path.startsWith('/') ? payload.path : '/'
    const query = typeof payload.query === 'string' && payload.query !== '' ? `?${payload.query}` : ''
    const headers = new Headers()
    if (isObject(payload.headers)) {
      for (const [name, values] of Object.entries(payload.headers)) {
        for (const v of Array.isArray(values) ? values : [values]) if (typeof v === 'string') headers.append(name, v)
      }
    }
    const raw = typeof payload.body === 'string' && payload.body !== '' ? Buffer.from(payload.body, 'base64') : null
    const via = payload.via === 'external' ? 'external' : 'console'
    try {
      const request = new Request(new URL(path + query, 'http://plugin.local'), {
        method,
        headers,
        body: raw && method !== 'GET' && method !== 'HEAD' ? raw : undefined,
      })
      const value = await this.withDeadline(timeoutMs, `HTTP ${method} ${path}`, async (signal) => {
        const res = await handler(request, Object.freeze({ via, signal }))
        if (!(res instanceof Response)) throw new Error('HTTP handler must return a Response')
        const body = Buffer.from(await res.arrayBuffer())
        if (body.length > HTTP_BODY_MAX) throw new Error(`HTTP response body must not exceed ${HTTP_BODY_MAX} bytes`)
        const out: Record<string, string[]> = {}
        res.headers.forEach((v, k) => {
          if (k === 'set-cookie' || HOP_BY_HOP.includes(k)) return
          ;(out[k] ??= []).push(v)
        })
        const cookies = res.headers.getSetCookie()
        if (cookies.length > 0) out['set-cookie'] = cookies
        return { status: res.status, headers: out, body: body.toString('base64') }
      })
      return { ok: true, value }
    } catch (err) {
      if (!(err instanceof TimeoutError)) this.log('warn', `HTTP ${method} ${path} failed: ${this.brief(err)}`)
      return { ok: false, error: describeError(err) }
    }
  }

  /** Scheduled jobs (schedule): replies ok once handled. */
  async schedule(payload: Record<string, unknown>, timeoutMs: number): Promise<Reply> {
    if (this.status !== 'active') return { ok: false, error: '插件未加载' }
    const scheduleId = String(payload.scheduleId)
    const handler = this.schedules.get(scheduleId)
    if (!handler) return { ok: false, error: `Plugin has not registered scheduled job ${scheduleId}` }
    const cron = typeof payload.cron === 'string' ? payload.cron : ''
    const at = typeof payload.at === 'string' ? payload.at : new Date().toISOString()
    try {
      await this.withDeadline(timeoutMs, `Scheduled job ${scheduleId}`, async (signal) => handler(Object.freeze({ scheduleId, cron, at, signal })))
      return { ok: true }
    } catch (err) {
      if (!(err instanceof TimeoutError)) this.log('warn', `Scheduled job ${scheduleId} failed: ${this.brief(err)}`)
      return { ok: false, error: describeError(err) }
    }
  }

  /** Runs the start handlers after register (in registration order). */
  async start(): Promise<void> {
    for (const handler of [...this.lifecycle.start]) {
      await runAs(this.id, async () => handler({ signal: this.lifecycleController.signal }))
    }
  }

  async configChanged(config: ConfigPayload, timeoutMs: number): Promise<void> {
    this.config = config
    for (const listener of [...this.configListeners]) {
      try {
        await this.withDeadline(timeoutMs, 'Config listener', async () => listener())
      } catch (err) {
        this.log('warn', `Config listener failed: ${this.brief(err)}`)
      }
    }
  }

  /** Disable: fires all in-flight signals, runs stop in reverse registration order, then releases every registration. Can be called again. */
  async stop(timeoutMs: number): Promise<void> {
    if (this.status === 'unloading' || this.status === 'unloaded') return
    this.status = 'unloading'
    const reason = new Error('Plugin is disabled')
    for (const c of this.controllers) c.abort(reason)
    this.lifecycleController.abort(reason)
    const deadline = Date.now() + timeoutMs
    for (const handler of [...this.lifecycle.stop].reverse()) {
      const left = deadline - Date.now()
      if (left <= 0) {
        this.log('warn', 'stop handler timed out; not waiting for the remaining ones')
        break
      }
      try {
        await this.withDeadline(left, 'stop', async (signal) => handler({ signal }))
      } catch (err) {
        this.log('warn', `stop handler failed: ${this.brief(err)}`)
      }
    }
    for (const dispose of [...this.disposers].reverse()) {
      try {
        dispose()
      } catch (err) {
        this.log('warn', `Failed to release a registration: ${this.brief(err)}`)
      }
    }
    this.handlers.clear()
    this.configListeners.clear()
    this.status = 'unloaded'
  }

  /** For validation: what the plugin registered. */
  registrations(): { events: string[]; actions: string[]; schedules: string[]; channels: string[]; http: boolean } {
    return {
      events: [...this.handlers.entries()].filter(([, l]) => l.length > 0).map(([t]) => t),
      actions: [...this.actions.keys()],
      schedules: [...this.schedules.keys()],
      channels: [...this.channels.keys()],
      http: this.httpHandler !== null,
    }
  }
}
