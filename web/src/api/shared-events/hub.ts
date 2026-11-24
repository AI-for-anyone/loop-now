import { compareEventIds } from '../event-ids'
import type { ConnectionState, StreamRequest, TabMessage, WorkerMessage } from './protocol'
import type { StreamTransport } from './transport'

// The shared worker's side: which tab gets which events. The transport opens the streams (transport.ts); the hub counts the tabs
// subscribed to each stream, opens it for the first and closes it after the last, and passes every event and connection state on to
// each subscriber, in order.
//
// A tab that subscribes to a stream already open must get what a new connection would give it: a snapshot, then what follows. The
// hub keeps, per stream, the last snapshot and every event after it, and replays them to the newcomer before it gets live events.
// The worker runs one task at a time, so nothing arrives between the replay and the next live event: nothing is lost or repeated,
// and the other subscribers see nothing of it. Replaying the snapshot and the events since then leaves the tab in the state a fresh
// snapshot would (the snapshot carries the stream position, and the events after it are exactly those after that position). When
// that record grows too long (a long-lived Agent stream with many text deltas), it is dropped; a newcomer then gets a fresh
// snapshot through a second, short connection (transport.snapshot), followed by the events the shared stream delivered after the
// snapshot's position.
//
// A tab that goes away without a word (crash, killed process) answers no pings: after `missedPings` unanswered ones it is forgotten
// with its subscriptions. A forgotten tab that comes back (a frozen page resuming) is told to subscribe again ('reset').

export interface HubPort {
  postMessage(message: WorkerMessage): void
  onmessage: ((event: MessageEvent<TabMessage>) => void) | null
  start?: () => void
}

export interface HubLimits {
  // Unanswered pings before a tab is forgotten.
  missedPings: number
  // The longest record of a stream kept for newcomers: events after the snapshot, and characters of event data.
  maxEvents: number
  maxChars: number
}

export const DEFAULT_LIMITS: HubLimits = { missedPings: 4, maxEvents: 5000, maxChars: 2_000_000 }

interface Cached {
  type: string
  id: string
  data: string
}

interface Tab {
  port: HubPort
  subs: Map<number, Stream>
  missed: number
}

interface Subscriber {
  tab: Tab
  sub: number
  // False while it waits for a fresh snapshot (catch-up); it gets no live events until then.
  live: boolean
}

interface Stream {
  request: StreamRequest
  subscribers: Subscriber[]
  state: ConnectionState
  // The connection started without a position and its snapshot hasn't come yet: a newcomer just joins and gets it live.
  fresh: boolean
  // The last snapshot and the events after it; null when there is none or it grew too long.
  record: { events: Cached[]; chars: number } | null
  // Closed for good, waiting for a subscriber to ask for a retry.
  awaitingRetry: boolean
  catchUp: { stop: () => void; pending: Cached[] } | null
}

export class EventHub {
  private tabs = new Map<HubPort, Tab>()
  private streams = new Map<string, Stream>()

  constructor(
    private transport: StreamTransport,
    private limits: HubLimits = DEFAULT_LIMITS,
  ) {}

  attach(port: HubPort) {
    this.tabs.set(port, { port, subs: new Map(), missed: 0 })
    port.onmessage = (event) => this.receive(port, event.data)
    port.start?.()
  }

  // Called on a timer by the worker: forget tabs that stopped answering, ping the others.
  tick() {
    for (const tab of [...this.tabs.values()]) {
      if (tab.missed >= this.limits.missedPings) {
        this.forget(tab)
        continue
      }
      tab.missed += 1
      tab.port.postMessage({ kind: 'ping' })
    }
  }

  // The streams open now and how many subscribers each has (for tests and debugging).
  openStreams(): Record<string, number> {
    return Object.fromEntries([...this.streams].map(([url, stream]) => [url, stream.subscribers.length]))
  }

  receive(port: HubPort, message: TabMessage) {
    const tab = this.tabs.get(port)
    if (!tab) {
      // A tab forgotten earlier: its subscriptions are gone, so it subscribes again to what it still shows.
      if (message.kind === 'bye') return
      this.tabs.set(port, { port, subs: new Map(), missed: 0 })
      port.postMessage({ kind: 'reset' })
      return
    }
    tab.missed = 0
    switch (message.kind) {
      case 'subscribe':
        this.subscribe(tab, message.sub, { url: message.url, names: message.names, retryMinMs: message.retryMinMs, retryMaxMs: message.retryMaxMs })
        break
      case 'unsubscribe':
        this.unsubscribe(tab, message.sub)
        break
      case 'retry': {
        const stream = tab.subs.get(message.sub)
        if (!stream) break
        if (!message.retry) {
          this.unsubscribe(tab, message.sub)
          break
        }
        if (stream.awaitingRetry) {
          stream.awaitingRetry = false
          this.transport.retry(stream.request.url)
        }
        break
      }
      case 'pong':
        break
      case 'bye':
        this.forget(tab)
        break
    }
  }

  private forget(tab: Tab) {
    for (const sub of [...tab.subs.keys()]) this.unsubscribe(tab, sub)
    this.tabs.delete(tab.port)
  }

