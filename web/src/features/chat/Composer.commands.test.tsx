import { afterEach, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { createRef } from 'react'
import { Composer, type ComposerHandle } from './Composer'
import { apiUrl, makeMessage, makeRun } from '@/test/api'
import { claudeCodeList, commandList } from '@/test/commands'
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

// 平台命令因忙被拒一律是 409 agent_not_idle（不带 details）：说明原因，不重试、不转成排队，命令留在输入框里。
it.each(['/reload', '/resume', '/new'])('%s 遇到 agent_not_idle：说明 Agent 不空闲，不重试也不排队', async (command) => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json({ error: { code: 'agent_not_idle', message: 'agent is not idle' } }, { status: 409 })
  }))
  const onCommand = renderComposer()
  await userEvent.type(screen.getByRole('textbox', { name: '消息' }), command)
  await userEvent.keyboard('{Escape}{Enter}')
  // /new 先确认。
  if (command === '/new') await userEvent.click(await screen.findByRole('button', { name: '重开会话' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Agent 不空闲：还有进行中的回复或排队消息')
  expect(sent).toEqual([{ text: command }])
  expect(onCommand).not.toHaveBeenCalled()
  expect(screen.getByLabelText('消息')).toHaveValue(command)
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

it.each([
  ['浏览器拒绝写入（已不在按键手势内）', { writeText: () => Promise.reject(new DOMException('denied', 'NotAllowedError')) }],
  ['用 http 的局域网地址打开，没有 navigator.clipboard', undefined],
])('/copy 写剪贴板失败（%s）：不报错、不丢内容，结果交给对话页并标明要手动复制，输入框里的命令照常清掉', async (_name, clipboard) => {
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: clipboard })
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), () => HttpResponse.json(commandResult({ command: 'copy', copyText: '最近一条成功回复' }))))
  const onCommand = renderComposer()
  const box = screen.getByRole('textbox', { name: '消息' })
  await userEvent.type(box, '/copy')
  await userEvent.keyboard('{Escape}{Enter}')
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(onCommand.mock.calls[0][0].copyText).toBe('最近一条成功回复')
  expect(onCommand.mock.calls[0][1]).toBe('/copy')
  expect(onCommand.mock.calls[0][2]).toEqual({ copyFailed: true })
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(box).toHaveValue('')
})

// 从列表恢复时输入框里被填上“/resume <id>”。这不是在输入命令：不该打开命令菜单，也就不该为它去读命令列表。
// （菜单即使打开，确认框拿走焦点时也会被关掉，所以看不到它；这里用“有没有去读命令列表”来确认菜单根本没有被要求打开。）
it('从会话列表点“恢复”：弹出确认框，不打开命令菜单', async () => {
  let reads = 0
  server.use(http.get(apiUrl('/agents/sleep-agent/commands'), () => { reads += 1; return HttpResponse.json(commandList()) }))
  const handle = createRef<ComposerHandle>()
  renderWithQuery(<Composer ref={handle} agentId="sleep-agent" agentName="开发助手" busy={false} disabled={false} maxMessageBytes={4096} onSent={vi.fn()} />)
  const box = screen.getByRole('textbox', { name: '消息' })
  act(() => handle.current!.resume('12345678-1234-1234-1234-123456789abc'))
  expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
  expect(box).toHaveValue('/resume 12345678-1234-1234-1234-123456789abc')
  await new Promise((resolve) => setTimeout(resolve, 50))
  expect(reads).toBe(0)
  expect(document.querySelector('[role="listbox"]')).toBeNull()
  expect(box).toHaveAttribute('aria-expanded', 'false')
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
  // blob 地址要等浏览器读完再撤销（见 session-export.test.ts），不在点击后立刻撤销。
  expect(revokeObjectURL).not.toHaveBeenCalled()
  expect(click.mock.invocationCallOrder[0]).toBeLessThan(onCommand.mock.invocationCallOrder[0])
})

