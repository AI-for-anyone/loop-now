// Plugin host: loads platform sides on Go's load, dispatches observe events (answered with ack) and synchronous calls (answered
// with result), reads and writes state_* through Go, and attributes plugin logs and output to the plugin.
// Any plugin error is only answered as {ok:false}; it never makes the host exit.

import path from 'node:path'

import { importEntry, pickRegister } from './loader.ts'
import { installOutput, type Level, type Output, runAs } from './output.ts'
import { Channel, type Message, PROTOCOL_VERSION, type Reply } from './protocol.ts'
import { briefError, type CallKind, describeError, parseConfig, parseLoad, PluginRuntime, TimeoutError } from './runtime.ts'

declare const __HOST_VERSION__: string

export const HOST_VERSION = typeof __HOST_VERSION__ === 'string' ? __HOST_VERSION__ : '0.0.0-dev'

function envMs(name: string, fallback: number): number {
  const v = Number(process.env[name])
  return Number.isFinite(v) && v > 0 ? v : fallback
}

// Go waits 30 seconds for load; the host gives up on its own before that, cleans up the plugin and then replies with a failure,
// so that Go never considers the load failed while the plugin is still in the host.
const LOAD_TIMEOUT = envMs('LOOP_NOW_PLUGIN_HOST_LOAD_TIMEOUT_MS', 25_000)
const STOP_TIMEOUT = envMs('LOOP_NOW_PLUGIN_HOST_STOP_TIMEOUT_MS', 10_000)
const SHUTDOWN_TIMEOUT = 4_000
const EVENT_TIMEOUT = 60_000
const CALL_TIMEOUT = 15_000
const CONFIG_TIMEOUT = 10_000
const ACTION_TIMEOUT = 30_000
const HTTP_TIMEOUT = 30_000
const SCHEDULE_TIMEOUT = 10 * 60_000
const MASK_MIN = 4

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function timeoutOf(v: unknown, fallback: number): number {
  return typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : fallback
}

async function within<T>(ms: number, what: string, work: Promise<T>): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new TimeoutError(`${what} timed out after ${ms} ms`)), Math.max(ms, 0))
  })
  work.catch(() => undefined)
  try {
    return await Promise.race([work, timeout])
  } finally {
    clearTimeout(timer)
  }
}

export class Host {
  private readonly output: Output
  private readonly channel: Channel
  /** Ready plugins, in load order. */
  private readonly plugins = new Map<string, PluginRuntime>()
  /** Plugins that are loading or ready, used to attribute and mask logs. */
  private readonly runtimes = new Map<string, PluginRuntime>()
  private readonly ops = new Map<string, Promise<unknown>>()
  private shuttingDown = false

  constructor() {
    this.output = installOutput({
      log: (plugin, level, message) => this.pluginLog(plugin, level, message),
      mask: (plugin, text) => this.mask(plugin, text),
    })
    this.channel = new Channel(
      (line, done) => this.output.protocol(line, done),
      (m) => this.handle(m),
      (problem) => this.output.hostError(problem),
    )
    process.on('uncaughtException', (err) => this.unhandled(err, 'Uncaught exception'))
    process.on('unhandledRejection', (err) => this.unhandled(err, 'Unhandled promise rejection'))
  }

  start(): void {
    this.channel.listen(process.stdin, () => void this.shutdown())
    this.channel.send({ type: 'ready', payload: { protocol: PROTOCOL_VERSION, hostVersion: HOST_VERSION } })
  }

  private pluginLog(plugin: string, level: Level, message: string): void {
    try {
      this.channel.send({ type: 'log', plugin, payload: { level, message: this.mask(plugin, message) } })
    } catch (err) {
      this.output.hostError(`[${plugin}] failed to send log: ${describeError(err)}`)
    }
  }

  private mask(plugin: string, text: string): string {
    const rt = this.runtimes.get(plugin)
    if (!rt) return text
    let out = text
    for (const secret of rt.secretValues().sort((a, b) => b.length - a.length)) {
      if (secret.length >= MASK_MIN) out = out.split(secret).join('***')
    }
    return out
  }

