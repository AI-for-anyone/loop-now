import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, it, vi } from 'vitest'
import { Composer, KeyHint } from './Composer'
import { resetChatDraftsForTest } from './draft-store'
import { apiUrl, makeMessage, makeRun } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import type { Schemas } from '@/api/client'

afterEach(() => resetChatDraftsForTest())

it('sleep uses the idle busy=false send path, without requiring whenBusy or asking first', async () => {
  const sent: Schemas['SendMessageInput'][] = [], onSent = vi.fn()
  const run = makeRun({ status: 'starting', endedAt: null })
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']; sent.push(body)
    return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
  }))
  renderWithQuery(<Composer agentId="sleep-agent" agentName="dev-helper" busy={false} disabled={false} maxMessageBytes={4096} onSent={onSent} />)
  await userEvent.type(screen.getByRole('textbox', { name: 'Message' }), 'New work')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(sent).toEqual([{ text: 'New work' }]))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(onSent).toHaveBeenCalledTimes(1)
})

it('when an idle send races with a real busy state, does not ask and sends again with queue right away', async () => {
  const onSent = vi.fn()
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']; sent.push(body)
    if (!body.whenBusy) return HttpResponse.json({ error: { code: 'agent_busy', message: 'The Agent is replying; interrupt it or queue the message', details: { currentRunId: makeRun().id, queued: { interrupt: 0, normal: 1 } } } }, { status: 409 })
    const run = makeRun({ status: 'queued', whenBusy: body.whenBusy })
    return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
  }))
  renderWithQuery(<Composer agentId="sleep-agent" agentName="dev-helper" busy={false} disabled={false} maxMessageBytes={4096} onSent={onSent} />)
  await userEvent.type(screen.getByRole('textbox', { name: 'Message' }), 'Keep this work')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(sent).toEqual([{ text: 'Keep this work' }, { text: 'Keep this work', whenBusy: 'queue' }]))
  await waitFor(() => expect(onSent).toHaveBeenCalledTimes(1))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.getByLabelText('Message')).toHaveValue('')
})

it('a send error while busy is not retried; keeps the text and shows the error', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    sent.push(await request.json() as Schemas['SendMessageInput'])
    return HttpResponse.json({ error: { code: 'internal_error', message: 'Something went wrong' } }, { status: 500 })
  }))
  renderWithQuery(<Composer agentId="sleep-agent" agentName="dev-helper" busy disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  await userEvent.type(screen.getByRole('textbox', { name: 'Message' }), 'Work while busy')
  await userEvent.click(screen.getByRole('button', { name: 'Queue' }))
  expect(await screen.findByText(/Internal server error/)).toBeInTheDocument()
  expect(sent).toEqual([{ text: 'Work while busy', whenBusy: 'queue' }])
  expect(screen.getByLabelText('Message')).toHaveValue('Work while busy')
})

it('when busy, Enter queues and ⌘/Ctrl+Enter sends as an interrupt; when an idle send finds the Agent busy, sends again as the chosen interrupt', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  server.use(http.post(apiUrl('/agents/sleep-agent/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']; sent.push(body)
    if (!body.whenBusy) return HttpResponse.json({ error: { code: 'agent_busy', message: 'The Agent is replying; interrupt it or queue the message', details: { currentRunId: makeRun().id, queued: { interrupt: 0, normal: 0 } } } }, { status: 409 })
    const run = makeRun({ status: 'queued', whenBusy: body.whenBusy })
    return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
  }))
  const view = renderWithQuery(<Composer agentId="sleep-agent" agentName="dev-helper" busy disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'Queue this after{Enter}')
  await waitFor(() => expect(sent).toEqual([{ text: 'Queue this after', whenBusy: 'queue' }]))
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, 'Handle this first{Meta>}{Enter}{/Meta}')
  await waitFor(() => expect(sent.at(-1)).toEqual({ text: 'Handle this first', whenBusy: 'interrupt' }))
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, 'Ctrl works too{Control>}{Enter}{/Control}')
  await waitFor(() => expect(sent.at(-1)).toEqual({ text: 'Ctrl works too', whenBusy: 'interrupt' }))

  view.rerender(<Composer agentId="sleep-agent" agentName="dev-helper" busy={false} disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, 'Thought it was idle{Meta>}{Enter}{/Meta}')
  await waitFor(() => expect(sent.slice(-2)).toEqual([{ text: 'Thought it was idle' }, { text: 'Thought it was idle', whenBusy: 'interrupt' }]))
})

