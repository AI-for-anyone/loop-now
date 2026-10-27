import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, context, errorBody, limits, makeMessage, makeQueueItem, makeRun, makeStatus } from '@/test/api'
import { agentParty } from '@/test/inbox'
import { renderChat } from '@/test/chat'

// 排队消息逐条显示在输入框顶上的排队托盘，不在对话流中；列表以快照与 queue.updated 为准。

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']>, queuedRuns: Schemas['Run'][] = [], queueItems: Schemas['QueueItem'][] = []) {
  return { status: makeStatus(agentId, status), currentText: '', queuedRuns, queueItems }
}

const queueList = () => screen.getByRole('region', { name: '排队消息' })
// 每条排队消息一行；行内的图片缩略图也是 listitem，不算。
const rows = () => within(queueList()).getAllByRole('listitem').filter((item) => item.hasAttribute('data-kind'))
const texts = () => rows().map((item) => within(item).getByRole('button', { expanded: false }).textContent)
const log = () => screen.getByRole('log')
const manager = agentParty({ id: '57000000-0000-7000-8000-000000000001', name: 'test', address: '测试项目/test' })

function receipt(agentId: string, actionId: string, operation: Schemas['AgentQueueActionReceipt']['operation'], item: Schemas['QueueItem'] | null, queue: Schemas['QueueItem'][], run: Schemas['Run'] | null = null): Schemas['AgentQueueActionReceipt'] {
  return { actionId, agentId, operation, status: 'accepted', item, run, queue }
}

