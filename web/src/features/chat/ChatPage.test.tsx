import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultAgentPath, errorBody, makeAgent, makeMessage, makeQueueItem, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'
import { FakeEventSource } from '@/test/fake-event-source'

function turn(text: string, reply: string, overrides: Partial<Schemas['Run']> = {}, epoch = 1) {
  const run = makeRun(overrides)
  return [makeMessage({ text, run, sessionEpoch: epoch }), makeMessage({ role: 'assistant', text: reply, run, sessionEpoch: epoch })]
}

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']>, currentText: string | null = null, queuedRuns: Schemas['Run'][] = [], queueItems: Schemas['QueueItem'][] = []) {
  return { status: makeStatus(agentId, status), currentText, queuedRuns, queueItems }
}

function acceptSend(sent: Schemas['SendMessageInput'][], runOverrides: Partial<Schemas['Run']> = {}) {
  return http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
    const body = (await request.json()) as Schemas['SendMessageInput']
    sent.push(body)
    const run = makeRun({ status: body.whenBusy ? 'queued' : 'starting', whenBusy: body.whenBusy ?? null, ...runOverrides })
    return HttpResponse.json({ message: makeMessage({ text: body.text, run }), run }, { status: 202 })
  })
}

// 发送请求停在闸门上，由测试放行；fail 为真时放行后返回 500。每次放行后为下一次请求换一道新闸门。
function gatedSend(sent: Schemas['SendMessageInput'][], { fail = false } = {}) {
  let open = () => {}
  let gate = Promise.resolve()
  const close = () => {
    gate = new Promise<void>((resolve) => (open = resolve))
  }
  close()
  const handler = http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
    const body = (await request.json()) as Schemas['SendMessageInput']
    sent.push(body)
    await gate
    if (fail) return HttpResponse.json(errorBody('internal_error', '出错了'), { status: 500 })
    const run = makeRun({ status: body.whenBusy ? 'queued' : 'starting', whenBusy: body.whenBusy ?? null, endedAt: null })
    return HttpResponse.json({ message: makeMessage({ text: body.text, run }), run }, { status: 202 })
  })
  return {
    handler,
    release: () => {
      open()
      close()
    },
  }
}

describe('对话历史与连续对话', () => {
  it('向上滚动时加载更早的消息，不重复不缺失', async () => {
    const older = turn('第一问', '第一答')
    const newer = turn('第二问', '第二答')
    let calls = 0
    const { user } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), ({ request }) => {
          calls += 1
          const before = new URL(request.url).searchParams.get('before')
          return before === newer[0].id
            ? HttpResponse.json({ messages: older, hasMore: false })
            : HttpResponse.json({ messages: newer, hasMore: true })
        }),
      ],
    })
    expect(await screen.findByText('第二问')).toBeInTheDocument()
    fireEvent.scroll(screen.getByRole('log'))
    expect(await screen.findByText('第一问')).toBeInTheDocument()
    const log = screen.getByRole('log')
    const texts = within(log).getAllByText(/^第.[问答]$/).map((el) => el.textContent)
    expect(texts).toEqual(['第一问', '第一答', '第二问', '第二答'])
    expect(calls).toBe(2)
    expect(screen.queryByRole('button', { name: '加载更早的消息' })).toBeNull()
    void user
  })

  it('刷新后历史仍在，可以继续发送', async () => {
    const history = [...turn('你好', '你好呀'), ...turn('再见', '回见')]
    const first = await renderChat({ messages: history })
    expect(await screen.findByText('回见')).toBeInTheDocument()
    first.unmount()
    const sent: Schemas['SendMessageInput'][] = []
    const { user } = await renderChat({ agent: first.agent, messages: history, handlers: [acceptSend(sent)] })
    expect(await screen.findByText('回见')).toBeInTheDocument()
    await user.type(screen.getByLabelText('消息'), '第三条')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(sent).toEqual([{ text: '第三条' }]))
    expect(screen.getByLabelText('消息')).toHaveValue('')
  })

  it('会话重开前后的消息之间显示分界', async () => {
    await renderChat({ messages: [...turn('旧会话', '旧回复', {}, 1), ...turn('新会话', '新回复', {}, 2)] })
    expect(await screen.findByRole('separator')).toHaveTextContent('会话已重开，之前的内容 Agent 不再记得')
  })
})

