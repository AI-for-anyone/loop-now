import { streamKey, type StreamRequest } from './protocol'

// How the hub gets the events of its streams: the layer that decides which connections to open. All streams of one server go over
// one GET /events connection (MultiplexedTransport); the hub (who gets which events) doesn't know about connections.

export interface StreamSink {
  // The stream starts without a position on the next connection: its first event will be a snapshot.
  restart(): void
  event(type: string, id: string, data: string): void
  state(state: 'connecting' | 'open' | 'reconnecting'): void
  // The stream was closed for good (the connection was, or the server reported a stream.error for it); nothing more comes until
  // retry().
  closed(): void
}

export interface StreamTransport {
  open(request: StreamRequest, sink: StreamSink): void
  // Take a stream closed for good back, after its backoff; it starts with a snapshot.
  retry(key: string): void
  close(key: string): void
  // A fresh snapshot of a stream, without touching the connection that carries it: for a tab that joins it late when no snapshot is
  // kept. Returns a function that stops waiting for it.
  snapshot(request: StreamRequest, onSnapshot: (id: string, data: string) => void): () => void
}

// How long a change of the set of streams waits for more changes (a tab moving from one Agent to another unsubscribes and
// subscribes): one reconnect for all of them.
export const SET_CHANGE_MS = 100
// The most streams GET /events carries; more wait until some leave.
export const MAX_STREAMS = 64

const CLOSED = 2

interface Multiplexed {
  stream: string
  id: string
  event: string
  data: unknown
}

interface Entry {
  request: StreamRequest
  sink: StreamSink
  // active: wanted on the connection; closed: closed for good, until retry() (then active again after its backoff).
  status: 'active' | 'closed'
  // The stream's own id of the last event received: where it resumes; null when it starts with a snapshot.
  position: string | null
  reported: 'connecting' | 'open' | 'reconnecting' | null
  started: boolean
  retries: number
  timer: ReturnType<typeof setTimeout> | null
}

interface Connection {
  endpoint: string
  entries: Map<string, Entry>
  source: EventSource | null
  // The streams the current connection carries.
  carried: Set<string>
  change: ReturnType<typeof setTimeout> | null
}

// One GET /events connection per server for every stream the hub wants. A change of the set closes it and opens a new one with
// the new list and each stream's position as the cursor: streams in both sets go on where they were, new ones start with their
// snapshot, and the subscribers of the streams that go on see nothing of the swap. The browser's own reconnect resumes every stream
// (the SSE id is the combined cursor). A stream.error closes that stream alone; the connection closing for good closes all of them.
export class MultiplexedTransport implements StreamTransport {
  private connections = new Map<string, Connection>()

  constructor(private create: (url: string) => EventSource = (url) => new EventSource(url)) {}

  open(request: StreamRequest, sink: StreamSink) {
    let connection = this.connections.get(request.endpoint)
    if (!connection) {
      connection = { endpoint: request.endpoint, entries: new Map(), source: null, carried: new Set(), change: null }
      this.connections.set(request.endpoint, connection)
    }
    connection.entries.set(request.stream, { request, sink, status: 'active', position: null, reported: null, started: false, retries: 0, timer: null })
    this.schedule(connection)
  }

  retry(key: string) {
    const found = this.find(key)
    if (!found) return
    const { connection, entry } = found
    if (entry.status !== 'closed' || entry.timer !== null) return
    const delay = Math.min(entry.request.retryMaxMs, entry.request.retryMinMs * 2 ** entry.retries)
    entry.retries += 1
    entry.timer = setTimeout(() => {
      entry.timer = null
      entry.status = 'active'
      entry.position = null
      this.schedule(connection)
    }, delay)
  }

  close(key: string) {
    const found = this.find(key)
    if (!found) return
    const { connection, entry } = found
    if (entry.timer !== null) clearTimeout(entry.timer)
    connection.entries.delete(entry.request.stream)
    if (connection.entries.size > 0) {
      this.schedule(connection)
      return
    }
    if (connection.change !== null) clearTimeout(connection.change)
    connection.source?.close()
    this.connections.delete(connection.endpoint)
  }

