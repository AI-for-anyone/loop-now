import { spawn, type ChildProcess } from 'node:child_process'
import { EventEmitter } from 'node:events'
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'
import type { RotatingLog } from './log'
import { current, type PlatformId } from './platform'
import { isOurServer, processInfo, terminateChild, terminateStale, type Stamp } from './process'
import { RestartPolicy } from './restart'

// stopped：没有进程；starting：已拉起、等就绪；running／degraded：就绪，健康接口 200／503；
// restarting：崩溃后等待重启；failed：就绪前退出或超时；crashed：崩溃次数超限，不再重启；stopping：正在停。
export type ServerState = 'stopped' | 'starting' | 'running' | 'degraded' | 'restarting' | 'failed' | 'crashed' | 'stopping'

// 启动失败的原因，由界面换成文字。服务端的退出码：2 配置错误，3 数据目录或工作根被另一个实例占用，4 监听地址被占用。
export type StartFailure =
  | { kind: 'spawn'; message: string }
  | { kind: 'config' }
  | { kind: 'locked' }
  | { kind: 'exited'; code: number | null; signal: NodeJS.Signals | null }
  | { kind: 'timeout' }

const exitConfig = 2
const exitLocked = 3
const exitAddressInUse = 4

export type ServerOptions = {
  bin: string
  readyFile: string
  // 壳记下的服务端 pid 与启动时刻（就绪后写），下次启动清理孤儿时和就绪文件里的 pid 核对，免得 pid 被复用时认错。
  stampFile: string
  env: NodeJS.ProcessEnv
  // 服务端的 stdout、stderr 原样写进这里。
  log: RotatingLog
  // 壳自己的一行记录。
  note: (text: string) => void
  // 这次监听的端口；0 表示交给系统选。
  choosePort: () => Promise<number>
  platform?: PlatformId
}

export const readyTimeoutMs = 90_000
// 服务端 LOOP_NOW_SHUTDOWN_TIMEOUT 默认 15 秒，再留 5 秒。
const stopGraceMs = 20_000
const healthIntervalMs = 10_000

type Ready = { url: string; pid: number; version: string }
type Exit = { code: number | null; signal: NodeJS.Signals | null }
type Run = { child: ChildProcess; port: number; exited: Promise<Exit>; spawnError?: string }

// 拉起并守护 loop-server：等就绪文件与健康接口，就绪后定时查健康，崩溃按 RestartPolicy 重启。
export class ServerProcess extends EventEmitter<{ state: [ServerState]; ready: [url: string] }> {
  state: ServerState = 'stopped'
  url: string | undefined
  failure: StartFailure | undefined
  private run: Run | undefined
  private readonly stopRequested = new WeakSet<ChildProcess>()
  private readonly policy = new RestartPolicy()
  private restartTimer: NodeJS.Timeout | undefined
  private healthTimer: NodeJS.Timeout | undefined

  private readonly platform: PlatformId

  constructor(private readonly o: ServerOptions) {
    super()
    this.platform = o.platform ?? current
  }

  // 有没有服务端进程（包括还没就绪的）。
  get alive(): boolean {
    return this.run !== undefined
  }

  async start(): Promise<void> {
    if (this.run || this.state === 'starting' || this.state === 'stopping') return
    clearTimeout(this.restartTimer)
    this.failure = undefined
    this.setState('starting')
    await this.clearStale()
    const port = await this.o.choosePort()
    if ((this.state as ServerState) !== 'starting') return // 等待期间被停掉了
    this.launch(port)
  }

