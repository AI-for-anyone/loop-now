import { render, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { buildSteps } from '@/features/chat/timeline-steps'
import { TimelineSteps } from '@/features/chat/TimelineBody'
import { InboxReminderLine } from '@/features/chat/InboxReminder'
import { partyDisplay } from '@/features/inbox/participants'
import { apiUrl } from '@/test/api'
import { renderChat } from '@/test/chat'

const pluginParty = (overrides: Partial<Schemas['InboxParticipant']> = {}): Schemas['InboxParticipant'] => ({
  kind: 'plugin',
  id: null,
  address: 'plugin:lark-notify/lark',
  name: 'Lark notifications',
  deleted: false,
  pluginId: 'lark-notify',
  channelId: 'lark',
  ...overrides,
})

describe('/ commands in the chat', () => {
  it('a command claimed by a plugin: the plugin reply shows as a one-line note, not as a sent message, and the input is cleared', async () => {
    const { user } = await renderChat({
      handlers: [http.post(apiUrl('/agents/:agentId/messages'), () => HttpResponse.json({ handled: true, source: 'plugin', command: '', plugin: 'webhook', reply: 'Sent 9 events in the last 24 hours, 0 failed.' }))],
    })
    const box = screen.getByRole('textbox', { name: 'Message' })
    await user.type(box, '/webhook status')
    await user.click(screen.getByRole('button', { name: 'Send' }))
    const command = await waitFor(() => document.querySelector('[data-console-command]') as HTMLElement)
    expect(command).toHaveTextContent('/webhook status')
    expect(command).toHaveTextContent('Plugin webhook handled this command')
    expect(command).toHaveTextContent('Sent 9 events in the last 24 hours, 0 failed.')
    expect(box).toHaveValue('')
    // It did not turn into a "sent by you" message bubble: the command text appears only in this one-line note.
    const shown = within(screen.getByRole('log')).getAllByText('/webhook status')
    expect(shown).toHaveLength(1)
    expect(shown[0].closest('[data-console-command]')).toBe(command)
  })
})

describe('Senders from a plugin channel', () => {
  it('the title reads "Plugin · name" and is marked when the plugin is uninstalled', () => {
    expect(partyDisplay(pluginParty())).toEqual({ title: 'Plugin · Lark notifications', deleted: null, address: 'plugin:lark-notify/lark' })
    expect(partyDisplay(pluginParty({ deleted: true, name: 'Uninstalled plugin' }))).toEqual({ title: 'Plugin · lark-notify', deleted: 'Uninstalled', address: 'plugin:lark-notify/lark' })
  })

  it('the platform reminder line shows a small tag and does not treat it as an external identity', () => {
    render(<InboxReminderLine at="2026-10-27T04:41:00Z" sender={pluginParty()} preview="Someone @-mentioned you in a Lark group" />)
    const note = screen.getByRole('note')
    expect(within(note).getByText('Plugin · Lark notifications').parentElement).toHaveAttribute('title', 'plugin:lark-notify/lark')
    expect(note).not.toHaveTextContent('From')
  })
})

describe('Extension requests in the run record', () => {
  const entry = (overrides: Partial<Extract<Schemas['TimelineEntry'], { type: 'extension_ui' }>>): Schemas['TimelineEntry'] => ({
    type: 'extension_ui',
    at: '2026-10-27T04:41:00Z',
    method: 'notify',
    message: null,
    level: null,
    statusKey: null,
    answered: null,
    questionId: null,
    ...overrides,
  })

  it('notices and statuses are faint steps (warnings and errors are colored); dialogs say answered, expired or cancelled, and those turned into questions carry the question ID', () => {
    const steps = buildSteps(null, [
      entry({ method: 'notify', message: 'Browser opened', level: 'info' }),
      entry({ method: 'notify', message: 'Login expired', level: 'error' }),
      entry({ method: 'setStatus', statusKey: 'browser', message: 'Loading example.com' }),
      entry({ method: 'setStatus', statusKey: 'browser', message: null }),
      entry({ method: 'confirm', message: 'Close all tabs?', answered: 'answered', questionId: '76000000-0000-7000-8000-000000000001' }),
      entry({ method: 'select', message: 'Pick a branch', answered: 'expired', questionId: '76000000-0000-7000-8000-000000000002' }),
      entry({ method: 'editor', message: 'Edit the commit message', answered: 'cancelled' }),
    ]).filter((step) => step.key.endsWith('extension_ui'))
    expect(steps.map((s) => s.questionId ?? null)).toEqual([null, null, null, null, '76000000-0000-7000-8000-000000000001', '76000000-0000-7000-8000-000000000002', null])
    expect(steps.map((s) => [s.label, s.field, s.detail, s.lines, s.tone])).toEqual([
      ['Extension notice', 'Info', 'Browser opened', [], 'neutral'],
      ['Extension notice', 'Error', 'Login expired', [], 'fail'],
      ['Extension status', 'browser', 'Loading example.com', [], 'neutral'],
      ['Extension status', 'browser', 'Cleared', [], 'neutral'],
      ['Extension dialog request', 'confirm', 'Close all tabs?', ['Answered'], 'neutral'],
      ['Extension dialog request', 'select', 'Pick a branch', ['Expired: no answer before the deadline, or the turn ended first'], 'neutral'],
      ['Extension dialog request', 'editor', 'Edit the commit message', ['Cancelled: this kind of dialog can\'t become a question, so it was answered with cancel'], 'neutral'],
    ])
  })

  it('a dialog request turned into a question has "View question", which jumps to that question in the chat', async () => {
    const steps = buildSteps(null, [entry({ method: 'confirm', message: 'Close all tabs?', answered: 'answered', questionId: '76000000-0000-7000-8000-000000000001' })])
    render(
      <>
        <div data-console-message-id="76000000-0000-7000-8000-000000000001">Original question</div>
        <TimelineSteps steps={steps} />
      </>,
    )
    const target = screen.getByText('Original question')
    const scroll = vi.fn()
    target.scrollIntoView = scroll
    screen.getByRole('button', { name: 'View question' }).click()
    expect(scroll).toHaveBeenCalledWith({ block: 'center', behavior: 'smooth' })
  })
})