  snapshot(request: StreamRequest, onSnapshot: (id: string, data: string) => void): () => void {
    let source: EventSource | null = null
    let timer: ReturnType<typeof setTimeout> | null = null
    let attempts = 0
    const stop = () => {
      if (timer !== null) clearTimeout(timer)
      timer = null
      source?.close()
      source = null
    }
    const again = () => {
      stop()
      timer = setTimeout(start, Math.min(request.retryMaxMs, request.retryMinMs * 2 ** attempts++))
    }
    const start = () => {
      timer = null
      const es = this.create(this.url(request.endpoint, [request.stream], ''))
      source = es
      es.addEventListener('event', (message) => {
        const event = parse((message as MessageEvent<string>).data)
        if (source !== es || !event || event.stream !== request.stream || event.event !== 'snapshot') return
        stop()
        onSnapshot(event.id, JSON.stringify(event.data))
      })
      es.addEventListener('stream.error', () => {
        if (source === es) again()
      })
      es.onerror = () => {
        if (source === es && es.readyState === CLOSED) again()
      }
    }
    start()
    return stop
  }

  private find(key: string) {
    for (const connection of this.connections.values()) {
      for (const entry of connection.entries.values()) if (streamKey(entry.request) === key) return { connection, entry }
    }
    return null
  }

  private schedule(connection: Connection) {
    if (connection.change !== null) return
    connection.change = setTimeout(() => {
      connection.change = null
      this.connect(connection)
    }, SET_CHANGE_MS)
  }

  private report(entry: Entry, state: 'connecting' | 'open' | 'reconnecting') {
    if (entry.reported === state) return
    entry.reported = state
    entry.sink.state(state)
  }

  private url(endpoint: string, streams: readonly string[], cursor: string) {
    const url = new URL(endpoint)
    for (const stream of streams) url.searchParams.append('stream', stream)
    if (cursor) url.searchParams.set('cursor', cursor)
    return url.href
  }

  private connect(connection: Connection) {
    if (this.connections.get(connection.endpoint) !== connection) return
    const wanted = [...connection.entries.values()].filter((entry) => entry.status === 'active').slice(0, MAX_STREAMS)
    const names = wanted.map((entry) => entry.request.stream)
    const same = connection.source !== null && names.length === connection.carried.size && names.every((name) => connection.carried.has(name))
    if (same) return
    connection.source?.close()
    connection.source = null
    const before = connection.carried
    connection.carried = new Set(names)
    if (wanted.length === 0) return
    const cursor = wanted.flatMap((entry) => (entry.position === null ? [] : [`${entry.request.stream}=${entry.position}`])).join(';')
    for (const entry of wanted) {
      if (entry.position === null) entry.sink.restart()
      // A stream that goes on over the new connection keeps its state; one that joins it is connecting.
      if (!before.has(entry.request.stream) || entry.reported !== 'open') this.report(entry, entry.started ? 'reconnecting' : 'connecting')
      entry.started = true
    }
    const es = this.create(this.url(connection.endpoint, names, cursor))
    connection.source = es
    const carriedEntries = () => [...connection.carried].flatMap((name) => connection.entries.get(name) ?? [])
    es.onopen = () => {
      if (connection.source !== es) return
      for (const entry of carriedEntries()) this.report(entry, 'open')
    }
    es.onerror = () => {
      if (connection.source !== es) return
      if (es.readyState !== CLOSED) {
        for (const entry of carriedEntries()) this.report(entry, 'reconnecting')
        return
      }
      es.close()
      connection.source = null
      const closed = carriedEntries()
      connection.carried = new Set()
      for (const entry of closed) this.closeEntry(entry)
    }
    es.addEventListener('event', (message) => {
      if (connection.source !== es) return
      const event = parse((message as MessageEvent<string>).data)
      const entry = event && connection.carried.has(event.stream) ? connection.entries.get(event.stream) : undefined
      if (!event || !entry || entry.status !== 'active') return
      entry.position = event.id
      entry.retries = 0
      if (entry.request.names.includes(event.event)) entry.sink.event(event.event, event.id, JSON.stringify(event.data))
    })
    es.addEventListener('stream.error', (message) => {
      if (connection.source !== es) return
      const stream = (parseObject((message as MessageEvent<string>).data) as { stream?: unknown } | null)?.stream
      const entry = typeof stream === 'string' && connection.carried.has(stream) ? connection.entries.get(stream) : undefined
      if (!entry) return
      // The server no longer follows it on this connection.
      connection.carried.delete(stream as string)
      this.closeEntry(entry)
    })
  }

  private closeEntry(entry: Entry) {
    entry.status = 'closed'
    entry.position = null
    entry.reported = 'reconnecting'
    entry.sink.closed()
  }
}

function parseObject(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

function parse(text: string): Multiplexed | null {
  const value = parseObject(text) as Partial<Multiplexed> | null
  if (!value || typeof value.stream !== 'string' || typeof value.id !== 'string' || typeof value.event !== 'string') return null
  return value as Multiplexed
}
