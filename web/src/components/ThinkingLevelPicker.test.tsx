import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { ThinkingLevelPicker } from './ThinkingLevelPicker'

it('offers only catalog levels plus default, and emits null for default', async () => {
  const onChange = vi.fn()
  render(<ThinkingLevelPicker value="high" levels={['low', 'high']} defaultLevel="low" onChange={onChange} />)
  expect(screen.getAllByRole('option').map(option => option.getAttribute('value'))).toEqual(['', 'low', 'high'])
  expect(screen.getByRole('option', { name: '默认（低）' })).toBeInTheDocument()
  await userEvent.selectOptions(screen.getByLabelText('思考等级'), '')
  expect(onChange).toHaveBeenCalledWith(null)
})

it('preserves an unavailable stored value visibly and shows the field error', () => {
  render(<ThinkingLevelPicker value="high" levels={['low']} onChange={() => {}} error="当前模型不支持该等级" />)
  expect(screen.getByLabelText('思考等级')).toHaveValue('high')
  expect(screen.getByRole('option', { name: '高（当前值，暂不可选）' })).toBeDisabled()
  expect(screen.getByLabelText('思考等级')).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByText('当前模型不支持该等级')).toBeInTheDocument()
})

it('can disable catalog selection without losing its value', () => {
  render(<ThinkingLevelPicker value={null} levels={[]} disabled onChange={() => {}} />)
  expect(screen.getByLabelText('思考等级')).toBeDisabled()
  expect(screen.getByLabelText('思考等级')).toHaveDisplayValue('默认')
})
