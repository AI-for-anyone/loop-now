import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'

// 壳自己的设置（userData/settings.json）。只有壳的事，业务设置都在服务端。
export type Settings = {
  // 上次服务端实际监听的端口。
  port?: number
  // 上次对外 TLS 端口（外部身份与 loopctl 用）实际监听的端口。
  externalPort?: number
}

export class SettingsFile {
  constructor(readonly file: string) {}

  read(): Settings {
    try {
      const value: unknown = JSON.parse(readFileSync(this.file, 'utf8'))
      return value && typeof value === 'object' ? (value as Settings) : {}
    } catch {
      return {}
    }
  }

  update(patch: Settings): void {
    const next = { ...this.read(), ...patch }
    mkdirSync(dirname(this.file), { recursive: true })
    const tmp = `${this.file}.tmp`
    writeFileSync(tmp, JSON.stringify(next, null, 2) + '\n', { mode: 0o600 })
    renameSync(tmp, this.file)
  }
}
