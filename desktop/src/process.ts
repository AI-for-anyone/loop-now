import { execFile, type ChildProcess } from 'node:child_process'
import { setTimeout as sleep } from 'node:timers/promises'
import { current, type PlatformId } from './platform'

// 服务端进程的平台相关操作：查一个 pid 跑的是什么、什么时候启动的，停掉它（优雅或强制）。
// macOS／Linux：SIGTERM 让服务端自己收尾（它会停掉所有 Agent），超时再对整个进程组 SIGKILL。
// Windows：没有 SIGTERM，服务端也还没有 HTTP 或文件信号的停止方式（Go 在 Windows 上只认控制台的 Ctrl+C／Ctrl+Break 事件，
// Node 发不出去；child.kill() 是 TerminateProcess，只杀服务端自己，Pi 与插件宿主会变成孤儿），所以用 taskkill /T /F
// 连同子进程一起强制结束。服务端提供优雅停止方式之前，Windows 上退出等于崩溃式停机（在途回复丢失）。

export type ProcessInfo = {
  // macOS 是 ps 的 command（服务端不带参数启动，就是可执行文件路径）；Windows 是可执行文件的完整路径。
  command: string
  // 启动时刻：macOS 是 ps 的 lstart 原文，Windows 是 CreationDate 的 FILETIME（都只用来比较是否相同）。
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
    // Windows 上对别人的进程 kill(pid, 0) 可能因权限返回 EPERM，但它确实活着。
    return (err as NodeJS.ErrnoException).code === 'EPERM'
  }
}

// pid 现在跑的是什么。进程不在时为空。
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

// Get-Process 的 Path 对别的用户的进程取不到，Win32_Process 的 ExecutablePath 同样只对自己的进程可靠；壳和服务端是同一用户。
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

// 这个 pid 是不是我们的服务端：跑的正是 bin（Windows 路径不分大小写），而且启动时刻与壳上次记下的一致（记过的话）。
// pid 会被系统回收复用，只比可执行文件还可能认错；启动时刻一样就不会。
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
    // 已经退出
  }
}

// 停掉自己拉起的服务端进程。macOS：SIGTERM 等它收尾，超时后整个进程组 SIGKILL；Windows：taskkill 进程树。
// 返回是否在宽限期内自己退出（Windows 一律 false）。
export async function terminateChild(child: ChildProcess, exited: Promise<unknown>, graceMs: number, p: PlatformId = current): Promise<boolean> {
  if (child.pid === undefined) return true
  if (p === 'win32') {
    await taskkill(child.pid)
    await exited
    return false
  }
  child.kill('SIGTERM')
  const inTime = await Promise.race([exited.then(() => true), sleep(graceMs).then(() => false)])
  if (inTime) return true
  try {
    process.kill(-child.pid, 'SIGKILL')
  } catch {
    child.kill('SIGKILL')
  }
  await exited
  return false
}

// 停掉不是自己拉起的服务端（壳上次异常退出留下的）。
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
