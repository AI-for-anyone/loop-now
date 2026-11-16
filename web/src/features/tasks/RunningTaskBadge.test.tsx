import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { enUS } from '@/i18n/en-US'
import { RunningTaskBadge } from './RunningTaskBadge'

describe('running task badge', () => {
  it('shows the count; screen readers and the hover tip read “N tasks running”, and the number itself is hidden from screen readers', () => {
    const { container } = render(<RunningTaskBadge count={3} />)
    expect(screen.getByText('3 tasks running')).toHaveClass('sr-only')
    expect(container.firstElementChild).toHaveAttribute('title', '3 tasks running')
    expect(screen.getByText('3')).toHaveAttribute('aria-hidden', 'true')
  })

  it('not shown when 0 or not read yet', () => {
    expect(render(<RunningTaskBadge count={0} />).container).toBeEmptyDOMElement()
    expect(render(<RunningTaskBadge count={undefined} />).container).toBeEmptyDOMElement()
  })

  it('English uses singular and plural', () => {
    expect(enUS.shell.runningTasks(1)).toBe('1 task running')
    expect(enUS.shell.runningTasks(2)).toBe('2 tasks running')
  })
})
