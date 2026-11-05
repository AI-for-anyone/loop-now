import { execFile } from 'node:child_process'
import { lstat, readlink } from 'node:fs/promises'
import { dirname } from 'node:path'

// macOS：装到 PATH 上的位置（macOS 默认的 PATH 含 /usr/local/bin），链到包内固定的 Resources/bin/loopctl。
export const cliLink = '/usr/local/bin/loopctl'

export type LinkState = { state: 'missing' } | { state: 'ours' } | { state: 'other'; points?: string }

// link 现在是什么：不存在、已经指向 target，或别的文件／链接。
export async function linkState(link: string, target: string): Promise<LinkState> {
  try {
    if (!(await lstat(link)).isSymbolicLink()) return { state: 'other' }
    const points = await readlink(link)
    return points === target ? { state: 'ours' } : { state: 'other', points }
  } catch (err) {
    return (err as NodeJS.ErrnoException).code === 'ENOENT' ? { state: 'missing' } : { state: 'other' }
  }
}

// 用管理员权限建 /usr/local/bin（Apple 芯片的 Mac 上可能没有）与链接的 AppleScript。
// 路径先按 AppleScript 字符串转义，再用 quoted form 交给 shell。
export function installScript(target: string, link = cliLink): string {
  const string = (value: string) => `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
  return `do shell script "mkdir -p " & quoted form of ${string(dirname(link))} & " && ln -sfn " & quoted form of ${string(target)} & " " & quoted form of ${string(link)} with administrator privileges`
}

// 弹出系统的管理员密码框并建链接。人取消时为 'cancelled'。
export function installLink(target: string): Promise<'done' | 'cancelled' | Error> {
  return new Promise((resolve) => {
    execFile('/usr/bin/osascript', ['-e', installScript(target)], (err, _stdout, stderr) => {
      if (!err) return resolve('done')
      // osascript 的 -128 是"用户已取消"。
      resolve(/-128/.test(stderr) ? 'cancelled' : new Error(stderr.trim() || err.message))
    })
  })
}

// Windows：把随包的 bin 目录加进当前用户的 PATH（注册表 HKCU\Environment），不动系统 PATH、不需要管理员。
// 用 [Environment]::SetEnvironmentVariable 而不是 setx：setx 把值截到 1024 个字符，会把人的 PATH 弄坏。
// 写完 Windows 会广播环境变量变化，新开的终端能看到；已经开着的终端要重开。

// 用户 PATH 里有没有这个目录（不分大小写，末尾的反斜杠不算）。
export function userPathHas(userPath: string, dir: string): boolean {
  const norm = (v: string) => v.trim().replace(/[\\/]+$/, '').toLowerCase()
  return userPath.split(';').some((entry) => entry !== '' && norm(entry) === norm(dir))
}

// PowerShell 单引号字符串：只需把单引号写成两个。
const psQuote = (value: string) => `'${value.replace(/'/g, "''")}'`

export const readUserPathScript = `[Environment]::GetEnvironmentVariable('Path', 'User')`

// 没有就追加；有了就不动。输出最终的用户 PATH。
export function addToUserPathScript(dir: string): string {
  return [
    `$dir = ${psQuote(dir)}`,
    `$path = [Environment]::GetEnvironmentVariable('Path', 'User')`,
    `if ($null -eq $path) { $path = '' }`,
    `$parts = $path -split ';' | Where-Object { $_ -ne '' }`,
    `if (-not ($parts | Where-Object { $_.TrimEnd('\\') -ieq $dir.TrimEnd('\\') })) { $path = (($parts + $dir) -join ';') }`,
    `[Environment]::SetEnvironmentVariable('Path', $path, 'User')`,
    `$path`,
  ].join('; ')
}

function powershell(script: string): Promise<string | Error> {
  return new Promise((resolve) => {
    execFile('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', script], { windowsHide: true, timeout: 30_000 }, (err, stdout, stderr) => {
      resolve(err ? new Error(String(stderr).trim() || err.message) : String(stdout).trim())
    })
  })
}

export async function readUserPath(): Promise<string | Error> {
  return powershell(readUserPathScript)
}

export async function addToUserPath(dir: string): Promise<'done' | Error> {
  const result = await powershell(addToUserPathScript(dir))
  if (result instanceof Error) return result
  return userPathHas(result, dir) ? 'done' : new Error(`PATH 写完后没有 ${dir}`)
}
