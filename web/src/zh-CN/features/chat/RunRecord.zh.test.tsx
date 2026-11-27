import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { RunRecord } from '@/features/chat/RunRecord'
import { t } from '@/i18n'
import { makeRun } from '@/test/api'
import { renderWithQuery } from '@/test/render'

describe('RunRecord compact token format (Chinese UI)', () => {
  it('formats Chinese compact token counts in 万 and 亿 while keeping counts below 10,000 exact', () => {
    const compact = t.timeline.toggleParts.compactTokens
    expect(compact(0)).toBe('0 token')
    expect(compact(999)).toBe('999 token')
    expect(compact(1_234)).toBe('1,234 token')
    expect(compact(9_999)).toBe('9,999 token')
    expect(compact(10_000)).toBe('1 万 token')
    expect(compact(123_456)).toBe('12.3 万 token')
    expect(compact(999_500)).toBe('100 万 token')
    expect(compact(3_132_788)).toBe('313 万 token')
    expect(compact(120_000_000)).toBe('1.2 亿 token')
  })

  it('shows “313 万 token” visually in body font and keeps the exact count in title and sr-only label', () => {
    const run = makeRun({
      queuedAt: '2026-10-12T08:00:00Z',
      startedAt: '2026-10-12T08:00:00Z',
      runningAt: '2026-10-12T08:00:01Z',
      endedAt: '2026-10-12T08:03:11Z',
      usage: { calls: 10, input: 3_000_000, output: 132_788, cacheRead: 0, cacheWrite: 0, total: 3_132_788 },
    })
    renderWithQuery(<RunRecord agentId="agent-1" run={run} />)
    const toggle = screen.getByRole('button', { name: '运行记录 · 3 分 11 秒 · 3,132,788 token' })
    expect(toggle).toHaveAttribute('title', '运行记录 · 3 分 11 秒 · 3,132,788 token')
    const parts = [...toggle.querySelectorAll('[data-receipt-part]')]
    expect(parts.map((part) => part.textContent)).toEqual(['运行记录', '3 分 11 秒', '313 万 token'])
    for (const part of parts) expect(part).not.toHaveClass('font-mono')
    expect(parts[1]).toHaveClass('tabular-nums')
    expect(parts[2]).toHaveClass('tabular-nums')
  })
})