  // Errors a plugin throws from timers and the like: attributed to the plugin by its async context or by the plugin directory in the call stack;
  // the host keeps running.
  private unhandled(err: unknown, what: string): void {
    const stack = err instanceof Error && err.stack ? err.stack : describeError(err)
    let owner: string | undefined
    for (const rt of this.runtimes.values()) {
      if (stack.includes(rt.payload.root + path.sep)) owner = rt.id
    }
    const rt = owner ? this.runtimes.get(owner) : undefined
    if (rt) this.pluginLog(rt.id, 'error', `${what}: ${briefError(err, rt.payload.root)}`)
    else this.output.hostError(`${what}: ${stack}`)
  }

  // load, unload and config_changed for the same plugin are handled one after another.
  private serialize(plugin: string, fn: () => Promise<void>): void {
    const prev = this.ops.get(plugin) ?? Promise.resolve()
    const next = prev.then(fn, fn).catch((err) => this.output.hostError(`Error handling a request for plugin ${plugin}: ${describeError(err)}`))
    this.ops.set(plugin, next)
    void next.finally(() => {
      if (this.ops.get(plugin) === next) this.ops.delete(plugin)
    })
  }

  private handle(m: Message): void {
    if (this.shuttingDown && m.type !== 'shutdown') {
      if (m.id) this.channel.reply(m, m.type === 'event' ? 'ack' : 'result', { ok: false, error: 'Host is shutting down' })
      return
    }
    const plugin = typeof m.plugin === 'string' ? m.plugin : ''
    switch (m.type) {
      case 'load':
        this.serialize(plugin, () => this.load(m))
        return
      case 'unload':
        this.serialize(plugin, () => this.unload(m))
        return
      case 'config_changed':
        this.serialize(plugin, () => this.configChanged(m))
        return
      case 'event':
        void this.event(m)
        return
      case 'call':
        void this.call(m)
        return
      case 'action':
      case 'http':
      case 'schedule':
        void this.request(m)
        return
      case 'shutdown':
        void this.shutdown()
        return
      default:
        if (m.id) this.channel.reply(m, 'result', { ok: false, error: `不支持的请求 ${m.type}` })
    }
  }

  private async load(m: Message): Promise<void> {
    let payload
    try {
      payload = parseLoad(m.plugin, m.payload)
    } catch (err) {
      this.channel.reply(m, 'result', { ok: false, error: describeError(err) })
      return
    }
    const id = payload.id
    const previous = this.runtimes.get(id)
    if (previous) {
      this.pluginLog(id, 'warn', 'Received load while the plugin is still in the host; unloading the old one first')
      await this.stopRuntime(previous)
    }
    const rt = new PluginRuntime(payload, {
      request: (type, plugin, p) => this.channel.request(type, plugin, p),
      log: (plugin, level, message) => this.pluginLog(plugin, level, message),
    })
    this.runtimes.set(id, rt)
    const deadline = Date.now() + LOAD_TIMEOUT
    const left = () => deadline - Date.now()
    const entry = path.relative(payload.root, payload.entry) || payload.entry
    let stage = `load ${entry}`
    try {
      const mod = await within(left(), stage, runAs(id, () => importEntry(payload.entry)))
      const register = pickRegister(mod)
      if (!register) throw new Error(`${entry} does not export a default register function`)
      stage = 'register'
      await within(left(), stage, runAs(id, async () => register(rt.api)))
      if (rt.violations.length > 0) throw new Error(rt.violations.join('; '))
      stage = 'lifecycle start'
      await within(left(), stage, rt.start())
      rt.status = 'active'
      this.plugins.set(id, rt)
      this.channel.reply(m, 'result', { ok: true })
    } catch (err) {
      const message = err instanceof TimeoutError || rt.violations.length > 0 ? describeError(err) : `${stage} failed: ${describeError(err)}`
      if (!(err instanceof TimeoutError) && rt.violations.length === 0) {
        this.pluginLog(id, 'error', `${stage} failed: ${briefError(err, payload.root)}`)
      }
      await this.stopRuntime(rt)
      this.channel.reply(m, 'result', { ok: false, error: message })
    }
  }

