// 外发 HTTP 投递库（plugins/SPEC.md §3、§7）：队列存在插件状态里，宿主或服务端重启后继续；失败按退避重试；
// 每次投递写一条 deliveries/<ULID> 记录（§7 的形状，插件页按这个前缀读出），只保留最近的若干条。
//
// 状态：
// - pending：等待发送（新建或人工重发后），nextAttemptAt 为预定时间；
// - delivered：收到 2xx；
// - failed：最后一次失败。nextAttemptAt 不为 null 时会在那时重试；为 null 时是不可重试的失败（4xx、重定向、签名密钥缺失），不再重试；
// - abandoned：重试次数用尽，放弃。
// 终态（delivered、不再重试的 failed、abandoned）可用 resend 人工重发。
//
// 请求本身（目标、头、正文、签名方式）另存在 delivery-requests/<ULID>，与记录一起保留、一起删除；签名密钥不落盘，
// 每次尝试时按字段名从插件配置的 secret 读取，所以改了密钥后重试用新密钥。

import { DELIVERY_HEADER, SIGNATURE_HEADER, signBody, TIMESTAMP_HEADER } from './signature.ts'
import type { PluginAPI } from './types.ts'
import { ulid } from './ulid.ts'

export type DeliveryStatus = 'pending' | 'delivered' | 'failed' | 'abandoned'

/** 状态键 deliveries/<id> 的值，形状由 plugins/SPEC.md §7 规定。 */
export interface DeliveryRecord {
  id: string
  at: string
  eventId: string | null
  eventType: string | null
  target: string
  status: DeliveryStatus
  attempts: number
  nextAttemptAt: string | null
  lastStatusCode: number | null
  lastError: string | null
  updatedAt: string
}

export interface DeliverySigning {
  /** 插件配置里 x-secret 字段的名称；每次尝试时用 api.secrets.get(secretField, projectId) 取值。 */
  secretField: string
  projectId?: string | null
}

export interface DeliveryRequest {
  /** http 或 https 地址。 */
  target: string
  /** 默认 POST。 */
  method?: string
  headers?: Record<string, string>
  /** 字符串原样发送；其他值按 JSON 发送并默认加 content-type: application/json。 */
  body?: unknown
  /** 设置后每次尝试加 x-loop-now-timestamp 与 x-loop-now-signature 头（见 signBody）。 */
  signing?: DeliverySigning
  /** 触发这次投递的事件；同一事件对同一目标只投递一次（观察类事件可能重复送达）。 */
  eventId?: string | null
  eventType?: string | null
  /** 这次投递最多尝试几次（含第一次），默认取 createDelivery 的 maxAttempts。 */
  maxAttempts?: number
}

export interface DeliveryOptions {
  /** 最多尝试几次（含第一次），默认 5。 */
  maxAttempts?: number
  /** 第 n 次失败后等多久重试；次数超过列表长度时用最后一项。默认 5 秒、30 秒、2 分钟、10 分钟、30 分钟、1 小时。 */
  backoffMs?: number[]
  /** 每次尝试的超时，默认 15 秒。 */
  timeoutMs?: number
  /** 保留最近多少条记录，默认 500；更早的终态记录连同请求一起删除。 */
  keep?: number
  /** 同时进行的尝试数，默认 4。 */
  concurrency?: number
  /** 默认 loop-now-plugin-<id>/<version>。 */
  userAgent?: string
  /** 测试用：替换 fetch。 */
  fetch?: typeof fetch
}

export interface Delivery {
  /** 保存这次投递并安排第一次尝试，立即返回记录（不等 HTTP 结果）。 */
  deliver(request: DeliveryRequest): Promise<DeliveryRecord>
  /** 人工重发一条终态记录；已在等待或重试中的不变。 */
  resend(id: string): Promise<DeliveryRecord>
  get(id: string): Promise<DeliveryRecord | null>
  /** 新的在前。 */
  list(): Promise<DeliveryRecord[]>
}

export const RECORD_PREFIX = 'deliveries/'
export const REQUEST_PREFIX = 'delivery-requests/'
export const DEFAULT_BACKOFF_MS = [5_000, 30_000, 120_000, 600_000, 1_800_000, 3_600_000]

const MAX_BODY_BYTES = 900 * 1024
const ERROR_SNIPPET = 200

interface StoredRequest {
  target: string
  method: string
  headers: Record<string, string>
  body: string | null
  signing: DeliverySigning | null
  maxAttempts: number
  /** 人工重发时的已尝试次数；本轮的次数 = attempts - round。 */
  round: number
}

