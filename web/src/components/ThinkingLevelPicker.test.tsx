import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { chooseThinking, thinkingChoice, thinkingOptions, thinkingPicker } from '@/test/thinking'
import { ThinkingLevelPicker } from './ThinkingLevelPicker'

// 改版：思考等级是一组分段按钮（2px 藏青描边、选中的一段黄色），单选组的语义，左右方向键在段之间移动。
it('offers only catalog levels plus default, and emits null for default', async () => {
  const onChange = vi.fn()
  render(<ThinkingLevelPicker value="high" levels={['low', 'high']} defaultLevel="low" onChange={onChange} />)
  expect(thinkingOptions(document.body)).toEqual(['默认（低）', '低', '高'])
  expect(thinkingChoice(document.body)).toBe('高')
  await chooseThinking(userEvent.setup(), document.body, '默认（低）')
  expect(onChange).toHaveBeenCalledWith(null)
})

it('looks like a segmented control: ink border, the chosen segment yellow', () => {
  render(<ThinkingLevelPicker value="high" levels={['low', 'high']} onChange={() => {}} />)
  const picker = thinkingPicker(document.body)
  expect(picker).toHaveClass('border-2', 'border-ink', 'rounded-input')
  for (const radio of screen.getAllByRole('radio')) expect(radio).toHaveClass('data-[state=checked]:bg-riso-yellow')
})

it('preserves an unavailable stored value visibly and shows the field error', () => {
  render(<ThinkingLevelPicker value="high" levels={['low']} onChange={() => {}} error="当前模型不支持该等级" />)
  // 存着的等级不在目录里：没有哪一段选中，下面写明当前值暂不可选。
  expect(thinkingChoice(document.body)).toBeNull()
  expect(screen.getByText('高（当前值，暂不可选）')).toBeInTheDocument()
  expect(thinkingPicker(document.body)).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByText('当前模型不支持该等级')).toBeInTheDocument()
})

it('can disable catalog selection without losing its value', () => {
  render(<ThinkingLevelPicker value={null} levels={[]} disabled onChange={() => {}} />)
  for (const radio of screen.getAllByRole('radio')) expect(radio).toBeDisabled()
  expect(thinkingChoice(document.body)).toBe('默认')
})
