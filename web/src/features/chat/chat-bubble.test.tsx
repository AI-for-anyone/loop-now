import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { UserChatBubble } from './ChatBubble'

// The user bubble is a component shared by Agent chat and task node chat: task node chat is not affected by the chat page's widening.
describe('User bubble', () => {
  it('is at most 80% of its column by default (task node chat uses this)', () => {
    render(<UserChatBubble text="Can you take a look" time="09:14" />)
    expect(screen.getByText('Can you take a look')).toHaveClass('max-w-[80%]')
  })

  it('the Agent chat page passes wide: on wide screens at most 88% of the chat column and no more than 1600px; on narrow screens still 80%', () => {
    render(<UserChatBubble wide text="Can you take a look" time="09:14" />)
    expect(screen.getByText('Can you take a look')).toHaveClass('nav:max-w-[min(88%,1600px)]', 'max-w-[80%]')
  })
})