type Outcome =
  | { kind: 'delivered'; code: number }
  | { kind: 'retry'; code: number | null; error: string }
  | { kind: 'fail'; code: number | null; error: string }

export function createDelivery(api: PluginAPI, options: DeliveryOptions = {}): Delivery {
  return new DeliveryQueue(api, options)
}

function iso(ms: number): string {
  return new Date(ms).toISOString()
}

function isTerminal(r: DeliveryRecord): boolean {
  return r.status === 'delivered' || r.status === 'abandoned' || (r.status === 'failed' && r.nextAttemptAt === null)
}

function isRecord(v: unknown): v is DeliveryRecord {
  if (typeof v !== 'object' || v === null) return false
  const r = v as Record<string, unknown>
  return typeof r.id === 'string' && typeof r.target === 'string' && typeof r.status === 'string' && typeof r.attempts === 'number'
}

function retryable(status: number): boolean {
  return status === 408 || status === 425 || status === 429 || status >= 500
}

function positive(n: number | undefined, fallback: number): number {
  return typeof n === 'number' && Number.isFinite(n) && n > 0 ? n : fallback
}

class DeliveryQueue implements Delivery {
  private readonly records = new Map<string, DeliveryRecord>()
  private readonly running = new Set<string>()
  private readonly stopController = new AbortController()
  private readonly maxAttempts: number
  private readonly backoff: number[]
  private readonly timeoutMs: number
  private readonly keep: number
  private readonly concurrency: number
  private readonly userAgent: string
  private readonly fetchImpl: typeof fetch
  private ready: Promise<void> | null = null
  private timer: ReturnType<typeof setTimeout> | null = null
  private chain: Promise<unknown> = Promise.resolve()
  private stopped = false
  private readonly api: PluginAPI

  constructor(api: PluginAPI, options: DeliveryOptions) {
    this.api = api
    this.maxAttempts = Math.floor(positive(options.maxAttempts, 5))
    this.backoff = options.backoffMs?.length ? options.backoffMs : DEFAULT_BACKOFF_MS
    this.timeoutMs = positive(options.timeoutMs, 15_000)
    this.keep = Math.floor(positive(options.keep, 500))
    this.concurrency = Math.floor(positive(options.concurrency, 4))
    this.userAgent = options.userAgent ?? `loop-now-plugin-${api.id}/${api.version}`
    this.fetchImpl = options.fetch ?? fetch
    api.lifecycle.on('start', () => this.ensureReady())
    api.lifecycle.on('stop', () => this.stop())
  }

  private ensureReady(): Promise<void> {
    this.ready ??= (async () => {
      const entries = await this.api.state.list(RECORD_PREFIX)
      for (const { value } of entries) {
        if (isRecord(value)) this.records.set(value.id, value)
      }
      this.schedule()
    })()
    return this.ready
  }

  private stop(): void {
    this.stopped = true
    this.stopController.abort(new Error('插件已停止'))
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
  }

  // serialize 让新建、重发与清理逐个进行，同一事件不会被并发的 deliver 建成两条。
  private serialize<T>(fn: () => Promise<T>): Promise<T> {
    const next = this.chain.then(fn, fn)
    this.chain = next.catch(() => undefined)
    return next
  }

  async deliver(request: DeliveryRequest): Promise<DeliveryRecord> {
    if (this.stopped) throw new Error('插件已停止，不能再投递')
    const stored = this.normalize(request)
    await this.ensureReady()
    return this.serialize(async () => {
      const eventId = request.eventId ?? null
      if (eventId !== null) {
        for (const r of this.records.values()) {
          if (r.eventId === eventId && r.target === stored.target) return { ...r }
        }
      }
      const now = Date.now()
      const id = ulid(now)
      const record: DeliveryRecord = {
        id,
        at: iso(now),
        eventId,
        eventType: request.eventType ?? null,
        target: stored.target,
        status: 'pending',
        attempts: 0,
        nextAttemptAt: iso(now),
        lastStatusCode: null,
        lastError: null,
        updatedAt: iso(now),
      }
      // 先写记录再写请求：中间退出时记录找不到请求，尝试时标为放弃，不会留下没人引用的请求。
      await this.api.state.set(RECORD_PREFIX + id, record)
      await this.api.state.set(REQUEST_PREFIX + id, stored)
      this.records.set(id, record)
      await this.prune()
      this.schedule()
      return { ...record }
    })
  }

