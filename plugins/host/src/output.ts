// 插件输出的归属：插件代码（加载、register、生命周期、处理函数）在 AsyncLocalStorage 里带着自己的 id 运行，
// 从中派生的定时器与 Promise 也带着它。console.* 变成该插件的 log 消息；写到标准输出或标准错误的内容逐行加 [<id>] 前缀
// 写到标准错误。标准输出只留给协议。宿主自身的输出不加前缀。

import { AsyncLocalStorage } from 'node:async_hooks'
import { format } from 'node:util'

export type Level = 'debug' | 'info' | 'warn' | 'error'

const context = new AsyncLocalStorage<string>()

export function runAs<T>(plugin: string, fn: () => T): T {
  return context.run(plugin, fn)
}

export function currentPlugin(): string | undefined {
  return context.getStore()
}

export interface OutputHooks {
  /** 插件的 console.* 调用。 */
  log(plugin: string, level: Level, message: string): void
  /** 遮住插件的 secret 值。 */
  mask(plugin: string, text: string): string
}

export interface Output {
  /** 写协议行（原始标准输出）。 */
  protocol(line: string, done?: () => void): void
  /** 宿主自身写标准错误。 */
  hostError(text: string): void
  /** 把插件没写完的半行补上换行写出。 */
  flush(plugin?: string): void
}

type WriteCallback = (err?: Error | null) => void

function toText(chunk: unknown, encoding?: unknown): string {
  if (typeof chunk === 'string') return chunk
  if (chunk instanceof Uint8Array) return Buffer.from(chunk).toString(typeof encoding === 'string' ? (encoding as BufferEncoding) : 'utf8')
  return String(chunk)
}

export function installOutput(hooks: OutputHooks): Output {
  const rawOut = process.stdout.write.bind(process.stdout) as (s: string, cb?: WriteCallback) => boolean
  const rawErr = process.stderr.write.bind(process.stderr) as (s: string, cb?: WriteCallback) => boolean
  const partial = new Map<string, string>()

  const pluginWrite = (plugin: string, text: string) => {
    const lines = ((partial.get(plugin) ?? '') + text).split('\n')
    const rest = lines.pop() ?? ''
    if (rest === '') partial.delete(plugin)
    else partial.set(plugin, rest)
    for (const line of lines) rawErr(`[${plugin}] ${hooks.mask(plugin, line)}\n`)
  }

  const redirect = (fallback: (s: string) => boolean) =>
    function write(chunk: unknown, encoding?: unknown, cb?: unknown): boolean {
      const done = (typeof encoding === 'function' ? encoding : cb) as WriteCallback | undefined
      const text = toText(chunk, typeof encoding === 'function' ? undefined : encoding)
      const plugin = currentPlugin()
      if (plugin) pluginWrite(plugin, text)
      else fallback(text)
      if (done) queueMicrotask(() => done(null))
      return true
    }

  // 任何人写标准输出（包括 console.dir、console.table）都改到标准错误，协议行只经 rawOut。
  process.stdout.write = redirect(rawErr) as typeof process.stdout.write
  process.stderr.write = redirect(rawErr) as typeof process.stderr.write

  const levels: [keyof Console, Level][] = [
    ['log', 'info'],
    ['info', 'info'],
    ['debug', 'debug'],
    ['warn', 'warn'],
    ['error', 'error'],
  ]
  for (const [method, level] of levels) {
    const original = (console[method] as (...args: unknown[]) => void).bind(console)
    ;(console as unknown as Record<string, unknown>)[method] = (...args: unknown[]) => {
      const plugin = currentPlugin()
      if (plugin) hooks.log(plugin, level, format(...args))
      else original(...args)
    }
  }

  return {
    protocol: (line, done) => {
      rawOut(line, done ? () => done() : undefined)
    },
    hostError: (text) => {
      rawErr(text.endsWith('\n') ? text : `${text}\n`)
    },
    flush: (plugin) => {
      for (const [id, rest] of partial) {
        if (plugin !== undefined && id !== plugin) continue
        partial.delete(id)
        rawErr(`[${id}] ${hooks.mask(id, rest)}\n`)
      }
    },
  }
}
