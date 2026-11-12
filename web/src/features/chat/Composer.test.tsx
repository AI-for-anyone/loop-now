import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it, vi } from 'vitest'
import { Composer, KeyHint } from './Composer'
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

// 改版“一张印出来的纸”：输入区并入纸张底部。
const mountBar = (busy = false) => renderWithQuery(<Composer agentId="bar-agent" agentName="开发助手" busy={busy} disabled={false} maxMessageBytes={4096} onSent={() => {}} stop={busy ? { onStop: () => {}, pending: false, retry: false } : undefined} />)

it('输入区是纸张底部的一条浅黄色栏，上缘一道 2px 的藏青线', () => {
  mountBar()
  const bar = screen.getByRole('textbox', { name: '消息' }).closest('[data-chat-bar]')
  expect(bar).toHaveClass('bg-tint-yellow', 'border-t-2', 'border-ink')
})

it('输入框是浅纸底、藏青描边；发送按钮是荧光粉圆章（藏青描边加藏青实色投影）', () => {
  mountBar()
  const editor = screen.getByRole('textbox', { name: '消息' })
  expect(editor.closest('[data-chat-input]')).toHaveClass('bg-paper-raised', 'border-2', 'border-ink')
  expect(screen.getByRole('button', { name: '发送' })).toHaveClass('size-11', 'rounded-full', 'border-2', 'border-ink', 'bg-riso-pink', 'shadow-[3px_3px_0_var(--ink)]')
})

it('空闲时占位文字是“给 Agent 发消息”，不再写按键说明', () => {
  mountBar()
  expect(screen.getByRole('textbox', { name: '消息' })).toHaveAttribute('placeholder', '给 开发助手 发消息')
})

it('空闲时输入框下面写着回车发送、Shift + 回车换行', () => {
  mountBar()
  const hint = screen.getByText('发送', { selector: 'span' }).closest('p')!
  expect(hint).toHaveTextContent('回车发送·Shift + 回车换行')
  expect(hint.querySelectorAll('kbd')).toHaveLength(2)
  expect(screen.queryByText('排队', { selector: 'span' })).toBeNull()
})

it('忙时按键说明换成排队与打断，输入框为空时发送按钮换成藏青色的停止按钮', () => {
  mountBar(true)
  const hint = screen.getByText('排队', { selector: 'span' }).closest('p')!
  // 中文界面里按键统一写“回车”，修饰键写法不变。
  expect(hint).toHaveTextContent('回车排队·Ctrl 回车立即打断')
  expect(hint).not.toHaveTextContent('Enter')
  // 在输入区里靠左，用输入区的按键样式。
  expect(hint).toHaveClass('justify-start', 'text-ink-700')
  expect(hint.querySelector('kbd')).toHaveClass('border-ink', 'bg-paper-raised')
  expect(screen.queryByText('换行')).toBeNull()
  const stop = screen.getByRole('button', { name: '停止当前回复' })
  expect(stop).toHaveClass('size-11', 'rounded-full', 'border-2', 'border-ink', 'bg-ink', 'text-white')
  expect(screen.queryByRole('button', { name: '排队发送' })).toBeNull()
})

it('排队托盘贴在输入框上沿：左右让出加号与发送按钮的位置', () => {
  renderWithQuery(<Composer agentId="bar-agent" agentName="开发助手" busy disabled={false} maxMessageBytes={4096} imageLimits={{ maxCount: 4, maxImageBytes: 1024, maxTotalBytes: 4096 }} onSent={() => {}} queue={<section aria-label="排队的消息">排队 1</section>} />)
  const slot = screen.getByRole('region', { name: '排队的消息' }).parentElement!
  // 手机宽度下托盘占满一行，不再让位，否则太窄。
  expect(slot).toHaveClass('sm:pl-[50px]', 'sm:pr-[57px]')
  expect(slot).not.toHaveClass('pl-[50px]')
  expect(slot.nextElementSibling).toContainElement(screen.getByRole('textbox', { name: '消息' }))
})

it('别处（任务节点的输入框）用的按键说明保持原样：居中、次要文字色、白底细描边的按键', () => {
  renderWithQuery(<KeyHint />)
  const hint = screen.getByText('排队', { selector: 'span' }).closest('p')!
  expect(hint).toHaveClass('justify-center', 'text-ink-500')
  expect(hint.querySelector('kbd')).toHaveClass('bg-white', 'border-ink/25', 'font-mono')
  // 按键的写法也保持原样。
  expect(hint).toHaveTextContent('Enter排队·Ctrl Enter立即打断')
  expect(hint).not.toHaveTextContent('回车')
})

it('输入框的文字在窄屏上是 16px（更小的字号会让 iOS 在聚焦时放大页面）', () => {
  mountBar()
  expect(screen.getByRole('textbox', { name: '消息' })).toHaveClass('text-[15.5px]', 'max-nav:text-base')
})
