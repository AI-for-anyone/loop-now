import { screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { renderRoutes } from '@/test/render'
import { AccountSelectionField } from './AccountSelectionField'

describe('Account selection display does not resolve on its own or fall back silently', () => {
  it('shows both candidates even when one is unavailable, does not preselect the first, and only allows an explicit save', async () => {
    const save = vi.fn(), change = vi.fn()
    renderRoutes([{ path: '/', element: <AccountSelectionField label="Model project account" description="Several matching accounts; choose one explicitly" options={[{ value: 'a', label: 'Account A · Unavailable' }, { value: 'b', label: 'Account B · Logged in' }]} value="" canRestore={false} settingsPath="/p/demo/settings" onChange={change} onSave={save} onRestore={() => {}} /> }], '/')
    expect(screen.getByRole('combobox')).toHaveValue('')
    expect(screen.getAllByRole('option')).toHaveLength(3)
    expect(screen.getByRole('button', { name: 'Save selection' })).toBeDisabled()
    expect(change).not.toHaveBeenCalled(); expect(save).not.toHaveBeenCalled()
  })
  it('keeps the stale value when the chosen account was deleted and does not pass off the only remaining account as the current choice', () => {
    renderRoutes([{ path: '/', element: <AccountSelectionField label="Model project account" description="The explicitly chosen account was deleted; no other account is used automatically" options={[{ value: 'b', label: 'Account B' }]} value="deleted-a" canRestore dirty={false} settingsPath="/p/demo/settings" onChange={() => {}} onSave={() => {}} onRestore={() => {}} /> }], '/')
    expect(screen.getByRole('combobox')).toHaveValue('deleted-a')
    expect(screen.getByRole('button', { name: 'Restore automatic selection for this provider' })).toBeEnabled()
  })
})
