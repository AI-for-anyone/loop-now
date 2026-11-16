// Attributing plugin output: plugin code (loading, register, lifecycle, handlers) runs in AsyncLocalStorage carrying its own id,
// and timers and Promises derived from it carry it too. console.* becomes a log message of that plugin; anything written to stdout
// or stderr goes to stderr with an [<id>] prefix on each line.
// stdout is reserved for the protocol. The host's own output gets no prefix.

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
  /** A plugin's console.* calls. */
  log(plugin: string, level: Level, message: string): void
  /** Masks the plugin's secret values. */
  mask(plugin: string, text: string): string
}

export interface Output {
  /** Writes a protocol line (raw stdout). */
  protocol(line: string, done?: () => void): void
  /** The host's own writes to stderr. */
  hostError(text: string): void
  /** Writes out a plugin's unfinished partial line, adding the newline. */
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

  // Anything anyone writes to stdout (including console.dir and console.table) goes to stderr; protocol lines go only through rawOut.
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
