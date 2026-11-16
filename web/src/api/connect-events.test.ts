import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeEventSource } from '@/test/fake-event-source'
import { agentEventsUrl, connectAgentEvents, connectOverviewEvents, overviewEventsUrl, type AgentEvent, type ConnectionState, type OverviewEvent } from './events'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

function connect(onClosed: () => Promise<boolean> = async () => true) {
  const events: AgentEvent[] = []
  const states: ConnectionState[] = []
  const close = connectAgentEvents({
    agentId: 'a1',
    onEvent: (e) => events.push(e),
    onState: (s) => states.push(s),
    onClosed,
    retryMinMs: 1000,
    retryMaxMs: 4000,
  })
  return { events, states, close }
}

describe('connectAgentEvents', () => {
  it('parses data by event name, ignoring unknown events and data that cannot be parsed', () => {
    const { events, states } = connect()
    const source = FakeEventSource.latest()
    expect(source.url).toBe(agentEventsUrl('a1'))
    source.open()
    source.emit('run.text', { runId: 'r1', delta: 'You' }, '7-0')
    source.emit('unknown.event', { x: 1 }, '8-0')
    source.addEventListener('run.text', () => {})
    expect(events).toEqual([{ type: 'run.text', id: '7-0', data: { runId: 'r1', delta: 'You' } }])
    expect(states).toEqual(['connecting', 'open'])
  })

  it('shows the reconnecting state while the browser reconnects on its own', () => {
    const { states } = connect()
    const source = FakeEventSource.latest()
    source.open()
    source.interrupt()
    expect(states.at(-1)).toBe('reconnecting')
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('after a permanent close, reconnects with backoff intervals', async () => {
    connect()
    FakeEventSource.latest().fail()
    await vi.advanceTimersByTimeAsync(999)
    expect(FakeEventSource.instances).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeEventSource.instances).toHaveLength(2)
    FakeEventSource.latest().fail()
    await vi.advanceTimersByTimeAsync(1999)
    expect(FakeEventSource.instances).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeEventSource.instances).toHaveLength(3)
  })

  it('goes to the closed state when it no longer reconnects', async () => {
    const { states } = connect(async () => false)
    FakeEventSource.latest().fail()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(states.at(-1)).toBe('closed')
  })

  it('handles no events after closing', () => {
    const { events, close } = connect()
    const source = FakeEventSource.latest()
    close()
    source.emit('run.text', { runId: 'r1', delta: 'x' }, '1-0')
    expect(events).toEqual([])
    expect(source.readyState).toBe(FakeEventSource.CLOSED)
  })

  it('the overview event stream has no scope parameter, parses by event name and also reconnects with backoff after a disconnect', async () => {
    const events: OverviewEvent[] = []
    connectOverviewEvents({ onEvent: (e) => events.push(e), onState: () => {}, onClosed: async () => true, retryMinMs: 1000 })
    const source = FakeEventSource.latest()
    expect(source.url).toBe(overviewEventsUrl())
    expect(source.url.endsWith('/overview/events')).toBe(true)
    source.emit('agent.removed', { agentId: 'a1', scope: 'platform', projectId: null }, '3-0')
    source.emit('run.text', { runId: 'r1', delta: 'x' }, '4-0')
    expect(events).toEqual([{ type: 'agent.removed', id: '3-0', data: { agentId: 'a1', scope: 'platform', projectId: null } }])
    source.fail()
    await vi.advanceTimersByTimeAsync(1000)
    expect(FakeEventSource.instances).toHaveLength(2)
  })
})
