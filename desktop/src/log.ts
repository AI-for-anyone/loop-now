import { closeSync, existsSync, mkdirSync, openSync, renameSync, rmSync, statSync, writeSync } from 'node:fs'
import { dirname } from 'node:path'

// An append-only log file that rotates once it exceeds maxBytes: file → file.1 → … → file.<keep>, and the oldest is deleted.
// It also keeps the most recent output in memory, so the last few lines can be shown to the user when startup fails.
export class RotatingLog {
  private fd = -1
  private size = 0
  private recent = ''

  constructor(
    readonly file: string,
    readonly maxBytes = 10 * 1024 * 1024,
    readonly keep = 5,
  ) {
    mkdirSync(dirname(file), { recursive: true })
    this.open()
  }

  write(chunk: string | Buffer): void {
    const data = typeof chunk === 'string' ? Buffer.from(chunk) : chunk
    if (this.size > 0 && this.size + data.length > this.maxBytes) this.rotate()
    writeSync(this.fd, data)
    this.size += data.length
    this.recent = (this.recent + data.toString('utf8')).slice(-32 * 1024)
  }

  // A timestamped line; used for the shell's own records.
  line(text: string): void {
    this.write(`${new Date().toISOString()} ${text}\n`)
  }

  // The last few lines of the recent output.
  tail(lines: number): string {
    return this.recent.trimEnd().split('\n').slice(-lines).join('\n')
  }

  // Clears the in-memory tail when a new run starts, so the previous run's output is not taken for this one's.
  resetTail(): void {
    this.recent = ''
  }

  close(): void {
    if (this.fd >= 0) closeSync(this.fd)
    this.fd = -1
  }

  private open(): void {
    this.fd = openSync(this.file, 'a', 0o600)
    this.size = statSync(this.file).size
  }

  private rotate(): void {
    this.close()
    rmSync(`${this.file}.${this.keep}`, { force: true })
    for (let i = this.keep - 1; i >= 1; i--) {
      if (existsSync(`${this.file}.${i}`)) renameSync(`${this.file}.${i}`, `${this.file}.${i + 1}`)
    }
    renameSync(this.file, `${this.file}.1`)
    this.open()
  }
}