// Unified floating composer card centered with the conversation stream.
const mountBar = (busy = false) => renderWithQuery(<Composer agentId="bar-agent" agentName="dev-helper" busy={busy} disabled={false} maxMessageBytes={4096} onSent={() => {}} stop={busy ? { onStop: () => {}, pending: false, retry: false } : undefined} />)

it('the composer dock floats cleanly on surface-canvas in mono skin and preserves Plate yellow bar styling', () => {
  mountBar()
  const bar = screen.getByRole('textbox', { name: 'Message' }).closest('[data-chat-bar]')
  expect(bar).toHaveClass('bg-surface-canvas', '[[data-skin=plate]_&]:bg-tint-yellow', '[[data-skin=plate]_&]:border-t-2', '[[data-skin=plate]_&]:border-ink')
  expect(bar).not.toHaveClass('border-t')
  expect(bar?.firstElementChild).toHaveClass('mx-auto', 'max-w-[800px]')
})

it('the unified card uses surface-elevated with hairline-strong border and subtle focus-within; the send button uses CTA tokens and preserves Plate styling', () => {
  mountBar()
  const editor = screen.getByRole('textbox', { name: 'Message' })
  expect(editor.closest('[data-chat-input]')).toHaveClass('bg-surface-elevated', 'border', 'border-hairline-strong', 'rounded-2xl', 'focus-within:border-riso-blue/50', '[[data-skin=plate]_&]:bg-paper-raised', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-ink')
  expect(screen.getByRole('button', { name: 'Send' })).toHaveClass('size-8', 'rounded-full', 'bg-cta-bg', 'text-cta-fg', '[[data-skin=plate]_&]:size-9', '[[data-skin=plate]_&]:bg-riso-pink')
})

it('when idle the placeholder is “Message <Agent>” and keyboard hints stay in button tooltips instead of a permanent footer strip', () => {
  mountBar()
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveAttribute('placeholder', 'Message dev-helper')
  expect(screen.getByRole('button', { name: 'Send' })).toHaveAttribute('title', 'Send (Enter)')
  expect(screen.queryByText('new line')).toBeNull()
})

it('when busy with an empty input, shows the compact Stop button inside the toolbar without a permanent key-hint footer', () => {
  mountBar(true)
  expect(screen.queryByText('new line')).toBeNull()
  const stop = screen.getByRole('button', { name: 'Stop the current reply' })
  expect(stop).toHaveClass('size-8', 'rounded-full', 'border', 'border-hairline-strong', 'bg-surface-elevated', '[[data-skin=plate]_&]:bg-ink', '[[data-skin=plate]_&]:text-white')
  expect(screen.queryByRole('button', { name: 'Queue' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Interrupt and send' })).toBeNull()
})

it('when busy with a typed draft, keeps the Stop button visible and renders an explicit clickable Interrupt button beside Queue', async () => {
  const sent: Schemas['SendMessageInput'][] = []
  const onStop = vi.fn()
  server.use(http.post(apiUrl('/agents/busy-agent/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']
    sent.push(body)
    const run = makeRun({ status: 'queued', whenBusy: body.whenBusy })
    return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
  }))
  renderWithQuery(
    <Composer
      agentId="busy-agent"
      agentName="dev-helper"
      busy
      disabled={false}
      maxMessageBytes={4096}
      onSent={() => {}}
      stop={{ onStop, pending: false, retry: false }}
    />,
  )
  const box = screen.getByRole('textbox', { name: 'Message' })
  await userEvent.type(box, 'Urgent fix')
  const stopButton = screen.getByRole('button', { name: 'Stop the current reply' })
  const interruptButton = screen.getByRole('button', { name: 'Interrupt and send' })
  const queueButton = screen.getByRole('button', { name: 'Queue' })
  expect(stopButton).toBeEnabled()
  expect(interruptButton).toBeEnabled()
  expect(queueButton).toBeEnabled()

  await userEvent.click(stopButton)
  expect(onStop).toHaveBeenCalledTimes(1)

  await userEvent.click(interruptButton)
  await waitFor(() => expect(sent).toEqual([{ text: 'Urgent fix', whenBusy: 'interrupt' }]))
  await waitFor(() => expect(box).toHaveValue(''))
})

it('preserves per-Agent draft text across unmount/remount and Agent switches, and clears it after sending', async () => {
  server.use(http.post(apiUrl('/agents/agent-a/messages'), async ({ request }) => {
    const body = await request.json() as Schemas['SendMessageInput']
    const run = makeRun({ status: 'starting', endedAt: null })
    return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
  }))
  const first = renderWithQuery(<Composer agentId="agent-a" agentName="alpha" busy={false} disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  await userEvent.type(screen.getByRole('textbox', { name: 'Message' }), 'Half typed for A')
  first.unmount()

  const second = renderWithQuery(<Composer agentId="agent-b" agentName="beta" busy={false} disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue('')
  await userEvent.type(screen.getByRole('textbox', { name: 'Message' }), 'Draft for B')
  second.unmount()

  const backToA = renderWithQuery(<Composer agentId="agent-a" agentName="alpha" busy={false} disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue('Half typed for A')
  await userEvent.click(screen.getByRole('button', { name: 'Send' }))
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue(''))
  backToA.unmount()

  renderWithQuery(<Composer agentId="agent-a" agentName="alpha" busy={false} disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveValue('')
})

it('the queue tray docks directly on top of the unified composer card with no hardcoded side offsets', () => {
  renderWithQuery(<Composer agentId="bar-agent" agentName="dev-helper" busy disabled={false} maxMessageBytes={4096} imageLimits={{ maxCount: 4, maxImageBytes: 1024, maxTotalBytes: 4096 }} onSent={() => {}} queue={<section aria-label="A queued message">Queued · 1</section>} />)
  const slot = screen.getByRole('region', { name: 'A queued message' }).parentElement!
  expect(slot).not.toHaveClass('sm:pl-[50px]', 'sm:pr-[57px]')
  expect(slot.nextElementSibling).toContainElement(screen.getByRole('textbox', { name: 'Message' }))
})

it('the key hints used elsewhere (the task node input) stay centered with secondary text and semantic surface-control keys', () => {
  renderWithQuery(<KeyHint />)
  const hint = screen.getByText('to queue', { selector: 'span' }).closest('p')!
  expect(hint).toHaveClass('justify-center', 'text-ink-500')
  expect(hint.querySelector('kbd')).toHaveClass('bg-surface-control', 'border-hairline-strong', 'font-mono', '[[data-skin=plate]_&]:bg-white', '[[data-skin=plate]_&]:border-ink/25')
  // The key names are also written as before (that they differ from the input area's Chinese “回车” (Enter) is checked in
  // src/zh-CN/features/chat/Composer.zh.test.tsx).
  expect(hint).toHaveTextContent('Enterto queue·Ctrl Enterto interrupt')
})

it('the input text is 14px on desktop and 16px on narrow screens (a smaller size makes iOS zoom the page on focus)', () => {
  mountBar()
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveClass('text-[14px]', 'max-nav:text-base')
})
