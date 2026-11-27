import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { UserChatBubble } from './ChatBubble'

// The user bubble is shared by Agent chat and task node chat, capped at 82% of the centered conversation column.
describe('User bubble', () => {
  it('is capped at 82% of the conversation column by default and with wide', () => {
    const { rerender } = render(<UserChatBubble text="Can you take a look" time="09:14" />)
    expect(screen.getByText('Can you take a look')).toHaveClass('max-w-[82%]')
    rerender(<UserChatBubble wide text="Can you take a look" time="09:14" />)
    expect(screen.getByText('Can you take a look')).toHaveClass('max-w-[82%]')
  })
})
