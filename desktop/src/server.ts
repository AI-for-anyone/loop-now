import { spawn, type ChildProcess } from 'node:child_process'
import { EventEmitter } from 'node:events'
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { setTimeout as sleep } from 'node:timers/promises'
import type { RotatingLog } from './log'
import { current, type PlatformId } from './platform'
import { isOurServer, processInfo, spawnPlan, terminateChild, terminateStale, type Stamp } from './process'
import { RestartPolicy } from './restart'

// stopped: no process; starting: launched, waiting until ready; running/degraded: ready, the health endpoint returns 200/503;
// restarting: waiting to restart after a crash; failed: exited or timed out before ready; crashed: too many crashes, no more restarts;
// stopping: being stopped.
export type ServerState = 'stopped' | 'starting' | 'running' | 'degraded' | 'restarting' | 'failed' | 'crashed' | 'stopping'

// Why a start failed; the UI turns it into text. Server exit codes: 2 configuration error, 3 data directory or work root held by another
// instance, 4 listen address in use.
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
  // The server pid and start time recorded by the shell (written once ready); on the next start, orphan cleanup checks them against the pid
  // in the ready file, so a reused pid is not misidentified.
  stampFile: string
  env: NodeJS.ProcessEnv
  // The server's stdout and stderr are written here as is.
  log: RotatingLog
  // One line of the shell's own log.
  note: (text: string) => void
  // The port to listen on this time; 0 means the system picks.
  choosePort: () => Promise<number>
  // External TLS port (for external identities and loopctl): opened when a directory is given; the server maintains a self-signed
  // certificate in that directory (LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED) and listens only on 127.0.0.1. Port 0 again means the system picks,
  // and the actual address is read from externalUrl in the ready file. Not opened when omitted.
  external?: { tlsDir: string; choosePort: () => Promise<number> }
  platform?: PlatformId
}

export const readyTimeoutMs = 90_000
// The server's LOOP_NOW_SHUTDOWN_TIMEOUT defaults to 15 seconds; allow 5 more.
const stopGraceMs = 20_000
const healthIntervalMs = 10_000

type Ready = { url: string; pid: number; version: string; externalUrl?: string }
type Exit = { code: number | null; signal: NodeJS.Signals | null }
// externalPort undefined means the external port is not open.
type Run = { child: ChildProcess; port: number; externalPort: number | undefined; exited: Promise<Exit>; spawnError?: string }

// Launches and supervises loop-server: waits for the ready file and the health endpoint, checks health periodically once ready, and
// restarts after crashes according to RestartPolicy.
export class ServerProcess extends EventEmitter<{ state: [ServerState]; ready: [url: string] }> {
  state: ServerState = 'stopped'
  url: string | undefined
  // The https address of the external TLS port; undefined when it is not open or not ready yet.
  externalUrl: string | undefined
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

