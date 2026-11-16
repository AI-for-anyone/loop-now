import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { InboxNotice } from './InboxNotice'

it.each([
  ['deadline', '已过回答期限'],
  ['some_new_reason', 'some_new_reason'],
])('a question-expired notice names its reason (%s); an unknown reason code is shown as is', (reason, text) => {
  render(<InboxNotice notice={{ type: 'question_expired', messageId: '00000000-0000-7000-8000-000000000001', reason }} />)
  expect(screen.getByText('问题到期通知')).toBeInTheDocument()
  expect(screen.getByText(text)).toBeInTheDocument()
})
