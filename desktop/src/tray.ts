import { app, Menu, Tray, nativeImage } from 'electron'
import { join } from 'node:path'
import { current, trayIcon, type PlatformId } from './platform'
import { defaultProfile, overridableDirs, type Profile } from './profile'
import type { ServerState } from './server'
import { strings, type Strings } from './strings'

export type TrayAction = 'update' | 'open' | 'browser' | 'logs' | 'cli' | 'external' | 'restart' | 'quit'
export type TrayItem = { label: string; enabled: boolean; action?: TrayAction } | 'separator'

const serving = (state: ServerState, url: string | undefined): url is string => (state === 'running' || state === 'degraded') && url !== undefined

// The first menu line: the server state, with host and port once ready.
export function trayStatus(state: ServerState, url: string | undefined, s: Strings): string {
  return serving(state, url) ? `${s.state[state]} · ${new URL(url).host}` : s.state[state]
}

// The current profile's name; the default profile shows as "默认" (default).
const profileName = (profile: Profile | undefined, s: Strings) => (profile === undefined || profile.label === defaultProfile ? s.tray.defaultProfile : profile.label)

// The profile line: the profile name, followed by the paths actually used when desktop.json specifies directories for it.
const profileLine = (profile: Profile | undefined, s: Strings) =>
  [s.tray.profile(profileName(profile, s)), ...overridableDirs.flatMap((key) => (profile?.overrides[key] ? [s.tray.dirs[key](profile[key])] : []))].join(' · ')

// The menu bar icon's menu: the first two lines are the state and the current profile (disabled); when there is a new version, an item
// leading to the download page follows; below that come Open Window, Open in Browser, logs, Install Command Line Tool (only in the packaged
// app), External Identity Connection (enabled when the server is ready and the external TLS port is open), Restart and Quit.
export function trayItems(
  state: ServerState,
  url: string | undefined,
  profile: Profile | undefined,
  s: Strings,
  packaged = true,
  update?: string,
  externalUrl?: string,
): TrayItem[] {
  return [
    { label: trayStatus(state, url, s), enabled: false },
    { label: profileLine(profile, s), enabled: false },
    ...(update ? [{ label: s.tray.update(update), enabled: true, action: 'update' as const }] : []),
    'separator',
    { label: s.tray.open, enabled: true, action: 'open' },
    { label: s.tray.browser, enabled: serving(state, url), action: 'browser' },
    'separator',
    { label: s.tray.logs, enabled: true, action: 'logs' },
    { label: s.tray.cli, enabled: packaged, action: 'cli' },
    { label: s.tray.external, enabled: serving(state, url) && externalUrl !== undefined, action: 'external' },
    { label: s.tray.restart, enabled: state !== 'starting' && state !== 'stopping', action: 'restart' },
    'separator',
    { label: s.tray.quit, enabled: true, action: 'quit' },
  ]
}

export class StatusTray {
  private readonly tray: Tray

  constructor(
    private readonly actions: Record<TrayAction, () => void>,
    private readonly profile: Profile | undefined,
    // The new version number found by the update check; undefined when there is none.
    private readonly newVersion: () => string | undefined,
    platform: PlatformId = current,
  ) {
    // macOS: a file name ending in Template is tinted automatically for a light or dark menu bar, and the @2x file in the same directory is
    // used on high-resolution screens. Windows: .ico, and the system picks among its sizes by DPI.
    this.tray = new Tray(nativeImage.createFromPath(join(__dirname, '..', 'assets', trayIcon(platform))))
    this.tray.setIgnoreDoubleClickEvents(true)
    // Windows convention: left-clicking the notification area icon opens the window, right-clicking shows the menu (on macOS, clicking the
    // icon shows the menu).
    if (platform === 'win32') this.tray.on('click', () => this.actions.open())
  }

  update(state: ServerState, url: string | undefined, externalUrl?: string): void {
    const s = strings()
    this.tray.setToolTip(`Loop Now · ${profileName(this.profile, s)} — ${trayStatus(state, url, s)}`)
    this.tray.setContextMenu(
      Menu.buildFromTemplate(
        trayItems(state, url, this.profile, s, app.isPackaged, this.newVersion(), externalUrl).map((item) => {
          if (item === 'separator') return { type: 'separator' }
          const action = item.action
          return { label: item.label, enabled: item.enabled, click: action ? this.actions[action] : undefined }
        }),
      ),
    )
  }
}
