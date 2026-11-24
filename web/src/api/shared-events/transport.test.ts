import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeEventSource } from '@/test/fake-event-source'
import { streamKey, type StreamRequest } from './protocol'
import { MAX_STREAMS, MultiplexedTransport, SET_CHANGE_MS, type StreamSink } from './transport'

const ENDPOINT = 'http://127.0.0.1:8780/api/v1/events'
const request = (stream: string, names = ['snapshot', 'run.text']): StreamRequest => ({ endpoint: ENDPOINT, stream, names, retryMinMs: 100, retryMaxMs: 1000 })

// What one stream's sink was told, compactly.
function sink() {
  const seen: string[] = []
  const s: StreamSink = {
    restart: () => seen.push('restart'),
    event: (type, id, data) => seen.push(`${type} ${id} ${data}`),
    state: (state) => seen.push(`state:${state}`),
    closed: () => seen.push('closed'),
  }
  return { sink: s, seen, clear: () => seen.splice(0) }
}

// An event as GET /events sends it: type `event`, the stream's own event inside, the combined cursor as the SSE id.
const emit = (source: FakeEventSource, stream: string, event: string, data: unknown, id: string) => source.emit('event', { stream, id, event, data }, `${stream}=${id}`)
const streamsOf = (source: FakeEventSource) => new URL(source.url).searchParams.getAll('stream')
const cursorOf = (source: FakeEventSource) => new URL(source.url).searchParams.get('cursor')

function setup() {
  return new MultiplexedTransport((url) => new FakeEventSource(url) as unknown as EventSource)
}
const settle = () => vi.advanceTimersByTime(SET_CHANGE_MS)

beforeEach(() => {
  FakeEventSource.reset()
  vi.useFakeTimers()
})
afterEach(() => vi.useRealTimers())