  private async stopRuntime(rt: PluginRuntime): Promise<void> {
    if (this.plugins.get(rt.id) === rt) this.plugins.delete(rt.id)
    await rt.stop(STOP_TIMEOUT)
    this.output.flush(rt.id)
    if (this.runtimes.get(rt.id) === rt) this.runtimes.delete(rt.id)
  }

  private async unload(m: Message): Promise<void> {
    const rt = this.runtimes.get(m.plugin ?? '')
    if (rt) await this.stopRuntime(rt)
    this.channel.reply(m, 'result', { ok: true })
  }

  private async configChanged(m: Message): Promise<void> {
    const rt = this.plugins.get(m.plugin ?? '')
    if (!rt) {
      this.channel.reply(m, 'result', { ok: false, error: '插件未加载' })
      return
    }
    if (!isObject(m.payload) || !isObject(m.payload.config)) {
      this.channel.reply(m, 'result', { ok: false, error: 'config_changed payload is missing config' })
      return
    }
    await rt.configChanged(parseConfig(m.payload.config), CONFIG_TIMEOUT)
    this.channel.reply(m, 'result', { ok: true })
  }

  private eventOf(payload: unknown): Record<string, unknown> | string {
    if (!isObject(payload) || !isObject(payload.event)) return 'Payload is missing event'
    const event = payload.event
    if (typeof event.type !== 'string' || typeof event.id !== 'string') return 'event is missing id or type'
    return event
  }

  private async event(m: Message): Promise<void> {
    const rt = this.plugins.get(m.plugin ?? '')
    let reply: Reply
    const event = this.eventOf(m.payload)
    if (!rt) reply = { ok: false, error: '插件未加载' }
    else if (typeof event === 'string') reply = { ok: false, error: event }
    else {
      const p = m.payload as Record<string, unknown>
      const attempt = typeof p.attempt === 'number' && p.attempt > 0 ? p.attempt : 1
      reply = await rt.event(event, attempt, timeoutOf(p.timeoutMs, EVENT_TIMEOUT))
    }
    this.channel.reply(m, 'ack', reply)
  }

  private async call(m: Message): Promise<void> {
    const rt = this.plugins.get(m.plugin ?? '')
    let reply: Reply
    const event = this.eventOf(m.payload)
    const kind = isObject(m.payload) ? m.payload.kind : undefined
    if (!rt) reply = { ok: false, error: '插件未加载' }
    else if (typeof event === 'string') reply = { ok: false, error: event }
    else if (kind !== 'intercept' && kind !== 'modify' && kind !== 'claim') reply = { ok: false, error: `Unknown call kind ${String(kind)}` }
    else reply = await rt.call(kind as CallKind, event, timeoutOf((m.payload as Record<string, unknown>).timeoutMs, CALL_TIMEOUT))
    this.channel.reply(m, 'result', reply)
  }

  // action, http, schedule: handed to the handler the plugin registered; answered with result.
  private async request(m: Message): Promise<void> {
    const rt = this.plugins.get(m.plugin ?? '')
    const p = isObject(m.payload) ? m.payload : {}
    let reply: Reply
    if (!rt) reply = { ok: false, error: '插件未加载' }
    else if (m.type === 'action') reply = await rt.action(p, timeoutOf(p.timeoutMs, ACTION_TIMEOUT))
    else if (m.type === 'http') reply = await rt.http(p, timeoutOf(p.timeoutMs, HTTP_TIMEOUT))
    else reply = await rt.schedule(p, timeoutOf(p.timeoutMs, SCHEDULE_TIMEOUT))
    this.channel.reply(m, 'result', reply)
  }

  /** Unloads all plugins in parallel (waiting at most 4 seconds), then exits; Go kills the process group 5 seconds after shutdown. */
  async shutdown(): Promise<void> {
    if (this.shuttingDown) return
    this.shuttingDown = true
    const all = [...this.runtimes.values()]
    await within(SHUTDOWN_TIMEOUT, 'Unloading all plugins', Promise.all(all.map((rt) => this.stopRuntime(rt)))).catch((err) =>
      this.output.hostError(describeError(err)),
    )
    this.channel.close('Host is shutting down')
    this.output.flush()
    this.output.protocol('', () => process.exit(0))
  }
}
