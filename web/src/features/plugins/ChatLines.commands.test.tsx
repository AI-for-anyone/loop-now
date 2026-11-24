import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { CommandLine } from './ChatLines'
import type { Schemas } from '@/api/client'
import { makeRun } from '@/test/api'

const base: Schemas['ConsoleCommandResult'] = { handled: true, source: 'platform', plugin: '', reply: null, command: 'compact' }

// The result of /compact is on the run of this compaction (compactRun.compaction).
const compaction = (overrides: Partial<Schemas['RunCompaction']>): Schemas['ConsoleCommandResult'] => ({
  ...base,
  command: 'compact',
  tokensBefore: null,
  estimatedTokensAfter: null,
  compactRun: { ...makeRun(), compaction: { instructions: '', outcome: null, skipReason: null, tokensBefore: null, estimatedTokensAfter: null, ...overrides } },
})

it('a platform command shows a structured result instead of a plugin reply', () => {
  render(<CommandLine command="/compact keep the decisions" at="2026-10-31T01:00:00Z" result={compaction({ outcome: 'compacted', tokensBefore: 4200, estimatedTokensAfter: 350 })} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent('Platform command')
  expect(line).toHaveTextContent('Context compacted: 4200 → about 350 tokens')
  expect(line).not.toHaveTextContent('Plugin')
})

it('/copy with no reply says clearly that there is nothing to copy', () => {
  render(<CommandLine command="/copy" at="2026-10-31T01:00:00Z" result={{ ...base, command: 'copy', copyText: null }} />)
  expect(screen.getByRole('note')).toHaveTextContent('No reply to copy')
})

it('/resume shows a readable title, preview, short ID and the current marker; clicking Resume passes back the full ID', () => {
  const targetID = '12345678-1234-1234-1234-123456789abc'
  let selected = ''
  const { rerender } = render(<CommandLine command="/resume" at="2026-10-31T01:00:00Z" onResume={id => { selected = id }} result={{ ...base, command: 'resume', resumeSessions: [
    { id: targetID, title: 'Fix the resume list', preview: 'Latest discussion: show the title and preview', updatedAt: '2026-10-31T01:00:00Z', current: false },
    { id: 'current-full-session-id', title: '', preview: '', updatedAt: '2026-10-30T01:00:00Z', current: true },
  ] }} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent('Fix the resume list')
  expect(line).toHaveTextContent('Latest discussion: show the title and preview')
  expect(line).toHaveTextContent('12345678…9abc')
  expect(line).toHaveTextContent('No text messages')
  expect(line).toHaveTextContent('Current')
  expect(line).not.toHaveTextContent(targetID)
  expect(screen.getByTitle(targetID)).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: 'Resume' })).toHaveLength(1)
  screen.getByRole('button', { name: 'Resume' }).click()
  expect(selected).toBe(targetID)
  expect(line).not.toHaveTextContent('/managed/')
  rerender(<CommandLine command={`/resume ${targetID}`} at="2026-10-31T01:00:00Z" result={{ ...base, command: 'resume', resumedSession: { state: 'active', epoch: 2 } }} />)
  expect(screen.getByRole('note')).toHaveTextContent('Session resumed (epoch 2); page history is kept')
})

// "Printed sheet" redesign: a command result's header is a segmented receipt, and the /resume session list follows the table rules.
it('a command result header is a segmented receipt (command | source | time) with command and time in monospace; the result sits below with a dashed line on the left', () => {
  render(<CommandLine command="/compact keep the decisions" at="2026-10-31T01:00:00Z" result={compaction({ outcome: 'compacted', tokensBefore: 4200, estimatedTokensAfter: 350 })} />)
  const line = screen.getByRole('note')
  const parts = [...line.querySelectorAll('[data-receipt-part]')]
  expect(parts.map(part => part.textContent)).toEqual(['/compact keep the decisions', 'Platform command', expect.stringMatching(/\d{2}:\d{2}$/)])
  expect(parts[0].querySelector('code')).toHaveClass('font-mono', 'font-bold')
  expect(parts[1]).toHaveClass('border-l-[1.5px]', 'border-dashed')
  expect(parts[2]).toHaveClass('border-l-[1.5px]', 'border-dashed', 'font-mono')
  expect(parts[0].parentElement).toHaveClass('bg-tint-blue', 'rounded-[6px]')
  const reply = screen.getByText('Context compacted: 4200 → about 350 tokens')
  expect(reply).toHaveClass('border-l-2', 'border-dashed')
})