  async resend(id: string): Promise<DeliveryRecord> {
    await this.ensureReady()
    return this.serialize(async () => {
      const record = this.records.get(id)
      if (!record) throw new Error(`没有投递记录 ${id}`)
      if (!isTerminal(record) || this.running.has(id)) return { ...record }
      const stored = await this.api.state.get<StoredRequest>(REQUEST_PREFIX + id)
      if (!stored) throw new Error(`投递 ${id} 的请求内容已删除，无法重发`)
      await this.api.state.set(REQUEST_PREFIX + id, { ...stored, round: record.attempts })
      const now = Date.now()
      const next: DeliveryRecord = { ...record, status: 'pending', nextAttemptAt: iso(now), updatedAt: iso(now) }
      await this.api.state.set(RECORD_PREFIX + id, next)
      this.records.set(id, next)
      this.schedule()
      return { ...next }
    })
  }

  async get(id: string): Promise<DeliveryRecord | null> {
    await this.ensureReady()
    const r = this.records.get(id)
    return r ? { ...r } : null
  }

  async list(): Promise<DeliveryRecord[]> {
    await this.ensureReady()
    return [...this.records.values()].sort((a, b) => (a.id < b.id ? 1 : -1)).map((r) => ({ ...r }))
  }

  private normalize(request: DeliveryRequest): StoredRequest {
    let url: URL
    try {
      url = new URL(request.target)
    } catch {
      throw new Error(`投递目标不是合法的地址：${request.target}`)
    }
    if (url.protocol !== 'http:' && url.protocol !== 'https:') throw new Error('投递目标须是 http 或 https 地址')
    const method = (request.method ?? 'POST').toUpperCase()
    if (!/^[A-Z]+$/.test(method)) throw new Error(`HTTP 方法无效：${request.method}`)
    const headers: Record<string, string> = {}
    for (const [k, v] of Object.entries(request.headers ?? {})) {
      if (typeof v !== 'string') throw new Error(`请求头 ${k} 的值须是字符串`)
      headers[k.toLowerCase()] = v
    }
    let body: string | null = null
    if (request.body !== undefined && request.body !== null) {
      if (typeof request.body === 'string') {
        body = request.body
      } else {
        body = JSON.stringify(request.body)
        headers['content-type'] ??= 'application/json'
      }
      if (Buffer.byteLength(body) > MAX_BODY_BYTES) throw new Error(`正文不能超过 ${MAX_BODY_BYTES} 字节`)
    }
    const signing = request.signing ? { secretField: request.signing.secretField, projectId: request.signing.projectId ?? null } : null
    if (signing && !signing.secretField) throw new Error('signing.secretField 不能为空')
    return {
      target: url.toString(),
      method,
      headers,
      body,
      signing,
      maxAttempts: Math.floor(positive(request.maxAttempts, this.maxAttempts)),
      round: 0,
    }
  }

  private schedule(): void {
    if (this.stopped || this.ready === null) return
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
    const now = Date.now()
    const waiting = [...this.records.values()]
      .filter((r) => !isTerminal(r) && r.nextAttemptAt !== null && !this.running.has(r.id))
      .sort((a, b) => Date.parse(a.nextAttemptAt!) - Date.parse(b.nextAttemptAt!) || (a.id < b.id ? -1 : 1))
    let soonest: number | null = null
    for (const r of waiting) {
      const due = Date.parse(r.nextAttemptAt!)
      if (due <= now && this.running.size < this.concurrency) {
        this.running.add(r.id)
        void this.attempt(r.id).finally(() => {
          this.running.delete(r.id)
          this.schedule()
        })
      } else if (due > now) {
        soonest = soonest === null ? due : Math.min(soonest, due)
      }
    }
    if (soonest !== null) {
      this.timer = setTimeout(() => this.schedule(), Math.min(soonest - now, 2 ** 31 - 1))
      this.timer.unref?.()
    }
  }

  private async attempt(id: string): Promise<void> {
    const record = this.records.get(id)
    if (!record || isTerminal(record)) return
    let stored: StoredRequest | null
    try {
      stored = await this.api.state.get<StoredRequest>(REQUEST_PREFIX + id)
    } catch (err) {
      if (!this.stopped) this.api.log.warn(`读取投递 ${id} 的请求失败：${(err as Error).message}`)
      return
    }
    if (this.stopped) return
    if (!stored) {
      await this.finish(record, { kind: 'fail', code: null, error: '请求内容已丢失' }, null, true)
      return
    }
    const outcome = await this.send(id, stored)
    if (outcome === null) return // 插件停止，留到下次加载
    await this.finish(record, outcome, stored, false)
  }