it('输入 / 显示全部内置命令，过滤后用键盘填入，再次回车才发送', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json(commandResult({ command: 'resume', resumeSessions: [] }))
  }))
  renderComposer()
  const box = screen.getByRole('textbox', { name: '消息' })
  await userEvent.type(box, '/')
  expect(screen.getByRole('listbox', { name: '命令' })).toBeInTheDocument()
  expect(screen.getAllByRole('option')).toHaveLength(7)
  for (const usage of ['/session', '/new', '/compact [instructions]', '/reload', '/copy', '/export [html|jsonl]', '/resume [session-id]']) {
    expect(screen.getByText(usage)).toBeInTheDocument()
  }
  await userEvent.type(box, 're')
  expect(screen.getAllByRole('option')).toHaveLength(2)
  expect(screen.getByRole('option', { name: /\/reload/ })).toHaveAttribute('aria-selected', 'true')
  await userEvent.keyboard('{ArrowDown}')
  expect(screen.getByRole('option', { name: /\/resume/ })).toHaveAttribute('aria-selected', 'true')
  await userEvent.keyboard('{Enter}')
  expect(box).toHaveValue('/resume')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  expect(sent).toEqual([])
  await userEvent.keyboard('{Enter}')
  await waitFor(() => expect(sent).toEqual([{ text: '/resume' }]))
})

it('鼠标选择命令只填入输入框，不直接发送', async () => {
  renderComposer()
  const box = screen.getByRole('textbox', { name: '消息' })
  await userEvent.type(box, '/')
  await userEvent.click(screen.getByRole('option', { name: /\/export/ }))
  expect(box).toHaveValue('/export')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

it('Esc 关闭提示后保留命令，回车仍可直接发送', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json(commandResult({ command: 'reload' }))
  }))
  renderComposer()
  const box = screen.getByRole('textbox', { name: '消息' })
  await userEvent.type(box, '/reload')
  expect(screen.getByRole('listbox')).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  expect(box).toHaveValue('/reload')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  await userEvent.keyboard('{Enter}')
  await waitFor(() => expect(sent).toEqual([{ text: '/reload' }]))
})

it('会话丢失时命令提示只开放 /resume', async () => {
  renderWithQuery(<Composer agentId="sleep-agent" agentName="开发助手" busy={false} disabled allowResumeWhenDisabled maxMessageBytes={4096} onSent={vi.fn()} />)
  // 命令列表取到、确认 /resume 此刻可用之后，输入框才放开。
  const box = screen.getByRole('textbox', { name: '消息' })
  expect(box).toBeDisabled()
  await waitFor(() => expect(box).toBeEnabled())
  await userEvent.type(box, '/')
  expect(screen.getAllByRole('option')).toHaveLength(1)
  expect(screen.getByRole('option', { name: /\/resume/ })).toBeInTheDocument()
  expect(screen.queryByRole('option', { name: /\/new/ })).not.toBeInTheDocument()
})

it('会话丢失而 /resume 不可用（Claude Code 的 Agent）时输入框保持禁用，不留只能输入一条必被拒绝的命令的死路', async () => {
  let reads = 0
  server.use(http.get(apiUrl('/agents/sleep-agent/commands'), () => { reads += 1; return HttpResponse.json(claudeCodeList()) }))
  renderWithQuery(<Composer agentId="sleep-agent" agentName="开发助手" busy={false} disabled allowResumeWhenDisabled maxMessageBytes={4096} onSent={vi.fn()} />)
  await waitFor(() => expect(reads).toBe(1))
  await new Promise((resolve) => setTimeout(resolve, 30))
  expect(screen.getByRole('textbox', { name: '消息' })).toBeDisabled()
  expect(screen.getByRole('button', { name: '发送' })).toBeDisabled()
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
})

