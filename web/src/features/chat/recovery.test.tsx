import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeMessage, makeQueueItem, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'
import { FakeEventSource } from '@/test/fake-event-source'
import { server } from '@/test/server'

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']>, currentText: string | null, queuedRuns: Schemas['Run'][] = [], queueItems: Schemas['QueueItem'][] = []) {
  return { status: makeStatus(agentId, status), currentText, queuedRuns, queueItems }
}

describe('断线续传与快照', () => {
  it('断线期间显示重连状态；续传后文字连续，重复投递的事件不重复显示', async () => {
    const run = makeRun({ status: 'running' })
    const { agent, source, emit } = await renderChat({ messages: [makeMessage({ text: '写一首诗', run })] })
    act(() => source.open())
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, '床前'), '10-0')
    await emit('run.text', { runId: run.id, delta: '明月' }, '11-0')
    expect(await screen.findByText('床前明月')).toBeInTheDocument()

    act(() => source.interrupt())
    expect(await screen.findByText('连接中断，正在重连…')).toBeInTheDocument()

    // 浏览器带 Last-Event-ID 重连后，服务端从 11-0 之后续传；网络抖动导致 11-0 被重复投递一次。
    act(() => source.open())
    await emit('run.text', { runId: run.id, delta: '明月' }, '11-0')
    await emit('run.text', { runId: run.id, delta: '光' }, '12-0')
    expect(await screen.findByText('床前明月光')).toBeInTheDocument()
    expect(screen.getByText('实时连接正常')).toBeInTheDocument()
  })

  it('超出保留范围时服务端推送快照，以快照文字为准整体替换', async () => {
    const run = makeRun({ status: 'running' })
    const { agent, emit } = await renderChat({ messages: [makeMessage({ text: '问题', run })] })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, '旧的片段'), '10-0')
    expect(await screen.findByText('旧的片段')).toBeInTheDocument()
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, '断线期间已经写了很多'), '50-0')
    expect(await screen.findByText('断线期间已经写了很多')).toBeInTheDocument()
    expect(screen.queryByText('旧的片段')).toBeNull()
  })
})

describe('服务端重启与竞态', () => {
  it('重启后快照中原回复为服务端重启中断，排队消息随后开始', async () => {
    const interrupted = makeRun({ status: 'interrupted', endReason: 'server_restart' })
    const queued = makeRun({ status: 'queued', whenBusy: 'queue' })
    let history: Schemas['Message'][] = [
      makeMessage({ text: '长任务', run: makeRun({ ...interrupted, status: 'running', endReason: null }) }),
      makeMessage({ text: '排队的', run: queued }),
    ]
    const item = makeQueueItem({ runId: queued.id, text: '排队的' })
    const { agent, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), () => HttpResponse.json({ messages: history, hasMore: false })),
        http.get(apiUrl('/agents/:agentId/queue'), ({ params }) => HttpResponse.json({ agentId: String(params.agentId), items: [item] })),
      ],
    })
    expect(await screen.findByText('排队的')).toBeInTheDocument()
    // 服务端重启：历史中原回复已标为服务端重启中断，并保存了已产生的文字。
    history = [
      makeMessage({ text: '长任务', run: interrupted }),
      makeMessage({ role: 'assistant', text: '做了一半的回复', run: interrupted }),
      makeMessage({ text: '排队的', run: queued }),
    ]
    await emit('snapshot', snapshot(agent.id, { busy: true, queued: { interrupt: 0, normal: 1 } }, null, [queued], [item]), '1-0')
    expect(await screen.findByText('服务端重启中断')).toBeInTheDocument()
    expect(screen.getByText('做了一半的回复')).toBeInTheDocument()
    await emit('run.updated', { run: { ...queued, status: 'starting', phase: 'launching' } }, '2-0')
    await emit('queue.updated', { queuedRuns: [], items: [] }, '3-0')
    // 头部也显示当前阶段，这里只看对话里的那一轮。
    expect(await within(screen.getByRole('log')).findByText('启动中')).toBeInTheDocument()
    // 开始执行的消息离开排队列表，作为一轮对话出现在对话流中。
    expect(screen.queryByRole('region', { name: '排队消息' })).toBeNull()
    expect(within(screen.getByRole('log')).getByText('排队的')).toBeInTheDocument()
  })
})

describe('事件流永久关闭', () => {
  it('Agent 已删除（404）时不再重连，提示不存在', async () => {
    const { agent, source } = await renderChat()
    server.use(http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(errorBody('not_found'), { status: 404 })))
    act(() => source.fail())
    expect(await screen.findByText('该 Agent 不存在或已删除。')).toBeInTheDocument()
    expect(FakeEventSource.instances.filter((s) => s.url === source.url)).toHaveLength(1)
  })

  it('服务端出错（500）时按退避间隔重建连接，恢复后显示快照内容', async () => {
    const run = makeRun({ status: 'running' })
    const { agent, source } = await renderChat({ messages: [makeMessage({ text: '问题', run })] })
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      act(() => source.fail())
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1100)
      })
      await waitFor(() => expect(FakeEventSource.instances.filter((s) => s.url === source.url).length).toBe(2))
      const next = FakeEventSource.latest()
      await act(async () => {
        next.open()
        next.emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, '恢复后的文字'), '99-0')
        await vi.advanceTimersByTimeAsync(50)
      })
      expect(await screen.findByText('恢复后的文字')).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })
})
