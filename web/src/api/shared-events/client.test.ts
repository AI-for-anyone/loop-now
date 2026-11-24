import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeEventSource } from '@/test/fake-event-source'
import { AGENT_EVENT_NAMES, agentEventsUrl, connectAgentEvents, type AgentEvent, type ConnectionState, type StreamOptions } from '../events'
import { SharedStreams, sharedStreams, type PageEvents, type WorkerLike, type WorkerPort } from './client'
import { EventHub, type HubPort } from './hub'
import type { TabMessage, WorkerMessage } from './protocol'
import { EventSourceTransport } from './transport'

const URL_A = 'http://127.0.0.1:8780/api/v1/agents/a1/events'

// A worker whose port records what the tab sends; the test plays the worker's side.
function fakeWorker() {
  const sent: TabMessage[] = []
  const errors: (() => void)[] = []
  const port: WorkerPort = { postMessage: (message) => sent.push(message), onmessage: null }
  const worker: WorkerLike = { port, addEventListener: (_type, listener) => errors.push(listener) }
  return { worker, port, sent, fail: () => errors.forEach((listener) => listener()), say: (message: WorkerMessage) => port.onmessage?.({ data: message } as MessageEvent<WorkerMessage>) }
}

function page() {
  const listeners = new Map<string, ((event: Event) => void)[]>()
  const target = (prefix: string): PageEvents => ({ addEventListener: (type, listener) => listeners.set(prefix + type, [...(listeners.get(prefix + type) ?? []), listener]) })
  const fire = (name: string, event: Event = new Event(name.split(':')[1])) => listeners.get(name)?.forEach((listener) => listener(event))
  return { page: { window: target('window:'), document: { ...target('document:'), visibilityState: 'visible' } }, fire }
}

function recorder() {
  const events: AgentEvent[] = []
  const states: ConnectionState[] = []
  let retry = true
  const options: StreamOptions<AgentEvent> = { onEvent: (event) => events.push(event), onState: (state) => states.push(state), onClosed: async () => retry }
  return { options, events, states, decline: () => (retry = false) }
}

beforeEach(() => FakeEventSource.reset())
afterEach(() => vi.useRealTimers())

describe("a tab's side of the shared streams", () => {
  it('subscribes through the worker with the absolute URL, and hands states and parsed events to the caller', () => {
    const w = fakeWorker()
    const streams = new SharedStreams(() => w.worker, vi.fn(), undefined, 'http://127.0.0.1:8780/console/')
    const r = recorder()
    streams.subscribe('/api/v1/agents/a1/events', AGENT_EVENT_NAMES, r.options)
    expect(w.sent).toEqual([{ kind: 'subscribe', sub: 1, url: URL_A, names: [...AGENT_EVENT_NAMES], retryMinMs: 1000, retryMaxMs: 30000 }])
    w.say({ kind: 'state', sub: 1, state: 'open' })
    w.say({ kind: 'event', sub: 1, type: 'run.text', id: '5-0', data: '{"runId":"r"}' })
    w.say({ kind: 'event', sub: 1, type: 'run.text', id: '6-0', data: 'not json' })
    w.say({ kind: 'event', sub: 9, type: 'run.text', id: '7-0', data: '{}' })
    expect(r.states).toEqual(['connecting', 'open'])
    expect(r.events).toEqual([{ type: 'run.text', id: '5-0', data: { runId: 'r' } }])
  })

  it('answers pings, subscribes again to what is still open on reset, and tells the worker when a stream is closed', () => {
    const w = fakeWorker()
    const streams = new SharedStreams(() => w.worker, vi.fn())
    const close1 = streams.subscribe(URL_A, AGENT_EVENT_NAMES, recorder().options)
    const close2 = streams.subscribe(URL_A, AGENT_EVENT_NAMES, recorder().options)
    close1()
    close1()
    w.sent.splice(0)
    w.say({ kind: 'ping' })
    w.say({ kind: 'reset' })
    expect(w.sent.map((m) => (m.kind === 'subscribe' ? `subscribe ${m.sub}` : m.kind))).toEqual(['pong', 'subscribe 2'])
    close2()
    expect(w.sent.at(-1)).toEqual({ kind: 'unsubscribe', sub: 2 })
  })

  it('a stream closed for good asks the caller whether to retry; one that declines is closed', async () => {
    const w = fakeWorker()
    const streams = new SharedStreams(() => w.worker, vi.fn())
    const keep = recorder(), leave = recorder()
    leave.decline()
    streams.subscribe(URL_A, AGENT_EVENT_NAMES, keep.options)
    streams.subscribe(URL_A, AGENT_EVENT_NAMES, leave.options)
    w.sent.splice(0)
    w.say({ kind: 'closed', sub: 1 })
    w.say({ kind: 'closed', sub: 2 })
    await vi.waitFor(() => expect(w.sent).toHaveLength(2))
    expect(w.sent).toEqual([{ kind: 'retry', sub: 1, retry: true }, { kind: 'retry', sub: 2, retry: false }])
    expect(leave.states.at(-1)).toBe('closed')
    expect(keep.states.at(-1)).toBe('connecting')
  })

  it('when the worker fails to start, every stream becomes one the tab holds itself', () => {
    const w = fakeWorker()
    const local = vi.fn(() => () => {})
    const streams = new SharedStreams(() => w.worker, local)
    const r = recorder()
    streams.subscribe(URL_A, AGENT_EVENT_NAMES, r.options)
    w.fail()
    expect(local).toHaveBeenCalledWith(URL_A, [...AGENT_EVENT_NAMES], r.options)
    streams.subscribe(URL_A, AGENT_EVENT_NAMES, r.options)
    expect(local).toHaveBeenCalledTimes(2)
    const throwing = new SharedStreams(() => {
      throw new Error('blocked')
    }, local)
    throwing.subscribe(URL_A, AGENT_EVENT_NAMES, r.options)
    expect(local).toHaveBeenCalledTimes(3)
  })

  it('says bye on pagehide; restored from the back-forward cache it connects again and subscribes again; on resume it checks in', () => {
    const workers = [fakeWorker(), fakeWorker()]
    let n = 0
    const p = page()
    const streams = new SharedStreams(() => workers[n++].worker, vi.fn(), p.page)
    streams.subscribe(URL_A, AGENT_EVENT_NAMES, recorder().options)
    p.fire('window:pagehide')
    expect(workers[0].sent.at(-1)).toEqual({ kind: 'bye' })
    const restored = Object.assign(new Event('pageshow'), { persisted: true })
    p.fire('window:pageshow', restored)
    expect(workers[1].sent).toMatchObject([{ kind: 'subscribe', sub: 1, url: URL_A }])
    p.fire('document:resume')
    expect(workers[1].sent.at(-1)).toEqual({ kind: 'pong' })
    // Messages on the old port are ignored.
    const r = recorder()
    streams.subscribe(URL_A, AGENT_EVENT_NAMES, r.options)
    workers[0].say({ kind: 'state', sub: 2, state: 'open' })
    expect(r.states).toEqual(['connecting'])
  })

  it('without shared workers (as in these tests), connectAgentEvents opens its own EventSource as before', () => {
    expect(typeof SharedWorker).toBe('undefined')
    expect(sharedStreams()).toBeNull()
    const close = connectAgentEvents({ agentId: 'a1', ...recorder().options })
    expect(FakeEventSource.latest().url).toBe(agentEventsUrl('a1'))
    close()
  })
})

