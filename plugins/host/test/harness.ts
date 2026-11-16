// The "Go side" for tests: starts the built host (dist/main.js, or the entry given by LOOP_NOW_PLUGIN_HOST_ENTRY), exchanges messages per
// api/plugin-host-protocol.md, and answers state_* in memory like Go does. It mirrors the fake-host tests in internal/server/pluginhost.

import { type ChildProcess, spawn } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { createInterface } from 'node:readline'
import { fileURLToPath } from 'node:url'

export const HOST_ENTRY = process.env.LOOP_NOW_PLUGIN_HOST_ENTRY || fileURLToPath(new URL('../dist/main.js', import.meta.url))

export interface Msg {
  id?: string
  type: string
  plugin?: string
  // Tests pick the fields they need.
  payload?: any
}

interface Waiter {
  pred: (m: Msg) => boolean
  resolve: (m: Msg) => void
}

export interface StartOptions {
  env?: Record<string, string>
}

export class HostHarness {
  readonly dir: string
  readonly messages: Msg[] = []
  readonly stderr: string[] = []
  /** Lines on stdout that are not JSON: should always be empty. */
  readonly badLines: string[] = []
  readonly state = new Map<string, unknown>()
  /** Answers for accounts_list, keyed by <plugin>/<projectId>/<kind>; projects not in enabledIn are answered as not enabled. */
  readonly accounts = new Map<string, unknown[]>()
  readonly enabledIn = new Set<string>()
  readonly exited: Promise<{ code: number | null; signal: NodeJS.Signals | null }>
  private readonly child: ChildProcess
  private readonly permissions = new Map<string, string[]>()
  private readonly kinds = new Map<string, string[]>()
  private readonly waiters: Waiter[] = []
  private readonly stderrWaiters: (() => void)[] = []
  private next = 0

  private constructor(options: StartOptions) {
    this.dir = mkdtempSync(path.join(tmpdir(), 'loop-now-host-'))
    this.child = spawn(process.execPath, [HOST_ENTRY], {
      cwd: this.dir,
      env: { PATH: process.env.PATH ?? '/usr/bin:/bin', HOME: this.dir, LANG: 'C.UTF-8', ...options.env },
      stdio: ['pipe', 'pipe', 'pipe'],
    })
    this.exited = new Promise((resolve) => this.child.once('exit', (code, signal) => resolve({ code, signal })))
    createInterface({ input: this.child.stdout! }).on('line', (line) => this.receive(line))
    createInterface({ input: this.child.stderr! }).on('line', (line) => {
      this.stderr.push(line)
      for (const check of [...this.stderrWaiters]) check()
    })
  }

  static async start(options: StartOptions = {}): Promise<HostHarness> {
    const h = new HostHarness(options)
    await h.waitFor((m) => m.type === 'ready', 15_000)
    return h
  }

  private receive(line: string): void {
    let m: Msg
    try {
      m = JSON.parse(line)
    } catch {
      this.badLines.push(line)
      return
    }
    this.messages.push(m)
    if (m.type.startsWith('state_') && m.id) this.answerState(m)
    if (m.type === 'accounts_list' && m.id) this.answerAccounts(m)
    for (const w of [...this.waiters]) {
      if (w.pred(m)) {
        this.waiters.splice(this.waiters.indexOf(w), 1)
        w.resolve(m)
      }
    }
  }

  private answerState(m: Msg): void {
    const plugin = m.plugin ?? ''
    const p = m.payload ?? {}
    const reply = (ok: boolean, value?: unknown, error?: string) =>
      this.send({ id: m.id, type: 'result', plugin, payload: ok ? { ok, ...(value === undefined ? {} : { value }) } : { ok, error } })
    if (!(this.permissions.get(plugin) ?? []).includes('state')) return reply(false, undefined, '清单没有声明 state 权限')
    const scope = `${plugin}\u0000${p.projectId ?? ''}\u0000`
    switch (m.type) {
      case 'state_get':
        return reply(true, this.state.has(scope + p.key) ? this.state.get(scope + p.key) : null)
      case 'state_set':
        this.state.set(scope + p.key, p.value)
        return reply(true)
      case 'state_delete':
        this.state.delete(scope + p.key)
        return reply(true)
      case 'state_list': {
        const out = [...this.state.entries()]
          .filter(([k]) => k.startsWith(scope + (p.prefix ?? '')))
          .map(([k, value]) => ({ key: k.slice(scope.length), value }))
          .sort((a, b) => (a.key < b.key ? -1 : 1))
          .slice(0, 1000)
        return reply(true, out)
      }
    }
  }

