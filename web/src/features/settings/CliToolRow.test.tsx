import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import type { DesktopBridge, DesktopCliInstallResult, DesktopCliStatus } from '@/features/desktop/desktop'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { apiUrl } from '@/test/api'
import { http, HttpResponse } from 'msw'

function installDesktopBridge(overrides: Partial<DesktopBridge>) {
  const bridge: DesktopBridge = {
    platform: 'darwin',
    notify: vi.fn(),
    setBadge: vi.fn(),
    onNavigate: vi.fn(() => () => undefined),
    ...overrides,
  }
  window.loopNowDesktop = bridge
  return bridge
}

async function openSettings() {
  server.use(...baseHandlers([]))
  renderRoutes(routes, '/settings')
  return screen.findByRole('region', { name: 'Command line tool (loopctl)' })
}

afterEach(() => {
  delete window.loopNowDesktop
  vi.restoreAllMocks()
})

describe('Global settings loopctl row (desktop app)', () => {
  it('shows Not installed, runs one-click Install, and transitions to Installed with the next-step connection guidance', async () => {
    const user = userEvent.setup()
    let current: DesktopCliStatus = {
      state: 'not_installed',
      mode: 'symlink',
      location: '/usr/local/bin/loopctl',
      target: '/Applications/Loop Now.app/Contents/Resources/bin/loopctl',
      externalAvailable: true,
    }
    const cliStatus = vi.fn(async () => current)
    const installCli = vi.fn(async (): Promise<DesktopCliInstallResult> => {
      current = {
        state: 'installed',
        mode: 'symlink',
        location: '/usr/local/bin/loopctl',
        target: '/Applications/Loop Now.app/Contents/Resources/bin/loopctl',
        externalAvailable: true,
      }
      return { outcome: 'installed', status: current }
    })
    installDesktopBridge({ cliStatus, installCli })

    const section = await openSettings()
    expect(await within(section).findByText('Not installed')).toBeInTheDocument()
    expect(within(section).getByText(/\/usr\/local\/bin\/loopctl/)).toBeInTheDocument()

    await user.click(within(section).getByRole('button', { name: 'Install' }))
    expect(installCli).toHaveBeenCalledOnce()
    expect(await within(section).findByText('Installed')).toBeInTheDocument()
    expect(within(section).getByText(/Linked at \/usr\/local\/bin\/loopctl/)).toBeInTheDocument()
    expect(within(section).getByText(/External Identity Connection…/)).toBeInTheDocument()
    expect(within(section).getByRole('link', { name: 'External identities' })).toHaveAttribute('href', '/identities')
    expect(within(section).queryByRole('button', { name: 'Install' })).toBeNull()
  })

  it('shows Points elsewhere with the existing target path and allows replacing it', async () => {
    const user = userEvent.setup()
    const installedStatus: DesktopCliStatus = {
      state: 'installed',
      mode: 'symlink',
      location: '/usr/local/bin/loopctl',
      target: '/Applications/Loop Now.app/Contents/Resources/bin/loopctl',
      externalAvailable: true,
    }
    const cliStatus = vi.fn(async (): Promise<DesktopCliStatus> => ({
      state: 'other',
      mode: 'symlink',
      location: '/usr/local/bin/loopctl',
      target: '/Applications/Loop Now.app/Contents/Resources/bin/loopctl',
      points: '/opt/homebrew/bin/loopctl',
      externalAvailable: true,
    }))
    const installCli = vi.fn(async (): Promise<DesktopCliInstallResult> => ({
      outcome: 'installed',
      status: installedStatus,
    }))
    installDesktopBridge({ cliStatus, installCli })

    const section = await openSettings()
    expect(await within(section).findByText('Points elsewhere')).toBeInTheDocument()
    expect(within(section).getByText(/\/opt\/homebrew\/bin\/loopctl/)).toBeInTheDocument()

    await user.click(within(section).getByRole('button', { name: 'Replace' }))
    expect(installCli).toHaveBeenCalledOnce()
    expect(await within(section).findByText('Installed')).toBeInTheDocument()
  })

  it('shows Points elsewhere without a symlink target when a regular file occupies the path', async () => {
    installDesktopBridge({
      cliStatus: vi.fn(async (): Promise<DesktopCliStatus> => ({
        state: 'other',
        mode: 'symlink',
        location: '/usr/local/bin/loopctl',
        target: '/Applications/Loop Now.app/Contents/Resources/bin/loopctl',
        externalAvailable: true,
      })),
      installCli: vi.fn(),
    })

    const section = await openSettings()
    expect(await within(section).findByText('Points elsewhere')).toBeInTheDocument()
    expect(within(section).getByText(/\/usr\/local\/bin\/loopctl already exists/)).toBeInTheDocument()
  })

  it('handles Windows user PATH mode, cancellation without error, failure alert, moveFirst warning, and unavailable dev state', async () => {
    const user = userEvent.setup()
    const winDir = 'C:\\Users\\someone\\AppData\\Local\\Programs\\Loop Now\\resources\\bin'
    const winInstalled: DesktopCliStatus = {
      state: 'installed',
      mode: 'path',
      location: winDir,
      target: `${winDir}\\loopctl.exe`,
      externalAvailable: true,
    }
    const winNotInstalled: DesktopCliStatus = {
      state: 'not_installed',
      mode: 'path',
      location: winDir,
      target: `${winDir}\\loopctl.exe`,
      externalAvailable: true,
    }

    const cliStatus = vi.fn(async () => winNotInstalled)
    const installCli = vi
      .fn<() => Promise<DesktopCliInstallResult>>()
      .mockResolvedValueOnce({ outcome: 'cancelled', status: winNotInstalled })
      .mockResolvedValueOnce({ outcome: 'failed', error: 'Access denied', status: winNotInstalled })
      .mockResolvedValueOnce({ outcome: 'installed', status: winInstalled })

    installDesktopBridge({ platform: 'win32', cliStatus, installCli })

    const section = await openSettings()
    expect(await within(section).findByText('Not installed')).toBeInTheDocument()
    expect(within(section).getByText(/no administrator password needed/i)).toBeInTheDocument()

    // Cancelled: stays Not installed with no error alert.
    await user.click(within(section).getByRole('button', { name: 'Install' }))
    await waitFor(() => expect(installCli).toHaveBeenCalledTimes(1))
    expect(within(section).queryByRole('alert')).toBeNull()

    // Failed: shows error alert.
    await user.click(within(section).getByRole('button', { name: 'Install' }))
    expect(await within(section).findByRole('alert')).toHaveTextContent('Access denied')

    // Installed: clears error and shows PATH message + next step.
    await user.click(within(section).getByRole('button', { name: 'Install' }))
    expect(await within(section).findByText('Installed')).toBeInTheDocument()
    expect(within(section).queryByRole('alert')).toBeNull()
    expect(within(section).getByText(/Reopen any terminals/i)).toBeInTheDocument()
  })

  it('disables Install and shows move-to-Applications notice when moveFirst is set', async () => {
    installDesktopBridge({
      cliStatus: vi.fn(async (): Promise<DesktopCliStatus> => ({
        state: 'not_installed',
        mode: 'symlink',
        location: '/usr/local/bin/loopctl',
        target: '/Downloads/Loop Now.app/Contents/Resources/bin/loopctl',
        moveFirst: true,
        externalAvailable: true,
      })),
      installCli: vi.fn(),
    })

    const section = await openSettings()
    expect(await within(section).findByText(/Move Loop Now to the Applications folder/i)).toBeInTheDocument()
    expect(within(section).getByRole('button', { name: 'Install' })).toBeDisabled()
  })

  it('shows unavailable notice in unpackaged dev desktop runs', async () => {
    installDesktopBridge({
      cliStatus: vi.fn(async (): Promise<DesktopCliStatus> => ({
        state: 'unavailable',
        mode: 'symlink',
        reason: 'not_packaged',
        externalAvailable: false,
      })),
      installCli: vi.fn(),
    })

    const section = await openSettings()
    expect(await within(section).findByText('Unavailable')).toBeInTheDocument()
    expect(within(section).getByText(/unavailable in development mode/i)).toBeInTheDocument()
  })
})

