import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it, vi } from 'vitest'
import { Composer } from './Composer'
import { apiUrl, makeMessage, makeRun } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import type { Schemas } from '@/api/client'

it('sleep使用空闲busy=false的发送入口，不要求whenBusy或先弹选择', async () => {
  const sent: Schemas['SendMessageInput'][] = [], onSent = vi.fn()
  const run = makeRun({ status: 'starting', endedAt: null })
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']; sent.push(body)
    return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
  }))
  renderWithQuery(<Composer agentId="sleep-agent" agentName="开发助手" busy={false} disabled={false} maxMessageBytes={4096} onSent={onSent} />)
  await userEvent.type(screen.getByRole('textbox', { name: '消息' }), '新的工作')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(sent).toEqual([{ text: '新的工作' }]))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(onSent).toHaveBeenCalledTimes(1)
})

it('空闲发送与真正busy竞争时不弹选择，直接以queue再发一次', async () => {
  const onSent = vi.fn()
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']; sent.push(body)
    if (!body.whenBusy) return HttpResponse.json({ error: { code: 'agent_busy', message: 'Agent正忙', details: { currentRunId: makeRun().id, queued: { interrupt: 0, normal: 1 } } } }, { status: 409 })
    const run = makeRun({ status: 'queued', whenBusy: body.whenBusy })
    return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
  }))
  renderWithQuery(<Composer agentId="sleep-agent" agentName="开发助手" busy={false} disabled={false} maxMessageBytes={4096} onSent={onSent} />)
  await userEvent.type(screen.getByRole('textbox', { name: '消息' }), '保留这条工作')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(sent).toEqual([{ text: '保留这条工作' }, { text: '保留这条工作', whenBusy: 'queue' }]))
  await waitFor(() => expect(onSent).toHaveBeenCalledTimes(1))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.getByLabelText('消息')).toHaveValue('')
})

it('忙时发送出错不再重发，保留文字并显示错误', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json({ error: { code: 'internal_error', message: '出错了' } }, { status: 500 })
  }))
  renderWithQuery(<Composer agentId="sleep-agent" agentName="开发助手" busy disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  await userEvent.type(screen.getByRole('textbox', { name: '消息' }), '忙时的工作')
  await userEvent.click(screen.getByRole('button', { name: '排队发送' }))
  expect(await screen.findByText(/服务端内部错误/)).toBeInTheDocument()
  expect(sent).toEqual([{ text: '忙时的工作', whenBusy: 'queue' }])
  expect(screen.getByLabelText('消息')).toHaveValue('忙时的工作')
})

it('忙时 Enter 排队，⌘／Ctrl+Enter 作为打断发送；空闲时才发现正忙的，按所选的打断再发一次', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']; sent.push(body)
    if (!body.whenBusy) return HttpResponse.json({ error: { code: 'agent_busy', message: 'Agent正忙', details: { currentRunId: makeRun().id, queued: { interrupt: 0, normal: 0 } } } }, { status: 409 })
    const run = makeRun({ status: 'queued', whenBusy: body.whenBusy })
    return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
  }))
  const view = renderWithQuery(<Composer agentId="sleep-agent" agentName="开发助手" busy disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  const box = screen.getByRole('textbox', { name: '消息' })
  // 忙时输入框下方说明按键。
  expect(screen.getByText('立即打断')).toBeVisible()
  await userEvent.type(box, '排在后面{Enter}')
  await waitFor(() => expect(sent).toEqual([{ text: '排在后面', whenBusy: 'queue' }]))
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, '先处理这条{Meta>}{Enter}{/Meta}')
  await waitFor(() => expect(sent.at(-1)).toEqual({ text: '先处理这条', whenBusy: 'interrupt' }))
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, '也可以用 Ctrl{Control>}{Enter}{/Control}')
  await waitFor(() => expect(sent.at(-1)).toEqual({ text: '也可以用 Ctrl', whenBusy: 'interrupt' }))

  view.rerender(<Composer agentId="sleep-agent" agentName="开发助手" busy={false} disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  expect(screen.queryByText('立即打断')).toBeNull()
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, '以为空闲{Meta>}{Enter}{/Meta}')
  await waitFor(() => expect(sent.slice(-2)).toEqual([{ text: '以为空闲' }, { text: '以为空闲', whenBusy: 'interrupt' }]))
})