  // Same accounts_list rules as the server: permission, kind, project, enabled.
  private answerAccounts(m: Msg): void {
    const plugin = m.plugin ?? ''
    const p = m.payload ?? {}
    const reply = (ok: boolean, value?: unknown, error?: string) =>
      this.send({ id: m.id, type: 'result', plugin, payload: ok ? { ok, value } : { ok, error } })
    if (!(this.permissions.get(plugin) ?? []).includes('accounts.read')) return reply(false, undefined, '清单没有声明 accounts.read 权限')
    if (!(this.kinds.get(plugin) ?? []).includes(p.kind)) return reply(false, undefined, `清单没有声明账号种类 ${p.kind}`)
    if (typeof p.projectId !== 'string' || !/^[0-9a-f-]{36}$/.test(p.projectId)) return reply(false, undefined, 'projectId 无效')
    if (!this.enabledIn.has(`${plugin}/${p.projectId}`)) return reply(false, undefined, '插件没有在这个项目里启用')
    reply(true, this.accounts.get(`${plugin}/${p.projectId}/${p.kind}`) ?? [])
  }

  /** The plugin's global state (projectId empty) or one project's state. */
  stateOf(plugin: string, projectId: string | null = null): Map<string, unknown> {
    const scope = `${plugin}\u0000${projectId ?? ''}\u0000`
    return new Map([...this.state.entries()].filter(([k]) => k.startsWith(scope)).map(([k, v]) => [k.slice(scope.length), v]))
  }

  send(m: Msg): void {
    this.child.stdin!.write(`${JSON.stringify(m)}\n`)
  }

  /** Waits for a message that matches; one already received counts too. */
  waitFor(pred: (m: Msg) => boolean, timeoutMs = 10_000): Promise<Msg> {
    const found = this.messages.find(pred)
    if (found) return Promise.resolve(found)
    return new Promise((resolve, reject) => {
      const w: Waiter = {
        pred,
        resolve: (m) => {
          clearTimeout(timer)
          resolve(m)
        },
      }
      const timer = setTimeout(() => {
        this.waiters.splice(this.waiters.indexOf(w), 1)
        reject(new Error(`等待消息超时；已收到：${JSON.stringify(this.messages.slice(-5))}；stderr：${this.stderr.slice(-5).join(' | ')}`))
      }, timeoutMs)
      this.waiters.push(w)
    })
  }

  /**
   * Waits until stderr matches; lines already received count too, and the check runs again on every new line.
   * stderr and protocol messages travel through two pipes with no ordering guarantee, so waitFor cannot be used to wait for stderr.
   */
  waitStderr(pred: (lines: string[]) => boolean, timeoutMs = 10_000): Promise<void> {
    if (pred(this.stderr)) return Promise.resolve()
    return new Promise((resolve, reject) => {
      const check = () => {
        if (!pred(this.stderr)) return
        this.stderrWaiters.splice(this.stderrWaiters.indexOf(check), 1)
        clearTimeout(timer)
        resolve()
      }
      const timer = setTimeout(() => {
        this.stderrWaiters.splice(this.stderrWaiters.indexOf(check), 1)
        reject(new Error(`等待标准错误超时；已收到：${this.stderr.slice(-5).join(' | ')}`))
      }, timeoutMs)
      this.stderrWaiters.push(check)
    })
  }

  /** Sends a Go→host request and waits for its result or ack. */
  async request(type: string, plugin: string | undefined, payload: unknown, timeoutMs = 10_000): Promise<Msg> {
    const id = `g${++this.next}`
    const reply = this.waitFor((m) => m.id === id && (m.type === 'result' || m.type === 'ack'), timeoutMs)
    this.send({ id, type, plugin, payload })
    return reply
  }

  async load(payload: LoadPayload): Promise<Msg> {
    this.permissions.set(payload.id, payload.manifest.permissions)
    this.kinds.set(payload.id, payload.manifest.accounts.map((a) => (a as { kind: string }).kind))
    return this.request('load', payload.id, payload, 30_000)
  }

