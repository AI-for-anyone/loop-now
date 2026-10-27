import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { ToggleGroup, ToggleGroupItem } from './toggle-group'

function Ranges({ initial = 'today' }: { initial?: string }) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <ToggleGroup aria-label="时间段" value={value} onValueChange={setValue}>
        <ToggleGroupItem value="today">今天</ToggleGroupItem>
        <ToggleGroupItem value="week">本周</ToggleGroupItem>
        <ToggleGroupItem value="locked" disabled>
          不可选
        </ToggleGroupItem>
        <ToggleGroupItem value="month">近 30 天</ToggleGroupItem>
      </ToggleGroup>
      <output>{value}</output>
    </>
  )
}

describe('分段控件', () => {
  it('单选：点击切换，选中状态随之变化', async () => {
    const user = userEvent.setup()
    render(<Ranges />)
    expect(screen.getByRole('radiogroup', { name: '时间段' })).toBeInTheDocument()
    const today = screen.getByRole('radio', { name: '今天' })
    const week = screen.getByRole('radio', { name: '本周' })
    expect(today).toHaveAttribute('aria-checked', 'true')
    expect(today).toHaveAttribute('data-state', 'on')
    await user.click(week)
    expect(week).toHaveAttribute('aria-checked', 'true')
    expect(week).toHaveAttribute('data-state', 'on')
    expect(today).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByRole('status')).toHaveTextContent('week')
  })

  it('再点一次选中项不会取消选择', async () => {
    const user = userEvent.setup()
    render(<Ranges />)
    await user.click(screen.getByRole('radio', { name: '今天' }))
    expect(screen.getByRole('radio', { name: '今天' })).toBeChecked()
    expect(screen.getByRole('status')).toHaveTextContent('today')
  })

  it('左右方向键移动焦点并跳过禁用项，空格或回车选中', async () => {
    const user = userEvent.setup()
    render(<Ranges />)
    await user.tab()
    expect(screen.getByRole('radio', { name: '今天' })).toHaveFocus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('radio', { name: '本周' })).toHaveFocus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('radio', { name: '近 30 天' })).toHaveFocus()
    await user.keyboard(' ')
    expect(screen.getByRole('radio', { name: '近 30 天' })).toBeChecked()
    await user.keyboard('{ArrowLeft}')
    expect(screen.getByRole('radio', { name: '本周' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(screen.getByRole('radio', { name: '本周' })).toBeChecked()
  })

  it('禁用项不能选中', async () => {
    const user = userEvent.setup()
    render(<Ranges />)
    const locked = screen.getByRole('radio', { name: '不可选' })
    expect(locked).toBeDisabled()
    await user.click(locked)
    expect(locked).not.toBeChecked()
    expect(screen.getByRole('status')).toHaveTextContent('today')
  })
})
