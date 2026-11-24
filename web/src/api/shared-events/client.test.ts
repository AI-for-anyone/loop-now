import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeEventSource } from '@/test/fake-event-source'
import { AGENT_EVENT_NAMES, connectAgentEvents, connectInboxEvents, INBOX_EVENT_NAMES, useOwnEventStreams, type AgentEvent, type ConnectionState, type InboxEvent, type StreamOptions } from '../events'
import { SharedStreams, type PageEvents, type SharedStreamsSetup, type WorkerLike, type WorkerPort } from './client'
import { EventHub, type HubPort } from './hub'
import type { TabMessage, WorkerMessage } from './protocol'
import { MultiplexedTransport, SET_CHANGE_MS } from './transport'

const ENDPOINT = 'http://127.0.0.1:8780/api/v1/events'

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

function recorder<E extends { type: string } = AgentEvent>() {
  const events: E[] = []
  const states: ConnectionState[] = []
  let retry = true
  const options: StreamOptions<E> = { onEvent: (event) => events.push(event), onState: (state) => states.push(state), onClosed: async () => retry }
  return { options, events, states, decline: () => (retry = false) }
}

const streams = (setup: Partial<SharedStreamsSetup> & Pick<SharedStreamsSetup, 'connect'>) => new SharedStreams({ inPage: () => fakeWorker().worker, base: 'http://127.0.0.1:8780/', ...setup })

beforeEach(() => FakeEventSource.reset())
afterEach(() => vi.useRealTimers())