  event(plugin: string, event: Record<string, unknown>, options: { attempt?: number; timeoutMs?: number } = {}): Promise<Msg> {
    return this.request('event', plugin, { event: makeEvent(event), attempt: options.attempt ?? 1, timeoutMs: options.timeoutMs ?? 5_000 })
  }

  call(plugin: string, kind: string, event: Record<string, unknown>, timeoutMs = 5_000): Promise<Msg> {
    return this.request('call', plugin, { kind, event: makeEvent(event), timeoutMs })
  }

  logs(plugin?: string): { level: string; message: string }[] {
    return this.messages.filter((m) => m.type === 'log' && (plugin === undefined || m.plugin === plugin)).map((m) => m.payload)
  }

  async waitLog(plugin: string, pattern: RegExp, timeoutMs = 10_000): Promise<{ level: string; message: string }> {
    const m = await this.waitFor((x) => x.type === 'log' && x.plugin === plugin && pattern.test(String(x.payload?.message)), timeoutMs)
    return m.payload
  }

  async shutdown(): Promise<{ code: number | null; signal: NodeJS.Signals | null }> {
    if (this.child.exitCode === null && this.child.signalCode === null) this.send({ type: 'shutdown' })
    const timer = setTimeout(() => this.child.kill('SIGKILL'), 8_000)
    const result = await this.exited
    clearTimeout(timer)
    return result
  }

  async close(): Promise<void> {
    if (this.child.exitCode === null && this.child.signalCode === null) {
      this.child.kill('SIGKILL')
      await this.exited
    }
    rmSync(this.dir, { recursive: true, force: true })
  }
}

let eventSeq = 0

/** Fills in the event's fixed fields. */
export function makeEvent(fields: Record<string, unknown>): Record<string, unknown> {
  return {
    id: `01JTEST${String(++eventSeq).padStart(19, '0')}`,
    at: new Date().toISOString(),
    projectId: null,
    agentId: null,
    taskId: null,
    runId: null,
    ...fields,
  }
}

export interface LoadPayload {
  id: string
  version: string
  root: string
  entry: string
  dataDir: string
  manifest: {
    events: string[]
    permissions: string[]
    failClosed: boolean
    configSchema: Record<string, unknown>
    accounts: unknown[]
    actions: { id: string }[]
    channels: { id: string }[]
    schedules: { id: string }[]
    http: boolean
  }
  config: { values: Record<string, unknown>; secrets: Record<string, string>; projects: Record<string, { values: Record<string, unknown>; secrets: Record<string, string> }> }
  token: string
  apiUrl: string
}

export interface PayloadOptions {
  events?: string[]
  permissions?: string[]
  failClosed?: boolean
  config?: Partial<LoadPayload['config']>
  token?: string
  apiUrl?: string
  version?: string
  manifest?: Partial<LoadPayload['manifest']>
}

export function loadPayload(id: string, root: string, entry: string, o: PayloadOptions = {}): LoadPayload {
  return {
    id,
    version: o.version ?? '1.0.0',
    root,
    entry,
    dataDir: path.join(root, '..', `${id}-data`),
    manifest: {
      events: o.events ?? [],
      permissions: o.permissions ?? [],
      failClosed: o.failClosed ?? false,
      configSchema: {},
      accounts: [],
      actions: [],
      channels: [],
      schedules: [],
      http: false,
      ...o.manifest,
    },
    config: { values: {}, secrets: {}, projects: {}, ...o.config },
    token: o.token ?? 'lnp_test_token_value',
    apiUrl: o.apiUrl ?? 'http://127.0.0.1:9/api/v1',
  }
}

/** Writes a platform-side-only plugin into a temp directory and returns the load payload. */
export class Fixtures {
  readonly dir = mkdtempSync(path.join(tmpdir(), 'loop-now-fixtures-'))

  write(id: string, files: Record<string, string>, options: PayloadOptions = {}): LoadPayload {
    const root = path.join(this.dir, id)
    for (const [name, content] of Object.entries(files)) {
      mkdirSync(path.dirname(path.join(root, name)), { recursive: true })
      writeFileSync(path.join(root, name), content)
    }
    return loadPayload(id, root, path.join(root, 'index.ts'), options)
  }

  cleanup(): void {
    rmSync(this.dir, { recursive: true, force: true })
  }
}

export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
