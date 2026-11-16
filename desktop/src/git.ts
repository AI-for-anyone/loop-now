import { execFile } from 'node:child_process'
import { accessSync, constants } from 'node:fs'
import { shell } from 'electron'
import { current, gitDownloadPage, pathOf, type PlatformId } from './platform'

// Runs a command and only looks at whether it succeeded.
export type Probe = (file: string, args: string[]) => Promise<boolean>

const probe: Probe = (file, args) => new Promise((resolve) => execFile(file, args, { timeout: 10_000, windowsHide: true }, (err) => resolve(!err)))

// Finds an executable on PATH (used on Windows): the first existing name, in directory order.
export function findInPath(name: string, pathEnv: string | undefined, p: PlatformId = current, exists: (file: string) => boolean = fileExists): string | undefined {
  const { delimiter, join } = pathOf(p)
  for (const dir of (pathEnv ?? '').split(delimiter)) {
    if (dir === '') continue
    const file = join(dir, name)
    if (exists(file)) return file
  }
  return undefined
}

function fileExists(file: string): boolean {
  try {
    accessSync(file, constants.F_OK)
    return true
  } catch {
    return false
  }
}

// Whether the git the server uses actually works.
// On macOS without the command line tools, /usr/bin/git is only a placeholder that pops up the system install dialog as soon as it runs;
// xcode-select -p shows no dialog, and if it fails there are neither command line tools nor Xcode, so git is left alone.
// Windows has no placeholder git: find git.exe on PATH (Git for Windows puts it there once installed), then run git --version; not found
// means not installed.
export async function gitUsable(run: Probe = probe, p: PlatformId = current, env: NodeJS.ProcessEnv = process.env, exists?: (file: string) => boolean): Promise<boolean> {
  if (p === 'win32') {
    const git = findInPath('git.exe', env.PATH ?? env.Path, p, exists)
    return git !== undefined && run(git, ['--version'])
  }
  if (!(await run('/usr/bin/xcode-select', ['-p']))) return false
  return run('/usr/bin/git', ['--version'])
}

// Guides the install: on macOS, opens the system's dialog for installing the command line tools; on Windows, opens the git-scm.com download page.
export function installGit(p: PlatformId = current): void {
  if (p === 'win32') {
    void shell.openExternal(gitDownloadPage)
    return
  }
  execFile('/usr/bin/xcode-select', ['--install'], () => undefined)
}
