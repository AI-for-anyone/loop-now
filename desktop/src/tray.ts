import { app, Menu, Tray, nativeImage } from 'electron'
import { join } from 'node:path'
import { current, trayIcon, type PlatformId } from './platform'
import { defaultProfile, overridableDirs, type Profile } from './profile'
import type { ServerState } from './server'
import { strings, type Strings } from './strings'

export type TrayAction = 'update' | 'open' | 'browser' | 'logs' | 'cli' | 'external' | 'restart' | 'quit'
export type TrayItem = { label: string; enabled: boolean; action?: TrayAction } | 'separator'

const serving = (state: ServerState, url: string | undefined): url is string => (state === 'running' || state === 'degraded') && url !== undefined

// 菜单第一行：服务端状态，就绪时带主机和端口。
export function trayStatus(state: ServerState, url: string | undefined, s: Strings): string {
  return serving(state, url) ? `${s.state[state]} · ${new URL(url).host}` : s.state[state]
}

// 当前配置的名字；默认配置显示成"默认"。
const profileName = (profile: Profile | undefined, s: Strings) => (profile === undefined || profile.label === defaultProfile ? s.tray.defaultProfile : profile.label)

// 配置行：配置名；desktop.json 为它指定了目录时，后面跟上实际用的路径。
const profileLine = (profile: Profile | undefined, s: Strings) =>
  [s.tray.profile(profileName(profile, s)), ...overridableDirs.flatMap((key) => (profile?.overrides[key] ? [s.tray.dirs[key](profile[key])] : []))].join(' · ')

// 菜单栏图标的菜单：前两行是状态与当前配置（不可点），有新版本时下面多一项去下载页，再下面是打开窗口、在浏览器打开、日志、
// 安装命令行工具（只在打包后的 app 里）、外部身份连接方式（服务端就绪且开了对外 TLS 端口时可点）、重启与退出。
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
    // 检查到的新版本号；没有时为空。
    private readonly newVersion: () => string | undefined,
    platform: PlatformId = current,
  ) {
    // macOS：文件名以 Template 结尾，按菜单栏的深浅色自动着色，同目录的 @2x 用于高分屏。Windows：.ico，多个尺寸由系统按 DPI 选。
    this.tray = new Tray(nativeImage.createFromPath(join(__dirname, '..', 'assets', trayIcon(platform))))
    this.tray.setIgnoreDoubleClickEvents(true)
    // Windows 的习惯：左键点通知区图标打开窗口，右键出菜单（macOS 上点图标就是出菜单）。
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