describe('发送消息', () => {
  it('输入法组字时按回车不发送', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    await renderChat({ handlers: [acceptSend(sent)] })
    const input = screen.getByLabelText('消息')
    fireEvent.change(input, { target: { value: 'nihao' } })
    fireEvent.keyDown(input, { key: 'Enter', keyCode: 229, isComposing: true })
    await new Promise((r) => setTimeout(r, 50))
    expect(sent).toEqual([])
    fireEvent.keyDown(input, { key: 'Enter' })
    await waitFor(() => expect(sent).toEqual([{ text: 'nihao' }]))
  })

  it('不发送空消息', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const { user } = await renderChat({ handlers: [acceptSend(sent)] })
    await user.type(screen.getByLabelText('消息'), '   ')
    await user.click(screen.getByRole('button', { name: '发送' }))
    expect(await screen.findByText('消息不能为空')).toBeInTheDocument()
    expect(sent).toEqual([])
  })

  it('发送成功后去掉已发出的部分，只留下发送期间新输入的文字', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const send = gatedSend(sent)
    const { user } = await renderChat({ handlers: [send.handler] })
    const input = screen.getByLabelText('消息')
    await user.type(input, '第一条')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(sent).toEqual([{ text: '第一条' }]))
    await user.type(input, '，接着写的第二条')
    send.release()
    await waitFor(() => expect(screen.getByRole('button', { name: '发送' })).toBeEnabled())
    expect(input).toHaveValue('，接着写的第二条')
  })

  it('发送期间改动了已发出的部分时，发送成功后内容全部保留', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const send = gatedSend(sent)
    const { user } = await renderChat({ handlers: [send.handler] })
    const input = screen.getByLabelText('消息')
    await user.type(input, '第一条')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(sent).toEqual([{ text: '第一条' }]))
    await user.clear(input)
    await user.type(input, '第1条，改过了')
    send.release()
    await waitFor(() => expect(screen.getByRole('button', { name: '发送' })).toBeEnabled())
    expect(input).toHaveValue('第1条，改过了')
  })

  it('发送失败时输入框保持原样', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const send = gatedSend(sent, { fail: true })
    const { user } = await renderChat({ handlers: [send.handler] })
    const input = screen.getByLabelText('消息')
    await user.type(input, '第一条')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(sent).toEqual([{ text: '第一条' }]))
    await user.type(input, '，接着写')
    send.release()
    expect(await screen.findByText(/服务端内部错误/)).toBeInTheDocument()
    expect(input).toHaveValue('第一条，接着写')
  })

  it('忙时排队发送也只留下发送期间新输入的文字', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const send = gatedSend(sent)
    const { agent, user, emit } = await renderChat({ handlers: [send.handler] })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: makeRun({ status: 'running' }) }, ''))
    const input = screen.getByLabelText('消息')
    await user.type(input, '排队的话')
    await user.click(screen.getByRole('button', { name: '排队发送' }))
    await waitFor(() => expect(sent).toEqual([{ text: '排队的话', whenBusy: 'queue' }]))
    await user.type(input, '，还有一句')
    send.release()
    await waitFor(() => expect(screen.getByRole('button', { name: '排队发送' })).toBeEnabled())
    expect(input).toHaveValue('，还有一句')
  })

  it('网络错误时显示无法连接并保留内容', async () => {
    const { user } = await renderChat({ handlers: [http.post(apiUrl('/agents/:agentId/messages'), () => HttpResponse.error())] })
    await user.type(screen.getByLabelText('消息'), '断网时的消息')
    await user.click(screen.getByRole('button', { name: '发送' }))
    expect(await screen.findByText('无法连接服务端')).toBeInTheDocument()
    expect(screen.getByLabelText('消息')).toHaveValue('断网时的消息')
  })

  it('服务端拒绝超长消息时显示原因并保留内容', async () => {
    const { user } = await renderChat({
      handlers: [http.post(apiUrl('/agents/:agentId/messages'), () => HttpResponse.json(errorBody('payload_too_large', '消息超过 64 KiB'), { status: 413 }))],
    })
    await user.type(screen.getByLabelText('消息'), '很长的消息')
    await user.click(screen.getByRole('button', { name: '发送' }))
    expect(await screen.findByText(/超过大小上限/)).toBeInTheDocument()
    expect(screen.getByLabelText('消息')).toHaveValue('很长的消息')
  })
})

