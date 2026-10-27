// 宿主与 Go 服务之间的 stdio 协议（api/plugin-host-protocol.md）：每行一个 JSON，{"id","type","plugin","payload"}。
// 标准输出只写协议行；读入超过上限的行整行丢弃。

import type { Readable } from 'node:stream'

export const PROTOCOL_VERSION = 1
export const MAX_LINE = 16 << 20

export interface Message {
  id?: string
  type: string
  plugin?: string
  payload?: unknown
}

export interface Reply {
  ok: boolean
  value?: unknown
  error?: string
}

interface Pending {
  resolve: (value: unknown) => void
  reject: (err: Error) => void
  timer: ReturnType<typeof setTimeout>
}

export class Channel {
  private next = 0
  private readonly pending = new Map<string, Pending>()
  private closed = false
  private readonly write: (line: string, done?: () => void) => void
  private readonly onMessage: (m: Message) => void
  private readonly onProblem: (message: string) => void

  constructor(write: (line: string, done?: () => void) => void, onMessage: (m: Message) => void, onProblem: (message: string) => void) {
    this.write = write
    this.onMessage = onMessage
    this.onProblem = onProblem
  }

  /** 开始读入；输入结束时调用 onEnd。 */
  listen(input: Readable, onEnd: () => void): void {
    let chunks: Buffer[] = []
    let size = 0
    let skipping = false
    input.on('data', (data: Buffer) => {
      let start = 0
      for (;;) {
        const nl = data.indexOf(10, start)
        if (nl < 0) break
        const part = data.subarray(start, nl)
        start = nl + 1
        if (skipping) {
          skipping = false
          continue
        }
        const line = chunks.length === 0 ? part : Buffer.concat([...chunks, part])
        chunks = []
        size = 0
        this.dispatch(line)
      }
      if (start < data.length && !skipping) {
        const rest = data.subarray(start)
        size += rest.length
        if (size >= MAX_LINE) {
          this.onProblem(`收到一行超过 ${MAX_LINE} 字节的消息，已丢弃`)
          chunks = []
          size = 0
          skipping = true
        } else {
          chunks.push(Buffer.from(rest))
        }
      }
    })
    input.on('end', onEnd)
    input.on('error', onEnd)
  }

  private dispatch(line: Buffer): void {
    if (line.length === 0) return
    let m: Message
    try {
      m = JSON.parse(line.toString('utf8'))
    } catch {
      this.onProblem(`收到不合协议的一行：${line.toString('utf8').slice(0, 200)}`)
      return
    }
    if (typeof m !== 'object' || m === null || typeof m.type !== 'string') {
      this.onProblem('收到缺少 type 的消息')
      return
    }
    if (m.type === 'result' && typeof m.id === 'string' && this.pending.has(m.id)) {
      const p = this.pending.get(m.id)!
      this.pending.delete(m.id)
      clearTimeout(p.timer)
      const r = (m.payload ?? {}) as Reply
      if (r.ok) p.resolve(r.value ?? null)
      else p.reject(new Error(r.error || '平台拒绝了请求'))
      return
    }
    this.onMessage(m)
  }

  /** 写一行；超过上限时抛错，调用方改发错误回应。 */
  send(m: Message, done?: () => void): void {
    const line = JSON.stringify(m)
    if (Buffer.byteLength(line) + 1 > MAX_LINE) throw new Error(`消息超过 ${MAX_LINE} 字节`)
    this.write(`${line}\n`, done)
  }

  /** 回应 Go 的请求；value 无法序列化或过大时改回错误。 */
  reply(req: Message, type: 'result' | 'ack', reply: Reply): void {
    const base = { id: req.id, type, plugin: req.plugin }
    try {
      this.send({ ...base, payload: reply })
    } catch (err) {
      this.send({ ...base, payload: { ok: false, error: `回应无法发送：${(err as Error).message}` } })
    }
  }

  /** 发出宿主→Go 的请求（state_*），等待 result。 */
  request(type: string, plugin: string, payload: unknown, timeoutMs = 30_000): Promise<unknown> {
    if (this.closed) return Promise.reject(new Error('宿主正在退出'))
    const id = `h${++this.next}`
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(new Error(`平台没有在 ${timeoutMs} 毫秒内回应 ${type}`))
      }, timeoutMs)
      this.pending.set(id, { resolve, reject, timer })
      try {
        this.send({ id, type, plugin, payload })
      } catch (err) {
        clearTimeout(timer)
        this.pending.delete(id)
        reject(err as Error)
      }
    })
  }

  close(reason: string): void {
    this.closed = true
    for (const [id, p] of this.pending) {
      clearTimeout(p.timer)
      p.reject(new Error(reason))
      this.pending.delete(id)
    }
  }
}