  private subscribe(tab: Tab, sub: number, request: StreamRequest) {
    if (tab.subs.has(sub)) this.unsubscribe(tab, sub)
    let stream = this.streams.get(request.url)
    const subscriber: Subscriber = { tab, sub, live: true }
    if (!stream) {
      stream = { request, subscribers: [subscriber], state: 'connecting', fresh: true, record: null, awaitingRetry: false, catchUp: null }
      this.streams.set(request.url, stream)
      tab.subs.set(sub, stream)
      this.transport.open(request, this.sink(stream))
      return
    }
    stream.subscribers.push(subscriber)
    tab.subs.set(sub, stream)
    this.post(subscriber, { kind: 'state', sub, state: stream.state })
    if (stream.awaitingRetry) this.post(subscriber, { kind: 'closed', sub })
    if (stream.fresh) return
    if (stream.record) {
      for (const event of stream.record.events) this.post(subscriber, { kind: 'event', sub, ...event })
      return
    }
    subscriber.live = false
    this.catchUp(stream)
  }

  private unsubscribe(tab: Tab, sub: number) {
    const stream = tab.subs.get(sub)
    if (!stream) return
    tab.subs.delete(sub)
    stream.subscribers = stream.subscribers.filter((subscriber) => subscriber.tab !== tab || subscriber.sub !== sub)
    if (stream.catchUp && stream.subscribers.every((subscriber) => subscriber.live)) this.stopCatchUp(stream)
    if (stream.subscribers.length > 0) return
    this.stopCatchUp(stream)
    this.transport.close(stream.request.url)
    this.streams.delete(stream.request.url)
  }

  // A fresh snapshot for the subscribers waiting for one, without touching the shared connection: the events the shared stream
  // delivers meanwhile are kept, and those after the snapshot's position follow it.
  private catchUp(stream: Stream) {
    if (stream.catchUp) return
    const pending: Cached[] = []
    const stop = this.transport.snapshot(stream.request, (id, data) => {
      if (stream.catchUp?.pending !== pending) return
      stream.catchUp = null
      const events = [{ type: 'snapshot', id, data }, ...pending.filter((event) => compareEventIds(event.id, id) > 0)]
      this.keep(stream, events)
      for (const subscriber of stream.subscribers) {
        if (subscriber.live) continue
        subscriber.live = true
        for (const event of events) this.post(subscriber, { kind: 'event', sub: subscriber.sub, ...event })
      }
    })
    stream.catchUp = { stop, pending }
  }

  private stopCatchUp(stream: Stream) {
    stream.catchUp?.stop()
    stream.catchUp = null
  }

  private keep(stream: Stream, events: Cached[]) {
    const chars = events.reduce((sum, event) => sum + event.data.length, 0)
    stream.record = events.length - 1 > this.limits.maxEvents || chars > this.limits.maxChars ? null : { events, chars }
  }

  private sink(stream: Stream) {
    return {
      restart: () => {
        // A snapshot comes first on this connection: whoever waits for one gets it live.
        stream.fresh = true
        stream.record = null
        this.stopCatchUp(stream)
        for (const subscriber of stream.subscribers) subscriber.live = true
      },
      event: (type: string, id: string, data: string) => {
        if (this.streams.get(stream.request.url) !== stream) return
        const event = { type, id, data }
        // A snapshot on the shared connection itself (it resumed beyond what the server keeps): whoever waits for one gets it live.
        if (type === 'snapshot' && stream.catchUp) {
          this.stopCatchUp(stream)
          for (const subscriber of stream.subscribers) subscriber.live = true
        }
        if (type === 'snapshot') this.keep(stream, [event])
        else if (stream.record) {
          stream.record.events.push(event)
          stream.record.chars += data.length
          if (stream.record.events.length - 1 > this.limits.maxEvents || stream.record.chars > this.limits.maxChars) stream.record = null
        }
        stream.fresh = false
        stream.catchUp?.pending.push(event)
        for (const subscriber of stream.subscribers) if (subscriber.live) this.post(subscriber, { kind: 'event', sub: subscriber.sub, ...event })
      },
      state: (state: 'connecting' | 'open' | 'reconnecting') => {
        if (this.streams.get(stream.request.url) !== stream) return
        stream.state = state
        for (const subscriber of stream.subscribers) this.post(subscriber, { kind: 'state', sub: subscriber.sub, state })
      },
      closed: () => {
        if (this.streams.get(stream.request.url) !== stream) return
        stream.awaitingRetry = true
        stream.state = 'reconnecting'
        // The next connection starts with a snapshot: whoever joins or waits meanwhile gets it live.
        stream.fresh = true
        stream.record = null
        this.stopCatchUp(stream)
        for (const subscriber of stream.subscribers) subscriber.live = true
        for (const subscriber of stream.subscribers) {
          this.post(subscriber, { kind: 'state', sub: subscriber.sub, state: 'reconnecting' })
          this.post(subscriber, { kind: 'closed', sub: subscriber.sub })
        }
      },
    }
  }

  private post(subscriber: Subscriber, message: WorkerMessage) {
    subscriber.tab.port.postMessage(message)
  }
}