describe("a tab's side of the shared streams", () => {
  it('subscribes through the worker by stream name at the absolute GET /events URL, and hands states and parsed events to the caller', () => {
    const w = fakeWorker()
    const s = streams({ connect: () => w.worker })
    const r = recorder()
    s.subscribe('agent:a1', AGENT_EVENT_NAMES, r.options)
    expect(w.sent).toEqual([{ kind: 'subscribe', sub: 1, endpoint: ENDPOINT, stream: 'agent:a1', names: [...AGENT_EVENT_NAMES], retryMinMs: 1000, retryMaxMs: 30000 }])
    w.say({ kind: 'state', sub: 1, state: 'open' })
    w.say({ kind: 'event', sub: 1, type: 'run.text', id: '5-0', data: '{"runId":"r"}' })
    w.say({ kind: 'event', sub: 1, type: 'run.text', id: '6-0', data: 'not json' })
    w.say({ kind: 'event', sub: 9, type: 'run.text', id: '7-0', data: '{}' })
    expect(r.states).toEqual(['connecting', 'open'])
    expect(r.events).toEqual([{ type: 'run.text', id: '5-0', data: { runId: 'r' } }])
  })

  it('answers pings, subscribes again to what is still open on reset, and tells the worker when a stream is closed', () => {
    const w = fakeWorker()
    const s = streams({ connect: () => w.worker })
    const close1 = s.subscribe('agent:a1', AGENT_EVENT_NAMES, recorder().options)
    const close2 = s.subscribe('inbox', INBOX_EVENT_NAMES, recorder<InboxEvent>().options)
    close1()
    close1()
    w.sent.splice(0)
    w.say({ kind: 'ping' })
    w.say({ kind: 'reset' })
    expect(w.sent.map((m) => (m.kind === 'subscribe' ? `subscribe ${m.sub} ${m.stream}` : m.kind))).toEqual(['pong', 'subscribe 2 inbox'])
    close2()
    expect(w.sent.at(-1)).toEqual({ kind: 'unsubscribe', sub: 2 })
  })

  it('a stream closed for good asks the caller whether to retry; one that declines is closed', async () => {
    const w = fakeWorker()
    const s = streams({ connect: () => w.worker })
    const keep = recorder(), leave = recorder()
    leave.decline()
    s.subscribe('agent:a1', AGENT_EVENT_NAMES, keep.options)
    s.subscribe('agent:a1', AGENT_EVENT_NAMES, leave.options)
    w.sent.splice(0)
    w.say({ kind: 'closed', sub: 1 })
    w.say({ kind: 'closed', sub: 2 })
    await vi.waitFor(() => expect(w.sent).toHaveLength(2))
    expect(w.sent).toEqual([{ kind: 'retry', sub: 1, retry: true }, { kind: 'retry', sub: 2, retry: false }])
    expect(leave.states.at(-1)).toBe('closed')
    expect(keep.states.at(-1)).toBe('connecting')
  })

  it('when the shared worker fails to start, the hub runs inside the page and every stream subscribes there', () => {
    const w = fakeWorker(), inPage = fakeWorker()
    const s = streams({ connect: () => w.worker, inPage: () => inPage.worker })
    s.subscribe('agent:a1', AGENT_EVENT_NAMES, recorder().options)
    w.fail()
    expect(inPage.sent).toMatchObject([{ kind: 'subscribe', sub: 1, stream: 'agent:a1' }])
    s.subscribe('inbox', INBOX_EVENT_NAMES, recorder<InboxEvent>().options)
    expect(inPage.sent.at(-1)).toMatchObject({ kind: 'subscribe', sub: 2, stream: 'inbox' })
    expect(w.sent).toHaveLength(1)
    const throwing = fakeWorker()
    streams({ connect: () => { throw new Error('blocked') }, inPage: () => throwing.worker }).subscribe('inbox', INBOX_EVENT_NAMES, recorder<InboxEvent>().options)
    expect(throwing.sent).toMatchObject([{ kind: 'subscribe', stream: 'inbox' }])
  })

  it('says bye on pagehide; restored from the back-forward cache it connects again and subscribes again; on resume it checks in', () => {
    const workers = [fakeWorker(), fakeWorker()]
    let n = 0
    const p = page()
    const s = streams({ connect: () => workers[n++].worker, page: p.page })
    s.subscribe('agent:a1', AGENT_EVENT_NAMES, recorder().options)
    p.fire('window:pagehide')
    expect(workers[0].sent.at(-1)).toEqual({ kind: 'bye' })
    p.fire('window:pageshow', Object.assign(new Event('pageshow'), { persisted: true }))
    expect(workers[1].sent).toMatchObject([{ kind: 'subscribe', sub: 1, stream: 'agent:a1' }])
    p.fire('document:resume')
    expect(workers[1].sent.at(-1)).toEqual({ kind: 'pong' })
    // Messages on the old port are ignored.
    const r = recorder()
    s.subscribe('agent:a2', AGENT_EVENT_NAMES, r.options)
    workers[0].say({ kind: 'state', sub: 2, state: 'open' })
    expect(r.states).toEqual(['connecting'])
  })
})

describe('a worker that went silent', () => {
  it('is checked once, then replaced: the tab connects to a new worker and subscribes again; a worker that answers the check is kept', () => {
    vi.useFakeTimers()
    const workers = [fakeWorker(), fakeWorker()]
    let n = 0
    const s = streams({ connect: () => workers[n++].worker, timing: { checkMs: 1000, silentMs: 3000 } })
    s.subscribe('agent:a1', AGENT_EVENT_NAMES, recorder().options)
    for (let i = 0; i < 5; i++) {
      vi.advanceTimersByTime(1000)
      workers[0].say({ kind: 'ping' })
    }
    expect(n).toBe(1)
    const pongs = () => workers[0].sent.filter((m) => m.kind === 'pong').length
    const before = pongs()
    vi.advanceTimersByTime(3000)
    expect(pongs()).toBe(before + 1)
    expect(n).toBe(1)
    workers[0].say({ kind: 'ping' })
    vi.advanceTimersByTime(2000)
    expect(n).toBe(1)
    vi.advanceTimersByTime(2000)
    expect(n).toBe(2)
    expect(workers[0].sent.at(-1)).toEqual({ kind: 'bye' })
    expect(workers[1].sent).toMatchObject([{ kind: 'subscribe', sub: 1, stream: 'agent:a1' }])
  })
})