describe('Global settings loopctl row (plain browser)', () => {
  it('shows where loopctl lives and a copyable PATH command without desktop bridge', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    server.use(
      http.get(apiUrl('/health'), () =>
        HttpResponse.json(
          { ok: true, database: { ok: true, error: null, walBytes: { data: 0, events: 0 }, walWarning: false } },
          { headers: { 'Loop-Now-Version': '0.3.6' } },
        ),
      ),
      ...baseHandlers([]),
    )

    renderRoutes(routes, '/settings')
    const section = await screen.findByRole('region', { name: 'Command line tool (loopctl)' })
    await waitFor(() => expect(within(section).getByText(/In a server release bundle, loopctl is at loopctl-dist\/0\.3\.6\//)).toBeInTheDocument())
    expect(within(section).getByText(/^sudo ln -sfn .*0\.3\.6.*\/usr\/local\/bin\/loopctl$/)).toBeInTheDocument()
    expect(within(section).getByRole('link', { name: 'External identities' })).toHaveAttribute('href', '/identities')

    await user.click(within(section).getByRole('button', { name: 'Copy' }))
    expect(writeText).toHaveBeenCalledWith('sudo ln -sfn /path/to/loopctl-dist/0.3.6/loopctl-<os>-<arch> /usr/local/bin/loopctl')
    expect(await within(section).findByRole('button', { name: 'Copied' })).toBeInTheDocument()
  })
})
