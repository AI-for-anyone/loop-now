import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeEventSource } from '@/test/fake-event-source'
import { EventHub, type HubPort } from './hub'
import type { TabMessage, WorkerMessage } from './protocol'
import { EventSourceTransport } from './transport'

const AGENT = 'http://127.0.0.1:8780/api/v1/agents/a1/events'
const INBOX = 'http://127.0.0.1:8780/api/v1/inbox/events'
const names = ['snapshot', 'message.created', 'run.text']

// A tab as the worker sees it: a port that records what the worker sends it.
function tab(hub: EventHub) {
  const messages: WorkerMessage[] = []
  const port: HubPort = { postMessage: (message) => messages.push(message), onmessage: null }
  hub.attach(port)
  const send = (message: TabMessage) => port.onmessage?.({ data: message } as MessageEvent<TabMessage>)
  return {
    port,
    messages,
    send,
    subscribe: (sub: number, url = AGENT) => send({ kind: 'subscribe', sub, url, names, retryMinMs: 100, retryMaxMs: 1000 }),
    // What it received, compactly: events as "type id", states as "state:…".
    seen: () => messages.flatMap((m) => (m.kind === 'event' ? [`${m.sub} ${m.type} ${m.id}`] : m.kind === 'state' ? [`${m.sub} state:${m.state}`] : m.kind === 'closed' ? [`${m.sub} closed`] : [m.kind])),
    clear: () => messages.splice(0),
  }
}

function setup(limits = { missedPings: 2, maxEvents: 5000, maxChars: 2_000_000 }) {
  const hub = new EventHub(new EventSourceTransport((url) => new FakeEventSource(url) as unknown as EventSource), limits)
  return hub
}

const sources = (url = AGENT) => FakeEventSource.instances.filter((source) => source.url === url)

beforeEach(() => {
  FakeEventSource.reset()
  vi.useFakeTimers()
})
afterEach(() => vi.useRealTimers())

