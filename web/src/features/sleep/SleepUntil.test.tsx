import { act, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { SleepUntil } from './SleepUntil'

afterEach(() => vi.useRealTimers())
it('按固定到期时间倒计时，到零继续展示等待，不发唤醒或其他网络请求', () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date('2026-10-15T00:00:00Z'))
  const fetch = vi.spyOn(globalThis, 'fetch')
  render(<SleepUntil at="2026-10-15T00:00:02Z" reason="等待外部结果" />)
  expect(screen.getByText('剩余 0 分 2 秒')).toBeVisible()
  act(() => vi.advanceTimersByTime(3000))
  expect(screen.getByText('等待服务端安排唤醒')).toBeVisible()
  expect(screen.getByText('等待外部结果')).toBeVisible()
  expect(document.querySelector('time')).toHaveAttribute('dateTime', '2026-10-15T00:00:02Z')
  expect(fetch).not.toHaveBeenCalled()
})

it('重新挂载沿用原到期时间，不从当前时刻重计完整时长', () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date('2026-10-15T00:00:00Z'))
  const first = render(<SleepUntil at="2026-10-15T00:02:00Z" />)
  first.unmount(); vi.setSystemTime(new Date('2026-10-15T00:01:30Z'))
  render(<SleepUntil at="2026-10-15T00:02:00Z" />)
  expect(screen.getByText('剩余 0 分 30 秒')).toBeVisible()
})

it('紧凑模式原因单行保留全文提示，倒计时到期仍等待服务端', () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date('2026-10-15T00:00:00Z'))
  const reason = '等待外部结果及后续确认'.repeat(40)
  const fetch = vi.spyOn(globalThis, 'fetch')
  render(<SleepUntil compact at="2026-10-15T00:00:02Z" reason={reason} />)
  expect(screen.getByText(reason)).toHaveAttribute('title', reason)
  expect(screen.getByText(reason)).toHaveClass('truncate')
  expect(screen.getByText('剩余 0 分 2 秒')).toBeVisible()
  act(() => vi.advanceTimersByTime(3000))
  expect(screen.getByText('等待服务端安排唤醒')).toBeVisible()
  expect(fetch).not.toHaveBeenCalled()
})