describe('one GET /events connection for every stream', () => {
  it('streams asked for together share one connection; a new stream starts without a position, its snapshot first, and only its named events pass', () => {
    const t = setup()
    const agent = sink(), inbox = sink()
    t.open(request('agent:a1'), agent.sink)
    t.open(request('inbox', ['snapshot']), inbox.sink)
    expect(FakeEventSource.instances).toHaveLength(0)
    settle()
    expect(FakeEventSource.instances).toHaveLength(1)
    const source = FakeEventSource.latest()
    expect(source.url.startsWith(`${ENDPOINT}?`)).toBe(true)
    expect(streamsOf(source)).toEqual(['agent:a1', 'inbox'])
    expect(cursorOf(source)).toBeNull()
    source.open()
    emit(source, 'agent:a1', 'snapshot', { s: 1 }, '10-0')
    emit(source, 'inbox', 'snapshot', { unreadTotal: 0 }, '0-0')
    emit(source, 'agent:a1', 'run.text', { t: 'x' }, '11-0')
    emit(source, 'agent:a1', 'unknown.event', {}, '12-0')
    emit(source, 'task:t9', 'snapshot', {}, '1-0')
    expect(agent.seen).toEqual(['restart', 'state:connecting', 'state:open', 'snapshot 10-0 {"s":1}', 'run.text 11-0 {"t":"x"}'])
    expect(inbox.seen).toEqual(['restart', 'state:connecting', 'state:open', 'snapshot 0-0 {"unreadTotal":0}'])
  })

  it('a change of the set waits a moment, so several make one reconnect; the streams that go on resume from their positions and see no state change', () => {
    const t = setup()
    const agent = sink(), inbox = sink(), other = sink()
    t.open(request('agent:a1'), agent.sink)
    t.open(request('inbox'), inbox.sink)
    settle()
    const first = FakeEventSource.latest()
    first.open()
    emit(first, 'agent:a1', 'snapshot', {}, '10-0')
    emit(first, 'agent:a1', 'run.text', {}, '12-0')
    emit(first, 'inbox', 'snapshot', {}, '5-0')
    agent.clear()
    inbox.clear()
    // A tab moves from one Agent to another: one leaves, one joins, in quick succession.
    t.open(request('agent:a2'), other.sink)
    t.close(streamKey(request('agent:a1')))
    settle()
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(first.readyState).toBe(FakeEventSource.CLOSED)
    const second = FakeEventSource.latest()
    expect(streamsOf(second)).toEqual(['inbox', 'agent:a2'])
    expect(cursorOf(second)).toBe('inbox=5-0')
    second.open()
    emit(second, 'inbox', 'run.text', {}, '6-0')
    expect(inbox.seen).toEqual(['run.text 6-0 {}'])
    expect(other.seen).toEqual(['restart', 'state:connecting', 'state:open'])
    expect(agent.seen).toEqual([])
    // A join and a leave that cancel out reconnect nothing.
    t.open(request('agent:a3'), sink().sink)
    t.close(streamKey(request('agent:a3')))
    settle()
    expect(FakeEventSource.instances).toHaveLength(2)
  })

  it('a stream.error closes that stream alone; when retried after its backoff, it comes back with a snapshot and the others resume', () => {
    const t = setup()
    const agent = sink(), inbox = sink()
    t.open(request('agent:gone'), agent.sink)
    t.open(request('inbox'), inbox.sink)
    settle()
    const first = FakeEventSource.latest()
    first.open()
    emit(first, 'inbox', 'snapshot', {}, '5-0')
    first.emit('stream.error', { stream: 'agent:gone', error: { code: 'not_found', message: 'gone' } }, 'inbox=5-0')
    expect(agent.seen.at(-1)).toBe('closed')
    emit(first, 'agent:gone', 'run.text', {}, '1-0')
    expect(agent.seen.at(-1)).toBe('closed')
    emit(first, 'inbox', 'run.text', {}, '6-0')
    expect(inbox.seen.at(-1)).toBe('run.text 6-0 {}')
    t.retry(streamKey(request('agent:gone')))
    vi.advanceTimersByTime(99)
    expect(FakeEventSource.instances).toHaveLength(1)
    vi.advanceTimersByTime(1 + SET_CHANGE_MS)
    const second = FakeEventSource.latest()
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(streamsOf(second)).toEqual(['agent:gone', 'inbox'])
    expect(cursorOf(second)).toBe('inbox=6-0')
    // It was already reported closed (and reconnecting): it starts again from a snapshot, and opens with the connection.
    expect(agent.seen.slice(-2)).toEqual(['closed', 'restart'])
    second.open()
    expect(agent.seen.at(-1)).toBe('state:open')
  })

  it('the connection closing for good closes every stream; each comes back when retried, starting with a snapshot', () => {
    const t = setup()
    const agent = sink(), inbox = sink()
    t.open(request('agent:a1'), agent.sink)
    t.open(request('inbox'), inbox.sink)
    settle()
    const first = FakeEventSource.latest()
    first.open()
    emit(first, 'agent:a1', 'snapshot', {}, '10-0')
    emit(first, 'inbox', 'snapshot', {}, '5-0')
    first.fail()
    expect(agent.seen.at(-1)).toBe('closed')
    expect(inbox.seen.at(-1)).toBe('closed')
    t.retry(streamKey(request('agent:a1')))
    t.retry(streamKey(request('inbox')))
    vi.advanceTimersByTime(100 + SET_CHANGE_MS)
    const second = FakeEventSource.latest()
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(streamsOf(second)).toEqual(['agent:a1', 'inbox'])
    expect(cursorOf(second)).toBeNull()
    expect(agent.seen.slice(-2)).toEqual(['closed', 'restart'])
    second.open()
    expect(agent.seen.at(-1)).toBe('state:open')
  })

  it("the browser's own reconnect reports reconnecting, then open, and nothing else", () => {
    const t = setup()
    const agent = sink()
    t.open(request('agent:a1'), agent.sink)
    settle()
    const source = FakeEventSource.latest()
    source.open()
    agent.clear()
    source.interrupt()
    source.open()
    emit(source, 'agent:a1', 'run.text', {}, '11-0')
    expect(agent.seen).toEqual(['state:reconnecting', 'state:open', 'run.text 11-0 {}'])
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('a fresh snapshot for a late tab comes over a short connection of that stream alone, which closes once it has it', () => {
    const t = setup()
    t.open(request('agent:a1'), sink().sink)
    settle()
    const snapshots: string[] = []
    const stop = t.snapshot(request('agent:a1'), (id, data) => snapshots.push(`${id} ${data}`))
    const short = FakeEventSource.latest()
    expect(streamsOf(short)).toEqual(['agent:a1'])
    expect(cursorOf(short)).toBeNull()
    short.open()
    emit(short, 'agent:a1', 'snapshot', { s: 2 }, '20-0')
    expect(snapshots).toEqual(['20-0 {"s":2}'])
    expect(short.readyState).toBe(FakeEventSource.CLOSED)
    stop()
  })

  it('the connection closes when its last stream leaves; it carries at most 64 streams, and the rest join as others leave', () => {
    const t = setup()
    for (let i = 0; i <= MAX_STREAMS; i++) t.open(request(`agent:${i}`), sink().sink)
    settle()
    const first = FakeEventSource.latest()
    expect(streamsOf(first)).toHaveLength(MAX_STREAMS)
    expect(streamsOf(first)).not.toContain(`agent:${MAX_STREAMS}`)
    t.close(streamKey(request('agent:0')))
    settle()
    expect(streamsOf(FakeEventSource.latest())).toContain(`agent:${MAX_STREAMS}`)
    for (let i = 1; i <= MAX_STREAMS; i++) t.close(streamKey(request(`agent:${i}`)))
    expect(FakeEventSource.latest().readyState).toBe(FakeEventSource.CLOSED)
    settle()
    expect(FakeEventSource.instances).toHaveLength(2)
  })
})
