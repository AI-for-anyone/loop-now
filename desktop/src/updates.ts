import { EventEmitter } from 'node:events'

// The parts of electron-updater in use; unit tests swap in a fake.
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

// Automatic updates (GitHub Releases, stable and pre-release): checks 10 seconds after start and then every 6 hours; when there is a new
// version, the menu bar points to the download page.
// No automatic download and no automatic install: on macOS, installing an update requires a properly signed app, so it cannot work until
// signing is in place. No checks in development mode (the caller decides whether to call start).
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
    // Allow pre-releases and set no channel: with a channel set, electron-updater's GitHub provider only accepts tags whose pre-release part
    // equals the channel name ('dev' only accepts v0.2.0-dev.N, 'latest' accepts none at all), so both the stable v0.2.0 and v0.2.0-rc.N
    // get skipped. With no channel, an installed stable release takes the newest one in Releases, stable or pre-release alike; when the
    // installed version is a pre-release, the channel comes from its version number:
    // an installed rc only accepts rc tags, an installed beta or alpha also accepts stable releases.
    updater.allowPrerelease = true
    // Only newer versions.
    updater.allowDowngrade = false
    const log = (message?: unknown) => note(`Updater: ${String(message)}`)
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
