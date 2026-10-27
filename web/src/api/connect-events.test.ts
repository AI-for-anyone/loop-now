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
  it('按事件名解析数据，忽略未知事件与无法解析的数据', () => {
    const { events, states } = connect()
    const source = FakeEventSource.latest()
    expect(source.url).toBe(agentEventsUrl('a1'))
    source.open()
    source.emit('run.text', { runId: 'r1', delta: '你' }, '7-0')
    source.emit('unknown.event', { x: 1 }, '8-0')
    source.addEventListener('run.text', () => {})
    expect(events).toEqual([{ type: 'run.text', id: '7-0', data: { runId: 'r1', delta: '你' } }])
    expect(states).toEqual(['connecting', 'open'])
  })

  it('浏览器自动重连期间显示重连状态', () => {
    const { states } = connect()
    const source = FakeEventSource.latest()
    source.open()
    source.interrupt()
    expect(states.at(-1)).toBe('reconnecting')
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('永久关闭后按退避间隔重建连接', async () => {
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

  it('不再重连时进入关闭状态', async () => {
    const { states } = connect(async () => false)
    FakeEventSource.latest().fail()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(states.at(-1)).toBe('closed')
  })

  it('关闭后不再处理事件', () => {
    const { events, close } = connect()
    const source = FakeEventSource.latest()
    close()
    source.emit('run.text', { runId: 'r1', delta: 'x' }, '1-0')
    expect(events).toEqual([])
    expect(source.readyState).toBe(FakeEventSource.CLOSED)
  })

  it('概览事件流不带范围参数，按事件名解析，断开后同样按退避间隔重连', async () => {
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
