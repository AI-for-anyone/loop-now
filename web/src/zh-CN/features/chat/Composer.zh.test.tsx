import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'
import { Composer, KeyHint } from '@/features/chat/Composer'
import { resetChatDraftsForTest } from '@/features/chat/draft-store'
import { renderWithQuery } from '@/test/render'

// The Chinese UI writes the Enter key as “回车” (Enter) in the chat page's input area, but keeps “Enter” in the key hints used
// elsewhere (the task node input). Moved from src/features/chat/Composer.test.tsx, which checks the same layout in English.

afterEach(() => resetChatDraftsForTest())

const mountBar = (busy = false) => renderWithQuery(<Composer agentId="bar-agent" agentName="dev-helper" busy={busy} disabled={false} maxMessageBytes={4096} onSent={() => {}} stop={busy ? { onStop: () => {}, pending: false, retry: false } : undefined} />)

it('when busy, Stop stays available even with a draft, and explicit Interrupt and Queue buttons appear with Chinese key tooltips', async () => {
  mountBar(true)
  expect(screen.queryByText('换行')).toBeNull()
  const stop = screen.getByRole('button', { name: '停止当前回复' })
  expect(stop).toHaveClass('size-7', 'rounded-full', 'bg-red', 'text-white')
  expect(screen.queryByRole('button', { name: '排队发送' })).toBeNull()

  await userEvent.type(screen.getByRole('textbox', { name: '消息' }), '新的指示')
  expect(screen.getByRole('button', { name: '停止当前回复' })).toBeEnabled()
  const interrupt = screen.getByRole('button', { name: '打断并发送' })
  expect(interrupt).toBeEnabled()
  expect(interrupt).toHaveAttribute('title', '打断并发送（Ctrl 回车）')
  expect(screen.getByRole('button', { name: '排队发送' })).toBeEnabled()
})

it('the key hints used elsewhere (the task node input) stay centered with secondary text and semantic surface-control keys', () => {
  renderWithQuery(<KeyHint />)
  const hint = screen.getByText('排队', { selector: 'span' }).closest('p')!
  expect(hint).toHaveClass('justify-center', 'text-ink-500')
  expect(hint.querySelector('kbd')).toHaveClass('bg-surface-control', 'border-hairline-strong', 'font-mono')
  // The key names are also written as before.
  expect(hint).toHaveTextContent('Enter排队·Ctrl Enter立即打断')
  expect(hint).not.toHaveTextContent('回车')
})