describe('流式显示、工具状态与切换', () => {
  it('文字增量立即追加，结束时以最终文字为准', async () => {
    const { agent, emit } = await renderChat()
    const run = makeRun({ status: 'running' })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, ''))
    await emit('message.created', { message: makeMessage({ text: '问题', run }) })
    await emit('run.text', { runId: run.id, delta: '被丢弃的' })
    expect(await screen.findByText('被丢弃的')).toBeInTheDocument()
    await emit('run.text', { runId: run.id, delta: '片段' })
    expect(await screen.findByText('被丢弃的片段')).toBeInTheDocument()
    const ended = { ...run, status: 'completed' as const }
    await emit('run.ended', { run: ended, message: makeMessage({ role: 'assistant', text: '最终回复', run: ended }) })
    expect(await screen.findByText('最终回复')).toBeInTheDocument()
    expect(screen.queryByText('被丢弃的片段')).toBeNull()
  })

  it('工具调用只显示名称与状态', async () => {
    const { agent, emit } = await renderChat()
    const run = makeRun({ status: 'running' })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, ''))
    await emit('run.tool', { runId: run.id, toolCallId: 't1', toolName: 'bash', state: 'started', at: '2026-10-12T00:00:00Z' })
    const tools = await screen.findByRole('list', { name: '工具调用' })
    expect(tools).toHaveTextContent('bash进行中')
    await emit('run.tool', { runId: run.id, toolCallId: 't1', toolName: 'bash', state: 'succeeded', at: '2026-10-12T00:00:01Z' })
    await waitFor(() => expect(tools).toHaveTextContent('bash成功'))
  })

  it('切换 Agent 后不再接收原 Agent 的事件', async () => {
    const other = makeAgent({ name: '另一个' })
    const { router, source } = await renderChat({
      handlers: [
        http.get(apiUrl(`/agents/${other.id}`), () => HttpResponse.json(other)),
        http.get(apiUrl(`/agents/${other.id}/status`), () => HttpResponse.json(makeStatus(other.id))),
        http.get(apiUrl(`/agents/${other.id}/messages`), () => HttpResponse.json({ messages: turn('B 的问题', 'B 的回答'), hasMore: false })),
      ],
    })
    await router.navigate(defaultAgentPath(other.id, 'chat'))
    expect(await screen.findByText('B 的问题')).toBeInTheDocument()
    expect(source.readyState).toBe(FakeEventSource.CLOSED)
    expect(FakeEventSource.latest().url).toContain(`/agents/${other.id}/events`)
  })
})

