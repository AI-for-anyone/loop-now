import { screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { Composer, KeyHint } from '@/features/chat/Composer'
import { renderWithQuery } from '@/test/render'

// The Chinese UI writes the Enter key as “回车” (Enter) in the chat page's input area, but keeps “Enter” in the key hints used
// elsewhere (the task node input). Moved from src/features/chat/Composer.test.tsx, which checks the same layout in English.

// "Printed sheet" redesign: the input area merges into the bottom of the sheet.
const mountBar = (busy = false) => renderWithQuery(<Composer agentId="bar-agent" agentName="开发助手" busy={busy} disabled={false} maxMessageBytes={4096} onSent={() => {}} stop={busy ? { onStop: () => {}, pending: false, retry: false } : undefined} />)

it('when busy, the key hints switch to queue and interrupt, and with an empty input the send button becomes a navy stop button', () => {
  mountBar(true)
  const hint = screen.getByText('排队', { selector: 'span' }).closest('p')!
  // The Chinese UI always writes the Enter key as “回车” (Enter); modifier keys are written as before.
  expect(hint).toHaveTextContent('回车排队·Ctrl 回车立即打断')
  expect(hint).not.toHaveTextContent('Enter')
  // Left-aligned inside the input area, using the input area's key style.
  expect(hint).toHaveClass('justify-start', 'text-ink-700')
  expect(hint.querySelector('kbd')).toHaveClass('border-ink', 'bg-paper-raised')
  expect(screen.queryByText('换行')).toBeNull()
  const stop = screen.getByRole('button', { name: '停止当前回复' })
  expect(stop).toHaveClass('size-11', 'rounded-full', 'border-2', 'border-ink', 'bg-ink', 'text-white')
  expect(screen.queryByRole('button', { name: '排队发送' })).toBeNull()
})

it('the key hints used elsewhere (the task node input) stay as they were: centered, secondary text color, keys on white with a thin border', () => {
  renderWithQuery(<KeyHint />)
  const hint = screen.getByText('排队', { selector: 'span' }).closest('p')!
  expect(hint).toHaveClass('justify-center', 'text-ink-500')
  expect(hint.querySelector('kbd')).toHaveClass('bg-white', 'border-ink/25', 'font-mono')
  // The key names are also written as before.
  expect(hint).toHaveTextContent('Enter排队·Ctrl Enter立即打断')
  expect(hint).not.toHaveTextContent('回车')
})
