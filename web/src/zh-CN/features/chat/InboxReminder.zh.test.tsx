import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { externalParty, makeIdentity } from '@/test/inbox'
import { InboxReminderLine } from '@/features/chat/InboxReminder'

// The Chinese UI joins the deleted mark with full-width parentheses. Moved from src/features/chat/InboxReminder.test.tsx, which keeps
// the same scenario in English.

const at = '2026-10-22T12:00:00Z'

it('keeps the deleted sender label and its original parentheses', () => {
  render(<InboxReminderLine at={at} sender={externalParty(makeIdentity({ name: '原发件人' }), true)} preview="原始摘要" />)
  expect(screen.getByRole('note')).toHaveTextContent('来自 原发件人（已删除身份）')
})
