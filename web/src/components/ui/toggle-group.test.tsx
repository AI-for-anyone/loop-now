import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { ToggleGroup, ToggleGroupItem } from './toggle-group'

function Ranges({ initial = 'today' }: { initial?: string }) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <ToggleGroup aria-label="Period" value={value} onValueChange={setValue}>
        <ToggleGroupItem value="today">Today</ToggleGroupItem>
        <ToggleGroupItem value="week">This week</ToggleGroupItem>
        <ToggleGroupItem value="locked" disabled>
          Unavailable
        </ToggleGroupItem>
        <ToggleGroupItem value="month">Last 30 days</ToggleGroupItem>
      </ToggleGroup>
      <output>{value}</output>
    </>
  )
}

describe('Segmented control', () => {
  it('single choice: a click switches, and the checked state follows', async () => {
    const user = userEvent.setup()
    render(<Ranges />)
    expect(screen.getByRole('radiogroup', { name: 'Period' })).toBeInTheDocument()
    const today = screen.getByRole('radio', { name: 'Today' })
    const week = screen.getByRole('radio', { name: 'This week' })
    expect(today).toHaveAttribute('aria-checked', 'true')
    expect(today).toHaveAttribute('data-state', 'on')
    await user.click(week)
    expect(week).toHaveAttribute('aria-checked', 'true')
    expect(week).toHaveAttribute('data-state', 'on')
    expect(today).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByRole('status')).toHaveTextContent('week')
  })

  it('clicking the checked item again does not clear the choice', async () => {
    const user = userEvent.setup()
    render(<Ranges />)
    await user.click(screen.getByRole('radio', { name: 'Today' }))
    expect(screen.getByRole('radio', { name: 'Today' })).toBeChecked()
    expect(screen.getByRole('status')).toHaveTextContent('today')
  })

  it('the left and right arrow keys move focus and skip disabled items; Space or Enter selects', async () => {
    const user = userEvent.setup()
    render(<Ranges />)
    await user.tab()
    expect(screen.getByRole('radio', { name: 'Today' })).toHaveFocus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('radio', { name: 'This week' })).toHaveFocus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('radio', { name: 'Last 30 days' })).toHaveFocus()
    await user.keyboard(' ')
    expect(screen.getByRole('radio', { name: 'Last 30 days' })).toBeChecked()
    await user.keyboard('{ArrowLeft}')
    expect(screen.getByRole('radio', { name: 'This week' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(screen.getByRole('radio', { name: 'This week' })).toBeChecked()
  })

  it('a disabled item cannot be selected', async () => {
    const user = userEvent.setup()
    render(<Ranges />)
    const locked = screen.getByRole('radio', { name: 'Unavailable' })
    expect(locked).toBeDisabled()
    await user.click(locked)
    expect(locked).not.toBeChecked()
    expect(screen.getByRole('status')).toHaveTextContent('today')
  })
})
