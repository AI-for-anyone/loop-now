// Outgoing HTTP delivery library (plugins/SPEC.md §3, §7): the queue is stored in plugin state and resumes after a host or server
// restart; failures are retried with backoff. Each delivery writes a deliveries/<ULID> record (the §7 shape; the plugin page reads
// this prefix), and only the most recent ones are kept.
//
// Statuses:
// - pending: waiting to be sent (new, or after a manual resend); nextAttemptAt is the scheduled time;
// - delivered: got a 2xx;
// - failed: the last attempt failed. With a non-null nextAttemptAt it is retried at that time; with null it is a non-retryable failure
//   (4xx, redirect, missing signing secret) and is not retried;
// - abandoned: retries used up; given up.
// Terminal states (delivered, failed with no further retry, abandoned) can be resent manually with resend.
//
// The request itself (target, headers, body, signing settings) is stored separately in delivery-requests/<ULID>, kept and deleted
// together with the record. The signing secret is never written to disk: each attempt reads it by field name from the plugin config's
// secrets, so retries after a secret change use the new secret.

import { DELIVERY_HEADER, SIGNATURE_HEADER, signBody, TIMESTAMP_HEADER } from './signature.ts'
import type { PluginAPI } from './types.ts'
import { ulid } from './ulid.ts'

export type DeliveryStatus = 'pending' | 'delivered' | 'failed' | 'abandoned'

/** Value of the state key deliveries/<id>; the shape is defined by plugins/SPEC.md §7. */
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
  /** Name of an x-secret field in the plugin config; each attempt reads its value with api.secrets.get(secretField, projectId). */
  secretField: string
  projectId?: string | null
}

export interface DeliveryRequest {
  /** An http or https URL. */
  target: string
  /** Defaults to POST. */
  method?: string
  headers?: Record<string, string>
  /** Strings are sent as is; other values are sent as JSON, with content-type: application/json added by default. */
  body?: unknown
  /** When set, each attempt adds the x-loop-now-timestamp and x-loop-now-signature headers (see signBody). */
  signing?: DeliverySigning
  /** The event that triggered this delivery; one event is delivered to one target only once (observe events may arrive more than once). */
  eventId?: string | null
  eventType?: string | null
  /** Maximum attempts for this delivery (including the first); defaults to createDelivery's maxAttempts. */
  maxAttempts?: number
}

export interface DeliveryOptions {
  /** Maximum attempts (including the first), 5 by default. */
  maxAttempts?: number
  /**
   * How long to wait before retrying after the nth failure; past the end of the list the last item is used.
   * Defaults to 5 seconds, 30 seconds, 2 minutes, 10 minutes, 30 minutes, 1 hour.
   */
  backoffMs?: number[]
  /** Timeout per attempt, 15 seconds by default. */
  timeoutMs?: number
  /** How many recent records to keep, 500 by default; older terminal records are deleted along with their requests. */
  keep?: number
  /** Number of attempts running at the same time, 4 by default. */
  concurrency?: number
  /** Defaults to loop-now-plugin-<id>/<version>. */
  userAgent?: string
  /** For tests: replaces fetch. */
  fetch?: typeof fetch
}

export interface Delivery {
  /** Saves this delivery and schedules the first attempt; returns the record right away (without waiting for the HTTP result). */
  deliver(request: DeliveryRequest): Promise<DeliveryRecord>
  /** Manually resends a terminal record; records already waiting or retrying are left unchanged. */
  resend(id: string): Promise<DeliveryRecord>
  get(id: string): Promise<DeliveryRecord | null>
  /** Newest first. */
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
  /** Attempts already made at the time of a manual resend; attempts in this round = attempts - round. */
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

  // serialize runs create, resend and prune one at a time, so concurrent deliver calls cannot create two records for the same event.
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
      // Write the record before the request: if we exit in between, the record finds no request and is marked abandoned on its attempt,
      // and no request is left behind without a record referring to it.
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
      if (!this.stopped) this.api.log.warn(`Failed to read the request for delivery ${id}: ${(err as Error).message}`)
      return
    }
    if (this.stopped) return
    if (!stored) {
      await this.finish(record, { kind: 'fail', code: null, error: '请求内容已丢失' }, null, true)
      return
    }
    const outcome = await this.send(id, stored)
    if (outcome === null) return // The plugin stopped; leave it for the next load
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
      if (!this.stopped) this.api.log.warn(`Failed to save delivery record ${record.id}: ${(err as Error).message}`)
    }
  }

  // prune deletes only terminal records: ones still waiting or retrying are kept until they finish, even beyond the keep count.
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
