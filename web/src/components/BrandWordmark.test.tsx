import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setSkin, setThemeMode } from '@/theme/store'
import { BrandWordmark } from './BrandWordmark'

describe('BrandWordmark', () => {
  beforeEach(() => {
    setSkin('mono')
    setThemeMode('light')
  })

  afterEach(() => {
    vi.useRealTimers()
    setSkin('mono')
    setThemeMode('system')
  })

  it('renders clean brand text, starts with an intro sweep, and alternates sweep phase on hover', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-26T12:00:00Z'))

    const { container } = render(<BrandWordmark />)
    const wordmark = container.querySelector('[data-brand-wordmark]') as HTMLElement | null
    expect(wordmark).not.toBeNull()
    expect(wordmark?.textContent).toBe('LOOP NOW')
    expect(wordmark?.getAttribute('data-sheen')).toBe('LOOP NOW')
    expect(wordmark?.getAttribute('data-sweep')).toBe('intro')

    // First hover immediately triggers sweep 'a'.
    fireEvent.mouseEnter(wordmark!)
    expect(wordmark?.getAttribute('data-sweep')).toBe('a')

    // Rapid jitter inside the cooldown window does not restart the beam mid-flight.
    act(() => {
      vi.advanceTimersByTime(100)
    })
    fireEvent.mouseEnter(wordmark!)
    expect(wordmark?.getAttribute('data-sweep')).toBe('a')

    // Hovering again after the cooldown switches to sweep 'b' to replay cleanly.
    act(() => {
      vi.advanceTimersByTime(500)
    })
    fireEvent.mouseEnter(wordmark!)
    expect(wordmark?.getAttribute('data-sweep')).toBe('b')
  })

  it('preserves classic Risograph styling without sheen attributes in plate skin', () => {
    setSkin('plate')
    const { container } = render(<BrandWordmark />)
    expect(container.querySelector('[data-brand-wordmark]')).toBeNull()
    expect(screen.getByText('LOOP NOW')).toBeInTheDocument()
  })
})
