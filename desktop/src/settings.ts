import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'

// The shell's own settings (userData/settings.json). Only shell concerns; all business settings live in the server.
export type Settings = {
  // The port the server actually listened on last time.
  port?: number
  // The port the external TLS port (for external identities and loopctl) actually listened on last time.
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
