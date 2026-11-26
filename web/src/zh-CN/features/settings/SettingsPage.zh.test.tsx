import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

// In the Chinese UI the language picker shows “中文” (Chinese) as the current language. Moved from
// src/features/settings/SettingsPage.test.tsx, which keeps the same scenario in English (English is the current language there).

describe('Global settings on the sheet', () => {
  const region = (name: string) => screen.getByRole('region', { name })
  const open = async () => {
    server.use(...baseHandlers([]))
    renderRoutes(routes, '/settings')
    return screen.findByRole('heading', { level: 1, name: '全局设置' })
  }

  it('the UI language is a segmented button, using hairline active styling in mono skin and yellow in Plate skin', async () => {
    await open()
    const group = within(region('界面语言')).getByRole('radiogroup', { name: '界面语言' })
    expect(group).toHaveClass('border', 'border-hairline-strong', 'rounded-ticket', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-ink', '[[data-skin=plate]_&]:rounded-full')
    expect(within(group).getByRole('radio', { name: '中文' })).toHaveAttribute('aria-checked', 'true')
    expect(within(group).getByRole('radio', { name: '中文' })).toHaveClass('data-[state=on]:bg-surface-active', '[[data-skin=plate]_&]:data-[state=on]:bg-riso-yellow')
  })
})