  private launch(port: number): void {
    this.o.log.resetTail()
    this.o.note(`启动服务端 ${this.o.bin}，端口 ${port === 0 ? '由系统选择' : port}`)
    rmSync(this.o.readyFile, { force: true })
    const child = spawn(this.o.bin, [], {
      cwd: dirname(this.o.bin),
      env: {
        ...this.o.env,
        LOOP_NOW_LISTEN_ADDR: `127.0.0.1:${port}`,
        LOOP_NOW_READY_FILE: this.o.readyFile,
        LOOP_NOW_CONSOLE_ORIGINS: 'none',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
      // 自成进程组：终端里按 Ctrl+C 不会直接打到服务端，由壳按顺序停它。Windows 上 detached 会给控制台程序开一个窗口，不用；
      // windowsHide 不让服务端出现控制台窗口。
      detached: this.platform !== 'win32',
      windowsHide: true,
    })
    const run: Run = { child, port, exited: undefined as unknown as Promise<Exit> }
    run.exited = new Promise<Exit>((resolve) => {
      child.once('exit', (code, signal) => resolve({ code, signal }))
      child.on('error', (err) => {
        if (child.pid !== undefined) return this.o.note(`服务端进程出错：${err.message}`)
        run.spawnError = err.message
        resolve({ code: null, signal: null })
      })
    })
    child.stdout?.on('data', (chunk: Buffer) => this.o.log.write(chunk))
    child.stderr?.on('data', (chunk: Buffer) => this.o.log.write(chunk))
    this.run = run
    void this.supervise(run)
  }

  async stop(): Promise<void> {
    clearTimeout(this.restartTimer)
    clearInterval(this.healthTimer)
    const run = this.run
    if (run) {
      this.stopRequested.add(run.child)
      this.setState('stopping')
      await this.terminate(run)
      this.run = undefined
    }
    this.url = undefined
    this.setState('stopped')
  }

  // 人手动重启：崩溃计数清零。
  async restart(): Promise<void> {
    this.policy.reset()
    await this.stop()
    await this.start()
  }

  private async supervise(run: Run): Promise<void> {
    const ready = await this.waitReady(run)
    if (ready === 'timeout') {
      this.o.note(`服务端 ${readyTimeoutMs / 1000} 秒内没有就绪，停掉它`)
      await this.terminate(run)
    }
    if (ready === 'exited' || ready === 'timeout') {
      const exit = await run.exited
      this.run = undefined
      if (this.stopRequested.has(run.child)) return
      if (ready === 'timeout') return this.fail({ kind: 'timeout' })
      if (run.spawnError !== undefined) return this.fail({ kind: 'spawn', message: run.spawnError })
      // 试探之后、服务端监听之前端口被别人拿走：改由系统选一次。
      if (exit.code === exitAddressInUse && run.port !== 0) {
        this.o.note(`端口 ${run.port} 已被占用，改由系统选择`)
        return this.launch(0)
      }
      if (exit.code === exitConfig) return this.fail({ kind: 'config' })
      if (exit.code === exitLocked) return this.fail({ kind: 'locked' })
      return this.fail({ kind: 'exited', ...exit })
    }

    this.url = ready.url
    this.o.note(`服务端已就绪：${ready.url}（${ready.version}，pid ${ready.pid}）`)
    await this.writeStamp(ready.pid)
    this.setState(ready.healthy ? 'running' : 'degraded')
    this.emit('ready', ready.url)
    this.healthTimer = setInterval(() => void this.checkHealth(), healthIntervalMs)

    const exit = await run.exited
    clearInterval(this.healthTimer)
    this.run = undefined
    if (this.stopRequested.has(run.child)) return
    this.o.note(`服务端意外退出：${describeExit(exit)}`)
    const delay = this.policy.next(Date.now())
    if (delay === null) {
      this.o.note('5 分钟内崩溃超过 3 次，不再自动重启')
      this.setState('crashed')
      return
    }
    this.setState('restarting')
    this.restartTimer = setTimeout(() => void this.start(), delay)
  }

  // 等就绪文件里出现这个进程的 pid，并且健康接口有回答（200 可用，503 库不可用，都算就绪）。
  private async waitReady(run: Run): Promise<(Ready & { healthy: boolean }) | 'exited' | 'timeout'> {
    let exited = false
    void run.exited.then(() => (exited = true))
    const deadline = Date.now() + readyTimeoutMs
    while (Date.now() < deadline) {
      if (exited) return 'exited'
      const ready = readReady(this.o.readyFile)
      if (ready && ready.pid === run.child.pid) {
        const healthy = await probeHealth(ready.url)
        if (healthy !== undefined) return { ...ready, healthy }
      }
      await sleep(200)
    }
    return exited ? 'exited' : 'timeout'
  }

  private async checkHealth(): Promise<void> {
    if (!this.url || (this.state !== 'running' && this.state !== 'degraded')) return
    const healthy = await probeHealth(this.url)
    if (healthy !== undefined && (this.state === 'running' || this.state === 'degraded')) this.setState(healthy ? 'running' : 'degraded')
  }

  // 先让它自己收尾，超时再强制结束（平台差异见 process.ts；Windows 上只能强制）。
  private async terminate(run: Run): Promise<void> {
    const { child } = run
    if (child.pid === undefined || child.exitCode !== null || child.signalCode !== null) return
    if (this.platform === 'win32') this.o.note('Windows 上服务端还没有优雅停止方式，taskkill 结束进程树')
    const inTime = await terminateChild(child, run.exited, stopGraceMs, this.platform)
    if (!inTime && this.platform !== 'win32') this.o.note(`服务端 ${stopGraceMs / 1000} 秒内没有退出，强制结束`)
  }

  // 壳上次异常退出时留下的服务端会占着数据目录：就绪文件里的 pid 还活着、正是这个可执行文件、启动时刻也对得上壳记下的，就先停掉它。
  private async clearStale(): Promise<void> {
    const ready = readReady(this.o.readyFile)
    if (ready && isOurServer(await processInfo(ready.pid, this.platform), this.o.bin, ready.pid, readStamp(this.o.stampFile), this.platform)) {
      this.o.note(`停掉上次留下的服务端（pid ${ready.pid}）`)
      await terminateStale(ready.pid, stopGraceMs, this.platform)
    }
    rmSync(this.o.readyFile, { force: true })
    rmSync(this.o.stampFile, { force: true })
  }

  private async writeStamp(pid: number): Promise<void> {
    const info = await processInfo(pid, this.platform)
    if (!info) return
    try {
      mkdirSync(dirname(this.o.stampFile), { recursive: true })
      writeFileSync(this.o.stampFile, JSON.stringify({ pid, started: info.started } satisfies Stamp))
    } catch (err) {
      this.o.note(`记不下服务端的启动时刻：${(err as Error).message}`)
    }
  }

  private fail(failure: StartFailure): void {
    this.failure = failure
    this.o.note(`服务端启动失败：${JSON.stringify(failure)}`)
    this.setState('failed')
  }

  private setState(state: ServerState): void {
    if (this.state === state) return
    this.state = state
    this.emit('state', state)
  }
}

function readReady(file: string): Ready | undefined {
  try {
    const value = JSON.parse(readFileSync(file, 'utf8')) as Partial<Ready>
    if (typeof value.url === 'string' && typeof value.pid === 'number' && typeof value.version === 'string') {
      return { url: value.url, pid: value.pid, version: value.version }
    }
  } catch {
    // 还没写出来，或不是这个格式
  }
  return undefined
}

// GET /healthz：200 为 true，503 为 false，没有回答或其他状态为 undefined。
async function probeHealth(url: string): Promise<boolean | undefined> {
  try {
    const response = await fetch(new URL('/healthz', url), { signal: AbortSignal.timeout(3_000) })
    await response.body?.cancel()
    return response.status === 200 ? true : response.status === 503 ? false : undefined
  } catch {
    return undefined
  }
}

function describeExit(exit: Exit): string {
  return exit.signal ? `信号 ${exit.signal}` : `退出码 ${exit.code}`
}

function readStamp(file: string): Stamp | undefined {
  try {
    const value = JSON.parse(readFileSync(file, 'utf8')) as Partial<Stamp>
    if (typeof value.pid === 'number' && typeof value.started === 'string') return { pid: value.pid, started: value.started }
  } catch {
    // 没记过
  }
  return undefined
}
