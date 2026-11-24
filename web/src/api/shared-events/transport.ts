import type { StreamRequest } from './protocol'

// How the worker gets the events of a stream: the layer that decides which connections to open. Step 1 opens one EventSource per
// stream on the existing endpoints (EventSourceTransport); step 2 replaces it with the server's one multiplexed stream, and the hub
// (who gets which events) stays as it is.

export interface StreamSink {
  // A connection starts without a position (a new EventSource, so no Last-Event-ID): its first event will be a snapshot.
  restart(): void
  event(type: string, id: string, data: string): void
  state(state: 'connecting' | 'open' | 'reconnecting'): void
  // The connection was closed for good (e.g. the server returned an error status); nothing more comes until retry().
  closed(): void
}

export interface StreamTransport {
  open(request: StreamRequest, sink: StreamSink): void
  // Open a stream closed for good again, after the backoff.
  retry(url: string): void
  close(url: string): void
  // A fresh snapshot of a stream that stays open as it is: for a tab that joins it late when no snapshot is kept. Returns a function
  // that stops waiting for it.
  snapshot(request: StreamRequest, onSnapshot: (id: string, data: string) => void): () => void
}

interface Open {
  request: StreamRequest
  sink: StreamSink
  source: EventSource | null
  timer: ReturnType<typeof setTimeout> | null
  attempts: number
}

const CLOSED = 2

// One EventSource per stream, as each tab used to keep: the browser's automatic reconnect resumes with Last-Event-ID; after a
// permanent close the stream waits for retry() and opens a new connection after a backoff interval, on which the server sends a
// snapshot first.
export class EventSourceTransport implements StreamTransport {
  private streams = new Map<string, Open>()

  constructor(private create: (url: string) => EventSource = (url) => new EventSource(url)) {}

  open(request: StreamRequest, sink: StreamSink) {
    const stream: Open = { request, sink, source: null, timer: null, attempts: 0 }
    this.streams.set(request.url, stream)
    this.start(stream)
  }

  retry(url: string) {
    const stream = this.streams.get(url)
    if (!stream || stream.source || stream.timer !== null) return
    const delay = Math.min(stream.request.retryMaxMs, stream.request.retryMinMs * 2 ** stream.attempts)
    stream.attempts += 1
    stream.timer = setTimeout(() => {
      stream.timer = null
      this.start(stream)
    }, delay)
  }

  close(url: string) {
    const stream = this.streams.get(url)
    if (!stream) return
    this.streams.delete(url)
    if (stream.timer !== null) clearTimeout(stream.timer)
    stream.source?.close()
    stream.source = null
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
    const start = () => {
      const es = this.create(request.url)
      source = es
      es.addEventListener('snapshot', (message) => {
        if (source !== es) return
        stop()
        onSnapshot((message as MessageEvent<string>).lastEventId, (message as MessageEvent<string>).data)
      })
      es.onerror = () => {
        if (source !== es || es.readyState !== CLOSED) return
        es.close()
        source = null
        timer = setTimeout(start, Math.min(request.retryMaxMs, request.retryMinMs * 2 ** attempts++))
      }
    }
    start()
    return stop
  }

  private start(stream: Open) {
    if (!this.streams.has(stream.request.url)) return
    const { sink } = stream
    sink.restart()
    sink.state(stream.attempts === 0 ? 'connecting' : 'reconnecting')
    const es = this.create(stream.request.url)
    stream.source = es
    es.onopen = () => {
      if (stream.source !== es) return
      stream.attempts = 0
      sink.state('open')
    }
    es.onerror = () => {
      if (stream.source !== es) return
      if (es.readyState !== CLOSED) {
        sink.state('reconnecting')
        return
      }
      es.close()
      stream.source = null
      sink.closed()
    }
    for (const name of stream.request.names) {
      es.addEventListener(name, (message) => {
        if (stream.source !== es) return
        const event = message as MessageEvent<string>
        sink.event(name, event.lastEventId, event.data)
      })
    }
  }
}
