import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { StepPrompt, WithConversationBadge } from './StepPrompt'

describe('StepPrompt', () => {
  it('renders nothing when the step has no prompt or only whitespace', () => {
    const empty = render(<StepPrompt prompt={undefined} />)
    expect(empty.container).toBeEmptyDOMElement()
    empty.unmount()

    const blank = render(<StepPrompt prompt={'   \n\t  '} />)
    expect(blank.container).toBeEmptyDOMElement()
  })

  it('starts collapsed and expands to render Markdown, then collapses again', async () => {
    const user = userEvent.setup()
    render(
      <StepPrompt
        prompt={'### Briefing\n\n- **Plan:** read `PLAN.md`\n- Watch out for edge cases\n\n```ts\nconst clean = true\n```'}
      />,
    )

    const region = screen.getByRole('region', { name: 'Step prompt' })
    const toggle = within(region).getByRole('button', { name: /Step prompt/ })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle).toHaveTextContent('Expand')
    expect(within(region).queryByText('Briefing')).not.toBeInTheDocument()
    expect(within(region).queryByText('PLAN.md')).not.toBeInTheDocument()

    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(toggle).toHaveTextContent('Collapse')
    expect(within(region).getByRole('heading', { level: 3, name: 'Briefing' })).toBeVisible()
    expect(within(region).getByText('PLAN.md').tagName).toBe('CODE')
    // Document mode keeps bold list prefixes as plain strong text rather than yellow chat lead pills.
    expect(region.querySelector('.md-lead')).toBeNull()
    expect(region.querySelector('pre code')).toHaveTextContent('const clean = true')

    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(within(region).queryByText('Briefing')).not.toBeInTheDocument()
  })
})

describe('WithConversationBadge', () => {
  it('shows the conversation label and hint in both default and compact forms', () => {
    const { rerender } = render(<WithConversationBadge />)
    const chip = screen.getByText('With conversation').closest('[data-step-with-conversation]')!
    expect(chip).toHaveAttribute('title', "Starts from a copy of this Agent's regular conversation instead of a clean session.")

    rerender(<WithConversationBadge compact />)
    const compact = screen.getByText('With conversation').closest('[data-step-with-conversation]')!
    expect(compact).toHaveAttribute('title', "Starts from a copy of this Agent's regular conversation instead of a clean session.")
    expect(compact).toHaveClass('bg-surface-control')
  })
})