// Tabs and the hub wired together: messages cross on a later task, as between a page and a shared worker.
describe('tabs through one hub', () => {
  function wire(hub: EventHub) {
    const tabPort: WorkerPort = { postMessage: (message) => queueMicrotask(() => hubPort.onmessage?.({ data: structuredClone(message) } as MessageEvent<TabMessage>)), onmessage: null }
    const hubPort: HubPort = { postMessage: (message) => queueMicrotask(() => tabPort.onmessage?.({ data: structuredClone(message) } as MessageEvent<WorkerMessage>)), onmessage: null }
    hub.attach(hubPort)
    return streams({ connect: () => ({ port: tabPort, addEventListener: () => {} }) })
  }
  const emit = (source: FakeEventSource, stream: string, event: string, data: unknown, id: string) => source.emit('event', { stream, id, event, data }, `${stream}=${id}`)

  it('share one GET /events connection for all their streams; a late tab gets the snapshot and every event; the connection closes when all have left', async () => {
    const hub = new EventHub(new MultiplexedTransport((url) => new FakeEventSource(url) as unknown as EventSource))
    const one = wire(hub), two = wire(hub)
    const a = recorder(), inbox = recorder<InboxEvent>(), b = recorder()
    const closeA = one.subscribe('agent:a1', AGENT_EVENT_NAMES, a.options)
    const closeInbox = one.subscribe('inbox', INBOX_EVENT_NAMES, inbox.options)
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1), { timeout: SET_CHANGE_MS * 5 })
    const source = FakeEventSource.latest()
    expect(new URL(source.url).searchParams.getAll('stream')).toEqual(['agent:a1', 'inbox'])
    source.open()
    emit(source, 'agent:a1', 'snapshot', { status: {} }, '10-0')
    emit(source, 'inbox', 'snapshot', { unreadTotal: 2 }, '3-0')
    emit(source, 'agent:a1', 'run.text', { runId: 'r', delta: 'hi' }, '11-0')
    const closeB = two.subscribe('agent:a1', AGENT_EVENT_NAMES, b.options)
    await vi.waitFor(() => expect(b.events).toHaveLength(2))
    emit(source, 'agent:a1', 'run.text', { runId: 'r', delta: '!' }, '12-0')
    await vi.waitFor(() => expect(b.events).toHaveLength(3))
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(a.events.map((e) => `${e.type} ${e.id}`)).toEqual(['snapshot 10-0', 'run.text 11-0', 'run.text 12-0'])
    expect(b.events.map((e) => `${e.type} ${e.id}`)).toEqual(['snapshot 10-0', 'run.text 11-0', 'run.text 12-0'])
    expect(inbox.events).toEqual([{ type: 'snapshot', id: '3-0', data: { unreadTotal: 2 } }])
    expect(b.states).toEqual(['connecting', 'open'])
    closeA()
    closeInbox()
    await new Promise((resolve) => setTimeout(resolve, SET_CHANGE_MS * 2))
    expect(source.readyState).toBe(FakeEventSource.CLOSED)
    const reconnected = FakeEventSource.latest()
    expect(new URL(reconnected.url).searchParams.getAll('stream')).toEqual(['agent:a1'])
    expect(new URL(reconnected.url).searchParams.get('cursor')).toBe('agent:a1=12-0')
    closeB()
    await vi.waitFor(() => expect(reconnected.readyState).toBe(FakeEventSource.CLOSED))
  })

  it("without a shared worker a tab still opens one GET /events for its streams (the hub runs in the page)", async () => {
    useOwnEventStreams(false)
    try {
      expect(typeof SharedWorker).toBe('undefined')
      const closeAgent = connectAgentEvents({ agentId: 'a1', ...recorder().options })
      const closeInbox = connectInboxEvents(recorder<InboxEvent>().options)
      await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1), { timeout: SET_CHANGE_MS * 5 })
      const url = new URL(FakeEventSource.latest().url)
      expect(url.pathname.endsWith('/events')).toBe(true)
      expect(url.searchParams.getAll('stream')).toEqual(['agent:a1', 'inbox'])
      closeAgent()
      closeInbox()
    } finally {
      useOwnEventStreams(true)
    }
  })
})
