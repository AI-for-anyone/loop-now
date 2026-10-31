import { afterEach, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { Composer } from './Composer'
import { apiUrl } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import type { Schemas } from '@/api/client'

const originalCreateObjectURL = Object.getOwnPropertyDescriptor(URL, 'createObjectURL')
const originalRevokeObjectURL = Object.getOwnPropertyDescriptor(URL, 'revokeObjectURL')

afterEach(() => {
  vi.restoreAllMocks()
  for (const [key, descriptor] of [['createObjectURL', originalCreateObjectURL], ['revokeObjectURL', originalRevokeObjectURL]] as const) {
    if (descriptor) Object.defineProperty(URL, key, descriptor)
    else Reflect.deleteProperty(URL, key)
  }
})

function commandResult(overrides: Partial<Schemas['ConsoleCommandResult']> = {}): Schemas['ConsoleCommandResult'] {
  return { handled: true, source: 'platform', plugin: '', reply: null, command: 'session', ...overrides }
}

function renderComposer(onCommand = vi.fn()) {
  renderWithQuery(<Composer agentId="sleep-agent" agentName="开发助手" busy={false} disabled={false} maxMessageBytes={4096} onSent={vi.fn()} onCommand={onCommand} />)
  return onCommand
}

it('/new 发送前必须确认，确认后才调用接口并显示结果', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json(commandResult({ command: 'new', resetSession: { state: 'none', epoch: 2 } }))
  }))
  const onCommand = renderComposer()
  const box = screen.getByRole('textbox', { name: '消息' })
  await userEvent.type(box, '/new')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  expect(await screen.findByRole('alertdialog')).toHaveTextContent('确认重开会话')
  expect(sent).toEqual([])
  expect(onCommand).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: '重开会话' }))
  await waitFor(() => expect(sent).toEqual([{ text: '/new' }]))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(onCommand.mock.calls[0][0].resetSession?.epoch).toBe(2)
  expect(box).toHaveValue('')
})

it('/resume 带 ID 时取消不发请求，确认说明不回滚配置与工作区', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json(commandResult({ command: 'resume', resumedSession: { state: 'active', epoch: 3 } }))
  }))
  const onCommand = renderComposer()
  const box = screen.getByRole('textbox', { name: '消息' })
  await userEvent.type(box, '/resume full-session-id')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  expect(await screen.findByRole('alertdialog')).toHaveTextContent('不会回滚工作区、模型配置、Prompt、Skills 或权限')
  await userEvent.click(screen.getByRole('button', { name: '取消' }))
  expect(sent).toEqual([])
  expect(onCommand).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await userEvent.click(await screen.findByRole('button', { name: '恢复会话' }))
  await waitFor(() => expect(sent).toEqual([{ text: '/resume full-session-id' }]))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(onCommand.mock.calls[0][0].resumedSession?.epoch).toBe(3)
})

it('/resume 列表不需要确认', async () => {
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), () => HttpResponse.json(commandResult({ command: 'resume', resumeSessions: [] }))))
  const onCommand = renderComposer()
  await userEvent.type(screen.getByRole('textbox', { name: '消息' }), '/resume')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

it('平台命令遇到 agent_busy 不自动排队重试', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json({ error: { code: 'agent_busy', message: 'Agent正忙', details: { currentRunId: crypto.randomUUID(), queued: { interrupt: 0, normal: 0 } } } }, { status: 409 })
  }))
  const onCommand = renderComposer()
  await userEvent.type(screen.getByRole('textbox', { name: '消息' }), '/reload')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  expect(await screen.findByText(/Agent 正忙/)).toBeInTheDocument()
  expect(sent).toEqual([{ text: '/reload' }])
  expect(onCommand).not.toHaveBeenCalled()
  expect(screen.getByLabelText('消息')).toHaveValue('/reload')
})

it('/copy 先写剪贴板再显示结果；没有回复时不写剪贴板', async () => {
  const writeText = vi.fn(() => Promise.resolve())
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  let copyText: string | null = '最近一条成功回复'
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), () => HttpResponse.json(commandResult({ command: 'copy', copyText }))))
  const onCommand = renderComposer()
  const box = screen.getByRole('textbox', { name: '消息' })
  await userEvent.type(box, '/copy')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(writeText).toHaveBeenCalledWith('最近一条成功回复')
  expect(writeText.mock.invocationCallOrder[0]).toBeLessThan(onCommand.mock.invocationCallOrder[0])
  copyText = null
  await userEvent.type(box, '/copy')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(2))
  expect(writeText).toHaveBeenCalledTimes(1)
  expect(onCommand.mock.calls[1][0].copyText).toBeNull()
})

it('/export 取得文件并触发下载后才显示命令结果', async () => {
  const createObjectURL = vi.fn(() => 'blob:session')
  const revokeObjectURL = vi.fn()
  Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL })
  Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL })
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    expect(this.download).toBe('session.html')
  })
  server.use(
    http.post(apiUrl('/agents/sleep-agent/messages'), () => HttpResponse.json(commandResult({ command: 'export', exportFormat: 'html' }))),
    http.get(apiUrl('/agents/sleep-agent/session/export'), ({ request }) => {
      expect(new URL(request.url).searchParams.get('format')).toBe('html')
      return new HttpResponse('<html>session</html>', { headers: { 'Content-Type': 'text/html', 'Content-Disposition': 'attachment; filename="session.html"' } })
    }),
  )
  const onCommand = renderComposer()
  await userEvent.type(screen.getByRole('textbox', { name: '消息' }), '/export')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(createObjectURL).toHaveBeenCalledTimes(1)
  expect(click).toHaveBeenCalledTimes(1)
  expect(revokeObjectURL).toHaveBeenCalledWith('blob:session')
  expect(click.mock.invocationCallOrder[0]).toBeLessThan(onCommand.mock.invocationCallOrder[0])
})
