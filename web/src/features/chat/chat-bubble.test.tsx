import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { UserChatBubble } from './ChatBubble'

// The user bubble is a component shared by Agent chat and task node chat: mono skin caps at 82% inside the centered column,
// while Plate skin preserves 80% by default and widens to 88% when wide is passed.
describe('User bubble', () => {
  it('is at most 82% in mono skin and 80% in Plate skin by default', () => {
    render(<UserChatBubble text="Can you take a look" time="09:14" />)
    expect(screen.getByText('Can you take a look')).toHaveClass('max-w-[82%]', '[[data-skin=plate]_&]:max-w-[80%]')
  })

  it('when wide is passed, preserves Plate wide sizing while staying crisp at 82% in mono skin', () => {
    render(<UserChatBubble wide text="Can you take a look" time="09:14" />)
    expect(screen.getByText('Can you take a look')).toHaveClass('max-w-[82%]', '[[data-skin=plate]_&]:nav:max-w-[min(88%,1100px)]')
  })
})