it('命令列表取不到时，/new 仍然先确认再发', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(
    http.get(apiUrl('/agents/sleep-agent/commands'), () => HttpResponse.json({ error: { code: 'internal_error', message: 'boom' } }, { status: 500 })),
    http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
      sent.push(await request.json() as Schemas['SendMessageInput'])
      return HttpResponse.json(commandResult({ command: 'new', resetSession: { state: 'active', epoch: 2 } }))
    }),
  )
  const onCommand = renderComposer()
  await userEvent.type(screen.getByRole('textbox', { name: '消息' }), '/new')
  await userEvent.keyboard('{Escape}{Enter}')
  expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
  expect(sent).toEqual([])
  await userEvent.click(screen.getByRole('button', { name: '重开会话' }))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(sent).toEqual([{ text: '/new' }])
})

it('没在服务端列表里的 "/" 开头文字是普通消息：正忙时照常排队重发', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']; sent.push(body)
    if (!body.whenBusy) return HttpResponse.json({ error: { code: 'agent_busy', message: 'Agent正忙', details: { currentRunId: crypto.randomUUID(), queued: { interrupt: 0, normal: 0 } } } }, { status: 409 })
    const run = makeRun({ status: 'queued', whenBusy: body.whenBusy })
    return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
  }))
  renderComposer()
  await userEvent.type(screen.getByRole('textbox', { name: '消息' }), '/usr/bin 是什么')
  await userEvent.keyboard('{Enter}')
  await waitFor(() => expect(sent).toEqual([{ text: '/usr/bin 是什么' }, { text: '/usr/bin 是什么', whenBusy: 'queue' }]))
})

it('补全平台命令时保留已经写好的参数', async () => {
  renderComposer()
  const box = screen.getByRole('textbox', { name: '消息' })
  await userEvent.type(box, '/comp 保留关键决定')
  await userEvent.keyboard('{Home}{ArrowRight}')
  await userEvent.click(screen.getByRole('option', { name: /\/compact/ }))
  expect(box).toHaveValue('/compact 保留关键决定')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
})

it('会话丢失时读不到命令列表：不把输入框锁死，仍放行 /resume（由服务端判断），并在菜单里说明读不到', async () => {
  server.use(http.get(apiUrl('/agents/sleep-agent/commands'), () => HttpResponse.json({ error: { code: 'internal_error', message: 'boom' } }, { status: 500 })))
  renderWithQuery(<Composer agentId="sleep-agent" agentName="开发助手" busy={false} disabled allowResumeWhenDisabled maxMessageBytes={4096} onSent={vi.fn()} />)
  const box = screen.getByRole('textbox', { name: '消息' })
  await waitFor(() => expect(box).toBeEnabled())
  await userEvent.type(box, '/res')
  expect(await screen.findByRole('status')).toHaveTextContent(/^读不到命令列表：/)
  await userEvent.clear(box)
  await userEvent.type(box, '/resume')
  await userEvent.keyboard('{Escape}')
  expect(screen.getByRole('button', { name: '发送' })).toBeEnabled()
  await userEvent.clear(box)
  await userEvent.type(box, '你好')
  expect(screen.getByRole('button', { name: '发送' })).toBeDisabled()
})

it('发送前还要现取命令列表时，发送按钮先变成不可点，表示正在处理', async () => {
  let release!: () => void
  const gate = new Promise<void>((resolve) => { release = resolve })
  const sent: Schemas['SendMessageInput'][] = []
  server.use(
    http.get(apiUrl('/agents/sleep-agent/commands'), async () => { await gate; return HttpResponse.json(commandList()) }),
    http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
      sent.push(await request.json() as Schemas['SendMessageInput'])
      return HttpResponse.json(commandResult({ command: 'reload', processStopped: true }))
    }),
  )
  const onCommand = renderComposer()
  const box = screen.getByRole('textbox', { name: '消息' })
  // 粘贴进来的命令没有经过“/”菜单，列表还没取过。
  await userEvent.click(box)
  await userEvent.paste('/reload')
  await userEvent.keyboard('{Escape}')
  await userEvent.click(screen.getByRole('button', { name: '发送' }))
  await waitFor(() => expect(screen.getByRole('button', { name: '发送' })).toBeDisabled())
  expect(sent).toEqual([])
  release()
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(sent).toEqual([{ text: '/reload' }])
})
