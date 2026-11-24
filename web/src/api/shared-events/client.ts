import { config } from '@/config'
import type { StreamOptions } from '../events'
import { EventHub, type HubPort } from './hub'
import type { StreamRequest, TabMessage, WorkerMessage } from './protocol'
import { MultiplexedTransport } from './transport'

// A tab's side of the shared event streams (the worker is worker.ts, its logic hub.ts). connectEventStream (events.ts) subscribes
// here and gets the same events and connection states a stream's own endpoint would give it.
//
// - The tab answers the worker's pings; a tab that leaves (pagehide) says so, so its streams are let go at once.
// - A page restored from the back-forward cache, or a frozen page resuming, connects to the worker again or checks it is still
//   known; the worker answers 'reset' when it had given the tab up, and the tab subscribes again to everything it still shows.
// - A worker that went silent (crashed, or ended by the browser) is asked once and then replaced by a new one.
// - Where there are no shared workers, or the worker fails to load, the same hub runs inside the page (inPageHub): the tab then
//   keeps one GET /events connection of its own, for all its streams.

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

interface Subscription {
  request: StreamRequest
  options: StreamOptions<AnyEvent>
}

export interface SharedStreamsSetup {
  // Connects to the shared worker; null where there is none.
  connect: (() => WorkerLike) | null
  // The hub inside the page, used without a shared worker.
  inPage: () => WorkerLike
  page?: { window: PageEvents; document: PageEvents & { visibilityState: string } }
  // The tab's own URL, against which the API base resolves.
  base?: string
  timing?: { checkMs: number; silentMs: number }
}

export class SharedStreams {
  private subs = new Map<number, Subscription>()
  private next = 1
  private port: WorkerPort | null = null
  private shared: boolean
  private readonly endpoint: string
  private readonly timing: { checkMs: number; silentMs: number }
  // When the worker was last heard from, and when a silent worker was asked whether it is still there.
  private heard = Date.now()
  private probedAt: number | null = null

  constructor(private setup: SharedStreamsSetup) {
    this.shared = setup.connect !== null
    this.endpoint = new URL(`${config.apiBase}/events`, setup.base ?? (typeof location === 'undefined' ? 'http://localhost/' : location.href)).href
    this.timing = setup.timing ?? { checkMs: 15_000, silentMs: 45_000 }
    this.open()
    // A worker that died says nothing: a tab that hasn't heard from it for silentMs asks once, and when that goes unanswered too,
    // connects to a new worker and subscribes again. The hub inside the page doesn't ping, and can't die on its own.
    setInterval(() => this.watch(), this.timing.checkMs)
    const page = setup.page
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

  // stream: the stream's name in GET /events.
  subscribe<E extends AnyEvent>(stream: string, names: readonly E['type'][], options: StreamOptions<E>): () => void {
    const sub = this.next++
    const request: StreamRequest = {
      endpoint: this.endpoint,
      stream,
      names: [...names],
      retryMinMs: options.retryMinMs ?? config.eventRetryMinMs,
      retryMaxMs: options.retryMaxMs ?? config.eventRetryMaxMs,
    }
    this.subs.set(sub, { request, options: options as unknown as StreamOptions<AnyEvent> })
    options.onState('connecting')
    this.post({ kind: 'subscribe', sub, ...request })
    return () => {
      if (!this.subs.delete(sub)) return
      this.post({ kind: 'unsubscribe', sub })
    }
  }

  private open() {
    let worker: WorkerLike
    try {
      worker = this.shared && this.setup.connect ? this.setup.connect() : this.setup.inPage()
    } catch {
      this.fallBack()
      return
    }
    if (this.shared) worker.addEventListener('error', () => this.fallBack())
    const port = worker.port
    port.onmessage = (event) => {
      if (this.port === port) this.receive(event.data)
    }
    port.start?.()
    this.port = port
  }

  private reopen() {
    this.heard = Date.now()
    this.probedAt = null
    this.open()
    this.resubscribe()
  }

  private resubscribe() {
    for (const [sub, subscription] of this.subs) this.post({ kind: 'subscribe', sub, ...subscription.request })
  }

  // The shared worker can't run here: the hub runs inside the page instead, and every stream subscribes there.
  private fallBack() {
    if (!this.shared) return
    this.shared = false
    this.port = null
    this.reopen()
  }

  private watch() {
    if (!this.shared) return
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

  private post(message: TabMessage) {
    this.port?.postMessage(message)
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
    if (!subscription) return
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

// The hub running inside the page, for a tab without a shared worker: the tab talks to it over a message channel exactly as it would
// to the worker, and it keeps one GET /events connection for the tab's streams. No pings: it lives and dies with the page.
export function inPageHub(transport = new MultiplexedTransport()): WorkerLike {
  const hub = new EventHub(transport)
  const channel = new MessageChannel()
  hub.attach(channel.port2 as unknown as HubPort)
  return { port: channel.port1 as unknown as WorkerPort, addEventListener: () => {} }
}

let instance: SharedStreams | null | undefined

// The tab's connection to its event streams: the shared worker where the browser has one, otherwise the hub inside the page.
export function sharedStreams(): SharedStreams | null {
  if (instance !== undefined) return instance
  instance = null
  if (typeof window === 'undefined' || typeof MessageChannel === 'undefined') return null
  instance = new SharedStreams({
    connect:
      typeof SharedWorker === 'undefined'
        ? null
        : () => new SharedWorker(new URL('./worker.ts', import.meta.url), { type: 'module', name: 'loop-now-events' }) as unknown as WorkerLike,
    inPage: () => inPageHub(),
    page: { window, document },
  })
  return instance
}
