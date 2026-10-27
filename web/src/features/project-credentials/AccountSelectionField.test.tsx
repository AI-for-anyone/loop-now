import { screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { renderRoutes } from '@/test/render'
import { AccountSelectionField } from './AccountSelectionField'

describe('账号选择展示不自行解析或静默回落', () => {
  it('两候选含不可用仍都展示、不默认选第一条；只允许明确保存', async () => {
    const save = vi.fn(), change = vi.fn()
    renderRoutes([{ path: '/', element: <AccountSelectionField label="模型项目账号" description="多个对应账号，需要明确选择" options={[{ value: 'a', label: '账号A · 不可用' }, { value: 'b', label: '账号B · 已登录' }]} value="" canRestore={false} settingsPath="/p/demo/settings" onChange={change} onSave={save} onRestore={() => {}} /> }], '/')
    expect(screen.getByRole('combobox')).toHaveValue('')
    expect(screen.getAllByRole('option')).toHaveLength(3)
    expect(screen.getByRole('button', { name: '保存选择' })).toBeDisabled()
    expect(change).not.toHaveBeenCalled(); expect(save).not.toHaveBeenCalled()
  })
  it('已选删除时保留失效值，不把剩余唯一账号冒充当前选中', () => {
    renderRoutes([{ path: '/', element: <AccountSelectionField label="模型项目账号" description="明确选择的账号已删除，不自动换用" options={[{ value: 'b', label: '账号B' }]} value="deleted-a" canRestore dirty={false} settingsPath="/p/demo/settings" onChange={() => {}} onSave={() => {}} onRestore={() => {}} /> }], '/')
    expect(screen.getByRole('combobox')).toHaveValue('deleted-a')
    expect(screen.getByRole('button', { name: '恢复当前提供方自动取用' })).toBeEnabled()
  })
})