// Two tabs and the worker, wired together: messages cross the ports on a later task, as between a page and a shared worker.
describe('two tabs through one worker', () => {
  function wire(hub: EventHub) {
    const tabPort: WorkerPort = { postMessage: (message) => queueMicrotask(() => hubPort.onmessage?.({ data: structuredClone(message) } as MessageEvent<TabMessage>)), onmessage: null }
    const hubPort: HubPort = { postMessage: (message) => queueMicrotask(() => tabPort.onmessage?.({ data: structuredClone(message) } as MessageEvent<WorkerMessage>)), onmessage: null }
    hub.attach(hubPort)
    return new SharedStreams(() => ({ port: tabPort, addEventListener: () => {} }), vi.fn())
  }
  const flush = () => new Promise((resolve) => setTimeout(resolve, 0))

  it('share one connection: each gets the snapshot and every event, and the connection closes when both have left', async () => {
    const hub = new EventHub(new EventSourceTransport((url) => new FakeEventSource(url) as unknown as EventSource))
    const one = wire(hub), two = wire(hub)
    const a = recorder(), b = recorder()
    const closeA = one.subscribe(URL_A, AGENT_EVENT_NAMES, a.options)
    await flush()
    const source = FakeEventSource.latest()
    source.open()
    source.emit('snapshot', { status: {} }, '10-0')
    source.emit('run.text', { runId: 'r', delta: 'hi' }, '11-0')
    await flush()
    const closeB = two.subscribe(URL_A, AGENT_EVENT_NAMES, b.options)
    await flush()
    source.emit('run.text', { runId: 'r', delta: '!' }, '12-0')
    await flush()
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(a.events.map((e) => `${e.type} ${e.id}`)).toEqual(['snapshot 10-0', 'run.text 11-0', 'run.text 12-0'])
    expect(b.events.map((e) => `${e.type} ${e.id}`)).toEqual(['snapshot 10-0', 'run.text 11-0', 'run.text 12-0'])
    expect(b.states).toEqual(['connecting', 'open'])
    closeA()
    await flush()
    expect(source.readyState).not.toBe(FakeEventSource.CLOSED)
    closeB()
    await flush()
    expect(source.readyState).toBe(FakeEventSource.CLOSED)
  })
})

describe('a worker that went silent', () => {
  it('is checked once, then replaced: the tab connects to a new worker and subscribes again; a worker that answers the check is kept', () => {
    vi.useFakeTimers()
    const workers = [fakeWorker(), fakeWorker()]
    let n = 0
    const streams = new SharedStreams(() => workers[n++].worker, vi.fn(), undefined, undefined, { checkMs: 1000, silentMs: 3000 })
    streams.subscribe(URL_A, AGENT_EVENT_NAMES, recorder().options)
    // Pings keep it alive.
    for (let i = 0; i < 5; i++) {
      vi.advanceTimersByTime(1000)
      workers[0].say({ kind: 'ping' })
    }
    expect(n).toBe(1)
    // Silent: first a check (a pong of its own, which a living worker answers with its next message)...
    const pongs = () => workers[0].sent.filter((m) => m.kind === 'pong').length
    const before = pongs()
    vi.advanceTimersByTime(3000)
    expect(pongs()).toBe(before + 1)
    expect(n).toBe(1)
    // ...answered: kept.
    workers[0].say({ kind: 'ping' })
    vi.advanceTimersByTime(2000)
    expect(n).toBe(1)
    // Silent again, and the check goes unanswered: a new worker, the same subscriptions.
    vi.advanceTimersByTime(2000)
    expect(n).toBe(2)
    expect(workers[0].sent.at(-1)).toEqual({ kind: 'bye' })
    expect(workers[1].sent).toMatchObject([{ kind: 'subscribe', sub: 1, url: URL_A }])
  })
})
