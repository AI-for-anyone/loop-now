import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { delay, http, HttpResponse } from 'msw'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { apiUrl, errorBody } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { automaticSettings, haikuWriter, neverAnswers, textGenerationBackend } from '@/test/text-generation'
import { PromptWriter, type PromptWriterAsk } from './PromptWriter'

// An editor as the Prompt editors hold one: a label, a text box whose text the writer replaces, locked while it writes.
function Editor({ initial, ask }: { initial: string; ask: PromptWriterAsk }) {
  const [text, setText] = useState(initial)
  return (
    <PromptWriter label={<Label htmlFor="prompt">Agent prompt</Label>} text={text} onReplace={setText} ask={ask}>
      {(writing) => <Textarea id="prompt" value={text} disabled={writing} onChange={(event) => setText(event.target.value)} />}
    </PromptWriter>
  )
}

function setup({ initial = '', ask = { target: 'agent', agentId: '0196a1b2-0000-7000-8000-000000000001' }, ...init }: { initial?: string; ask?: PromptWriterAsk } & Parameters<typeof textGenerationBackend>[0] = {}) {
  const backend = textGenerationBackend(init)
  server.use(...backend.handlers)
  const { unmount } = renderRoutes([{ path: '/', element: <Editor initial={initial} ask={ask} /> }], '/')
  return { ...backend, unmount, user: userEvent.setup() }
}
const editor = () => screen.getByLabelText('Agent prompt')
const open = () => screen.getByRole('button', { name: 'Write with AI' })
const panel = () => screen.getByRole('region', { name: 'Write with AI' })
const request = () => within(panel()).getByLabelText('What should it do?')
const write = () => within(panel()).getByRole('button', { name: 'Write' })

