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
  renderWithQuery(<Composer agentId="sleep-agent" agentName="dev-helper" busy={false} disabled={false} maxMessageBytes={4096} onSent={vi.fn()} onCommand={onCommand} />)
  return onCommand
}

it('/new must be confirmed before sending; only after confirming does it call the API and show the result', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json(commandResult({ command: 'new', resetSession: { state: 'none', epoch: 2 } }))
  }))
  const onCommand = renderComposer()
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '/new')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(await screen.findByRole('alertdialog')).toHaveTextContent('Start a new session')
  expect(sent).toEqual([])
  expect(onCommand).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: 'Start new session' }))
  await waitFor(() => expect(sent).toEqual([{ text: '/new' }]))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(onCommand.mock.calls[0][0].resetSession?.epoch).toBe(2)
  expect(box).toHaveValue('')
})

it('/resume with an ID sends no request when cancelled, and the confirmation says configuration and workspace are not rolled back', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json(commandResult({ command: 'resume', resumedSession: { state: 'active', epoch: 3 } }))
  }))
  const onCommand = renderComposer()
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '/resume full-session-id')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  expect(await screen.findByRole('alertdialog')).toHaveTextContent('The workspace, model configuration, Prompt, Skills, and permissions are not rolled back')
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(sent).toEqual([])
  expect(onCommand).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Resume session' }))
  await waitFor(() => expect(sent).toEqual([{ text: '/resume full-session-id' }]))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(onCommand.mock.calls[0][0].resumedSession?.epoch).toBe(3)
})

it('the /resume list needs no confirmation', async () => {
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), () => HttpResponse.json(commandResult({ command: 'resume', resumeSessions: [] }))))
  const onCommand = renderComposer()
  await userEvent.type(screen.getByRole('textbox', { name: 'Message' }), '/resume')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

// A platform command rejected because the Agent is busy always gets 409 agent_not_idle (without details):
// explain why, do not retry, do not turn it into a queued message, and leave the command in the input.
it.each(['/reload', '/resume', '/new'])('%s hitting agent_not_idle: says the Agent is not idle, neither retries nor queues', async (command) => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json({ error: { code: 'agent_not_idle', message: 'agent is not idle' } }, { status: 409 })
  }))
  const onCommand = renderComposer()
  await userEvent.type(screen.getByRole('textbox', { name: 'Message' }), command)
  await userEvent.keyboard('{Escape}{Enter}')
  // /new asks for confirmation first.
  if (command === '/new') await userEvent.click(await screen.findByRole('button', { name: 'Start new session' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('The Agent isn\'t idle: a reply is in progress or messages are queued')
  expect(sent).toEqual([{ text: command }])
  expect(onCommand).not.toHaveBeenCalled()
  expect(screen.getByLabelText('Message')).toHaveValue(command)
})


it('/copy writes the clipboard before showing the result; with no reply it does not write the clipboard', async () => {
  const writeText = vi.fn(() => Promise.resolve())
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  let copyText: string | null = 'Latest successful reply'
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), () => HttpResponse.json(commandResult({ command: 'copy', copyText }))))
  const onCommand = renderComposer()
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '/copy')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(writeText).toHaveBeenCalledWith('Latest successful reply')
  expect(writeText.mock.invocationCallOrder[0]).toBeLessThan(onCommand.mock.invocationCallOrder[0])
  copyText = null
  await userEvent.type(box, '/copy')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(2))
  expect(writeText).toHaveBeenCalledTimes(1)
  expect(onCommand.mock.calls[1][0].copyText).toBeNull()
})

it.each([
  ['the browser refuses the write (no longer within the key gesture)', { writeText: () => Promise.reject(new DOMException('denied', 'NotAllowedError')) }],
  ['opened at a LAN http address, with no navigator.clipboard', undefined],
])('/copy fails to write the clipboard (%s): no error and no lost content; the result goes to the chat page marked for manual copying, and the command in the input is cleared as usual', async (_name, clipboard) => {
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: clipboard })
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), () => HttpResponse.json(commandResult({ command: 'copy', copyText: 'Latest successful reply' }))))
  const onCommand = renderComposer()
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '/copy')
  await userEvent.keyboard('{Escape}{Enter}')
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(onCommand.mock.calls[0][0].copyText).toBe('Latest successful reply')
  expect(onCommand.mock.calls[0][1]).toBe('/copy')
  expect(onCommand.mock.calls[0][2]).toEqual({ copyFailed: true })
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(box).toHaveValue('')
})

