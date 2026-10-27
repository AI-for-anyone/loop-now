// 插件宿主：按 Go 的 load 加载平台半边，分发观察类事件（回 ack）与同步调用（回 result），state_* 经 Go 读写，
// 插件的日志与输出归到插件。任何插件的错误都只回成 {ok:false}，不让宿主退出。

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

// Go 等 load 30 秒；宿主在那之前自己放弃，把插件清理干净再回失败，免得 Go 已判失败而插件仍在宿主里。
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
    timer = setTimeout(() => reject(new TimeoutError(`${what}超过 ${ms} 毫秒`)), Math.max(ms, 0))
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
  /** 已就绪的插件，按加载顺序。 */
  private readonly plugins = new Map<string, PluginRuntime>()
  /** 加载中与已就绪的插件，用于日志归属与遮蔽。 */
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
    process.on('uncaughtException', (err) => this.unhandled(err, '未捕获的异常'))
    process.on('unhandledRejection', (err) => this.unhandled(err, '未处理的 Promise 拒绝'))
  }

  start(): void {
    this.channel.listen(process.stdin, () => void this.shutdown())
    this.channel.send({ type: 'ready', payload: { protocol: PROTOCOL_VERSION, hostVersion: HOST_VERSION } })
  }

  private pluginLog(plugin: string, level: Level, message: string): void {
    try {
      this.channel.send({ type: 'log', plugin, payload: { level, message: this.mask(plugin, message) } })
    } catch (err) {
      this.output.hostError(`[${plugin}] 日志无法发送：${describeError(err)}`)
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

  // 插件在定时器等处抛出的错误：按异步上下文或调用栈里的插件目录归到插件，宿主继续运行。
  private unhandled(err: unknown, what: string): void {
    const stack = err instanceof Error && err.stack ? err.stack : describeError(err)
    let owner: string | undefined
    for (const rt of this.runtimes.values()) {
      if (stack.includes(rt.payload.root + path.sep)) owner = rt.id
    }
    const rt = owner ? this.runtimes.get(owner) : undefined
    if (rt) this.pluginLog(rt.id, 'error', `${what}：${briefError(err, rt.payload.root)}`)
    else this.output.hostError(`${what}：${stack}`)
  }

  // 同一插件的 load、unload、config_changed 依次处理。
  private serialize(plugin: string, fn: () => Promise<void>): void {
    const prev = this.ops.get(plugin) ?? Promise.resolve()
    const next = prev.then(fn, fn).catch((err) => this.output.hostError(`处理插件 ${plugin} 的请求出错：${describeError(err)}`))
    this.ops.set(plugin, next)
    void next.finally(() => {
      if (this.ops.get(plugin) === next) this.ops.delete(plugin)
    })
  }

  private handle(m: Message): void {
    if (this.shuttingDown && m.type !== 'shutdown') {
      if (m.id) this.channel.reply(m, m.type === 'event' ? 'ack' : 'result', { ok: false, error: '宿主正在退出' })
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
      this.pluginLog(id, 'warn', '收到 load 时插件仍在宿主里，先卸载旧的')
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
    let stage = `加载 ${entry}`
    try {
      const mod = await within(left(), `${stage} `, runAs(id, () => importEntry(payload.entry)))
      const register = pickRegister(mod)
      if (!register) throw new Error(`${entry} 没有默认导出 register 函数`)
      stage = 'register'
      await within(left(), `${stage} `, runAs(id, async () => register(rt.api)))
      if (rt.violations.length > 0) throw new Error(rt.violations.join('；'))
      stage = 'lifecycle start'
      await within(left(), `${stage} `, rt.start())
      rt.status = 'active'
      this.plugins.set(id, rt)
      this.channel.reply(m, 'result', { ok: true })
    } catch (err) {
      const message = err instanceof TimeoutError || rt.violations.length > 0 ? describeError(err) : `${stage} 失败：${describeError(err)}`
      if (!(err instanceof TimeoutError) && rt.violations.length === 0) {
        this.pluginLog(id, 'error', `${stage} 失败：${briefError(err, payload.root)}`)
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
      this.channel.reply(m, 'result', { ok: false, error: 'config_changed 载荷缺少 config' })
      return
    }
    await rt.configChanged(parseConfig(m.payload.config), CONFIG_TIMEOUT)
    this.channel.reply(m, 'result', { ok: true })
  }

  private eventOf(payload: unknown): Record<string, unknown> | string {
    if (!isObject(payload) || !isObject(payload.event)) return '载荷缺少 event'
    const event = payload.event
    if (typeof event.type !== 'string' || typeof event.id !== 'string') return 'event 缺少 id 或 type'
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
    else if (kind !== 'intercept' && kind !== 'modify' && kind !== 'claim') reply = { ok: false, error: `未知的调用种类 ${String(kind)}` }
    else reply = await rt.call(kind as CallKind, event, timeoutOf((m.payload as Record<string, unknown>).timeoutMs, CALL_TIMEOUT))
    this.channel.reply(m, 'result', reply)
  }

  // action、http、schedule：交给插件注册的处理函数，回 result。
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

  /** 并行卸载全部插件（最多等 4 秒）后退出；Go 在 shutdown 5 秒后会结束进程组。 */
  async shutdown(): Promise<void> {
    if (this.shuttingDown) return
    this.shuttingDown = true
    const all = [...this.runtimes.values()]
    await within(SHUTDOWN_TIMEOUT, '卸载全部插件', Promise.all(all.map((rt) => this.stopRuntime(rt)))).catch((err) =>
      this.output.hostError(describeError(err)),
    )
    this.channel.close('宿主正在退出')
    this.output.flush()
    this.output.protocol('', () => process.exit(0))
  }
}
