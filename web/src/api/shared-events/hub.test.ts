import { describe, expect, it } from 'vitest'
import { EventHub, type HubPort } from './hub'
import { streamKey, type StreamRequest, type TabMessage, type WorkerMessage } from './protocol'
import type { StreamSink, StreamTransport } from './transport'

const ENDPOINT = 'http://127.0.0.1:8780/api/v1/events'
const names = ['snapshot', 'message.created', 'run.text']

// The connection layer, played by the test: which streams the hub asked for, and their sinks to drive.
class FakeTransport implements StreamTransport {
  sinks = new Map<string, StreamSink>()
  log: string[] = []
  catchUps: { stream: string; deliver: (id: string, data: string) => void; stopped: boolean }[] = []
  open(request: StreamRequest, sink: StreamSink) {
    this.sinks.set(request.stream, sink)
    this.log.push(`open ${request.stream}`)
    sink.restart()
    sink.state('connecting')
  }
  retry(key: string) {
    this.log.push(`retry ${key.split(' ')[1]}`)
  }
  close(key: string) {
    this.sinks.delete(key.split(' ')[1])
    this.log.push(`close ${key.split(' ')[1]}`)
  }
  snapshot(request: StreamRequest, onSnapshot: (id: string, data: string) => void) {
    const catchUp = { stream: request.stream, deliver: onSnapshot, stopped: false }
    this.catchUps.push(catchUp)
    return () => (catchUp.stopped = true)
  }
  sink(stream: string) {
    const sink = this.sinks.get(stream)
    if (!sink) throw new Error(`${stream} is not open`)
    return sink
  }
}

// A tab as the worker sees it: a port that records what the worker sends it.
function tab(hub: EventHub) {
  const messages: WorkerMessage[] = []
  const port: HubPort = { postMessage: (message) => messages.push(message), onmessage: null }
  hub.attach(port)
  const send = (message: TabMessage) => port.onmessage?.({ data: message } as MessageEvent<TabMessage>)
  return {
    messages,
    send,
    subscribe: (sub: number, stream = 'agent:a1') => send({ kind: 'subscribe', sub, endpoint: ENDPOINT, stream, names, retryMinMs: 100, retryMaxMs: 1000 }),
    // What it received, compactly: events as "sub type id", states as "sub state:…".
    seen: () => messages.flatMap((m) => (m.kind === 'event' ? [`${m.sub} ${m.type} ${m.id}`] : m.kind === 'state' ? [`${m.sub} state:${m.state}`] : m.kind === 'closed' ? [`${m.sub} closed`] : [m.kind])),
    clear: () => messages.splice(0),
  }
}

function setup(limits = { missedPings: 2, maxEvents: 5000, maxChars: 2_000_000 }) {
  const transport = new FakeTransport()
  return { hub: new EventHub(transport, limits), t: transport }
}