// Resuming from the list fills the input with "/resume <id>". That is not typing a command:
// it should not open the command menu, and so should not read the command list for it.
// (Even if the menu did open, it would close when the confirmation dialog takes focus, so it can't be
// seen; here "was the command list read" is used to confirm that the menu was never asked to open at all.)
it('clicking “Resume” in the session list opens the confirmation dialog, not the command menu', async () => {
  let reads = 0
  server.use(http.get(apiUrl('/agents/sleep-agent/commands'), () => { reads += 1; return HttpResponse.json(commandList()) }))
  const handle = createRef<ComposerHandle>()
  renderWithQuery(<Composer ref={handle} agentId="sleep-agent" agentName="dev-helper" busy={false} disabled={false} maxMessageBytes={4096} onSent={vi.fn()} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  act(() => handle.current!.resume('12345678-1234-1234-1234-123456789abc'))
  expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
  expect(box).toHaveValue('/resume 12345678-1234-1234-1234-123456789abc')
  await new Promise((resolve) => setTimeout(resolve, 50))
  expect(reads).toBe(0)
  expect(document.querySelector('[role="listbox"]')).toBeNull()
  expect(box).toHaveAttribute('aria-expanded', 'false')
})

it('/export shows the command result only after fetching the file and starting the download', async () => {
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
  await userEvent.type(screen.getByRole('textbox', { name: 'Message' }), '/export')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(createObjectURL).toHaveBeenCalledTimes(1)
  expect(click).toHaveBeenCalledTimes(1)
  // The blob URL is revoked only after the browser has finished reading it (see session-export.test.ts), not right after the click.
  expect(revokeObjectURL).not.toHaveBeenCalled()
  expect(click.mock.invocationCallOrder[0]).toBeLessThan(onCommand.mock.invocationCallOrder[0])
})

it('typing / shows all built-in commands; after filtering, the keyboard fills one in, and only a second Enter sends', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json(commandResult({ command: 'resume', resumeSessions: [] }))
  }))
  renderComposer()
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '/')
  expect(screen.getByRole('listbox', { name: 'Commands' })).toBeInTheDocument()
  expect(screen.getAllByRole('option')).toHaveLength(7)
  for (const usage of ['/session', '/new', '/compact [instructions]', '/reload', '/copy', '/export [html|jsonl]', '/resume [session-id]']) {
    expect(screen.getByText(usage)).toBeInTheDocument()
  }
  await userEvent.type(box, 're')
  // In English “re” also matches the descriptions of /session, /compact, /copy and /export (“current”, “reply”); the two name
  // matches come first.
  expect(screen.getAllByRole('option')).toHaveLength(6)
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

it('choosing a command with the mouse only fills the input and does not send', async () => {
  renderComposer()
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '/')
  await userEvent.click(screen.getByRole('option', { name: /\/export/ }))
  expect(box).toHaveValue('/export')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})

it('Esc closes the suggestions and keeps the command; Enter still sends directly', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json(commandResult({ command: 'reload' }))
  }))
  renderComposer()
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '/reload')
  expect(screen.getByRole('listbox')).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  expect(box).toHaveValue('/reload')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  await userEvent.keyboard('{Enter}')
  await waitFor(() => expect(sent).toEqual([{ text: '/reload' }]))
})

it('when the session is missing, the command suggestions offer only /resume', async () => {
  renderWithQuery(<Composer agentId="sleep-agent" agentName="dev-helper" busy={false} disabled allowResumeWhenDisabled maxMessageBytes={4096} onSent={vi.fn()} />)
  // The input is enabled only after the command list has been fetched and confirms that /resume is available right now.
  const box = screen.getByRole('textbox', { name: 'Message' })
  expect(box).toBeDisabled()
  await waitFor(() => expect(box).toBeEnabled())
  await userEvent.type(box, '/')
  expect(screen.getAllByRole('option')).toHaveLength(1)
  expect(screen.getByRole('option', { name: /\/resume/ })).toBeInTheDocument()
  expect(screen.queryByRole('option', { name: /\/new/ })).not.toBeInTheDocument()
})