  private async send(id: string, stored: StoredRequest): Promise<Outcome | null> {
    const headers: Record<string, string> = { 'user-agent': this.userAgent, ...stored.headers, [DELIVERY_HEADER]: id }
    if (stored.signing) {
      const secret = this.api.secrets.get(stored.signing.secretField, stored.signing.projectId)
      if (!secret) return { kind: 'fail', code: null, error: `签名密钥 ${stored.signing.secretField} 没有设置` }
      const ts = Math.floor(Date.now() / 1000)
      headers[TIMESTAMP_HEADER] = String(ts)
      headers[SIGNATURE_HEADER] = signBody(secret, ts, stored.body ?? '')
    }
    const timeout = AbortSignal.timeout(this.timeoutMs)
    try {
      const res = await this.fetchImpl(stored.target, {
        method: stored.method,
        headers,
        body: stored.method === 'GET' || stored.method === 'HEAD' ? undefined : (stored.body ?? undefined),
        redirect: 'manual',
        signal: AbortSignal.any([timeout, this.stopController.signal]),
      })
      const text = await res.text().catch(() => '')
      if (res.status >= 200 && res.status < 300) return { kind: 'delivered', code: res.status }
      const snippet = text.trim().slice(0, ERROR_SNIPPET)
      const error = `HTTP ${res.status}${snippet ? `：${snippet}` : ''}`
      if (res.status >= 300 && res.status < 400) return { kind: 'fail', code: res.status, error: `${error}（不跟随重定向）` }
      return retryable(res.status) ? { kind: 'retry', code: res.status, error } : { kind: 'fail', code: res.status, error }
    } catch (err) {
      if (this.stopped) return null
      if (timeout.aborted) return { kind: 'retry', code: null, error: `超时（${Math.round(this.timeoutMs / 1000)} 秒没有响应）` }
      const e = err as Error & { cause?: { code?: string; message?: string } }
      const detail = e.cause?.code ?? e.cause?.message ?? e.message
      return { kind: 'retry', code: null, error: `请求失败：${detail}` }
    }
  }

  private async finish(record: DeliveryRecord, outcome: Outcome, stored: StoredRequest | null, lost: boolean): Promise<void> {
    const now = Date.now()
    const attempts = lost ? record.attempts : record.attempts + 1
    const next: DeliveryRecord = { ...record, attempts, updatedAt: iso(now), nextAttemptAt: null }
    switch (outcome.kind) {
      case 'delivered':
        Object.assign(next, { status: 'delivered', lastStatusCode: outcome.code, lastError: null })
        break
      case 'fail':
        Object.assign(next, { status: lost ? 'abandoned' : 'failed', lastStatusCode: outcome.code, lastError: outcome.error })
        break
      case 'retry': {
        const used = attempts - (stored?.round ?? 0)
        const limit = stored?.maxAttempts ?? this.maxAttempts
        Object.assign(next, { lastStatusCode: outcome.code, lastError: outcome.error })
        if (used >= limit) {
          next.status = 'abandoned'
        } else {
          next.status = 'failed'
          next.nextAttemptAt = iso(now + this.backoff[Math.min(used, this.backoff.length) - 1]!)
        }
        break
      }
    }
    this.records.set(record.id, next)
    try {
      await this.api.state.set(RECORD_PREFIX + record.id, next)
    } catch (err) {
      if (!this.stopped) this.api.log.warn(`保存投递记录 ${record.id} 失败：${(err as Error).message}`)
    }
  }

  // prune 只删终态记录：还在等待或重试的保留到结束，哪怕超出保留条数。
  private async prune(): Promise<void> {
    if (this.records.size <= this.keep) return
    const oldest = [...this.records.values()].sort((a, b) => (a.id < b.id ? -1 : 1))
    let excess = this.records.size - this.keep
    for (const r of oldest) {
      if (excess <= 0) break
      if (!isTerminal(r) || this.running.has(r.id)) continue
      this.records.delete(r.id)
      excess--
      await this.api.state.delete(RECORD_PREFIX + r.id)
      await this.api.state.delete(REQUEST_PREFIX + r.id)
    }
  }
}