describe('忙时发送直接排队', () => {
  it('忙时点发送直接排队，排在原排队消息之后', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const current = makeRun({ status: 'running' })
    const existing = makeRun({ status: 'queued', whenBusy: 'queue' })
    const { agent, user, emit } = await renderChat({
      messages: [makeMessage({ text: '正在处理', run: current }), makeMessage({ text: '原排队', run: existing })],
      handlers: [acceptSend(sent)],
    })
    const existingItem = makeQueueItem({ runId: existing.id, text: '原排队' })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current, queued: { interrupt: 0, normal: 1 } }, '', [existing], [existingItem]))
    // 输入框为空时是停止，有文字时是排队发送。
    expect(screen.getByRole('button', { name: '停止当前回复' })).toBeEnabled()
    await user.type(screen.getByLabelText('消息'), '新排队')
    await user.click(screen.getByRole('button', { name: '排队发送' }))
    await waitFor(() => expect(sent).toEqual([{ text: '新排队', whenBusy: 'queue' }]))
    expect(screen.queryByRole('dialog')).toBeNull()
    // 排队消息显示在输入框上方的排队托盘（以服务端推送的列表为准），不在对话流中。
    await emit('queue.updated', { queuedRuns: [existing], items: [existingItem, makeQueueItem({ text: '新排队' })] })
    const log = screen.getByRole('log')
    const queue = screen.getByRole('region', { name: '排队消息' })
    await waitFor(() => {
      const order = within(queue).getAllByText(/^(原排队|新排队)$/).map((el) => el.textContent)
      expect(order).toEqual(['原排队', '新排队'])
    })
    expect(within(log).getByText('正在处理')).toBeInTheDocument()
    expect(within(log).queryByText(/^(原排队|新排队)$/)).toBeNull()
  })

  it('忙时按回车直接排队，不弹选择', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const current = makeRun({ status: 'running' })
    const { agent, user, emit } = await renderChat({ handlers: [acceptSend(sent)] })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, ''))
    await user.type(screen.getByLabelText('消息'), '回车排队{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '回车排队', whenBusy: 'queue' }]))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByLabelText('消息')).toHaveValue('')
  })

  it('发送时才发现正忙：不弹选择，改为排队再发一次后进入队列', async () => {
    const sent: Schemas['SendMessageInput'][] = []
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
          const body = (await request.json()) as Schemas['SendMessageInput']
          sent.push(body)
          if (!body.whenBusy) {
            return HttpResponse.json(errorBody('agent_busy', 'busy', { details: { currentRunId: 'r1', queued: { interrupt: 1, normal: 2 } } }), { status: 409 })
          }
          const run = makeRun({ status: 'queued', whenBusy: body.whenBusy })
          return HttpResponse.json({ message: makeMessage({ text: body.text, run }), run }, { status: 202 })
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, {}))
    await user.type(screen.getByLabelText('消息'), '晚到的消息')
    await user.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(sent).toEqual([{ text: '晚到的消息' }, { text: '晚到的消息', whenBusy: 'queue' }]))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    await emit('queue.updated', { queuedRuns: [], items: [makeQueueItem({ text: '晚到的消息' })] })
    expect(await within(screen.getByRole('region', { name: '排队消息' })).findByText('晚到的消息')).toBeInTheDocument()
    expect(within(screen.getByRole('log')).queryByText('晚到的消息')).toBeNull()
    expect(screen.getByLabelText('消息')).toHaveValue('')
  })
})

describe('排队显示与回复状态', () => {
  it('打断消息排在普通排队消息之前并标明', async () => {
    const current = makeRun({ status: 'running' })
    const n1 = makeRun({ status: 'queued', whenBusy: 'queue' })
    const n2 = makeRun({ status: 'queued', whenBusy: 'queue' })
    const i1 = makeRun({ status: 'queued', whenBusy: 'interrupt' })
    const { agent, emit } = await renderChat({
      messages: [makeMessage({ text: '当前', run: current }), makeMessage({ text: '普通一', run: n1 }), makeMessage({ text: '普通二', run: n2 }), makeMessage({ text: '插队', run: i1 })],
    })
    const items = [makeQueueItem({ runId: i1.id, text: '插队', whenBusy: 'interrupt' }), makeQueueItem({ runId: n1.id, text: '普通一' }), makeQueueItem({ runId: n2.id, text: '普通二' })]
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, '', [i1, n1, n2], items))
    const log = screen.getByRole('log')
    const queue = screen.getByRole('region', { name: '排队消息' })
    await waitFor(() => {
      const order = within(queue).getAllByText(/^(普通一|普通二|插队)$/).map((el) => el.textContent)
      expect(order).toEqual(['插队', '普通一', '普通二'])
    })
    expect(within(queue).getAllByText('下一条处理')).toHaveLength(1)
    expect(within(queue).getAllByRole('button', { name: '打断' })).toHaveLength(2)
    expect(within(queue).getByRole('heading', { name: '排队 3' })).toBeInTheDocument()
    expect(within(log).getByText('当前')).toBeInTheDocument()
    expect(within(log).queryByText(/^(普通一|普通二|插队)$/)).toBeNull()
  })

  it('显示服务端重启中断、失败原因与等待名额', async () => {
    const restarted = turn('问一', '说到一半', { status: 'interrupted', endReason: 'server_restart' })
    const failed = turn('问二', '', { status: 'failed', errorCode: 'credential_unavailable', errorMessage: 'gh 凭证无法解密' })
    const interrupted = turn('问三', '半截', { status: 'interrupted', endReason: 'user_interrupt' })
    const waiting = makeRun({ status: 'starting', phase: 'waiting_slot' })
    const { agent, emit } = await renderChat({ messages: [...restarted, ...failed, ...interrupted, makeMessage({ text: '问四', run: waiting })] })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: waiting }, ''))
    expect(await screen.findByText('服务端重启中断')).toBeInTheDocument()
    expect(screen.getByText('说到一半')).toBeInTheDocument()
    expect(screen.getByText('失败：凭证不可用（gh 凭证无法解密）')).toBeInTheDocument()
    expect(screen.getByText('已打断')).toBeInTheDocument()
    expect(screen.getByText('半截')).toBeInTheDocument()
    // 头部也显示当前阶段，这里只看对话里的那一轮。
    expect(await within(screen.getByRole('log')).findByText('等待执行名额')).toBeInTheDocument()
  })

  it('打断时先显示正在打断，结束后显示已打断并保留文字', async () => {
    const run = makeRun({ status: 'running' })
    const { agent, emit } = await renderChat({ messages: [makeMessage({ text: '长任务', run })] })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run }, '做到一半'))
    await emit('run.updated', { run: { ...run, status: 'aborting' } })
    expect(await within(screen.getByRole('log')).findByText('正在打断')).toBeInTheDocument()
    const ended = { ...run, status: 'interrupted' as const, endReason: 'user_interrupt' as const }
    await emit('run.ended', { run: ended, message: makeMessage({ role: 'assistant', text: '做到一半', run: ended }) })
    expect(await screen.findByText('已打断')).toBeInTheDocument()
    expect(screen.getByText('做到一半')).toBeInTheDocument()
  })
})

