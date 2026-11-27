import { screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import type { DesktopBridge } from '@/features/desktop/desktop'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

// In the Chinese UI the language picker shows “中文” (Chinese) as the current language. Moved from
// src/features/settings/SettingsPage.test.tsx, which keeps the same scenario in English (English is the current language there).

describe('Global settings on the sheet', () => {
  afterEach(() => {
    delete window.loopNowDesktop
  })

  const region = (name: string) => screen.getByRole('region', { name })
  const open = async () => {
    server.use(...baseHandlers([]))
    renderRoutes(routes, '/settings')
    return screen.findByRole('heading', { level: 1, name: '全局设置' })
  }

  it('the UI language is a segmented button using semantic surface tokens', async () => {
    await open()
    const group = within(region('界面语言')).getByRole('radiogroup', { name: '界面语言' })
    expect(group).toHaveClass('border', 'border-hairline', 'bg-segment-track', 'rounded-[8px]')
    expect(within(group).getByRole('radio', { name: '中文' })).toHaveAttribute('aria-checked', 'true')
    expect(within(group).getByRole('radio', { name: '中文' })).toHaveClass('data-[state=on]:bg-segment-active')
  })

  it('shows the Chinese loopctl row and next-step external identity connection hint once installed in the desktop app', async () => {
    window.loopNowDesktop = {
      platform: 'darwin',
      notify: vi.fn(),
      setBadge: vi.fn(),
      onNavigate: vi.fn(() => () => undefined),
      cliStatus: vi.fn(async () => ({
        state: 'installed',
        mode: 'symlink',
        location: '/usr/local/bin/loopctl',
        target: '/Applications/Loop Now.app/Contents/Resources/bin/loopctl',
        externalAvailable: true,
      })),
      installCli: vi.fn(),
    } as unknown as DesktopBridge

    await open()
    const cli = region('命令行工具 loopctl')
    expect(await within(cli).findByText('已安装')).toBeInTheDocument()
    expect(within(cli).getByText(/外部身份连接方式…/)).toBeInTheDocument()
  })
})