it('when the session is missing and /resume is unavailable (a Claude Code Agent), the input stays disabled, leaving no dead end where the only thing to type is a command sure to be rejected', async () => {
  let reads = 0
  server.use(http.get(apiUrl('/agents/sleep-agent/commands'), () => { reads += 1; return HttpResponse.json(claudeCodeList()) }))
  renderWithQuery(<Composer agentId="sleep-agent" agentName="dev-helper" busy={false} disabled allowResumeWhenDisabled maxMessageBytes={4096} onSent={vi.fn()} />)
  await waitFor(() => expect(reads).toBe(1))
  await new Promise((resolve) => setTimeout(resolve, 30))
  expect(screen.getByRole('textbox', { name: 'Message' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled()
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
})

it('when the command list cannot be fetched, /new is still confirmed before sending', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(
    http.get(apiUrl('/agents/sleep-agent/commands'), () => HttpResponse.json({ error: { code: 'internal_error', message: 'boom' } }, { status: 500 })),
    http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
      sent.push(await request.json() as Schemas['SendMessageInput'])
      return HttpResponse.json(commandResult({ command: 'new', resetSession: { state: 'active', epoch: 2 } }))
    }),
  )
  const onCommand = renderComposer()
  await userEvent.type(screen.getByRole('textbox', { name: 'Message' }), '/new')
  await userEvent.keyboard('{Escape}{Enter}')
  expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
  expect(sent).toEqual([])
  await userEvent.click(screen.getByRole('button', { name: 'Start new session' }))
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(sent).toEqual([{ text: '/new' }])
})

it('text starting with "/" that is not in the server list is a normal message: when busy it is queued and sent again as usual', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']; sent.push(body)
    if (!body.whenBusy) return HttpResponse.json({ error: { code: 'agent_busy', message: 'The Agent is replying; interrupt it or queue the message', details: { currentRunId: crypto.randomUUID(), queued: { interrupt: 0, normal: 0 } } } }, { status: 409 })
    const run = makeRun({ status: 'queued', whenBusy: body.whenBusy })
    return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
  }))
  renderComposer()
  await userEvent.type(screen.getByRole('textbox', { name: 'Message' }), '/usr/bin, what is it?')
  await userEvent.keyboard('{Enter}')
  await waitFor(() => expect(sent).toEqual([{ text: '/usr/bin, what is it?' }, { text: '/usr/bin, what is it?', whenBusy: 'queue' }]))
})

it('completing a platform command keeps the arguments already written', async () => {
  renderComposer()
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, '/comp Keep the key decisions')
  await userEvent.keyboard('{Home}{ArrowRight}')
  await userEvent.click(screen.getByRole('option', { name: /\/compact/ }))
  expect(box).toHaveValue('/compact Keep the key decisions')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
})

it('when the session is missing and the command list cannot be read: does not lock the input, still lets /resume through (the server decides), and says in the menu that the list could not be read', async () => {
  server.use(http.get(apiUrl('/agents/sleep-agent/commands'), () => HttpResponse.json({ error: { code: 'internal_error', message: 'boom' } }, { status: 500 })))
  renderWithQuery(<Composer agentId="sleep-agent" agentName="dev-helper" busy={false} disabled allowResumeWhenDisabled maxMessageBytes={4096} onSent={vi.fn()} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await waitFor(() => expect(box).toBeEnabled())
  await userEvent.type(box, '/res')
  expect(await screen.findByRole('status')).toHaveTextContent(/^Couldn't load the command list: /)
  await userEvent.clear(box)
  await userEvent.type(box, '/resume')
  await userEvent.keyboard('{Escape}')
  expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled()
  await userEvent.clear(box)
  await userEvent.type(box, 'Hello')
  expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled()
})

it('when the command list still has to be fetched before sending, the send button is disabled first to show it is working', async () => {
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
  const box = screen.getByRole('textbox', { name: 'Message' })
  // A pasted command did not go through the "/" menu, so the list has not been fetched yet.
  await userEvent.click(box)
  await userEvent.paste('/reload')
  await userEvent.keyboard('{Escape}')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled())
  expect(sent).toEqual([])
  release()
  await waitFor(() => expect(onCommand).toHaveBeenCalledTimes(1))
  expect(sent).toEqual([{ text: '/reload' }])
})