describe('会话文件丢失与确认重开会话', () => {
  it('显示说明并禁用发送；确认后重开，恢复发送', async () => {
    let resetCalls = 0
    const { agent, user, emit } = await renderChat({
      agent: makeAgent({ session: { state: 'missing', epoch: 1 } }),
      status: { session: { state: 'missing', epoch: 1 } },
      handlers: [
        http.post(apiUrl('/agents/:agentId/session/reset'), () => {
          resetCalls += 1
          return HttpResponse.json({ state: 'none', epoch: 2 })
        }),
      ],
    })
    expect(await screen.findByText('会话文件丢失')).toBeInTheDocument()
    expect(screen.getByText(/之后的消息都会失败/)).toBeInTheDocument()
    expect(screen.getByLabelText('消息')).toBeDisabled()
    expect(screen.getByRole('button', { name: '发送' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '确认重开会话' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: '确认重开会话' }))
    await waitFor(() => expect(resetCalls).toBe(1))
    await emit('session.updated', { state: 'none', epoch: 2 })
    await waitFor(() => expect(screen.queryByText('会话文件丢失')).toBeNull())
    expect(screen.getByLabelText('消息')).toBeEnabled()
    void agent
  })

  it('仍有执行未结束时不可重开', async () => {
    const run = makeRun({ status: 'running' })
    const { agent, emit } = await renderChat({ agent: makeAgent({ session: { state: 'missing', epoch: 1 } }) })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: run, session: { state: 'missing', epoch: 1 } }, ''))
    expect(await screen.findByText('还有执行尚未结束，请等它结束后再重开会话。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '确认重开会话' })).toBeDisabled()
  })
})

it('sleep 终态只重读原历史，不合成消息；缺 message.created 时 timer 原来源仍显示', async () => {
  const agent = makeAgent()
  const sleepId = '00000000-0000-7000-8000-000000005001'
  const run = makeRun({ wakeSleepId: sleepId })
  const timer = makeMessage({ origin: 'sleep_wake', text: '服务端原计时提醒', run })
  let messages: Schemas['Message'][] = [], calls = 0
  const { emit } = await renderChat({ agent, handlers: [http.get(apiUrl('/agents/:agentId/messages'), () => {
    calls++
    return HttpResponse.json({ messages, hasMore: false, hasMoreAfter: false })
  })] })
  await emit('snapshot', snapshot(agent.id, {}))
  const before = calls
  messages = [timer]
  await emit('sleep.updated', { scopeKind: 'normal', agentId: agent.id, taskId: null, runtimeId: null, nodeRunId: null, taskVersion: null, sleepRevision: 3, sleep: null, endedSleepId: sleepId })
  await waitFor(() => expect(calls).toBeGreaterThan(before))
  expect(await screen.findByRole('note')).toHaveTextContent('sleep 到期唤醒')
  expect(screen.getByRole('note')).not.toHaveTextContent('服务端原计时提醒')
  expect(screen.getByRole('note')).toHaveAttribute('title', `服务端原计时提醒\nsleep ${sleepId}`)
})
