// The stdio protocol between the host and the Go server (api/plugin-host-protocol.md): one JSON per line, {"id","type","plugin","payload"}.
// stdout carries only protocol lines; an incoming line over the limit is dropped whole.

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

  /** Starts reading; calls onEnd when the input ends. */
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
          this.onProblem(`Received a line longer than ${MAX_LINE} bytes; dropped it`)
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
      this.onProblem(`Received a line that does not follow the protocol: ${line.toString('utf8').slice(0, 200)}`)
      return
    }
    if (typeof m !== 'object' || m === null || typeof m.type !== 'string') {
      this.onProblem('Received a message without type')
      return
    }
    if (m.type === 'result' && typeof m.id === 'string' && this.pending.has(m.id)) {
      const p = this.pending.get(m.id)!
      this.pending.delete(m.id)
      clearTimeout(p.timer)
      const r = (m.payload ?? {}) as Reply
      if (r.ok) p.resolve(r.value ?? null)
      else p.reject(new Error(r.error || 'Platform rejected the request'))
      return
    }
    this.onMessage(m)
  }

  /** Writes one line; throws when it exceeds the limit, and the caller then sends an error reply instead. */
  send(m: Message, done?: () => void): void {
    const line = JSON.stringify(m)
    if (Buffer.byteLength(line) + 1 > MAX_LINE) throw new Error(`the message is longer than ${MAX_LINE} bytes`)
    this.write(`${line}\n`, done)
  }

  /** Replies to a request from Go; replies with an error instead when value cannot be serialized or is too large. */
  reply(req: Message, type: 'result' | 'ack', reply: Reply): void {
    const base = { id: req.id, type, plugin: req.plugin }
    try {
      this.send({ ...base, payload: reply })
    } catch (err) {
      this.send({ ...base, payload: { ok: false, error: `Could not send the reply: ${(err as Error).message}` } })
    }
  }

  /** Sends a host→Go request (state_*) and waits for its result. */
  request(type: string, plugin: string, payload: unknown, timeoutMs = 30_000): Promise<unknown> {
    if (this.closed) return Promise.reject(new Error('Host is shutting down'))
    const id = `h${++this.next}`
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(new Error(`Platform did not answer ${type} within ${timeoutMs} ms`))
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
