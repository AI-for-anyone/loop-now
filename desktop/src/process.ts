import { execFile, type ChildProcess } from 'node:child_process'
import { setTimeout as sleep } from 'node:timers/promises'
import { current, type PlatformId } from './platform'

// Platform-specific operations on the server process: find out what a pid is running and when it started, and stop it (gracefully or by force).
// macOS/Linux: SIGTERM lets the server wind down by itself (it stops all Agents); on timeout, SIGKILL the whole process group.
// Windows: there is no SIGTERM (on Windows, Go only recognizes the console's Ctrl+C/Ctrl+Break events, which Node cannot send; child.kill() is
// TerminateProcess, which kills only the server itself and leaves Pi and the plugin host orphaned). The shell starts the server with
// LOOP_NOW_STOP_ON_STDIN_CLOSE=1 and holds on to its stdin; to stop it, the shell closes stdin.
// The server reads EOF and goes through the same graceful shutdown as on SIGTERM (LOOP_NOW_SHUTDOWN_TIMEOUT defaults to 15 seconds); on
// timeout, taskkill /T /F forcibly ends it together with its child processes.
// An orphan left over from last time has no stdin to close, so taskkill is the only option.

export type ProcessInfo = {
  // On macOS, ps's command (the server starts without arguments, so this is the executable path); on Windows, the full path of the executable.
  command: string
  // Start time: on macOS the raw lstart text from ps, on Windows the FILETIME of CreationDate (both are only compared for equality).
  started: string
}

export type Stamp = { pid: number; started: string }

const run = (file: string, args: string[]) =>
  new Promise<string | undefined>((resolve) => execFile(file, args, { timeout: 10_000, windowsHide: true }, (err, stdout) => resolve(err ? undefined : String(stdout))))

export function alive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch (err) {
    // On Windows, kill(pid, 0) on someone else's process may fail with EPERM for lack of permission, but the process is indeed alive.
    return (err as NodeJS.ErrnoException).code === 'EPERM'
  }
}

// What pid is running right now. Undefined when the process is gone.
export async function processInfo(pid: number, p: PlatformId = current): Promise<ProcessInfo | undefined> {
  if (!Number.isInteger(pid) || pid <= 0 || !alive(pid)) return undefined
  if (p === 'win32') {
    const out = await run('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', windowsProcessQuery(pid)])
    return out ? parseWindowsProcess(out) : undefined
  }
  const command = await run('/bin/ps', ['-p', String(pid), '-o', 'command='])
  const started = await run('/bin/ps', ['-p', String(pid), '-o', 'lstart='])
  if (command === undefined || started === undefined || command.trim() === '') return undefined
  return { command: command.trim(), started: started.trim() }
}

// Get-Process's Path is unavailable for other users' processes, and Win32_Process's ExecutablePath is likewise only reliable for our own
// processes; the shell and the server run as the same user.
export const windowsProcessQuery = (pid: number) =>
  `Get-CimInstance Win32_Process -Filter 'ProcessId=${pid}' | Select-Object ExecutablePath,@{n='Started';e={$_.CreationDate.ToFileTimeUtc()}} | ConvertTo-Json -Compress`

export function parseWindowsProcess(out: string): ProcessInfo | undefined {
  try {
    const v = JSON.parse(out) as { ExecutablePath?: unknown; Started?: unknown }
    if (typeof v.ExecutablePath !== 'string' || v.ExecutablePath === '') return undefined
    return { command: v.ExecutablePath, started: String(v.Started ?? '') }
  } catch {
    return undefined
  }
}

// Whether this pid is our server: it runs exactly bin (Windows paths are case-insensitive), and its start time matches what the shell
// recorded last time (if anything was recorded).
// The system recycles and reuses pids, so comparing only the executable can still misidentify it; a matching start time cannot.
export function isOurServer(info: ProcessInfo | undefined, bin: string, pid: number, stamp: Stamp | undefined, p: PlatformId = current): boolean {
  if (!info) return false
  const same = p === 'win32' ? info.command.toLowerCase() === bin.toLowerCase() : info.command === bin
  if (!same) return false
  return stamp === undefined || stamp.pid !== pid || stamp.started === info.started
}

export async function waitGone(pid: number, ms: number): Promise<boolean> {
  const deadline = Date.now() + ms
  while (Date.now() < deadline) {
    if (!alive(pid)) return true
    await sleep(100)
  }
  return !alive(pid)
}

function signal(pid: number, name: NodeJS.Signals): void {
  try {
    process.kill(pid, name)
  } catch {
    // already exited
  }
}

// The per-platform stdin and environment for launching the server: Windows holds on to stdin for a graceful stop; other platforms get no stdin.
export function spawnPlan(p: PlatformId = current): { stdin: 'pipe' | 'ignore'; env: NodeJS.ProcessEnv } {
  return p === 'win32' ? { stdin: 'pipe', env: { LOOP_NOW_STOP_ON_STDIN_CLOSE: '1' } } : { stdin: 'ignore', env: {} }
}

// Stops a server process the shell launched itself: SIGTERM on macOS, close stdin on Windows; wait for it to wind down, and on timeout
// SIGKILL the whole process group on macOS or taskkill the process tree on Windows.
// Returns whether it exited by itself within the grace period.
export async function terminateChild(child: ChildProcess, exited: Promise<unknown>, graceMs: number, p: PlatformId = current): Promise<boolean> {
  if (child.pid === undefined) return true
  if (p === 'win32') {
    if (child.stdin && !child.stdin.destroyed) child.stdin.end()
    else return taskkill(child.pid).then(() => exited).then(() => false)
  } else {
    child.kill('SIGTERM')
  }
  const inTime = await Promise.race([exited.then(() => true), sleep(graceMs).then(() => false)])
  if (inTime) return true
  if (p === 'win32') {
    await taskkill(child.pid)
  } else {
    try {
      process.kill(-child.pid, 'SIGKILL')
    } catch {
      child.kill('SIGKILL')
    }
  }
  await exited
  return false
}

// Stops a server the shell did not launch itself (one left behind when the shell last exited abnormally).
export async function terminateStale(pid: number, graceMs: number, p: PlatformId = current): Promise<void> {
  if (p === 'win32') {
    await taskkill(pid)
    await waitGone(pid, 5_000)
    return
  }
  signal(pid, 'SIGTERM')
  if (!(await waitGone(pid, graceMs))) {
    signal(pid, 'SIGKILL')
    await waitGone(pid, 5_000)
  }
}

export const taskkillArgs = (pid: number) => ['/PID', String(pid), '/T', '/F']

function taskkill(pid: number): Promise<void> {
  return new Promise((resolve) => execFile('taskkill.exe', taskkillArgs(pid), { windowsHide: true }, () => resolve()))
}
