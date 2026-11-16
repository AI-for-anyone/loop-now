import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { chooseThinking, thinkingChoice, thinkingOptions, thinkingPicker } from '@/test/thinking'
import { ThinkingLevelPicker } from './ThinkingLevelPicker'

// Redesign: the thinking level is a set of segmented buttons (2px navy border, the selected
// segment yellow) with radio-group semantics; the left and right arrow keys move between segments.
it('offers only catalog levels plus default, and emits null for default', async () => {
  const onChange = vi.fn()
  render(<ThinkingLevelPicker value="high" levels={['low', 'high']} defaultLevel="low" onChange={onChange} />)
  expect(thinkingOptions(document.body)).toEqual(['Default (Low)', 'Low', 'High'])
  expect(thinkingChoice(document.body)).toBe('High')
  await chooseThinking(userEvent.setup(), document.body, 'Default (Low)')
  expect(onChange).toHaveBeenCalledWith(null)
})

it('looks like a segmented control: ink border, the chosen segment yellow', () => {
  render(<ThinkingLevelPicker value="high" levels={['low', 'high']} onChange={() => {}} />)
  const picker = thinkingPicker(document.body)
  expect(picker).toHaveClass('border-2', 'border-ink', 'rounded-input')
  for (const radio of screen.getAllByRole('radio')) expect(radio).toHaveClass('data-[state=checked]:bg-riso-yellow')
})

it('preserves an unavailable stored value visibly and shows the field error', () => {
  render(<ThinkingLevelPicker value="high" levels={['low']} onChange={() => {}} error="The current model does not support this level" />)
  // The stored level is not in the catalog: no segment is selected, and a line below says the current value can't be chosen for now.
  expect(thinkingChoice(document.body)).toBeNull()
  expect(screen.getByText('High (current value, not selectable now)')).toBeInTheDocument()
  expect(thinkingPicker(document.body)).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByText('The current model does not support this level')).toBeInTheDocument()
})

it('can disable catalog selection without losing its value', () => {
  render(<ThinkingLevelPicker value={null} levels={[]} disabled onChange={() => {}} />)
  for (const radio of screen.getAllByRole('radio')) expect(radio).toBeDisabled()
  expect(thinkingChoice(document.body)).toBe('Default')
})
