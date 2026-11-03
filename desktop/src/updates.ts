import { EventEmitter } from 'node:events'

// 用到的 electron-updater 的那几样；单测换成假的。
export type Updater = {
  autoDownload: boolean
  autoInstallOnAppQuit: boolean
  allowPrerelease: boolean
  allowDowngrade: boolean
  channel: string | null
  logger: { info: (message?: unknown) => void; warn: (message?: unknown) => void; error: (message?: unknown) => void; debug?: (message: string) => void } | null
  on(event: 'update-available', listener: (info: { version: string }) => void): unknown
  checkForUpdates(): Promise<unknown>
}

export const checkDelayMs = 10_000
export const checkIntervalMs = 6 * 60 * 60_000

export const releasePage = (version: string) => `https://github.com/AI-for-anyone/loop-now/releases/tag/v${version}`

// 自动更新（GitHub Releases 的 dev 通道）：启动 10 秒后与每 6 小时检查一次，有新版本时由菜单栏提示去下载页，
// 不自动下载、不自动安装——macOS 上安装更新要求 app 有正式签名，签名到位之前装不上。开发模式下不检查（由调用方决定是否 start）。
export class UpdateChecker extends EventEmitter<{ available: [version: string] }> {
  available: string | undefined
  private readonly timers: NodeJS.Timeout[] = []

  constructor(
    private readonly updater: Updater,
    private readonly note: (text: string) => void,
  ) {
    super()
    updater.autoDownload = false
    updater.autoInstallOnAppQuit = false
    updater.allowPrerelease = true
    updater.channel = 'dev'
    // 设置通道会顺带允许降级，这里只要更新的版本。
    updater.allowDowngrade = false
    const log = (message?: unknown) => note(`更新：${String(message)}`)
    updater.logger = { info: log, warn: log, error: log, debug: () => undefined }
    updater.on('update-available', (info) => {
      if (this.available === info.version) return
      this.available = info.version
      this.emit('available', info.version)
    })
  }

  start(): void {
    this.timers.push(
      setTimeout(() => void this.check(), checkDelayMs),
      setInterval(() => void this.check(), checkIntervalMs),
    )
  }

  async check(): Promise<void> {
    try {
      await this.updater.checkForUpdates()
    } catch (err) {
      this.note(`检查更新失败：${(err as Error).message}`)
    }
  }
}
