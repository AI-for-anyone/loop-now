import { config } from '@/config'
import { connectLocalEventStream } from '../event-stream'
import type { StreamOptions } from '../events'
import type { StreamRequest, TabMessage, WorkerMessage } from './protocol'

// A tab's side of the shared event streams (the worker is worker.ts, its logic hub.ts). connectEventStream (events.ts) subscribes
// here instead of opening an EventSource, and gets the same events and connection states it would get from its own connection.
//
// - The tab answers the worker's pings; a tab that leaves (pagehide) says so, so its streams close at once.
// - A page restored from the back-forward cache, or a frozen page resuming, connects to the worker again or checks it is still
//   known; the worker answers 'reset' when it had given the tab up, and the tab subscribes again to everything it still shows.
// - When the worker can't run (its script fails to load), every stream falls back to one this tab holds itself, as before.

type AnyEvent = { type: string; id: string; data: unknown }

export interface WorkerPort {
  postMessage(message: TabMessage): void
  onmessage: ((event: MessageEvent<WorkerMessage>) => void) | null
  start?: () => void
}

export interface WorkerLike {
  port: WorkerPort
  addEventListener(type: 'error', listener: () => void): void
}

export interface PageEvents {
  addEventListener(type: string, listener: (event: Event) => void): void
}

type Local = <E extends AnyEvent>(url: string, names: readonly E['type'][], options: StreamOptions<E>) => () => void

interface Subscription {
  request: StreamRequest
  options: StreamOptions<AnyEvent>
  stopLocal: (() => void) | null
}

export class SharedStreams {
  private subs = new Map<number, Subscription>()
  private next = 1
  private failed = false
  private port: WorkerPort | null = null
  // When the worker was last heard from, and when a silent worker was asked whether it is still there.
  private heard = Date.now()
  private probedAt: number | null = null

  constructor(
    private connect: () => WorkerLike,
    private local: Local = connectLocalEventStream,
    page?: { window: PageEvents; document: PageEvents & { visibilityState: string } },
    private base = typeof location === 'undefined' ? 'http://localhost/' : location.href,
    private timing = { checkMs: 15_000, silentMs: 45_000 },
  ) {
    this.open()
    // A worker that died (crashed, or was ended by the browser) says nothing: a tab that hasn't heard from it for silentMs asks once,
    // and when that goes unanswered too, connects to a new worker and subscribes again.
    setInterval(() => this.watch(), timing.checkMs)
    if (!page) return
    page.window.addEventListener('pagehide', () => this.post({ kind: 'bye' }))
    page.window.addEventListener('pageshow', (event) => {
      // Restored from the back-forward cache: its subscriptions were dropped on pagehide, and its port may be gone too.
      if ((event as PageTransitionEvent).persisted) this.reopen()
    })
    // A frozen page that resumes may have been given up by the worker meanwhile: a pong makes the worker say 'reset' if so.
    page.document.addEventListener('resume', () => {
      this.heard = Date.now()
      this.post({ kind: 'pong' })
    })
    page.document.addEventListener('visibilitychange', () => {
      if (page.document.visibilityState === 'visible') this.post({ kind: 'pong' })
    })
  }

  subscribe<E extends AnyEvent>(url: string, names: readonly E['type'][], options: StreamOptions<E>): () => void {
    const generic = options as unknown as StreamOptions<AnyEvent>
    if (this.failed) return this.local(url, names, options)
    const sub = this.next++
    const request: StreamRequest = {
      url: new URL(url, this.base).href,
      names: [...names],
      retryMinMs: options.retryMinMs ?? config.eventRetryMinMs,
      retryMaxMs: options.retryMaxMs ?? config.eventRetryMaxMs,
    }
    this.subs.set(sub, { request, options: generic, stopLocal: null })
    options.onState('connecting')
    this.post({ kind: 'subscribe', sub, ...request })
    return () => {
      const subscription = this.subs.get(sub)
      if (!subscription) return
      this.subs.delete(sub)
      if (subscription.stopLocal) subscription.stopLocal()
      else this.post({ kind: 'unsubscribe', sub })
    }
  }

  private open() {
    let worker: WorkerLike
    try {
      worker = this.connect()
    } catch {
      this.fallBack()
      return
    }
    worker.addEventListener('error', () => this.fallBack())
    const port = worker.port
    port.onmessage = (event) => {
      if (this.port === port) this.receive(event.data)
    }
    port.start?.()
    this.port = port
  }

  private reopen() {
    if (this.failed) return
    this.heard = Date.now()
    this.probedAt = null
    this.open()
    this.resubscribe()
  }

  private watch() {
    if (this.failed) return
    const now = Date.now()
    if (now - this.heard < this.timing.silentMs) return
    if (this.probedAt === null) {
      this.probedAt = now
      this.post({ kind: 'pong' })
      return
    }
    if (now - this.probedAt < this.timing.checkMs) return
    // The old port may still lead to a worker that is merely stuck: say goodbye there before moving on.
    this.post({ kind: 'bye' })
    this.reopen()
  }

  private resubscribe() {
    for (const [sub, subscription] of this.subs) {
      if (subscription.stopLocal) continue
      this.post({ kind: 'subscribe', sub, ...subscription.request })
    }
  }

  // The worker can't run here: every stream becomes one this tab holds itself.
  private fallBack() {
    if (this.failed) return
    this.failed = true
    this.port = null
    for (const subscription of this.subs.values()) {
      if (!subscription.stopLocal) subscription.stopLocal = this.local(subscription.request.url, subscription.request.names, subscription.options)
    }
  }

  private post(message: TabMessage) {
    if (!this.failed) this.port?.postMessage(message)
  }

  private receive(message: WorkerMessage) {
    this.heard = Date.now()
    this.probedAt = null
    switch (message.kind) {
      case 'ping':
        this.post({ kind: 'pong' })
        return
      case 'reset':
        this.resubscribe()
        return
    }
    const subscription = this.subs.get(message.sub)
    if (!subscription || subscription.stopLocal) return
    const { options } = subscription
    switch (message.kind) {
      case 'state':
        options.onState(message.state)
        return
      case 'event': {
        let data: unknown
        try {
          data = JSON.parse(message.data)
        } catch {
          return
        }
        options.onEvent({ type: message.type, id: message.id, data })
        return
      }
      case 'closed': {
        const sub = message.sub
        void Promise.resolve()
          .then(() => options.onClosed())
          .catch(() => true)
          .then((retry) => {
            if (this.subs.get(sub) !== subscription) return
            this.post({ kind: 'retry', sub, retry })
            if (retry) return
            this.subs.delete(sub)
            options.onState('closed')
          })
      }
    }
  }
}

let instance: SharedStreams | null | undefined

// The tab's connection to the shared worker; null where the browser has no shared workers (the tab then holds its streams itself).
export function sharedStreams(): SharedStreams | null {
  if (instance !== undefined) return instance
  instance = null
  if (typeof SharedWorker === 'undefined' || typeof window === 'undefined') return null
  instance = new SharedStreams(
    () => new SharedWorker(new URL('./worker.ts', import.meta.url), { type: 'module', name: 'loop-now-events' }) as unknown as WorkerLike,
    connectLocalEventStream,
    { window, document },
  )
  return instance
}
