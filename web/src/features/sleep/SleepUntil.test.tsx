import { act, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { SleepUntil } from './SleepUntil'

afterEach(() => vi.useRealTimers())
it('counts down to the fixed wake time, keeps showing the wait at zero, and sends no wake or other network request', () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date('2026-10-15T00:00:00Z'))
  const fetch = vi.spyOn(globalThis, 'fetch')
  render(<SleepUntil at="2026-10-15T00:00:02Z" reason="Waiting for an external result" />)
  expect(screen.getByText('0 min 2 s left')).toBeVisible()
  act(() => vi.advanceTimersByTime(3000))
  expect(screen.getByText('Waiting for the server to schedule the wake')).toBeVisible()
  expect(screen.getByText('Waiting for an external result')).toBeVisible()
  expect(document.querySelector('time')).toHaveAttribute('dateTime', '2026-10-15T00:00:02Z')
  expect(fetch).not.toHaveBeenCalled()
})

it('remounting keeps the original wake time instead of restarting the full duration from now', () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date('2026-10-15T00:00:00Z'))
  const first = render(<SleepUntil at="2026-10-15T00:02:00Z" />)
  first.unmount(); vi.setSystemTime(new Date('2026-10-15T00:01:30Z'))
  render(<SleepUntil at="2026-10-15T00:02:00Z" />)
  expect(screen.getByText('0 min 30 s left')).toBeVisible()
})

it('compact mode shows the reason on one line with the full text as a tooltip, and still waits for the server when the countdown ends', () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date('2026-10-15T00:00:00Z'))
  const reason = 'Waiting for an external result and the follow-up confirmation.'.repeat(40)
  const fetch = vi.spyOn(globalThis, 'fetch')
  render(<SleepUntil compact at="2026-10-15T00:00:02Z" reason={reason} />)
  expect(screen.getByText(reason)).toHaveAttribute('title', reason)
  expect(screen.getByText(reason)).toHaveClass('truncate')
  expect(screen.getByText('0 min 2 s left')).toBeVisible()
  act(() => vi.advanceTimersByTime(3000))
  expect(screen.getByText('Waiting for the server to schedule the wake')).toBeVisible()
  expect(fetch).not.toHaveBeenCalled()
})