describe('Write with AI beside a Prompt', () => {
  it('opens a panel above the text: what to ask, that it fills the box, and who writes; Write waits for a request while the box is empty', async () => {
    const { user } = setup()
    expect(open()).toHaveAttribute('aria-expanded', 'false')
    await user.click(open())
    expect(open()).toHaveAttribute('aria-expanded', 'true')
    expect(request()).toHaveFocus()
    expect(request()).toHaveAttribute('maxlength', '4000')
    expect(request()).toHaveAttribute('placeholder', 'e.g. Every morning, sort the new GitHub issues and post them to the Lark group')
    expect(within(panel()).getByText('It fills the box when done. You can undo.')).toBeInTheDocument()
    expect(await within(panel()).findByText('By Claude Code · Claude Haiku (latest)')).toBeInTheDocument()
    expect(within(panel()).getByRole('link', { name: 'Change' })).toHaveAttribute('href', '/settings#settings-writing')
    expect(write()).toBeDisabled()
    await user.type(request(), '   ')
    expect(write()).toBeDisabled()
    await user.click(within(panel()).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('region', { name: 'Write with AI' })).toBeNull()
    expect(open()).toHaveFocus()
  })

  it('sends the request with the text in the box and what it is for, replaces the text, and Undo puts the old text back', async () => {
    const { user, state } = setup({ initial: 'Answer questions.' })
    await user.click(open())
    expect(within(panel()).getByText("It reads what's in the box, then replaces it. You can undo.")).toBeInTheDocument()
    await user.type(request(), 'Sort new issues')
    await user.click(write())
    expect(await screen.findByText('Replaced')).toBeInTheDocument()
    expect(state.drafts).toEqual([{ target: 'agent', agentId: '0196a1b2-0000-7000-8000-000000000001', request: 'Sort new issues', currentText: 'Answer questions.' }])
    expect(editor()).toHaveValue('Written: Sort new issues')
    expect(screen.queryByRole('region', { name: 'Write with AI' })).toBeNull()
    // Focus goes back to the button that opened the panel, not to the page.
    await vi.waitFor(() => expect(open()).toHaveFocus())
    await user.click(screen.getByRole('button', { name: 'Undo' }))
    expect(editor()).toHaveValue('Answer questions.')
    expect(screen.queryByText('Replaced')).toBeNull()
    expect(open()).toHaveFocus()
  })

  it('a double click on Write writes once: the second click does not land on Stop', async () => {
    const { user, state } = setup({
      draft: async (body) => {
        await delay(100)
        return HttpResponse.json({ text: `Written: ${body.request}`, writer: haikuWriter() } satisfies Schemas['PromptDraft'])
      },
    })
    await user.click(open())
    await user.type(request(), 'Sort new issues')
    await user.dblClick(write())
    expect(await screen.findByText('Filled in')).toBeInTheDocument()
    expect(state.drafts).toHaveLength(1)
    expect(state.aborted).toBe(0)
  })

  it('Escape closes the panel and gives focus back to the button; while writing it stops', async () => {
    const { user, state } = setup({ draft: neverAnswers })
    await user.click(open())
    await user.type(request(), 'Sort new issues')
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('region', { name: 'Write with AI' })).toBeNull()
    expect(open()).toHaveFocus()
    await user.click(open())
    await user.click(write())
    await within(panel()).findByRole('status')
    request().focus()
    await user.keyboard('{Escape}')
    await vi.waitFor(() => expect(state.aborted).toBe(1))
    expect(panel()).toBeInTheDocument()
  })

  it('with text in the box the request may be empty (make it better), and Ctrl+Enter writes', async () => {
    const { user, state } = setup({ initial: 'Answer questions.' })
    await user.click(open())
    expect(request()).toHaveAttribute('placeholder', 'e.g. Make it shorter, and ask me first when something fails. Leave empty to make it clearer as it is')
    expect(write()).toBeEnabled()
    await user.keyboard('{Control>}{Enter}{/Control}')
    await vi.waitFor(() => expect(state.drafts).toHaveLength(1))
    expect(state.drafts[0]).toMatchObject({ request: '', currentText: 'Answer questions.' })
  })

  it('while writing, the text is locked and the time counts; Stop aborts the request and replaces nothing', async () => {
    const { user, state } = setup({ initial: 'Answer questions.', draft: neverAnswers })
    await user.click(open())
    await user.type(request(), 'Sort new issues')
    await user.click(write())
    const status = await within(panel()).findByRole('status')
    expect(status).toHaveTextContent('Writing…')
    // The time so far ticks every second; it is left out of what screen readers announce.
    expect(within(status).getByText('0 s')).toHaveAttribute('aria-hidden', 'true')
    expect(editor()).toBeDisabled()
    expect(request()).toHaveAttribute('readonly')
    await user.click(within(panel()).getByRole('button', { name: 'Stop' }))
    await vi.waitFor(() => expect(state.aborted).toBe(1))
    expect(editor()).toBeEnabled()
    expect(editor()).toHaveValue('Answer questions.')
    expect(request()).toHaveValue('Sort new issues')
    expect(write()).toBeEnabled()
    expect(within(panel()).queryByRole('alert')).toBeNull()
  })

  it('stops the request when the editor goes away', async () => {
    const { user, state, unmount } = setup({ draft: neverAnswers })
    await user.click(open())
    await user.type(request(), 'Sort new issues')
    await user.click(write())
    await within(panel()).findByRole('status')
    // The editor goes away, as when the New Agent dialog is closed.
    unmount()
    await vi.waitFor(() => expect(state.aborted).toBe(1))
  })

  it("a failure stays in the panel with the server's reason, and the request is kept", async () => {
    const { user } = setup({ draft: () => HttpResponse.json(errorBody('text_generation_failed', 'Claude Code: usage limit reached'), { status: 502 }) })
    await user.click(open())
    await user.type(request(), 'Sort new issues')
    await user.click(write())
    expect(await within(panel()).findByText(/Claude Code: usage limit reached/)).toBeInTheDocument()
    expect(within(panel()).getByText(/Couldn't write it/)).toBeInTheDocument()
    expect(request()).toHaveValue('Sort new issues')
    expect(editor()).toHaveValue('')
  })

  it('when the chosen one could not write, says who wrote it instead', async () => {
    const fellBack = haikuWriter({ fellBack: true })
    const { user } = setup({
      settings: { selection: { provider: 'claude-code', modelId: 'opus', thinkingLevel: null }, writer: fellBack },
      draft: (body) => HttpResponse.json({ text: `Written: ${body.request}`, writer: fellBack } satisfies Schemas['PromptDraft']),
    })
    await user.click(open())
    await user.type(request(), 'Sort new issues')
    await user.click(write())
    expect(await screen.findByText("Claude Code · opus couldn't write this time, so Claude Code · Claude Haiku (latest) wrote it.")).toBeInTheDocument()
  })

  it('when the chosen one could not write and the setting could not be read, still says who wrote it', async () => {
    const { user } = setup({ draft: (body) => HttpResponse.json({ text: `Written: ${body.request}`, writer: haikuWriter({ fellBack: true }) } satisfies Schemas['PromptDraft']) })
    server.use(http.get(apiUrl('/text-generation-settings'), () => HttpResponse.json(errorBody('internal_error'), { status: 500 })))
    await user.click(open())
    await user.type(request(), 'Sort new issues')
    await user.click(write())
    expect(await screen.findByText("The one you chose couldn't write this time, so Claude Code · Claude Haiku (latest) wrote it.")).toBeInTheDocument()
  })

  it('the undo line goes once the text is edited', async () => {
    const { user } = setup()
    await user.click(open())
    await user.type(request(), 'Sort new issues')
    await user.click(write())
    await screen.findByText('Filled in')
    await user.type(editor(), '!')
    expect(screen.queryByText('Filled in')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Undo' })).toBeNull()
  })

  it('with no CLI ready, says so, links to the settings, and cannot write', async () => {
    const { user } = setup({ initial: 'Answer questions.', settings: automaticSettings(null) })
    await user.click(open())
    expect(await within(panel()).findByText('No CLI on this computer can write yet.')).toBeInTheDocument()
    expect(within(panel()).getByRole('link', { name: 'Set one up' })).toHaveAttribute('href', '/settings#settings-writing')
    expect(write()).toBeDisabled()
  })

  it('when who writes cannot be read, it still writes and the server picks', async () => {
    const { user, state } = setup({ initial: 'Answer questions.' })
    server.use(http.get(apiUrl('/text-generation-settings'), () => HttpResponse.json(errorBody('internal_error'), { status: 500 })))
    await user.click(open())
    expect(await within(panel()).findByText("Couldn't read who writes; the server picks one when you write.")).toBeInTheDocument()
    await user.click(write())
    await vi.waitFor(() => expect(state.drafts).toHaveLength(1))
  })

  it("a project's prompt asks with the project and its own example", async () => {
    const { user, state } = setup({ ask: { target: 'project', projectId: '0196a1b2-0000-7000-8000-0000000000aa' } })
    await user.click(open())
    expect(request()).toHaveAttribute('placeholder', 'e.g. This project is a mobile app; reply in Chinese; ask before deploying')
    await user.type(request(), 'Mobile app')
    await user.click(write())
    await vi.waitFor(() => expect(state.drafts).toEqual([{ target: 'project', projectId: '0196a1b2-0000-7000-8000-0000000000aa', request: 'Mobile app', currentText: '' }]))
  })
})
