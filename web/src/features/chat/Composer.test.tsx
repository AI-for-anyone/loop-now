import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it, vi } from 'vitest'
import { Composer, KeyHint } from './Composer'
import { apiUrl, makeMessage, makeRun } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import type { Schemas } from '@/api/client'

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
  // When busy, the key hints appear below the input.
  expect(screen.getByText('to interrupt')).toBeVisible()
  await userEvent.type(box, 'Queue this after{Enter}')
  await waitFor(() => expect(sent).toEqual([{ text: 'Queue this after', whenBusy: 'queue' }]))
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, 'Handle this first{Meta>}{Enter}{/Meta}')
  await waitFor(() => expect(sent.at(-1)).toEqual({ text: 'Handle this first', whenBusy: 'interrupt' }))
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, 'Ctrl works too{Control>}{Enter}{/Control}')
  await waitFor(() => expect(sent.at(-1)).toEqual({ text: 'Ctrl works too', whenBusy: 'interrupt' }))

  view.rerender(<Composer agentId="sleep-agent" agentName="dev-helper" busy={false} disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  expect(screen.queryByText('to interrupt')).toBeNull()
  await waitFor(() => expect(box).toHaveValue(''))
  await userEvent.type(box, 'Thought it was idle{Meta>}{Enter}{/Meta}')
  await waitFor(() => expect(sent.slice(-2)).toEqual([{ text: 'Thought it was idle' }, { text: 'Thought it was idle', whenBusy: 'interrupt' }]))
})

// "Printed sheet" redesign: the input area merges into the bottom of the sheet.
const mountBar = (busy = false) => renderWithQuery(<Composer agentId="bar-agent" agentName="dev-helper" busy={busy} disabled={false} maxMessageBytes={4096} onSent={() => {}} stop={busy ? { onStop: () => {}, pending: false, retry: false } : undefined} />)

it('the input area is a light yellow bar at the bottom of the sheet with a 2px navy line along its top edge', () => {
  mountBar()
  const bar = screen.getByRole('textbox', { name: 'Message' }).closest('[data-chat-bar]')
  expect(bar).toHaveClass('bg-tint-yellow', 'border-t-2', 'border-ink')
  // On wide screens the vertical padding tightens (narrow screens are unchanged); the key-hint row stays.
  expect(bar).toHaveClass('pt-3.5', 'pb-3', 'gap-2.5', 'nav:pt-2.5', 'nav:pb-2', 'nav:gap-1.5')
})

it('the input has a light paper background and a navy border; the send button is a fluorescent pink round stamp (navy border plus a solid navy shadow)', () => {
  mountBar()
  const editor = screen.getByRole('textbox', { name: 'Message' })
  expect(editor.closest('[data-chat-input]')).toHaveClass('bg-paper-raised', 'border-2', 'border-ink')
  expect(screen.getByRole('button', { name: 'Send' })).toHaveClass('size-11', 'rounded-full', 'border-2', 'border-ink', 'bg-riso-pink', 'shadow-[3px_3px_0_var(--ink)]')
})

it('when idle the placeholder is “Message <Agent>” and no longer explains the keys', () => {
  mountBar()
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveAttribute('placeholder', 'Message dev-helper')
})

it('when idle, the line under the input says Enter to send, Shift + Enter for a new line', () => {
  mountBar()
  const hint = screen.getByText('send', { selector: 'span' }).closest('p')!
  expect(hint).toHaveTextContent('Entersend·Shift + Enternew line')
  expect(hint.querySelectorAll('kbd')).toHaveLength(2)
  expect(screen.queryByText('to queue', { selector: 'span' })).toBeNull()
})

it('when busy, the key hints switch to queue and interrupt, and with an empty input the send button becomes a navy stop button', () => {
  mountBar(true)
  const hint = screen.getByText('to queue', { selector: 'span' }).closest('p')!
  // The input area writes keys its own way (t.chat.barKeys): in English that is “Enter” as elsewhere. The Chinese “回车” (Enter) is checked
  // in src/zh-CN/features/chat/Composer.zh.test.tsx.
  expect(hint).toHaveTextContent('Enterto queue·Ctrl Enterto interrupt')
  // Left-aligned inside the input area, using the input area's key style.
  expect(hint).toHaveClass('justify-start', 'text-ink-700')
  expect(hint.querySelector('kbd')).toHaveClass('border-ink', 'bg-paper-raised')
  expect(screen.queryByText('new line')).toBeNull()
  const stop = screen.getByRole('button', { name: 'Stop the current reply' })
  expect(stop).toHaveClass('size-11', 'rounded-full', 'border-2', 'border-ink', 'bg-ink', 'text-white')
  expect(screen.queryByRole('button', { name: 'Queue' })).toBeNull()
})

it('the queue tray sits on the top edge of the input, leaving room at the sides for the plus and send buttons', () => {
  renderWithQuery(<Composer agentId="bar-agent" agentName="dev-helper" busy disabled={false} maxMessageBytes={4096} imageLimits={{ maxCount: 4, maxImageBytes: 1024, maxTotalBytes: 4096 }} onSent={() => {}} queue={<section aria-label="A queued message">Queued · 1</section>} />)
  const slot = screen.getByRole('region', { name: 'A queued message' }).parentElement!
  // At phone width the tray spans the full row and no longer leaves room at the sides; otherwise it would be too narrow.
  expect(slot).toHaveClass('sm:pl-[50px]', 'sm:pr-[57px]')
  expect(slot).not.toHaveClass('pl-[50px]')
  expect(slot.nextElementSibling).toContainElement(screen.getByRole('textbox', { name: 'Message' }))
})

it('the key hints used elsewhere (the task node input) stay as they were: centered, secondary text color, keys on white with a thin border', () => {
  renderWithQuery(<KeyHint />)
  const hint = screen.getByText('to queue', { selector: 'span' }).closest('p')!
  expect(hint).toHaveClass('justify-center', 'text-ink-500')
  expect(hint.querySelector('kbd')).toHaveClass('bg-white', 'border-ink/25', 'font-mono')
  // The key names are also written as before (that they differ from the input area's Chinese “回车” (Enter) is checked in
  // src/zh-CN/features/chat/Composer.zh.test.tsx).
  expect(hint).toHaveTextContent('Enterto queue·Ctrl Enterto interrupt')
})

it('the input text is 16px on narrow screens (a smaller size makes iOS zoom the page on focus)', () => {
  mountBar()
  expect(screen.getByRole('textbox', { name: 'Message' })).toHaveClass('text-[15.5px]', 'max-nav:text-base')
})
