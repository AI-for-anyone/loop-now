import { closeSync, existsSync, mkdirSync, openSync, renameSync, rmSync, statSync, writeSync } from 'node:fs'
import { dirname } from 'node:path'

// 追加写的日志文件，超过 maxBytes 就轮转：file → file.1 → … → file.<keep>，最旧的删掉。
// 另在内存里留最近一段输出，启动失败时把末尾几行显示给人看。
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

  // 带时间的一行，壳自己的记录用它。
  line(text: string): void {
    this.write(`${new Date().toISOString()} ${text}\n`)
  }

  // 最近输出的末尾若干行。
  tail(lines: number): string {
    return this.recent.trimEnd().split('\n').slice(-lines).join('\n')
  }

  // 新一次运行开始时清空内存里的末尾，免得把上一次的输出当成这次的。
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
