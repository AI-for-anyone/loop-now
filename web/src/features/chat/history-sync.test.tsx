import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, makeMessage, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'

// 历史请求进行中（首次加载、加载更早的消息、快照触发的重新获取）时到达的事件与发送结果不能丢。

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((r) => (resolve = r))
  return { promise, resolve }
}

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']>, currentText: string | null = null) {
  return { status: makeStatus(agentId, status), currentText, queuedRuns: [], queueItems: [] }
}

function turn(text: string, reply: string) {
  const run = makeRun()
  return [makeMessage({ text, run }), makeMessage({ role: 'assistant', text: reply, run })]
}

// 一轮正在回复的对话，以及它结束时事件流给出的执行与回复。
function replyingTurn(text: string, reply: string) {
  const run = makeRun({ status: 'running', endedAt: null })
  const ended: Schemas['Run'] = { ...run, status: 'completed', endedAt: '2026-10-12T08:00:30Z' }
  return {
    run,
    question: makeMessage({ text, run }),
    ended,
    reply: makeMessage({ role: 'assistant', text: reply, run: ended }),
  }
}

const settle = () => act(() => new Promise((resolve) => setTimeout(resolve, 50)))

describe('历史请求进行中到达的事件', () => {
  it('加载更早的消息时回复结束：请求结果不覆盖结束事件', async () => {
    const { question, ended, reply } = replyingTurn('新问题', '最终回复')
    const gate = deferred()
    const { emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
          if (!new URL(request.url).searchParams.get('before')) return HttpResponse.json({ messages: [question], hasMore: true })
          await gate.promise
          return HttpResponse.json({ messages: turn('旧问题', '旧回答'), hasMore: false })
        }),
      ],
    })
    expect(await screen.findByText('新问题')).toBeInTheDocument()
    fireEvent.scroll(screen.getByRole('log'))
    expect(await screen.findByText('正在加载更早的消息…')).toBeInTheDocument()
    await emit('run.ended', { run: ended, message: reply })
    gate.resolve()
    expect(await screen.findByText('旧问题')).toBeInTheDocument()
    expect(screen.getByText('最终回复')).toBeInTheDocument()
    expect(screen.queryByText('回复中')).toBeNull()
  })

  it('快照触发的重新获取进行中回复结束：以结束事件为准', async () => {
    const { run, question, ended, reply } = replyingTurn('问题', '最终回复')
    const gate = deferred()
    let calls = 0
    const { agent, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async () => {
          calls += 1
          // 重新获取在执行结束之前读取了历史，到达时却晚于结束事件。
          if (calls > 1) await gate.promise
          return HttpResponse.json({ messages: [question], hasMore: false })
        }),
      ],
    })
    expect(await screen.findByText('问题')).toBeInTheDocument()
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, '写到'))
    await waitFor(() => expect(calls).toBe(2))
    await emit('run.ended', { run: ended, message: reply })
    gate.resolve()
    await settle()
    expect(screen.getByText('最终回复')).toBeInTheDocument()
    expect(screen.queryByText('回复中')).toBeNull()
  })

  it('首次加载完成之前到达的事件不丢失', async () => {
    const { question, ended, reply } = replyingTurn('问题', '最终回复')
    const gate = deferred()
    const { emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async () => {
          await gate.promise
          return HttpResponse.json({ messages: [question], hasMore: false })
        }),
      ],
    })
    // 排队托盘只跟随服务端的排队列表；这里用已开始的下一轮检验事件不丢失。
    const later = makeMessage({ text: '后来的问题', run: makeRun({ status: 'starting', phase: 'launching', startedAt: '2026-10-12T09:00:00Z', endedAt: null }) })
    await emit('run.ended', { run: ended, message: reply })
    await emit('message.created', { message: later })
    gate.resolve()
    expect(await screen.findByText('问题')).toBeInTheDocument()
    expect(screen.getByText('最终回复')).toBeInTheDocument()
    expect(screen.getByText('后来的问题')).toBeInTheDocument()
    expect(screen.queryByText('回复中')).toBeNull()
  })

  it('首次加载进行中收到快照：快照之后重新获取历史', async () => {
    const { question, ended, reply } = replyingTurn('问题', '最终回复')
    const gate = deferred()
    let calls = 0
    const { agent, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async () => {
          calls += 1
          if (calls === 1) {
            // 首次加载在执行结束之前读取；执行在快照之前结束，事件流不会再发结束事件。
            await gate.promise
            return HttpResponse.json({ messages: [question], hasMore: false })
          }
          return HttpResponse.json({ messages: [{ ...question, run: ended }, reply], hasMore: false })
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, {}))
    gate.resolve()
    expect(await screen.findByText('最终回复')).toBeInTheDocument()
    await settle()
    expect(calls).toBe(2)
    expect(screen.getByText('最终回复')).toBeInTheDocument()
    expect(screen.queryByText('回复中')).toBeNull()
  })

  it('发送结果在重新获取进行中到达时不丢失', async () => {
    const gate = deferred()
    let calls = 0
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async () => {
          calls += 1
          if (calls > 1) await gate.promise
          return HttpResponse.json({ messages: turn('之前的问题', '之前的回答'), hasMore: false })
        }),
        http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
          const body = (await request.json()) as Schemas['SendMessageInput']
          const run = makeRun({ status: 'starting', phase: 'launching', endedAt: null })
          return HttpResponse.json({ message: makeMessage({ text: body.text, run }), run }, { status: 202 })
        }),
      ],
    })
    expect(await screen.findByText('之前的回答')).toBeInTheDocument()
    // 事件流断开期间（收不到 message.created）快照触发重新获取，此时发送了一条消息。
    await emit('snapshot', snapshot(agent.id, {}))
    await waitFor(() => expect(calls).toBe(2))
    await user.type(screen.getByLabelText('消息'), '新消息')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(screen.getByLabelText('消息')).toHaveValue(''))
    gate.resolve()
    await settle()
    expect(screen.getByText('新消息')).toBeInTheDocument()
  })
})

describe('加载更早的消息与重新获取', () => {
  it('快照要求的重新获取进行中时不加载更早的消息，以免取消这次对账', async () => {
    const gate = deferred()
    let calls = 0
    let olderCalls = 0
    const first = turn('旧问题', '旧回答')
    const missed = turn('断线期间的问题', '断线期间的回答')
    const { agent, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
          if (new URL(request.url).searchParams.get('before')) {
            olderCalls += 1
            return HttpResponse.json({ messages: turn('更早的问题', '更早的回答'), hasMore: false })
          }
          calls += 1
          if (calls === 1) return HttpResponse.json({ messages: first, hasMore: true })
          await gate.promise
          return HttpResponse.json({ messages: [...first, ...missed], hasMore: true })
        }),
      ],
    })
    expect(await screen.findByText('旧回答')).toBeInTheDocument()
    await emit('snapshot', snapshot(agent.id, {}))
    await waitFor(() => expect(calls).toBe(2))
    fireEvent.scroll(screen.getByRole('log'))
    await settle()
    gate.resolve()
    expect(await screen.findByText('断线期间的回答')).toBeInTheDocument()
    expect(olderCalls).toBe(0)
    // 重新获取完成后可以继续加载更早的消息。
    fireEvent.scroll(screen.getByRole('log'))
    expect(await screen.findByText('更早的回答')).toBeInTheDocument()
    expect(screen.getByText('断线期间的回答')).toBeInTheDocument()
    expect(olderCalls).toBe(1)
  })
})