describe('the shared worker hub', () => {
  it('the first subscriber asks for a stream, a second one shares it, and the last one to leave lets it go', () => {
    const { hub, t } = setup()
    const a = tab(hub), b = tab(hub)
    a.subscribe(1)
    b.subscribe(7)
    b.subscribe(8, 'inbox')
    expect(t.log).toEqual(['open agent:a1', 'open inbox'])
    expect(hub.openStreams()).toEqual({ 'agent:a1': 2, inbox: 1 })
    a.send({ kind: 'unsubscribe', sub: 1 })
    expect(t.log).toEqual(['open agent:a1', 'open inbox'])
    b.send({ kind: 'unsubscribe', sub: 7 })
    expect(t.log.at(-1)).toBe('close agent:a1')
    expect(hub.openStreams()).toEqual({ inbox: 1 })
  })

  it('every subscriber gets the states and events of the stream, in order', () => {
    const { hub, t } = setup()
    const a = tab(hub), b = tab(hub)
    a.subscribe(1)
    b.subscribe(2)
    t.sink('agent:a1').state('open')
    t.sink('agent:a1').event('snapshot', '10-0', '{"s":1}')
    t.sink('agent:a1').event('message.created', '11-0', '{"m":1}')
    expect(a.seen()).toEqual(['1 state:connecting', '1 state:open', '1 snapshot 10-0', '1 message.created 11-0'])
    expect(b.seen()).toEqual(['2 state:connecting', '2 state:open', '2 snapshot 10-0', '2 message.created 11-0'])
    expect(a.messages.find((m) => m.kind === 'event' && m.type === 'message.created')).toMatchObject({ data: '{"m":1}' })
  })

  it('a tab that joins an open stream gets its state, the last snapshot and the events since, then live ones; the others hear nothing of it', () => {
    const { hub, t } = setup()
    const a = tab(hub)
    a.subscribe(1)
    const s = t.sink('agent:a1')
    s.state('open')
    s.event('snapshot', '10-0', '{}')
    s.event('run.text', '11-0', '{}')
    s.event('run.text', '12-0', '{}')
    a.clear()
    const b = tab(hub)
    b.subscribe(5)
    expect(b.seen()).toEqual(['5 state:open', '5 snapshot 10-0', '5 run.text 11-0', '5 run.text 12-0'])
    expect(a.seen()).toEqual([])
    s.event('run.text', '13-0', '{}')
    expect(b.seen().at(-1)).toBe('5 run.text 13-0')
    expect(a.seen()).toEqual(['1 run.text 13-0'])
    expect(t.log).toEqual(['open agent:a1'])
  })

  it('a tab that joins before the first snapshot just gets it live', () => {
    const { hub, t } = setup()
    const a = tab(hub), b = tab(hub)
    a.subscribe(1)
    t.sink('agent:a1').state('open')
    b.subscribe(2)
    t.sink('agent:a1').event('snapshot', '10-0', '{}')
    expect(b.seen()).toEqual(['2 state:open', '2 snapshot 10-0'])
  })

  it('a second snapshot on the stream (it resumed from 0-0 of an empty stream) starts the record again, and reaches everyone like any event', () => {
    const { hub, t } = setup()
    const a = tab(hub)
    a.subscribe(1, 'inbox')
    const s = t.sink('inbox')
    s.state('open')
    s.event('snapshot', '0-0', '{"unreadTotal":0}')
    s.event('snapshot', '3-0', '{"unreadTotal":1}')
    s.event('run.text', '4-0', '{}')
    expect(a.seen().slice(-3)).toEqual(['1 snapshot 0-0', '1 snapshot 3-0', '1 run.text 4-0'])
    const b = tab(hub)
    b.subscribe(2, 'inbox')
    expect(b.seen()).toEqual(['2 state:open', '2 snapshot 3-0', '2 run.text 4-0'])
  })

  it('when the kept record grew too long, a late tab gets a fresh snapshot fetched on the side, then the events after its position', () => {
    const { hub, t } = setup({ missedPings: 2, maxEvents: 2, maxChars: 2_000_000 })
    const a = tab(hub)
    a.subscribe(1)
    const s = t.sink('agent:a1')
    s.state('open')
    s.event('snapshot', '10-0', '{}')
    for (const id of ['11-0', '12-0', '13-0']) s.event('run.text', id, '{}')
    const b = tab(hub)
    b.subscribe(2)
    expect(t.catchUps.map((c) => c.stream)).toEqual(['agent:a1'])
    expect(b.seen()).toEqual(['2 state:open'])
    s.event('run.text', '14-0', '{}')
    s.event('run.text', '15-0', '{}')
    t.catchUps[0].deliver('14-0', '{"s":2}')
    expect(b.seen()).toEqual(['2 state:open', '2 snapshot 14-0', '2 run.text 15-0'])
    s.event('run.text', '16-0', '{}')
    expect(b.seen().at(-1)).toBe('2 run.text 16-0')
    expect(a.seen().filter((line) => line.includes('snapshot'))).toEqual(['1 snapshot 10-0'])
    // The fresh snapshot is kept again: a third tab replays it with nothing fetched.
    const c = tab(hub)
    c.subscribe(3)
    expect(c.seen()).toEqual(['3 state:open', '3 snapshot 14-0', '3 run.text 15-0', '3 run.text 16-0'])
    expect(t.catchUps).toHaveLength(1)
  })

  it('a snapshot on the stream itself ends a catch-up: whoever waited gets it live', () => {
    const { hub, t } = setup({ missedPings: 2, maxEvents: 1, maxChars: 2_000_000 })
    const a = tab(hub)
    a.subscribe(1)
    const s = t.sink('agent:a1')
    s.event('snapshot', '10-0', '{}')
    s.event('run.text', '11-0', '{}')
    s.event('run.text', '12-0', '{}')
    const b = tab(hub)
    b.subscribe(2)
    s.event('snapshot', '20-0', '{}')
    expect(t.catchUps[0].stopped).toBe(true)
    expect(b.seen().slice(-1)).toEqual(['2 snapshot 20-0'])
  })

  it('a stream closed for good asks every subscriber: one that declines leaves; it is retried when another asks, and starts with a snapshot', () => {
    const { hub, t } = setup()
    const a = tab(hub), b = tab(hub)
    a.subscribe(1)
    b.subscribe(2)
    const s = t.sink('agent:a1')
    s.state('open')
    s.event('snapshot', '10-0', '{}')
    a.clear()
    b.clear()
    s.closed()
    expect(a.seen()).toEqual(['1 state:reconnecting', '1 closed'])
    expect(b.seen()).toEqual(['2 state:reconnecting', '2 closed'])
    a.send({ kind: 'retry', sub: 1, retry: false })
    a.clear()
    expect(hub.openStreams()).toEqual({ 'agent:a1': 1 })
    // A tab joining now is asked as well, and gets no stale snapshot.
    const c = tab(hub)
    c.subscribe(3)
    expect(c.seen()).toEqual(['3 state:reconnecting', '3 closed'])
    b.send({ kind: 'retry', sub: 2, retry: true })
    c.send({ kind: 'retry', sub: 3, retry: true })
    expect(t.log.filter((line) => line.startsWith('retry'))).toEqual(['retry agent:a1'])
    s.restart()
    s.state('open')
    s.event('snapshot', '1-0', '{}')
    expect(b.seen().slice(-2)).toEqual(['2 state:open', '2 snapshot 1-0'])
    expect(c.seen().slice(-2)).toEqual(['3 state:open', '3 snapshot 1-0'])
    expect(a.seen()).toEqual([])
  })

  it('when every subscriber declines, the stream is let go', () => {
    const { hub, t } = setup()
    const a = tab(hub)
    a.subscribe(1)
    t.sink('agent:a1').closed()
    a.send({ kind: 'retry', sub: 1, retry: false })
    expect(hub.openStreams()).toEqual({})
    expect(t.log).toEqual(['open agent:a1', 'close agent:a1'])
  })

  it('a tab that stops answering pings is forgotten with its subscriptions; when it answers later it is told to subscribe again', () => {
    const { hub, t } = setup({ missedPings: 2, maxEvents: 5000, maxChars: 2_000_000 })
    const alive = tab(hub), gone = tab(hub)
    alive.subscribe(1, 'inbox')
    gone.subscribe(1)
    gone.subscribe(2, 'inbox')
    for (let i = 0; i < 3; i++) {
      hub.tick()
      alive.send({ kind: 'pong' })
    }
    expect(gone.messages.filter((m) => m.kind === 'ping')).toHaveLength(2)
    expect(hub.openStreams()).toEqual({ inbox: 1 })
    expect(t.log).toContain('close agent:a1')
    gone.clear()
    gone.send({ kind: 'pong' })
    expect(gone.seen()).toEqual(['reset'])
    gone.subscribe(1)
    expect(hub.openStreams()).toEqual({ inbox: 1, 'agent:a1': 1 })
  })

  it('a tab that says bye is forgotten at once', () => {
    const { hub } = setup()
    const a = tab(hub), b = tab(hub)
    a.subscribe(1)
    b.subscribe(1)
    a.send({ kind: 'bye' })
    expect(hub.openStreams()).toEqual({ 'agent:a1': 1 })
    b.send({ kind: 'bye' })
    expect(hub.openStreams()).toEqual({})
  })

  it('subscribing again under the same number replaces the subscription (a tab answering a reset)', () => {
    const { hub } = setup()
    const a = tab(hub)
    a.subscribe(1)
    a.subscribe(1)
    expect(hub.openStreams()).toEqual({ 'agent:a1': 1 })
  })

  it('streams are told apart by endpoint and name', () => {
    expect(streamKey({ endpoint: ENDPOINT, stream: 'inbox' })).not.toBe(streamKey({ endpoint: 'http://other/api/v1/events', stream: 'inbox' }))
  })
})
