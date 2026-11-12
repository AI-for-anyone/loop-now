import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { enUS } from '@/i18n/en-US'
import { RunningTaskBadge } from './RunningTaskBadge'

describe('运行中任务的徽标', () => {
  it('显示个数，读屏与悬停提示读“N 个任务正在运行”，数字本身对读屏隐藏', () => {
    const { container } = render(<RunningTaskBadge count={3} />)
    expect(screen.getByText('3 个任务正在运行')).toHaveClass('sr-only')
    expect(container.firstElementChild).toHaveAttribute('title', '3 个任务正在运行')
    expect(screen.getByText('3')).toHaveAttribute('aria-hidden', 'true')
  })

  it('为 0 或还没读到时不显示', () => {
    expect(render(<RunningTaskBadge count={0} />).container).toBeEmptyDOMElement()
    expect(render(<RunningTaskBadge count={undefined} />).container).toBeEmptyDOMElement()
  })

  it('英文按单复数', () => {
    expect(enUS.shell.runningTasks(1)).toBe('1 task running')
    expect(enUS.shell.runningTasks(2)).toBe('2 tasks running')
  })
})