  // Whether there is a server process (including one that is not ready yet).
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
    const externalPort = this.o.external ? await this.o.external.choosePort() : undefined
    if ((this.state as ServerState) !== 'starting') return // stopped while waiting
    this.launch(port, externalPort)
  }

  private launch(port: number, externalPort: number | undefined): void {
    this.o.log.resetTail()
    const chosen = (p: number) => (p === 0 ? '由系统选择' : String(p))
    this.o.note(`启动服务端 ${this.o.bin}，端口 ${chosen(port)}${externalPort === undefined ? '' : `，对外 TLS 端口 ${chosen(externalPort)}`}`)
    rmSync(this.o.readyFile, { force: true })
    const externalEnv: NodeJS.ProcessEnv =
      externalPort === undefined || !this.o.external
        ? {}
        : { LOOP_NOW_EXTERNAL_LISTEN_ADDR: `127.0.0.1:${externalPort}`, LOOP_NOW_EXTERNAL_TLS_SELF_SIGNED: this.o.external.tlsDir }
    // On Windows the shell holds stdin and sets LOOP_NOW_STOP_ON_STDIN_CLOSE=1; on exit it closes stdin so the server shuts down gracefully
    // (see process.ts).
    const plan = spawnPlan(this.platform)
    const child = spawn(this.o.bin, [], {
      cwd: dirname(this.o.bin),
      env: {
        ...this.o.env,
        ...plan.env,
        LOOP_NOW_LISTEN_ADDR: `127.0.0.1:${port}`,
        LOOP_NOW_READY_FILE: this.o.readyFile,
        LOOP_NOW_CONSOLE_ORIGINS: 'none',
        ...externalEnv,
      },
      stdio: [plan.stdin, 'pipe', 'pipe'],
      // Its own process group: Ctrl+C in a terminal does not hit the server directly, and the shell stops it in order. On Windows, detached
      // opens a window for console programs, so it is not used there;
      // windowsHide keeps the server from showing a console window.
      detached: this.platform !== 'win32',
      windowsHide: true,
    })
    const run: Run = { child, port, externalPort, exited: undefined as unknown as Promise<Exit> }
    run.exited = new Promise<Exit>((resolve) => {
      child.once('exit', (code, signal) => resolve({ code, signal }))
      child.on('error', (err) => {
        if (child.pid !== undefined) return this.o.note(`Server process error: ${err.message}`)
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
    this.externalUrl = undefined
    this.setState('stopped')
  }

  // Manual restart by the user: the crash count is reset.
  async restart(): Promise<void> {
    this.policy.reset()
    await this.stop()
    await this.start()
  }

  private async supervise(run: Run): Promise<void> {
    const ready = await this.waitReady(run)
    if (ready === 'timeout') {
      this.o.note(`Server not ready within ${readyTimeoutMs / 1000} seconds, stopping it`)
      await this.terminate(run)
    }
    if (ready === 'exited' || ready === 'timeout') {
      const exit = await run.exited
      this.run = undefined
      if (this.stopRequested.has(run.child)) return
      if (ready === 'timeout') return this.fail({ kind: 'timeout' })
      if (run.spawnError !== undefined) return this.fail({ kind: 'spawn', message: run.spawnError })
      // A port was taken by someone else after the probe but before the server listened (we don't know which one): let the system pick
      // both ports this once.
      if (exit.code === exitAddressInUse && (run.port !== 0 || (run.externalPort !== undefined && run.externalPort !== 0))) {
        this.o.note(`Port ${run.port}${run.externalPort ? ` or external TLS port ${run.externalPort}` : ''} is in use, letting the system choose`)
        return this.launch(0, run.externalPort === undefined ? undefined : 0)
      }
      if (exit.code === exitConfig) return this.fail({ kind: 'config' })
      if (exit.code === exitLocked) return this.fail({ kind: 'locked' })
      return this.fail({ kind: 'exited', ...exit })
    }

    this.url = ready.url
    this.externalUrl = ready.externalUrl
    this.o.note(`服务端已就绪：${ready.url}（${ready.version}，pid ${ready.pid}）${ready.externalUrl ? `，对外 TLS 端口 ${ready.externalUrl}` : ''}`)
    await this.writeStamp(ready.pid)
    this.setState(ready.healthy ? 'running' : 'degraded')
    this.emit('ready', ready.url)
    this.healthTimer = setInterval(() => void this.checkHealth(), healthIntervalMs)

    const exit = await run.exited
    clearInterval(this.healthTimer)
    this.run = undefined
    if (this.stopRequested.has(run.child)) return
    this.o.note(`Server exited unexpectedly: ${describeExit(exit)}`)
    const delay = this.policy.next(Date.now())
    if (delay === null) {
      this.o.note('Crashed more than 3 times within 5 minutes, no more automatic restarts')
      this.setState('crashed')
      return
    }
    this.setState('restarting')
    this.restartTimer = setTimeout(() => void this.start(), delay)
  }

  // Waits until the ready file holds this process's pid and the health endpoint answers (200 available, 503 database unavailable; both
  // count as ready).
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

  // Lets it wind down by itself first (SIGTERM on macOS, close stdin on Windows), and forces it to end on timeout (platform differences:
  // see process.ts).
  private async terminate(run: Run): Promise<void> {
    const { child } = run
    if (child.pid === undefined || child.exitCode !== null || child.signalCode !== null) return
    const inTime = await terminateChild(child, run.exited, stopGraceMs, this.platform)
    if (!inTime) this.o.note(`服务端 ${stopGraceMs / 1000} 秒内没有退出，强制结束`)
  }

  // A server left behind when the shell last exited abnormally would hold the data directory: if the pid in the ready file is still alive,
  // runs exactly this executable, and its start time matches what the shell recorded, stop it first.
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
      this.o.note(`Could not record the server start time: ${(err as Error).message}`)
    }
  }

  private fail(failure: StartFailure): void {
    this.failure = failure
    this.o.note(`Server failed to start: ${JSON.stringify(failure)}`)
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
      return { url: value.url, pid: value.pid, version: value.version, externalUrl: typeof value.externalUrl === 'string' ? value.externalUrl : undefined }
    }
  } catch {
    // not written yet, or not in this format
  }
  return undefined
}

// GET /healthz: true for 200, false for 503, undefined for no answer or any other status.
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
  return exit.signal ? `signal ${exit.signal}` : `exit code ${exit.code}`
}

function readStamp(file: string): Stamp | undefined {
  try {
    const value = JSON.parse(readFileSync(file, 'utf8')) as Partial<Stamp>
    if (typeof value.pid === 'number' && typeof value.started === 'string') return { pid: value.pid, started: value.started }
  } catch {
    // never recorded
  }
  return undefined
}