describe('the shared worker hub', () => {
  it('the first subscriber opens a stream, a second one shares it, and the last one to leave closes it', () => {
    const hub = setup()
    const a = tab(hub), b = tab(hub)
    a.subscribe(1)
    b.subscribe(7)
    b.subscribe(8, INBOX)
    expect(sources()).toHaveLength(1)
    expect(sources(INBOX)).toHaveLength(1)
    expect(hub.openStreams()).toEqual({ [AGENT]: 2, [INBOX]: 1 })
    a.send({ kind: 'unsubscribe', sub: 1 })
    expect(sources()[0].readyState).not.toBe(FakeEventSource.CLOSED)
    b.send({ kind: 'unsubscribe', sub: 7 })
    expect(sources()[0].readyState).toBe(FakeEventSource.CLOSED)
    expect(hub.openStreams()).toEqual({ [INBOX]: 1 })
  })

  it('every subscriber gets the states and events of the stream, in order', () => {
    const hub = setup()
    const a = tab(hub), b = tab(hub)
    a.subscribe(1)
    b.subscribe(2)
    const source = sources()[0]
    source.open()
    source.emit('snapshot', { s: 1 }, '10-0')
    source.emit('message.created', { m: 1 }, '11-0')
    expect(a.seen()).toEqual(['1 state:connecting', '1 state:open', '1 snapshot 10-0', '1 message.created 11-0'])
    expect(b.seen()).toEqual(['2 state:connecting', '2 state:open', '2 snapshot 10-0', '2 message.created 11-0'])
    const event = a.messages.find((m) => m.kind === 'event' && m.type === 'message.created')
    expect(event).toMatchObject({ data: JSON.stringify({ m: 1 }) })
  })

  it('a tab that joins an open stream gets its state, the last snapshot and the events since, then live ones; the others hear nothing of it', () => {
    const hub = setup()
    const a = tab(hub)
    a.subscribe(1)
    const source = sources()[0]
    source.open()
    source.emit('snapshot', { s: 1 }, '10-0')
    source.emit('run.text', { t: 'a' }, '11-0')
    source.emit('run.text', { t: 'b' }, '12-0')
    a.clear()
    const b = tab(hub)
    b.subscribe(5)
    expect(b.seen()).toEqual(['5 state:open', '5 snapshot 10-0', '5 run.text 11-0', '5 run.text 12-0'])
    expect(a.seen()).toEqual([])
    source.emit('run.text', { t: 'c' }, '13-0')
    expect(b.seen().at(-1)).toBe('5 run.text 13-0')
    expect(a.seen()).toEqual(['1 run.text 13-0'])
    expect(sources()).toHaveLength(1)
  })

  it('a tab that joins before the first snapshot just gets it live', () => {
    const hub = setup()
    const a = tab(hub), b = tab(hub)
    a.subscribe(1)
    const source = sources()[0]
    source.open()
    b.subscribe(2)
    source.emit('snapshot', { s: 1 }, '10-0')
    expect(b.seen()).toEqual(['2 state:open', '2 snapshot 10-0'])
  })

  it('when the kept record grew too long, a late tab gets a fresh snapshot over a short second connection, then the events after its position', () => {
    const hub = setup({ missedPings: 2, maxEvents: 2, maxChars: 2_000_000 })
    const a = tab(hub)
    a.subscribe(1)
    const shared = sources()[0]
    shared.open()
    shared.emit('snapshot', { s: 1 }, '10-0')
    for (const id of ['11-0', '12-0', '13-0']) shared.emit('run.text', { t: id }, id)
    const b = tab(hub)
    b.subscribe(2)
    // The record is gone; a second connection fetches a snapshot, the shared one goes on.
    expect(sources()).toHaveLength(2)
    const catchUp = sources()[1]
    expect(b.seen()).toEqual(['2 state:open'])
    shared.emit('run.text', { t: '14' }, '14-0')
    shared.emit('run.text', { t: '15' }, '15-0')
    catchUp.open()
    catchUp.emit('snapshot', { s: 2 }, '14-0')
    expect(catchUp.readyState).toBe(FakeEventSource.CLOSED)
    expect(b.seen()).toEqual(['2 state:open', '2 snapshot 14-0', '2 run.text 15-0'])
    shared.emit('run.text', { t: '16' }, '16-0')
    expect(b.seen().at(-1)).toBe('2 run.text 16-0')
    expect(a.seen().filter((line) => line.includes('snapshot'))).toEqual(['1 snapshot 10-0'])
    // The fresh snapshot is kept again: a third tab replays it without a new connection.
    const c = tab(hub)
    c.subscribe(3)
    expect(c.seen()).toEqual(['3 state:open', '3 snapshot 14-0', '3 run.text 15-0', '3 run.text 16-0'])
    expect(sources()).toHaveLength(2)
  })

  it("the browser's own reconnect resumes the stream and keeps the record: a tab joining afterwards gets every event once", () => {
    const hub = setup()
    const a = tab(hub)
    a.subscribe(1)
    const source = sources()[0]
    source.open()
    source.emit('snapshot', { s: 1 }, '10-0')
    source.emit('run.text', { t: 'a' }, '11-0')
    source.interrupt()
    expect(a.seen().at(-1)).toBe('1 state:reconnecting')
    source.open()
    source.emit('run.text', { t: 'b' }, '12-0')
    const b = tab(hub)
    b.subscribe(2)
    expect(b.seen()).toEqual(['2 state:open', '2 snapshot 10-0', '2 run.text 11-0', '2 run.text 12-0'])
    expect(sources()).toHaveLength(1)
  })

  it('a stream closed for good asks every subscriber: one that declines leaves, and it opens again after the backoff when another asks, with a new snapshot', () => {
    const hub = setup()
    const a = tab(hub), b = tab(hub)
    a.subscribe(1)
    b.subscribe(2)
    const first = sources()[0]
    first.open()
    first.emit('snapshot', { s: 1 }, '10-0')
    a.clear()
    b.clear()
    first.fail()
    expect(a.seen()).toEqual(['1 state:reconnecting', '1 closed'])
    expect(b.seen()).toEqual(['2 state:reconnecting', '2 closed'])
    a.send({ kind: 'retry', sub: 1, retry: false })
    a.clear()
    expect(hub.openStreams()).toEqual({ [AGENT]: 1 })
    // A tab joining now is asked as well.
    const c = tab(hub)
    c.subscribe(3)
    expect(c.seen()).toEqual(['3 state:reconnecting', '3 closed'])
    b.send({ kind: 'retry', sub: 2, retry: true })
    c.send({ kind: 'retry', sub: 3, retry: true })
    expect(sources()).toHaveLength(1)
    vi.advanceTimersByTime(100)
    expect(sources()).toHaveLength(2)
    const second = sources()[1]
    second.open()
    second.emit('snapshot', { s: 2 }, '1-0')
    expect(b.seen().slice(-2)).toEqual(['2 state:open', '2 snapshot 1-0'])
    expect(c.seen().slice(-2)).toEqual(['3 state:open', '3 snapshot 1-0'])
    expect(a.seen()).toEqual([])
  })

  it('when every subscriber declines, the stream is closed', () => {
    const hub = setup()
    const a = tab(hub)
    a.subscribe(1)
    sources()[0].fail()
    a.send({ kind: 'retry', sub: 1, retry: false })
    expect(hub.openStreams()).toEqual({})
    vi.advanceTimersByTime(60_000)
    expect(sources()).toHaveLength(1)
  })

  it('a tab that stops answering pings is forgotten with its subscriptions; when it answers later it is told to subscribe again', () => {
    const hub = setup({ missedPings: 2, maxEvents: 5000, maxChars: 2_000_000 })
    const alive = tab(hub), gone = tab(hub)
    alive.subscribe(1, INBOX)
    gone.subscribe(1)
    gone.subscribe(2, INBOX)
    for (let i = 0; i < 3; i++) {
      hub.tick()
      alive.send({ kind: 'pong' })
    }
    expect(gone.messages.filter((m) => m.kind === 'ping')).toHaveLength(2)
    expect(hub.openStreams()).toEqual({ [INBOX]: 1 })
    expect(sources()[0].readyState).toBe(FakeEventSource.CLOSED)
    gone.clear()
    gone.send({ kind: 'pong' })
    expect(gone.seen()).toEqual(['reset'])
    gone.subscribe(1)
    expect(hub.openStreams()).toEqual({ [INBOX]: 1, [AGENT]: 1 })
  })

  it('a tab that says bye is forgotten at once', () => {
    const hub = setup()
    const a = tab(hub), b = tab(hub)
    a.subscribe(1)
    b.subscribe(1)
    a.send({ kind: 'bye' })
    expect(hub.openStreams()).toEqual({ [AGENT]: 1 })
    b.send({ kind: 'bye' })
    expect(hub.openStreams()).toEqual({})
    expect(sources()[0].readyState).toBe(FakeEventSource.CLOSED)
  })

  it('subscribing again under the same number replaces the subscription (a tab answering a reset)', () => {
    const hub = setup()
    const a = tab(hub)
    a.subscribe(1)
    a.subscribe(1)
    expect(hub.openStreams()).toEqual({ [AGENT]: 1 })
  })
})
