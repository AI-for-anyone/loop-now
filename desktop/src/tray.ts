import { app, Menu, Tray, nativeImage } from 'electron'
import { join } from 'node:path'
import type { ServerState } from './server'
import { strings, type Strings } from './strings'

export type TrayAction = 'open' | 'browser' | 'logs' | 'cli' | 'restart' | 'quit'
export type TrayItem = { label: string; enabled: boolean; action?: TrayAction } | 'separator'

const serving = (state: ServerState, url: string | undefined): url is string => (state === 'running' || state === 'degraded') && url !== undefined

// 菜单第一行与提示文字：服务端状态，就绪时带主机和端口，隔离配置时带配置名。
export function trayStatus(state: ServerState, url: string | undefined, profile: string | undefined, s: Strings): string {
  const status = serving(state, url) ? `${s.state[state]} · ${new URL(url).host}` : s.state[state]
  return profile ? `${status} (${profile})` : status
}

// 菜单栏图标的菜单：第一行是状态（不可点），下面是打开窗口、在浏览器打开、日志、安装命令行工具（只在打包后的 app 里）、重启与退出。
export function trayItems(state: ServerState, url: string | undefined, profile: string | undefined, s: Strings, packaged = true): TrayItem[] {
  return [
    { label: trayStatus(state, url, profile, s), enabled: false },
    'separator',
    { label: s.tray.open, enabled: true, action: 'open' },
    { label: s.tray.browser, enabled: serving(state, url), action: 'browser' },
    'separator',
    { label: s.tray.logs, enabled: true, action: 'logs' },
    { label: s.tray.cli, enabled: packaged, action: 'cli' },
    { label: s.tray.restart, enabled: state !== 'starting' && state !== 'stopping', action: 'restart' },
    'separator',
    { label: s.tray.quit, enabled: true, action: 'quit' },
  ]
}

export class StatusTray {
  private readonly tray: Tray

  constructor(
    private readonly actions: Record<TrayAction, () => void>,
    private readonly profile?: string,
  ) {
    // 文件名以 Template 结尾，macOS 按菜单栏的深浅色自动着色；同目录的 @2x 用于高分屏。
    this.tray = new Tray(nativeImage.createFromPath(join(__dirname, '..', 'assets', 'trayTemplate.png')))
    this.tray.setIgnoreDoubleClickEvents(true)
  }

  update(state: ServerState, url: string | undefined): void {
    const s = strings()
    this.tray.setToolTip(`Loop Now — ${trayStatus(state, url, this.profile, s)}`)
    this.tray.setContextMenu(
      Menu.buildFromTemplate(
        trayItems(state, url, this.profile, s, app.isPackaged).map((item) => {
          if (item === 'separator') return { type: 'separator' }
          const action = item.action
          return { label: item.label, enabled: item.enabled, click: action ? this.actions[action] : undefined }
        }),
      ),
    )
  }
}
