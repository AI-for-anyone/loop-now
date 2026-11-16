import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import { localeStorageKey, page as browserPage } from '@/i18n/locale'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

// Switching the UI language from the Chinese UI: “中文” (Chinese) is the current language, and choosing English saves en-US.
// Moved from src/app/shell.test.tsx, which keeps the same scenario in English (switching from English to “中文” there).

describe('global settings page', () => {
  it('the UI language is saved in this browser, and the page reloads after switching', async () => {
    server.use(...baseHandlers([]))
    const reload = vi.spyOn(browserPage, 'reload').mockImplementation(() => {})
    const user = userEvent.setup()
    renderRoutes(routes, '/settings')
    // After the redesign, the interface language is a segmented control (it used to be a dropdown).
    const group = await screen.findByRole('radiogroup', { name: '界面语言' })
    expect(within(group).getByRole('radio', { name: '中文' })).toHaveAttribute('aria-checked', 'true')
    expect(group).toHaveAccessibleDescription('只影响这个浏览器；切换后页面会重新加载。')
    await user.click(within(group).getByRole('radio', { name: 'English' }))
    expect(window.localStorage.getItem(localeStorageKey)).toBe('en-US')
    expect(reload).toHaveBeenCalledOnce()
  })
})
