import { execFile } from 'node:child_process'
import { lstat, readlink } from 'node:fs/promises'
import { dirname } from 'node:path'

// macOS: the install location on PATH (the default macOS PATH includes /usr/local/bin), linked to the fixed Resources/bin/loopctl inside the app bundle.
export const cliLink = '/usr/local/bin/loopctl'

export type LinkState = { state: 'missing' } | { state: 'ours' } | { state: 'other'; points?: string }

// What link currently is: missing, already pointing to target, or some other file/link.
export async function linkState(link: string, target: string): Promise<LinkState> {
  try {
    if (!(await lstat(link)).isSymbolicLink()) return { state: 'other' }
    const points = await readlink(link)
    return points === target ? { state: 'ours' } : { state: 'other', points }
  } catch (err) {
    return (err as NodeJS.ErrnoException).code === 'ENOENT' ? { state: 'missing' } : { state: 'other' }
  }
}

// AppleScript that creates /usr/local/bin (it may be missing on Apple silicon Macs) and the link with administrator privileges.
// Paths are first escaped as AppleScript strings, then passed to the shell via quoted form.
export function installScript(target: string, link = cliLink): string {
  const string = (value: string) => `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
  return `do shell script "mkdir -p " & quoted form of ${string(dirname(link))} & " && ln -sfn " & quoted form of ${string(target)} & " " & quoted form of ${string(link)} with administrator privileges`
}

// Shows the system's administrator password prompt and creates the link. 'cancelled' when the user cancels.
export function installLink(target: string): Promise<'done' | 'cancelled' | Error> {
  return new Promise((resolve) => {
    execFile('/usr/bin/osascript', ['-e', installScript(target)], (err, _stdout, stderr) => {
      if (!err) return resolve('done')
      // osascript's -128 means "User canceled."
      resolve(/-128/.test(stderr) ? 'cancelled' : new Error(stderr.trim() || err.message))
    })
  })
}

// Windows: add the bundled bin directory to the current user's PATH (registry HKCU\Environment); the system PATH is left alone and no
// administrator is needed.
// Uses [Environment]::SetEnvironmentVariable rather than setx: setx truncates the value to 1024 characters, which would break the user's PATH.
// After the write, Windows broadcasts the environment change, so new terminals see it; terminals already open have to be reopened.

// Whether the user PATH contains this directory (case-insensitive, trailing backslashes ignored).
export function userPathHas(userPath: string, dir: string): boolean {
  const norm = (v: string) => v.trim().replace(/[\\/]+$/, '').toLowerCase()
  return userPath.split(';').some((entry) => entry !== '' && norm(entry) === norm(dir))
}

// PowerShell single-quoted string: only single quotes need to be doubled.
const psQuote = (value: string) => `'${value.replace(/'/g, "''")}'`

export const readUserPathScript = `[Environment]::GetEnvironmentVariable('Path', 'User')`

// Appends the directory if it is missing and leaves PATH alone if it is there. Outputs the resulting user PATH.
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