describe('输入框上方的排队托盘', () => {
  it('逐条列出：打断在前标明下一条处理；自己发的可编辑，Inbox 消息带发送方；每行都能取消，普通排队的行能打断', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const sent: Schemas['SendMessageInput'][] = []
    const interrupt = makeQueueItem({ text: '插队', whenBusy: 'interrupt' })
    const own = makeQueueItem({ text: '顺便把 lint 也跑一下' })
    const inbox = makeQueueItem({ kind: 'inbox', messageId: '58000000-0000-7000-8000-000000000001', from: manager, text: 'fixture 已更新，请先拉取 server-main' })
    const { agent, user, emit } = await renderChat({
      messages: [makeMessage({ text: '当前', run: current })],
      handlers: [
        http.post(apiUrl('/agents/:agentId/messages'), async ({ request }) => {
          const body = (await request.json()) as Schemas['SendMessageInput']
          sent.push(body)
          const run = makeRun({ status: 'queued', whenBusy: body.whenBusy ?? null, startedAt: null, endedAt: null })
          return HttpResponse.json({ message: makeMessage({ text: body.text, run }), run }, { status: 202 })
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [], [interrupt, own, inbox]))
    expect(within(queueList()).getByRole('heading', { name: '排队 3' })).toBeInTheDocument()
    expect(within(queueList()).getByText('当前回复结束后按顺序处理')).toBeInTheDocument()
    expect(rows().map((item) => item.getAttribute('data-kind'))).toEqual(['interrupt', 'queue', 'queue'])
    expect(texts()).toEqual(['插队', '顺便把 lint 也跑一下', 'fixture 已更新，请先拉取 server-main'])
    const [first, second, third] = rows()
    expect(within(first).getByText('下一条处理')).toBeInTheDocument()
    expect(within(first).queryByRole('button', { name: '打断' })).toBeNull()
    expect(within(first).getByRole('button', { name: '编辑这条排队消息' })).toBeEnabled()
    expect(within(second).getByRole('button', { name: '打断' })).toBeEnabled()
    expect(within(second).getByRole('button', { name: '编辑这条排队消息' })).toBeEnabled()
    // 别人发来的 Inbox 消息：标明发送方，可以打断和取消，不能编辑。
    expect(within(third).getByText('test')).toBeInTheDocument()
    expect(within(third).getByRole('button', { name: '打断' })).toBeEnabled()
    expect(within(third).queryByRole('button', { name: '编辑这条排队消息' })).toBeNull()
    for (const row of rows()) expect(within(row).getByRole('button', { name: '取消这条排队消息' })).toBeEnabled()
    expect(within(log()).queryByText(/^(插队|顺便把 lint 也跑一下)$/)).toBeNull()

    // 忙时直接排队，不弹选择；托盘随 queue.updated 更新。
    expect(screen.getByLabelText('消息')).toHaveAttribute('placeholder', '开发助手 正在回复，新消息会排队')
    await user.type(screen.getByLabelText('消息'), '再补一句{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '再补一句', whenBusy: 'queue' }]))
    expect(screen.queryByRole('dialog')).toBeNull()
    await emit('queue.updated', { queuedRuns: [], items: [interrupt, own, inbox, makeQueueItem({ text: '再补一句' })] })
    await waitFor(() => expect(within(queueList()).getByRole('heading', { name: '排队 4' })).toBeInTheDocument())
    expect(texts().at(-1)).toBe('再补一句')
    // 托盘在输入框正上方；输入框下方说明按键，忙碌状态不重复排队计数。
    expect(queueList().compareDocumentPosition(screen.getByLabelText('消息')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByText('Agent 正忙（回复中）')).toBeInTheDocument()
    expect(screen.getByText('只停止当前回复')).toBeInTheDocument()
  })

  it('正文截成一行，悬停可见全文，点击展开；图片显示缩略图', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const text = '第一行说明\n第二行补充'
    const image: Schemas['MessageImage'] = { id: '00000000-0000-7000-8000-000000000301', mimeType: 'image/png', size: 1024, width: 640, height: 480 }
    const { agent, user, emit } = await renderChat()
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [], [makeQueueItem({ text, images: [image] })]))
    const body = within(queueList()).getByRole('button', { name: /第一行说明/ })
    expect(body).toHaveAttribute('title', text)
    expect(body).toHaveAttribute('aria-expanded', 'false')
    await user.click(body)
    expect(body).toHaveAttribute('aria-expanded', 'true')
    expect(body).not.toHaveAttribute('title')
    expect(within(queueList()).getByRole('img', { name: '图片 1 · 640×480' })).toBeInTheDocument()
  })

  it('没有排队消息：不显示托盘，对话仍正常显示', async () => {
    const run = makeRun()
    const { agent, emit } = await renderChat({
      messages: [makeMessage({ text: '你好', run }), makeMessage({ role: 'assistant', text: '你好呀', run })],
    })
    await emit('snapshot', snapshot(agent.id, {}))
    expect(screen.queryByRole('region', { name: '排队消息' })).toBeNull()
    expect(within(log()).getByText('你好呀')).toBeInTheDocument()
  })

  it('收到快照之前，托盘取自排队接口；收到快照后以快照为准', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const { agent, emit } = await renderChat({
      status: { busy: true, currentRun: current },
      handlers: [http.get(apiUrl('/agents/:agentId/queue'), ({ params }) => HttpResponse.json({ agentId: String(params.agentId), items: [makeQueueItem({ text: '接口里的排队' })] }))],
    })
    expect(await within(queueList()).findByText('接口里的排队')).toBeInTheDocument()
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }))
    await waitFor(() => expect(screen.queryByRole('region', { name: '排队消息' })).toBeNull())
  })

  it('打断：带 actionId 点一下直接生效，进行中禁用；按回执的列表移到前面；Inbox 消息按消息转换', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const runA = makeRun({ status: 'queued', whenBusy: 'queue', startedAt: null, endedAt: null })
    const a = makeQueueItem({ runId: runA.id, text: '普通一' })
    const b = makeQueueItem({ kind: 'inbox', messageId: '58000000-0000-7000-8000-000000000002', from: manager, text: '来自 test' })
    const posts: { url: string; body: Schemas['AgentQueueActionInput'] }[] = []
    let release!: () => void
    const gate = new Promise<void>((resolve) => (release = resolve))
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.post(apiUrl('/agents/:agentId/inbox-messages/:messageId/promote-to-interrupt'), async ({ request, params }) => {
          const body = (await request.json()) as Schemas['AgentQueueActionInput']
          posts.push({ url: `inbox:${String(params.messageId)}`, body })
          await gate
          return HttpResponse.json(receipt(String(params.agentId), body.actionId, 'promote_inbox_message', b, [{ ...b, whenBusy: 'interrupt' }, a]))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [runA], [a, b]))
    expect(texts()).toEqual(['普通一', '来自 test'])
    const row = rows()[1]
    await user.click(within(row).getByRole('button', { name: '打断' }))
    const busyButton = within(row).getByRole('button', { name: '打断中…' })
    expect(busyButton).toBeDisabled()
    expect(within(row).getByRole('button', { name: '取消这条排队消息' })).toBeDisabled()
    await user.click(busyButton)
    expect(posts).toEqual([{ url: `inbox:${b.messageId}`, body: { actionId: expect.stringMatching(/^[\da-f-]{36}$/) } }])
    expect(screen.queryByRole('dialog')).toBeNull()

    await act(async () => release())
    await waitFor(() => expect(texts()).toEqual(['来自 test', '普通一']))
    expect(rows()[0]).toHaveAttribute('data-kind', 'interrupt')
    expect(within(rows()[0]).getByText('下一条处理')).toBeInTheDocument()
    expect(within(rows()[0]).queryByRole('button', { name: '打断' })).toBeNull()
  })

  it('queue.updated 整体替换托盘；回执晚于它到达时不用回执里的旧列表覆盖', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const a = makeQueueItem({ text: '普通一' }), b = makeQueueItem({ text: '普通二' })
    let release!: () => void
    const gate = new Promise<void>((resolve) => (release = resolve))
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.post(apiUrl('/agents/:agentId/runs/:runId/promote-to-interrupt'), async ({ request, params }) => {
          const body = (await request.json()) as Schemas['AgentQueueActionInput']
          await gate
          return HttpResponse.json(receipt(String(params.agentId), body.actionId, 'promote_run', b, [{ ...b, whenBusy: 'interrupt' }, a]))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [], [a, b]))
    await user.click(within(rows()[1]).getByRole('button', { name: '打断' }))
    // 回执到达之前：普通一已开始，服务端推送了新的列表。
    await emit('queue.updated', { queuedRuns: [], items: [{ ...b, whenBusy: 'interrupt' }] })
    await waitFor(() => expect(texts()).toEqual(['普通二']))
    await act(async () => release())
    await waitFor(() => expect(within(rows()[0]).queryByRole('button', { name: '打断中…' })).toBeNull())
    expect(texts()).toEqual(['普通二'])
  })

  it('已不在队列时 409：一行轻提示（已开始、已被读取），不弹窗、不报错，并重新读取', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const own = makeQueueItem({ text: '普通一' })
    const inbox = makeQueueItem({ kind: 'inbox', messageId: '58000000-0000-7000-8000-000000000003', from: manager, text: '刚被读取' })
    let statusReads = 0
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/status'), ({ params }) => {
          statusReads++
          return HttpResponse.json(makeStatus(String(params.agentId), { busy: true, currentRun: current }))
        }),
        http.post(apiUrl('/agents/:agentId/runs/:runId/promote-to-interrupt'), ({ params }) =>
          HttpResponse.json(errorBody('run_not_queued', '已开始', { details: { runId: String(params.runId), status: 'starting', whenBusy: 'queue' } }), { status: 409 }),
        ),
        http.post(apiUrl('/agents/:agentId/inbox-messages/:messageId/cancel'), ({ params }) =>
          HttpResponse.json(errorBody('run_not_queued', '已读', { details: { runId: null, status: null, whenBusy: 'queue', messageId: String(params.messageId), messageState: 'read' } }), { status: 409 }),
        ),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [], [own, inbox]))
    const before = statusReads
    await user.click(within(rows()[0]).getByRole('button', { name: '打断' }))
    expect(await screen.findByText('已开始处理，无法转为打断')).toHaveAttribute('role', 'status')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByRole('dialog')).toBeNull()
    await waitFor(() => expect(statusReads).toBeGreaterThan(before))
    expect(within(rows()[0]).getByRole('button', { name: '打断' })).toBeEnabled()
    await user.click(within(rows()[1]).getByRole('button', { name: '取消这条排队消息' }))
    expect(await screen.findByText('这条消息已被读取，不在排队中了')).toHaveAttribute('role', 'status')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('取消：结果不明时按钮改为重试，重试沿用同一 actionId；成功后按回执去掉这一行', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const runA = makeRun({ status: 'queued', whenBusy: 'queue', startedAt: null, endedAt: null })
    const a = makeQueueItem({ runId: runA.id, text: '不要了' }), b = makeQueueItem({ text: '留着' })
    const bodies: Schemas['AgentQueueActionInput'][] = []
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.post(apiUrl('/agents/:agentId/runs/:runId/cancel'), async ({ request, params }) => {
          const body = (await request.json()) as Schemas['AgentQueueActionInput']
          bodies.push(body)
          if (bodies.length === 1) return HttpResponse.json(errorBody('internal_error', '回执暂不可确认'), { status: 500 })
          const cancelled = { ...runA, status: 'interrupted' as const, endReason: 'user_cancelled' as const, endedAt: '2026-10-12T08:01:00Z' }
          return HttpResponse.json(receipt(String(params.agentId), body.actionId, 'cancel_run', a, [b], cancelled))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [runA], [a, b]))
    await user.click(within(rows()[0]).getByRole('button', { name: '取消这条排队消息' }))
    await user.click(await within(rows()[0]).findByRole('button', { name: '重试取消这条排队消息' }))
    await waitFor(() => expect(texts()).toEqual(['留着']))
    expect(bodies).toHaveLength(2)
    expect(bodies[1]).toEqual(bodies[0])
    // 撤回的这一轮没有执行，不进入对话流。
    expect(within(log()).queryByText('不要了')).toBeNull()
  })

  it('编辑：先取回图片，再撤回这条消息，把原文与图片放回输入框，输入框里已有的文字保留在前面', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const image: Schemas['MessageImage'] = { id: '00000000-0000-7000-8000-000000000302', mimeType: 'image/png', size: 8, width: 2, height: 2 }
    const item = makeQueueItem({ text: '原来的话', images: [image] })
    const order: string[] = []
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/context'), () => HttpResponse.json({ ...context, limits: { ...limits, maxMessageImages: 4, maxMessageImageBytes: 5 * 1024 * 1024 } })),
        http.get(apiUrl('/message-images/:imageId'), ({ params }) => {
          order.push(`image:${String(params.imageId)}`)
          return new HttpResponse(new Blob(['png-data'], { type: 'image/png' }), { headers: { 'Content-Type': 'image/png' } })
        }),
        http.post(apiUrl('/agents/:agentId/runs/:runId/cancel'), async ({ request, params }) => {
          const body = (await request.json()) as Schemas['AgentQueueActionInput']
          order.push('cancel')
          return HttpResponse.json(receipt(String(params.agentId), body.actionId, 'cancel_run', item, []))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }, [], [item]))
    await user.type(screen.getByLabelText('消息'), '先写的')
    await user.click(within(rows()[0]).getByRole('button', { name: '编辑这条排队消息' }))
    await waitFor(() => expect(screen.getByLabelText('消息')).toHaveValue('先写的\n原来的话'))
    expect(order).toEqual([`image:${image.id}`, 'cancel'])
    expect(await screen.findByRole('list', { name: '待发送图片' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '移除图片 1' })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: '排队消息' })).toBeNull()
  })

  it('停止：忙时输入框为空，发送按钮换成停止，只停这一次回复；已经结束时一行轻提示', async () => {
    const current = makeRun({ status: 'running', endedAt: null })
    const bodies: { runId: string; body: Schemas['AgentQueueActionInput'] }[] = []
    const { agent, user, emit } = await renderChat({
      handlers: [
        http.post(apiUrl('/agents/:agentId/runs/:runId/stop'), async ({ request, params }) => {
          const body = (await request.json()) as Schemas['AgentQueueActionInput']
          bodies.push({ runId: String(params.runId), body })
          if (bodies.length > 1) return HttpResponse.json(errorBody('run_not_active', '已结束', { details: { runId: String(params.runId), status: 'completed' } }), { status: 409 })
          return HttpResponse.json(receipt(String(params.agentId), body.actionId, 'stop_run', null, [], { ...current, status: 'aborting' }))
        }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, { busy: true, currentRun: current }))
    await user.click(screen.getByRole('button', { name: '停止当前回复' }))
    await waitFor(() => expect(bodies).toEqual([{ runId: current.id, body: { actionId: expect.stringMatching(/^[\da-f-]{36}$/) } }]))
    // 有文字时是排队发送。
    await user.type(screen.getByLabelText('消息'), '新消息')
    expect(screen.queryByRole('button', { name: '停止当前回复' })).toBeNull()
    expect(screen.getByRole('button', { name: '排队发送' })).toBeEnabled()
    await user.clear(screen.getByLabelText('消息'))
    await user.click(screen.getByRole('button', { name: '停止当前回复' }))
    expect(await screen.findByText('这次回复已经结束')).toHaveAttribute('role', 'status')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('对话流：被人停止的回复标明已停止；排队中即作废的轮次不显示', async () => {
    const stopped = makeRun({ status: 'interrupted', endReason: 'user_stopped', endedAt: '2026-10-12T08:01:00Z' })
    const merged = makeRun({ status: 'interrupted', endReason: 'merged_into_interrupt', startedAt: null, endedAt: '2026-10-12T08:01:00Z' })
    const { agent, emit } = await renderChat({
      messages: [
        makeMessage({ text: '跑一下测试', run: stopped }),
        makeMessage({ role: 'assistant', text: '正在跑', run: stopped }),
        makeMessage({ origin: 'inbox_reminder', text: '已并入打断的提醒', run: merged }),
      ],
    })
    await emit('snapshot', snapshot(agent.id, {}))
    expect(within(log()).getByText('已停止')).toBeInTheDocument()
    expect(within(log()).queryByText('已并入打断的提醒')).toBeNull()
    expect(within(log()).getAllByRole('article')).toHaveLength(1)
  })
})