it('the /resume session list: a solid line on top and dashed lines between rows; the current session is a yellow tag; ID and time in monospace; Resume is a white button with a navy outline', () => {
  render(<CommandLine command="/resume" at="2026-10-31T01:00:00Z" onResume={() => {}} result={{ ...base, command: 'resume', resumeSessions: [
    { id: '12345678-1234-1234-1234-123456789abc', title: 'Fix the resume list', preview: 'Latest discussion', updatedAt: '2026-10-31T01:00:00Z', current: false },
    { id: 'current-full-session-id', title: 'The current session', preview: '', updatedAt: '2026-10-30T01:00:00Z', current: true },
  ] }} />)
  const list = screen.getByRole('list', { name: 'Sessions available to resume' })
  expect(list).toHaveClass('border-t-2', 'border-ink')
  const rows = [...list.querySelectorAll('[role="listitem"]')]
  expect(rows).toHaveLength(2)
  for (const row of rows) expect(row).toHaveClass('border-b-[1.5px]', 'border-dashed')
  expect(screen.getByText('Current')).toHaveClass('bg-riso-yellow', 'text-ink', 'font-black')
  expect(screen.getByTitle('12345678-1234-1234-1234-123456789abc')).toHaveClass('font-mono')
  expect(rows[0].querySelector('time')).toHaveClass('font-mono')
  expect(screen.getByRole('button', { name: 'Resume' })).toHaveClass('border-2', 'border-ink', 'bg-paper-raised')
})

// The /compact result reads compactRun.compaction from the response: when it compacted, show the
// tokens before and after; when Pi did not compact, explain the reason (the tokens are null then).

it('/compact compacted: shows the tokens before and after (reads compactRun.compaction)', () => {
  render(<CommandLine command="/compact" at="2026-10-31T01:00:00Z" result={compaction({ outcome: 'compacted', tokensBefore: 52000, estimatedTokensAfter: 4100 })} />)
  expect(screen.getByRole('note')).toHaveTextContent('Context compacted: 52000 → about 4100 tokens')
})

it('/compact compacted on a runtime that does not report the tokens (Codex): says it compacted, without numbers', () => {
  render(<CommandLine command="/compact" at="2026-10-31T01:00:00Z" result={compaction({ outcome: 'compacted', tokensBefore: null, estimatedTokensAfter: null })} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(/Context compacted$/)
  expect(line).not.toHaveTextContent('null')
  expect(line).not.toHaveTextContent('Command completed')
})

it.each([
  ['too_small', 'The session is too short to need compacting'],
  ['already_compacted', 'Nothing new since the last compaction, so there is nothing to compact'],
  ['cancelled_by_extension', 'An extension cancelled this compaction'],
] as const)('/compact did not compact (%s): says "%s" and shows no tokens', (skipReason, wording) => {
  render(<CommandLine command="/compact" at="2026-10-31T01:00:00Z" result={compaction({ outcome: 'skipped', skipReason })} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(wording)
  expect(line).not.toHaveTextContent('Context compacted')
  expect(line).not.toHaveTextContent('null')
})

// /copy failed to write to the clipboard (in Safari the call is no longer inside the key gesture; an http
// URL has no navigator.clipboard): the content is not lost, and a way to copy it by hand is offered.
it('/copy could not write to the clipboard: explains why, puts the text in a read-only box that can be selected, and a button next to it copies again', async () => {
  const writeText = vi.fn(() => Promise.resolve())
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  render(<CommandLine command="/copy" at="2026-10-31T01:00:00Z" copyFailed result={{ ...base, command: 'copy', copyText: 'First line\nSecond line' }} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent("The browser didn't let the console write to the clipboard")
  expect(line).not.toHaveTextContent('Copied the latest successful reply')
  const box = screen.getByRole('textbox', { name: 'Text to copy' })
  expect(box).toHaveValue('First line\nSecond line')
  expect(box).toHaveAttribute('readonly')
  const button = screen.getByRole('button', { name: 'Copy' })
  await userEvent.click(button)
  expect(writeText).toHaveBeenCalledWith('First line\nSecond line')
  expect(await screen.findByRole('button', { name: 'Copied' })).toBeInTheDocument()
})

it('/copy copying by hand still fails: selects the text in the box so the copy shortcut can be pressed directly', async () => {
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
  render(<CommandLine command="/copy" at="2026-10-31T01:00:00Z" copyFailed result={{ ...base, command: 'copy', copyText: 'Reply to copy' }} />)
  const box = screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Text to copy' })
  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  await waitFor(() => expect(box).toHaveFocus())
  expect(box.selectionStart).toBe(0)
  expect(box.selectionEnd).toBe('Reply to copy'.length)
  expect(screen.queryByRole('button', { name: 'Copied' })).toBeNull()
})

it('/copy that succeeds just says it copied, as usual, with no box for copying by hand', () => {
  render(<CommandLine command="/copy" at="2026-10-31T01:00:00Z" result={{ ...base, command: 'copy', copyText: 'A reply' }} />)
  expect(screen.getByRole('note')).toHaveTextContent('Copied the latest successful reply')
  expect(screen.queryByRole('textbox')).toBeNull()
})

it('/compact reads only compactRun.compaction: the two deprecated fields (tokensBefore, estimatedTokensAfter) are no longer used', () => {
  render(<CommandLine command="/compact" at="2026-10-31T01:00:00Z" result={{ ...base, command: 'compact', tokensBefore: 4200, estimatedTokensAfter: 350 }} />)
  const line = screen.getByRole('note')
  expect(line).not.toHaveTextContent('4200')
  expect(line).toHaveTextContent('Command completed')
})
